package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"cfui/internal/cfaccount"
	"cfui/internal/cfoauth"
	"cfui/internal/config"

	"nhooyr.io/websocket"
)

func (s *Server) handleOAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status, err := s.ensureOAuthService().Status(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, status)
}

func (s *Server) handleOAuthRelayCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	check, err := s.ensureOAuthService().CheckRelay(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, check)
}

func (s *Server) handleOAuthConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		RelayCallbackURL string `json:"relay_callback_url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	relayURL, err := cfoauth.NormalizeRelayCallbackURL(req.RelayCallbackURL)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	cfg := s.cfgMgr.Get()
	cfg.OAuthRelayCallbackURL = relayURL
	if err := s.cfgMgr.Save(cfg); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	status, err := s.resetOAuthService().Status(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, status)
}

func (s *Server) handleOAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Scope      string `json:"scope"`
		Scopes     string `json:"scopes"`
		FreshLogin bool   `json:"fresh_login"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
	}
	scope := req.Scope
	if strings.TrimSpace(scope) == "" {
		scope = req.Scopes
	}
	oauthSvc := s.ensureOAuthService()
	callbackURL, err := oauthCallbackURLForRequest(r, oauthSvc.Config().LocalCallbackPath)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	url, err := oauthSvc.StartURLWithOptions(r.Context(), cfoauth.StartURLOptions{
		Scopes:      scope,
		FreshLogin:  req.FreshLogin,
		CallbackURL: callbackURL,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]string{"url": url})
}

func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	oauthSvc := s.ensureOAuthService()
	callbackURL, err := oauthCallbackURLForRequest(r, oauthSvc.Config().LocalCallbackPath)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	url, err := oauthSvc.StartURLWithOptions(r.Context(), cfoauth.StartURLOptions{
		Scopes:      r.URL.Query().Get("scope"),
		FreshLogin:  truthyQuery(r.URL.Query().Get("fresh_login")),
		CallbackURL: callbackURL,
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func oauthCallbackURLForRequest(r *http.Request, callbackPath string) (string, error) {
	scheme := firstForwardedHeaderValue(r.Header.Get("X-Forwarded-Proto"))
	if scheme != "http" && scheme != "https" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := firstForwardedHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return "", fmt.Errorf("request host is required for OAuth callback URL")
	}
	if strings.ContainsAny(host, " \t\r\n/\\") {
		return "", fmt.Errorf("request host is invalid for OAuth callback URL")
	}
	if strings.TrimSpace(callbackPath) == "" {
		callbackPath = "/oauth/callback"
	}
	if !strings.HasPrefix(callbackPath, "/") {
		callbackPath = "/" + callbackPath
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: callbackPath}).String(), nil
}

func firstForwardedHeaderValue(value string) string {
	part := strings.TrimSpace(strings.Split(value, ",")[0])
	return strings.ToLower(part)
}

func truthyQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if oauthErr := strings.TrimSpace(r.URL.Query().Get("error")); oauthErr != "" {
		http.Redirect(w, r, "/cloudflare?oauth=error&message="+url.QueryEscape(oauthCallbackErrorMessage(r.URL.Query())), http.StatusFound)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		http.Redirect(w, r, "/cloudflare?oauth=error&message=invalid_oauth_response", http.StatusFound)
		return
	}
	if _, err := s.ensureOAuthService().CompleteCallback(r.Context(), code, state); err != nil {
		http.Redirect(w, r, "/cloudflare?oauth=error&message="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/cloudflare?oauth=success", http.StatusFound)
}

func oauthCallbackErrorMessage(values url.Values) string {
	code := strings.TrimSpace(values.Get("error"))
	if code == "" {
		return "oauth_error"
	}
	description := strings.TrimSpace(values.Get("error_description"))
	if description == "" {
		return code
	}
	return code + ": " + description
}

func (s *Server) handleOAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		Revoke    bool   `json:"revoke"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if err := s.ensureOAuthService().Logout(r.Context(), req.SessionID, req.Revoke); err != nil && !errors.Is(err, cfoauth.ErrNotLoggedIn) {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	status, err := s.ensureOAuthService().Status(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, status)
}

func (s *Server) handleOAuthSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		status, err := s.ensureOAuthService().SwitchSession(r.Context(), req.SessionID)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, status)
	case http.MethodPatch:
		var req struct {
			SessionID string `json:"session_id"`
			Label     string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		status, err := s.ensureOAuthService().UpdateSessionLabel(r.Context(), req.SessionID, req.Label)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, status)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().Accounts(r.Context())
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().Overview(r.Context(), r.URL.Query().Get("account_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFAccountUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().AccountUsage(r.Context(), r.URL.Query().Get("account_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().CloudflareStatus(r.Context())
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFZones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().Zones(r.Context(), r.URL.Query().Get("account_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFZone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	zoneID := strings.TrimPrefix(r.URL.Path, "/api/cf/zones/")
	if zoneID == "" || strings.Contains(zoneID, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("zone_id is required"))
		return
	}
	zoneID, err := url.PathUnescape(zoneID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(zoneID) == "" || strings.Contains(zoneID, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("zone_id is required"))
		return
	}
	resp, err := s.ensureCFService().Zone(r.Context(), zoneID)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFDNSRecords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := s.ensureCFService().DNSRecords(r.Context(), r.URL.Query().Get("zone_id"))
		writeCFResponse(w, resp, err)
	case http.MethodPost:
		zoneID := r.URL.Query().Get("zone_id")
		var req cfaccount.DNSRecordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		record, err := s.ensureCFService().CreateDNSRecord(r.Context(), zoneID, req)
		writeCFResponse(w, record, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFDNSCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().DNSRecordCount(r.Context(), r.URL.Query().Get("zone_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFDNSRecord(w http.ResponseWriter, r *http.Request) {
	recordID := strings.TrimPrefix(r.URL.Path, "/api/cf/dns/")
	if recordID == "" {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("record id is required"))
		return
	}
	zoneID := r.URL.Query().Get("zone_id")
	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		var req cfaccount.DNSRecordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		record, err := s.ensureCFService().UpdateDNSRecord(r.Context(), zoneID, recordID, req)
		writeCFResponse(w, record, err)
	case http.MethodDelete:
		err := s.ensureCFService().DeleteDNSRecord(r.Context(), zoneID, recordID)
		writeCFResponse(w, map[string]bool{"success": true}, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFTunnels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := s.ensureCFService().Tunnels(r.Context(), r.URL.Query().Get("account_id"))
		if err != nil {
			writeCFResponse(w, nil, err)
			return
		}
		writeJSON(w, cfTunnelsResponse{
			Data:          resp.Data,
			LocalProfiles: s.localTunnelProfileSummaries(r.URL.Query().Get("account_id")),
			Session:       resp.Session,
			Capabilities:  resp.Capabilities,
		})
	case http.MethodPost:
		var req cfTunnelCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		accountID := r.URL.Query().Get("account_id")
		result, err := s.ensureCFService().CreateTunnel(r.Context(), accountID, cfaccount.TunnelCreateRequest{Name: req.Name})
		if err != nil {
			writeCFResponse(w, nil, err)
			return
		}
		var localProfile *cfLocalTunnelProfileSummary
		if req.SaveLocalProfile {
			localProfile, err = s.saveOAuthTunnelLocalProfile(result.Tunnel, result.Token, accountID, req.ActivateLocal)
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, err)
				return
			}
		}
		writeJSON(w, cfTunnelCreateResponse{
			Tunnel:       result.Tunnel,
			LocalProfile: localProfile,
			Session:      result.Session,
			Capabilities: result.Capabilities,
		})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFTunnel(w http.ResponseWriter, r *http.Request) {
	target, err := cfTunnelPathFromPath(r.URL.Path)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if len(target.Segments) > 0 {
		s.handleCFTunnelSubresource(w, r, target)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		accountID := r.URL.Query().Get("account_id")
		result, err := s.ensureCFService().DeleteTunnel(r.Context(), accountID, target.TunnelID)
		if err != nil {
			writeCFResponse(w, nil, err)
			return
		}
		var localProfile *cfLocalTunnelProfileSummary
		localProfileRemoved := false
		if parseBoolQuery(r.URL.Query().Get("delete_local_profile")) {
			localProfile, localProfileRemoved, err = s.cleanupOAuthTunnelLocalProfile(target.TunnelID)
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, err)
				return
			}
		}
		writeJSON(w, cfTunnelDeleteResponse{
			Success:             true,
			TunnelID:            result.TunnelID,
			LocalProfile:        localProfile,
			LocalProfileRemoved: localProfileRemoved,
			Session:             result.Session,
			Capabilities:        result.Capabilities,
		})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFTunnelSubresource(w http.ResponseWriter, r *http.Request, target cfTunnelPath) {
	if len(target.Segments) == 1 && target.Segments[0] == "config" {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp, err := s.ensureCFService().TunnelConfiguration(r.Context(), r.URL.Query().Get("account_id"), target.TunnelID)
		writeCFResponse(w, resp, err)
		return
	}
	if len(target.Segments) == 2 && target.Segments[0] == "config" && target.Segments[1] == "entries" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var entry cfaccount.TunnelIngressRule
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().AddTunnelIngressRule(r.Context(), r.URL.Query().Get("account_id"), target.TunnelID, entry)
		writeCFResponse(w, resp, err)
		return
	}
	if len(target.Segments) == 3 && target.Segments[0] == "config" && target.Segments[1] == "entries" && target.Segments[2] == "reorder" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Order []int `json:"order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().ReorderTunnelIngressRules(r.Context(), r.URL.Query().Get("account_id"), target.TunnelID, req.Order)
		writeCFResponse(w, resp, err)
		return
	}
	if len(target.Segments) == 3 && target.Segments[0] == "config" && target.Segments[1] == "entries" {
		index, err := strconv.Atoi(target.Segments[2])
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, errors.New("invalid entry index"))
			return
		}
		switch r.Method {
		case http.MethodPut, http.MethodPatch:
			var entry cfaccount.TunnelIngressRule
			if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
				writeAPIError(w, http.StatusBadRequest, err)
				return
			}
			resp, err := s.ensureCFService().UpdateTunnelIngressRule(r.Context(), r.URL.Query().Get("account_id"), target.TunnelID, index, entry)
			writeCFResponse(w, resp, err)
		case http.MethodDelete:
			resp, err := s.ensureCFService().DeleteTunnelIngressRule(r.Context(), r.URL.Query().Get("account_id"), target.TunnelID, index)
			writeCFResponse(w, resp, err)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	http.NotFound(w, r)
}

type cfTunnelPath struct {
	TunnelID string
	Segments []string
}

func cfTunnelPathFromPath(requestPath string) (cfTunnelPath, error) {
	rest := strings.Trim(strings.TrimPrefix(requestPath, "/api/cf/tunnels/"), "/")
	if rest == "" || rest == requestPath {
		return cfTunnelPath{}, fmt.Errorf("tunnel id is required")
	}
	parts := strings.Split(rest, "/")
	id, err := url.PathUnescape(parts[0])
	if err != nil {
		return cfTunnelPath{}, fmt.Errorf("tunnel id is invalid")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return cfTunnelPath{}, fmt.Errorf("tunnel id is required")
	}
	segments := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			return cfTunnelPath{}, fmt.Errorf("tunnel path is invalid")
		}
		segments = append(segments, part)
	}
	return cfTunnelPath{TunnelID: id, Segments: segments}, nil
}

func parseBoolQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type cfTunnelsResponse struct {
	Data          []cfaccount.Tunnel            `json:"data"`
	LocalProfiles []cfLocalTunnelProfileSummary `json:"local_profiles,omitempty"`
	Session       cfoauth.SessionSummary        `json:"session"`
	Capabilities  cfoauth.CapabilityMatrix      `json:"capabilities"`
}

type cfTunnelCreateRequest struct {
	Name             string `json:"name"`
	SaveLocalProfile bool   `json:"save_local_profile"`
	ActivateLocal    bool   `json:"activate_local"`
}

type cfTunnelCreateResponse struct {
	Tunnel       cfaccount.Tunnel             `json:"tunnel"`
	LocalProfile *cfLocalTunnelProfileSummary `json:"local_profile,omitempty"`
	Session      cfoauth.SessionSummary       `json:"session"`
	Capabilities cfoauth.CapabilityMatrix     `json:"capabilities"`
}

type cfTunnelDeleteResponse struct {
	Success             bool                         `json:"success"`
	TunnelID            string                       `json:"tunnel_id"`
	LocalProfile        *cfLocalTunnelProfileSummary `json:"local_profile,omitempty"`
	LocalProfileRemoved bool                         `json:"local_profile_removed"`
	Session             cfoauth.SessionSummary       `json:"session"`
	Capabilities        cfoauth.CapabilityMatrix     `json:"capabilities"`
}

type cfLocalTunnelProfileSummary struct {
	Key                     string `json:"key"`
	Name                    string `json:"name"`
	AccountID               string `json:"account_id,omitempty"`
	TunnelID                string `json:"tunnel_id,omitempty"`
	LocalEnabled            bool   `json:"local_enabled"`
	RemoteManagementEnabled bool   `json:"remote_management_enabled"`
	Active                  bool   `json:"active"`
}

func (s *Server) saveOAuthTunnelLocalProfile(tunnel cfaccount.Tunnel, token, accountID string, activate bool) (*cfLocalTunnelProfileSummary, error) {
	profile := config.DefaultTunnelProfileConfig()
	profile.Key = ""
	profile.Name = strings.TrimSpace(tunnel.Name)
	if profile.Name == "" {
		profile.Name = strings.TrimSpace(tunnel.ID)
	}
	profile.Token = strings.TrimSpace(token)
	profile.AccountID = strings.TrimSpace(accountID)
	profile.TunnelID = strings.TrimSpace(tunnel.ID)
	profile.LocalEnabled = true
	profile.RemoteManagementEnabled = true

	cfg, err := s.cfgMgr.SaveTunnelProfile("", profile)
	if err != nil {
		return nil, err
	}
	saved, ok := findTunnelProfileByTunnelID(cfg.Tunnels, profile.TunnelID)
	if !ok {
		return nil, fmt.Errorf("created local tunnel profile was not found")
	}
	if activate {
		cfg, err = s.cfgMgr.ActivateTunnelProfile(saved.Key)
		if err != nil {
			return nil, err
		}
		saved, _ = findTunnelProfileByTunnelID(cfg.Tunnels, profile.TunnelID)
	}
	summary := summarizeLocalTunnelProfile(saved, cfg.ActiveTunnelKey)
	return &summary, nil
}

func (s *Server) localTunnelProfileSummaries(accountID string) []cfLocalTunnelProfileSummary {
	accountID = strings.TrimSpace(accountID)
	cfg := s.cfgMgr.Get()
	out := make([]cfLocalTunnelProfileSummary, 0, len(cfg.Tunnels))
	for _, profile := range cfg.Tunnels {
		if strings.TrimSpace(profile.TunnelID) == "" {
			continue
		}
		if accountID != "" && strings.TrimSpace(profile.AccountID) != "" && profile.AccountID != accountID {
			continue
		}
		out = append(out, summarizeLocalTunnelProfile(profile, cfg.ActiveTunnelKey))
	}
	return out
}

func (s *Server) cleanupOAuthTunnelLocalProfile(tunnelID string) (*cfLocalTunnelProfileSummary, bool, error) {
	tunnelID = strings.TrimSpace(tunnelID)
	if tunnelID == "" {
		return nil, false, fmt.Errorf("tunnel id is required")
	}
	cfg := s.cfgMgr.Get()
	profile, ok := findTunnelProfileByTunnelID(cfg.Tunnels, tunnelID)
	if !ok {
		return nil, false, nil
	}
	if len(cfg.Tunnels) > 1 {
		nextCfg, err := s.cfgMgr.DeleteTunnelProfile(profile.Key)
		if err != nil {
			return nil, false, err
		}
		s.removeRunnerProfile(profile.Key)
		summary := summarizeLocalTunnelProfile(profile, nextCfg.ActiveTunnelKey)
		return &summary, true, nil
	}
	profile.Token = ""
	profile.AccountID = ""
	profile.TunnelID = ""
	profile.RemoteManagementEnabled = false
	nextCfg, err := s.cfgMgr.SaveTunnelProfile(profile.Key, profile)
	if err != nil {
		return nil, false, err
	}
	s.removeRunnerProfile(profile.Key)
	saved, _ := nextCfg.TunnelProfile(profile.Key)
	summary := summarizeLocalTunnelProfile(saved, nextCfg.ActiveTunnelKey)
	return &summary, false, nil
}

func (s *Server) removeRunnerProfile(key string) {
	if s.runner == nil || strings.TrimSpace(key) == "" {
		return
	}
	go func() {
		_ = s.runner.RemoveProfile(key)
	}()
}

func findTunnelProfileByTunnelID(profiles []config.TunnelProfileConfig, tunnelID string) (config.TunnelProfileConfig, bool) {
	tunnelID = strings.TrimSpace(tunnelID)
	for _, profile := range profiles {
		if tunnelID != "" && profile.TunnelID == tunnelID {
			return profile, true
		}
	}
	return config.TunnelProfileConfig{}, false
}

func summarizeLocalTunnelProfile(profile config.TunnelProfileConfig, activeKey string) cfLocalTunnelProfileSummary {
	return cfLocalTunnelProfileSummary{
		Key:                     profile.Key,
		Name:                    profile.Name,
		AccountID:               profile.AccountID,
		TunnelID:                profile.TunnelID,
		LocalEnabled:            profile.LocalEnabled,
		RemoteManagementEnabled: profile.RemoteManagementEnabled,
		Active:                  profile.Key != "" && profile.Key == activeKey,
	}
}

func (s *Server) handleCFWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().Workers(r.Context(), r.URL.Query().Get("account_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFWorker(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/cf/workers/")
	if strings.HasSuffix(rest, "/metrics") {
		scriptName := strings.TrimSuffix(rest, "/metrics")
		if scriptName == "" || strings.Contains(scriptName, "/") {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("worker script name is required"))
			return
		}
		s.handleCFWorkerMetrics(w, r, scriptName)
		return
	}
	if strings.HasSuffix(rest, "/tail") {
		scriptName := strings.TrimSuffix(rest, "/tail")
		if scriptName == "" || strings.Contains(scriptName, "/") {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("worker script name is required"))
			return
		}
		s.handleCFWorkerTail(w, r, scriptName)
		return
	}
	if rest == "" || strings.Contains(rest, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("worker script name is required"))
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().Worker(r.Context(), r.URL.Query().Get("account_id"), rest)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFWorkerMetrics(w http.ResponseWriter, r *http.Request, scriptName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().WorkerMetrics(
		r.Context(),
		r.URL.Query().Get("account_id"),
		scriptName,
		r.URL.Query().Get("range"),
	)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFWorkerTail(w http.ResponseWriter, r *http.Request, scriptName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("streaming is not supported"))
		return
	}

	accountID := r.URL.Query().Get("account_id")
	tail, err := s.ensureCFService().StartWorkerTail(r.Context(), accountID, scriptName)
	if err != nil {
		writeCFResponse(w, nil, err)
		return
	}
	cleanup := func() {
		if tail.ID == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.ensureCFService().DeleteWorkerTail(ctx, accountID, scriptName, tail.ID)
	}

	if strings.TrimSpace(tail.URL) == "" {
		cleanup()
		writeAPIError(w, http.StatusBadGateway, fmt.Errorf("worker tail session returned no websocket URL"))
		return
	}
	tailURL, err := url.Parse(tail.URL)
	if err != nil || (tailURL.Scheme != "wss" && tailURL.Scheme != "ws") {
		cleanup()
		writeAPIError(w, http.StatusBadGateway, fmt.Errorf("worker tail session returned an invalid websocket URL"))
		return
	}

	conn, _, err := websocket.Dial(r.Context(), tail.URL, &websocket.DialOptions{Subprotocols: []string{"trace-v1"}})
	if err != nil {
		cleanup()
		writeAPIError(w, http.StatusBadGateway, fmt.Errorf("failed to connect worker tail stream"))
		return
	}
	defer func() {
		_ = conn.Close(websocket.StatusGoingAway, "cfui worker tail closed")
		cleanup()
	}()

	if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"filters":[],"debug":false}`)); err != nil {
		writeAPIError(w, http.StatusBadGateway, fmt.Errorf("failed to initialize worker tail stream"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := writeSSEJSON(w, "tail_open", map[string]any{
		"id":         tail.ID,
		"expires_at": tail.ExpiresAt,
	}); err != nil {
		return
	}
	flusher.Flush()

	ctx := r.Context()
	messages := make(chan []byte, 64)
	errs := make(chan error, 1)
	var dropped atomic.Int64
	go func() {
		defer close(messages)
		for {
			messageType, msg, err := conn.Read(ctx)
			if err != nil {
				if ctx.Err() == nil {
					select {
					case errs <- err:
					default:
					}
				}
				return
			}
			if messageType != websocket.MessageText && messageType != websocket.MessageBinary {
				continue
			}
			if !utf8.Valid(msg) {
				dropped.Add(1)
				continue
			}
			cp := append([]byte(nil), msg...)
			select {
			case messages <- cp:
			default:
				dropped.Add(1)
			}
		}
	}()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.shutdownC:
			return
		case msg, ok := <-messages:
			if !ok {
				select {
				case <-errs:
					_ = writeSSEJSON(w, "tail_error", map[string]string{"code": "stream_closed"})
					flusher.Flush()
				default:
				}
				return
			}
			if err := writeSSEJSON(w, "tail_message", map[string]string{"data": string(msg)}); err != nil {
				return
			}
			flusher.Flush()
		case <-errs:
			_ = writeSSEJSON(w, "tail_error", map[string]string{"code": "stream_closed"})
			flusher.Flush()
			return
		case <-heartbeat.C:
			if n := dropped.Swap(0); n > 0 {
				if err := writeSSEJSON(w, "tail_dropped", map[string]int64{"count": n}); err != nil {
					return
				}
			}
			if _, err := w.Write([]byte(": heartbeat\n\n")); err != nil {
				return
			}
			flusher.Flush()
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = conn.Ping(pingCtx)
			cancel()
		}
	}
}

func (s *Server) handleCFR2Metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().R2Metrics(r.Context(), r.URL.Query().Get("account_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFR2Buckets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := s.ensureCFService().R2Buckets(r.Context(), r.URL.Query().Get("account_id"))
		writeCFResponse(w, resp, err)
	case http.MethodPost:
		var req cfaccount.R2BucketRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		bucket, err := s.ensureCFService().CreateR2Bucket(r.Context(), r.URL.Query().Get("account_id"), req)
		writeCFResponse(w, bucket, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFR2Bucket(w http.ResponseWriter, r *http.Request) {
	bucketName := strings.TrimPrefix(r.URL.Path, "/api/cf/r2/buckets/")
	if bucketName == "" {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("bucket name is required"))
		return
	}
	switch r.Method {
	case http.MethodDelete:
		err := s.ensureCFService().DeleteR2Bucket(r.Context(), r.URL.Query().Get("account_id"), bucketName)
		writeCFResponse(w, map[string]bool{"success": true}, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFR2Objects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	resp, err := s.ensureCFService().R2Objects(
		r.Context(),
		r.URL.Query().Get("account_id"),
		r.URL.Query().Get("bucket"),
		r.URL.Query().Get("cursor"),
		limit,
	)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFR2Object(w http.ResponseWriter, r *http.Request) {
	accountID := r.URL.Query().Get("account_id")
	bucketName := r.URL.Query().Get("bucket")
	key := r.URL.Query().Get("key")
	switch r.Method {
	case http.MethodGet:
		resp, err := s.ensureCFService().R2ObjectValue(r.Context(), accountID, bucketName, key)
		writeCFResponse(w, resp, err)
	case http.MethodPut:
		var req cfaccount.R2ObjectValueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().WriteR2ObjectValue(r.Context(), accountID, bucketName, key, req)
		writeCFResponse(w, resp, err)
	case http.MethodDelete:
		err := s.ensureCFService().DeleteR2Object(r.Context(), accountID, bucketName, key)
		writeCFResponse(w, map[string]bool{"success": true}, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFR2ObjectUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.ContentLength < 0 {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("r2 object upload content length is required"))
		return
	}
	resp, err := s.ensureCFService().WriteR2ObjectStream(
		r.Context(),
		r.URL.Query().Get("account_id"),
		r.URL.Query().Get("bucket"),
		r.URL.Query().Get("key"),
		r.Header.Get("Content-Type"),
		r.ContentLength,
		r.Body,
	)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFR2ObjectUploadSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, session, err := s.ensureOAuthService().CurrentAccessToken(r.Context())
	if err != nil {
		writeCFResponse(w, nil, err)
		return
	}
	if !session.Capabilities["r2"].Write {
		writeAPIError(w, http.StatusForbidden, fmt.Errorf("workers-r2.write scope is required"))
		return
	}
	var req r2UploadStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	status, err := s.ensureR2UploadManager().start(req)
	if err != nil {
		writeR2UploadError(w, err)
		return
	}
	writeJSON(w, status)
}

func (s *Server) handleCFR2ObjectUploadSessionItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/cf/r2/object/upload-session/"), "/")
	if rest == "" {
		writeAPIError(w, http.StatusNotFound, fmt.Errorf("r2 upload session not found"))
		return
	}
	parts := strings.Split(rest, "/")
	uploadID := parts[0]
	switch {
	case r.Method == http.MethodDelete && len(parts) == 1:
		status, err := s.ensureR2UploadManager().abort(uploadID)
		if err != nil {
			writeR2UploadError(w, err)
			return
		}
		writeJSON(w, status)
	case r.Method == http.MethodPut && len(parts) == 3 && parts[1] == "chunks":
		index, err := strconv.Atoi(parts[2])
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("r2 upload chunk index is invalid"))
			return
		}
		status, err := s.ensureR2UploadManager().writeChunk(uploadID, index, r.ContentLength, r.Body)
		if err != nil {
			writeR2UploadError(w, err)
			return
		}
		writeJSON(w, status)
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "complete":
		resp, err := s.ensureR2UploadManager().complete(r.Context(), uploadID, s.ensureCFService())
		if err != nil {
			if isR2UploadManagerError(err) {
				writeR2UploadError(w, err)
				return
			}
			writeCFResponse(w, nil, err)
			return
		}
		writeJSON(w, resp)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeR2UploadError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	message := err.Error()
	switch {
	case strings.Contains(message, "not found"):
		status = http.StatusNotFound
	case strings.Contains(message, "too large"):
		status = http.StatusRequestEntityTooLarge
	case strings.Contains(message, "completing"):
		status = http.StatusConflict
	}
	writeAPIError(w, status, err)
}

func isR2UploadManagerError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.HasPrefix(message, "r2 upload") || strings.Contains(message, "chunk size") || strings.Contains(message, "cloudflare service is not configured")
}

func (s *Server) handleCFR2ObjectCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req cfaccount.R2ObjectCopyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.ensureCFService().CopyR2Object(
		r.Context(),
		r.URL.Query().Get("account_id"),
		r.URL.Query().Get("bucket"),
		req,
	)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFR2ObjectDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().R2ObjectDownload(
		r.Context(),
		r.URL.Query().Get("account_id"),
		r.URL.Query().Get("bucket"),
		r.URL.Query().Get("key"),
	)
	if err != nil {
		writeCFResponse(w, nil, err)
		return
	}
	defer resp.Body.Close()
	filename := path.Base(resp.Key)
	if filename == "." || filename == "/" || filename == "" {
		filename = "download"
	}
	w.Header().Set("Content-Type", resp.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", r2ObjectContentDisposition(resp.ContentType, filename, r.URL.Query().Get("preview") == "1"))
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if resp.ETag != "" {
		w.Header().Set("ETag", resp.ETag)
	}
	if resp.LastModified != "" {
		w.Header().Set("Last-Modified", resp.LastModified)
	}
	_, _ = io.Copy(w, resp.Body)
}

func r2ObjectContentDisposition(contentType, filename string, preview bool) string {
	disposition := "attachment"
	if preview && isInlineR2PreviewContentType(contentType) {
		disposition = "inline"
	}
	return mime.FormatMediaType(disposition, map[string]string{"filename": filename})
}

func isInlineR2PreviewContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	}
	switch strings.ToLower(mediaType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif",
		"audio/aac", "audio/flac", "audio/mpeg", "audio/mp4", "audio/ogg", "audio/wav", "audio/webm",
		"video/mp4", "video/ogg", "video/quicktime", "video/webm",
		"application/pdf":
		return true
	default:
		return false
	}
}

func (s *Server) handleCFD1Databases(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := s.ensureCFService().D1Databases(r.Context(), r.URL.Query().Get("account_id"))
		writeCFResponse(w, resp, err)
	case http.MethodPost:
		var req cfaccount.D1DatabaseCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().CreateD1Database(r.Context(), r.URL.Query().Get("account_id"), req)
		writeCFResponse(w, resp, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFD1Database(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	databaseID := strings.TrimPrefix(r.URL.Path, "/api/cf/d1/databases/")
	if databaseID == "" || strings.Contains(databaseID, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("database_id is required"))
		return
	}
	databaseID, err := url.PathUnescape(databaseID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(databaseID) == "" || strings.Contains(databaseID, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("database_id is required"))
		return
	}
	var resp cfaccount.D1DatabaseDetailResponse
	if r.Method == http.MethodDelete {
		resp, err = s.ensureCFService().DeleteD1Database(r.Context(), r.URL.Query().Get("account_id"), databaseID)
	} else {
		resp, err = s.ensureCFService().D1Database(r.Context(), r.URL.Query().Get("account_id"), databaseID)
	}
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFD1Query(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req cfaccount.D1QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.ensureCFService().QueryD1(
		r.Context(),
		r.URL.Query().Get("account_id"),
		r.URL.Query().Get("database_id"),
		req,
	)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFD1Tables(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().D1Tables(r.Context(), r.URL.Query().Get("account_id"), r.URL.Query().Get("database_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFD1Table(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := s.ensureCFService().D1Table(
			r.Context(),
			r.URL.Query().Get("account_id"),
			r.URL.Query().Get("database_id"),
			r.URL.Query().Get("table"),
			r.URL.Query().Get("limit"),
			r.URL.Query().Get("offset"),
		)
		writeCFResponse(w, resp, err)
	case http.MethodPatch:
		var req cfaccount.D1RowMutationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().UpdateD1Row(
			r.Context(),
			r.URL.Query().Get("account_id"),
			r.URL.Query().Get("database_id"),
			r.URL.Query().Get("table"),
			req,
		)
		writeCFResponse(w, resp, err)
	case http.MethodDelete:
		var req cfaccount.D1RowMutationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().DeleteD1Row(
			r.Context(),
			r.URL.Query().Get("account_id"),
			r.URL.Query().Get("database_id"),
			r.URL.Query().Get("table"),
			req,
		)
		writeCFResponse(w, resp, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFKVNamespaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().KVNamespaces(r.Context(), r.URL.Query().Get("account_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFKVKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	resp, err := s.ensureCFService().KVKeys(
		r.Context(),
		r.URL.Query().Get("account_id"),
		r.URL.Query().Get("namespace_id"),
		r.URL.Query().Get("prefix"),
		r.URL.Query().Get("cursor"),
		limit,
	)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFKVValue(w http.ResponseWriter, r *http.Request) {
	accountID := r.URL.Query().Get("account_id")
	namespaceID := r.URL.Query().Get("namespace_id")
	key := r.URL.Query().Get("key")
	switch r.Method {
	case http.MethodGet:
		resp, err := s.ensureCFService().KVValue(r.Context(), accountID, namespaceID, key)
		writeCFResponse(w, resp, err)
	case http.MethodPut:
		var req cfaccount.KVValueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().WriteKVValue(r.Context(), accountID, namespaceID, key, req)
		writeCFResponse(w, resp, err)
	case http.MethodDelete:
		err := s.ensureCFService().DeleteKVValue(r.Context(), accountID, namespaceID, key)
		writeCFResponse(w, map[string]bool{"success": true}, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFSnippets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := s.ensureCFService().Snippets(r.Context(), r.URL.Query().Get("zone_id"))
		writeCFResponse(w, resp, err)
	case http.MethodPost:
		var req cfaccount.SnippetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().CreateOrUpdateSnippet(r.Context(), r.URL.Query().Get("zone_id"), req)
		writeCFResponse(w, resp, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFSnippet(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/cf/snippets/")
	if strings.HasSuffix(name, "/content") {
		name = strings.TrimSuffix(name, "/content")
		if name == "" || strings.Contains(name, "/") {
			writeAPIError(w, http.StatusBadRequest, fmt.Errorf("snippet name is required"))
			return
		}
		s.handleCFSnippetContent(w, r, name)
		return
	}
	if name == "" || strings.Contains(name, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("snippet name is required"))
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	err := s.ensureCFService().DeleteSnippet(r.Context(), r.URL.Query().Get("zone_id"), name)
	writeCFResponse(w, map[string]bool{"success": true}, err)
}

func (s *Server) handleCFSnippetContent(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		resp, err := s.ensureCFService().SnippetContent(r.Context(), r.URL.Query().Get("zone_id"), name)
		writeCFResponse(w, resp, err)
	case http.MethodPut:
		var req cfaccount.SnippetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().WriteSnippetContent(r.Context(), r.URL.Query().Get("zone_id"), name, req)
		writeCFResponse(w, resp, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFSnippetRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := s.ensureCFService().SnippetRules(
			r.Context(),
			r.URL.Query().Get("zone_id"),
			r.URL.Query().Get("snippet_name"),
		)
		writeCFResponse(w, resp, err)
	case http.MethodPost:
		var req cfaccount.SnippetRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().CreateSnippetRule(r.Context(), r.URL.Query().Get("zone_id"), req)
		writeCFResponse(w, resp, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFSnippetRule(w http.ResponseWriter, r *http.Request) {
	ruleID := strings.TrimPrefix(r.URL.Path, "/api/cf/snippets/rules/")
	if ruleID == "" || strings.Contains(ruleID, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("snippet rule id is required"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req cfaccount.SnippetRuleUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().UpdateSnippetRule(r.Context(), r.URL.Query().Get("zone_id"), ruleID, req)
		writeCFResponse(w, resp, err)
	case http.MethodDelete:
		resp, err := s.ensureCFService().DeleteSnippetRule(r.Context(), r.URL.Query().Get("zone_id"), ruleID)
		writeCFResponse(w, resp, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFWAF(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().WAFRules(r.Context(), r.URL.Query().Get("zone_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFWAFManagedExceptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().WAFManagedExceptions(r.Context(), r.URL.Query().Get("zone_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFWAFManagedExceptionRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req cfaccount.WAFRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.ensureCFService().CreateWAFManagedException(r.Context(), r.URL.Query().Get("zone_id"), req)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFWAFManagedExceptionRule(w http.ResponseWriter, r *http.Request) {
	ruleID := strings.TrimPrefix(r.URL.Path, "/api/cf/waf/managed-exceptions/rules/")
	if ruleID == "" || strings.Contains(ruleID, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("waf managed exception id is required"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req cfaccount.WAFRuleUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().UpdateWAFManagedException(r.Context(), r.URL.Query().Get("zone_id"), ruleID, req)
		writeCFResponse(w, resp, err)
	case http.MethodDelete:
		resp, err := s.ensureCFService().DeleteWAFManagedException(r.Context(), r.URL.Query().Get("zone_id"), ruleID)
		writeCFResponse(w, resp, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFWAFManagedOverrides(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().WAFManagedOverrides(r.Context(), r.URL.Query().Get("zone_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFWAFManagedOverrideRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req cfaccount.WAFManagedOverrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.ensureCFService().CreateWAFManagedOverride(r.Context(), r.URL.Query().Get("zone_id"), req)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFWAFManagedOverrideRule(w http.ResponseWriter, r *http.Request) {
	ruleID := strings.TrimPrefix(r.URL.Path, "/api/cf/waf/managed-overrides/rules/")
	if ruleID == "" || strings.Contains(ruleID, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("waf managed override id is required"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req cfaccount.WAFManagedOverrideUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().UpdateWAFManagedOverride(r.Context(), r.URL.Query().Get("zone_id"), ruleID, req)
		writeCFResponse(w, resp, err)
	case http.MethodDelete:
		resp, err := s.ensureCFService().DeleteWAFManagedOverride(r.Context(), r.URL.Query().Get("zone_id"), ruleID)
		writeCFResponse(w, resp, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFWAFRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req cfaccount.WAFRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.ensureCFService().CreateWAFRule(r.Context(), r.URL.Query().Get("zone_id"), req)
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFWAFRule(w http.ResponseWriter, r *http.Request) {
	ruleID := strings.TrimPrefix(r.URL.Path, "/api/cf/waf/rules/")
	if ruleID == "" || strings.Contains(ruleID, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("waf rule id is required"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req cfaccount.WAFRuleUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.ensureCFService().UpdateWAFRule(r.Context(), r.URL.Query().Get("zone_id"), ruleID, req)
		writeCFResponse(w, resp, err)
	case http.MethodDelete:
		resp, err := s.ensureCFService().DeleteWAFRule(r.Context(), r.URL.Query().Get("zone_id"), ruleID)
		writeCFResponse(w, resp, err)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCFZoneAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().ZoneAnalytics(r.Context(), r.URL.Query().Get("zone_id"), r.URL.Query().Get("range"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFZoneSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().ZoneSettings(r.Context(), r.URL.Query().Get("zone_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) handleCFZoneSetting(w http.ResponseWriter, r *http.Request) {
	settingID := strings.TrimPrefix(r.URL.Path, "/api/cf/zone-settings/")
	if settingID == "" || strings.Contains(settingID, "/") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("setting id is required"))
		return
	}
	if r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req cfaccount.ZoneSettingUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	setting, err := s.ensureCFService().UpdateZoneSetting(r.Context(), r.URL.Query().Get("zone_id"), settingID, req)
	writeCFResponse(w, setting, err)
}

func (s *Server) handleCFCachePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp, err := s.ensureCFService().PurgeZoneCache(r.Context(), r.URL.Query().Get("zone_id"))
	writeCFResponse(w, resp, err)
}

func (s *Server) ensureCFService() *cfaccount.Service {
	s.ensureOAuthService()
	return s.cfSvc
}

func writeSSEJSON(w http.ResponseWriter, event string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := w.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n\n"))
	return err
}

func writeCFResponse(w http.ResponseWriter, payload any, err error) {
	if err == nil {
		writeJSON(w, payload)
		return
	}
	if cfoauth.IsAuthError(err) {
		writeAPIError(w, http.StatusUnauthorized, err)
		return
	}
	var validationErr cfaccount.ValidationError
	if errors.As(err, &validationErr) {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	writeAPIError(w, http.StatusBadGateway, err)
}
