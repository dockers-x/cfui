package server

import (
	"bytes"
	"cfui/internal/cfoauth"
	"cfui/internal/cloudflared"
	"cfui/internal/config"
	"cfui/internal/configbackup"
	"cfui/internal/ddns"
	"cfui/internal/logger"
	"cfui/version"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"
)

const (
	maxBackupExportRequestBytes = 64 << 10
	maxBackupTextFieldBytes     = 64 << 10
	maxBackupMultipartOverhead  = 128 << 10
)

var errConfigBackupTooLarge = errors.New("config backup request too large")

type configBackupRuntimeHooks struct {
	saveConfig    func(config.Config) error
	removeProfile func(string) error
	profileStatus func(string) (cloudflared.Status, bool)
	restartDDNS   func()
	restartS3     func(context.Context) error
	resetOAuth    func()
}

type configBackupExportRequest struct {
	Sections                  []configbackup.Section `json:"sections"`
	IncludeSensitive          bool                   `json:"include_sensitive"`
	Password                  string                 `json:"password"`
	ConfirmPlaintextSensitive bool                   `json:"confirm_plaintext_sensitive"`
}

type configBackupImportResponse struct {
	ChangedSections []configbackup.Section `json:"changed_sections"`
	Warnings        []string               `json:"warnings,omitempty"`
	StopRequested   []string               `json:"stop_requested,omitempty"`
	RestartRequired []string               `json:"restart_required,omitempty"`
}

type configBackupErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type configBackupMultipart struct {
	File        []byte
	Password    string
	Sections    []configbackup.Section
	SectionsSet bool
}

func (s *Server) handleConfigBackupExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireConfigBackupSameOrigin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupExportRequestBytes)
	var request configBackupExportRequest
	if err := decodeStrictJSON(r.Body, &request); err != nil {
		if isMaxBytesError(err) {
			writeConfigBackupError(w, http.StatusRequestEntityTooLarge, "too_large")
			return
		}
		writeConfigBackupError(w, http.StatusBadRequest, "invalid_backup")
		return
	}
	if request.IncludeSensitive && request.Password == "" && !request.ConfirmPlaintextSensitive {
		writeConfigBackupError(w, http.StatusBadRequest, "plaintext_sensitive_confirmation_required")
		return
	}
	payload, err := configbackup.Build(s.cfgMgr.Get(), configbackup.ExportOptions{
		Sections:         request.Sections,
		IncludeSensitive: request.IncludeSensitive,
	}, version.GetVersion(), time.Now())
	if err != nil {
		writeConfigBackupMappedError(w, err)
		return
	}
	data, err := configbackup.Encode(payload, request.Password, rand.Reader)
	if err != nil {
		if errors.Is(err, configbackup.ErrTooLarge) {
			writeConfigBackupMappedError(w, err)
			return
		}
		writeConfigBackupError(w, http.StatusInternalServerError, "export_failed")
		return
	}
	filename := "cfui-backup-" + time.Now().UTC().Format("20060102T150405Z") + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleConfigBackupInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireConfigBackupSameOrigin(w, r) {
		return
	}
	request, err := readConfigBackupMultipart(w, r)
	if err != nil {
		writeConfigBackupMappedError(w, err)
		return
	}
	decoded, err := configbackup.Decode(request.File, request.Password)
	if err != nil {
		writeConfigBackupMappedError(w, err)
		return
	}
	inspection := configbackup.Inspect(decoded)
	if request.SectionsSet {
		preview, err := configbackup.Apply(s.cfgMgr.Get(), decoded.Payload, request.Sections)
		if err != nil {
			writeConfigBackupMappedError(w, err)
			return
		}
		preview.Config, err = s.validateConfigBackupCandidate(preview.Config)
		if err != nil {
			writeConfigBackupMappedError(w, configbackup.ErrInvalidBackup)
			return
		}
		inspection.Warnings = preview.Warnings
		inspection.RemovedTunnels = preview.RemovedTunnelKeys
		inspection.RestartRequired = runningTunnelKeys(s.configBackupRuntime().profileStatus, preview.ChangedTunnelKeys)
	}
	writeJSON(w, inspection)
}

func (s *Server) handleConfigBackupImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireConfigBackupSameOrigin(w, r) {
		return
	}
	request, err := readConfigBackupMultipart(w, r)
	if err != nil {
		writeConfigBackupMappedError(w, err)
		return
	}
	if !request.SectionsSet {
		writeConfigBackupError(w, http.StatusBadRequest, "invalid_selection")
		return
	}
	decoded, err := configbackup.Decode(request.File, request.Password)
	if err != nil {
		writeConfigBackupMappedError(w, err)
		return
	}
	before := s.cfgMgr.Get()
	result, err := configbackup.Apply(before, decoded.Payload, request.Sections)
	if err != nil {
		writeConfigBackupMappedError(w, err)
		return
	}
	result.Config, err = s.validateConfigBackupCandidate(result.Config)
	if err != nil {
		writeConfigBackupMappedError(w, configbackup.ErrInvalidBackup)
		return
	}
	hooks := s.configBackupRuntime()
	runningBefore := runningTunnelSet(hooks.profileStatus, before.Tunnels)
	authorized, err := s.localAuthStreamAuthorized(r)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, errors.New("local authentication is unavailable"))
		return
	}
	if !authorized {
		writeAPIError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}
	if err := hooks.saveConfig(result.Config); err != nil {
		writeConfigBackupError(w, http.StatusInternalServerError, "save_failed")
		return
	}
	saved := s.cfgMgr.Get()
	diff := configbackup.DiffTunnels(before, saved)
	restartRequired := filterRunningTunnelKeys(runningBefore, diff.ChangedKeys)

	for _, key := range diff.RemovedKeys {
		key := key
		go func() {
			if err := hooks.removeProfile(key); err != nil && logger.Sugar != nil {
				logger.Sugar.Warnf("Failed to remove tunnel profile %q after configuration import", key)
			}
		}()
	}
	if !reflect.DeepEqual(before.DDNS, saved.DDNS) {
		hooks.restartDDNS()
	}
	warnings := append([]string(nil), result.Warnings...)
	if !reflect.DeepEqual(before.S3WebDAV, saved.S3WebDAV) {
		if err := hooks.restartS3(context.Background()); err != nil {
			warnings = append(warnings, "S3 WebDAV runtime reconciliation failed")
			if logger.Sugar != nil {
				logger.Sugar.Warnf("Failed to reconcile S3 WebDAV after configuration import: %v", err)
			}
		}
	}
	if before.OAuthClientID != saved.OAuthClientID || before.OAuthRelayCallbackURL != saved.OAuthRelayCallbackURL {
		hooks.resetOAuth()
	}

	writeJSON(w, configBackupImportResponse{
		ChangedSections: result.ChangedSections,
		Warnings:        warnings,
		StopRequested:   diff.RemovedKeys,
		RestartRequired: restartRequired,
	})
}

func (s *Server) configBackupRuntime() configBackupRuntimeHooks {
	hooks := s.backupHooks
	if hooks.saveConfig == nil {
		hooks.saveConfig = s.cfgMgr.Save
	}
	if hooks.removeProfile == nil {
		hooks.removeProfile = func(key string) error {
			if s.runner == nil {
				return nil
			}
			return s.runner.RemoveProfile(key)
		}
	}
	if hooks.profileStatus == nil {
		hooks.profileStatus = func(key string) (cloudflared.Status, bool) {
			if s.runner == nil {
				return cloudflared.Status{}, false
			}
			return s.runner.ProfileStatus(key)
		}
	}
	if hooks.restartDDNS == nil {
		hooks.restartDDNS = func() {
			if s.ddnsSvc != nil {
				s.ddnsSvc.Restart()
			}
		}
	}
	if hooks.restartS3 == nil {
		hooks.restartS3 = func(ctx context.Context) error {
			if s.s3WebDAV != nil && s.s3WebDAV.isRunning() {
				return s.restartS3WebDAVDedicated(ctx)
			}
			return s.reconcileS3WebDAVDedicated(ctx, false)
		}
	}
	if hooks.resetOAuth == nil {
		hooks.resetOAuth = func() { s.resetOAuthService() }
	}
	return hooks
}

func (s *Server) validateConfigBackupCandidate(cfg config.Config) (config.Config, error) {
	if cfg.DDNS.Enabled && !cfg.ActiveTunnelProfile().RemoteManagementEnabled {
		return config.Config{}, errors.New("DDNS requires Remote Tunnel Manager to be enabled")
	}
	if cfg.DDNS.IntervalMins < 1 {
		cfg.DDNS.IntervalMins = 1
	} else if cfg.DDNS.IntervalMins > 60 {
		cfg.DDNS.IntervalMins = 60
	}
	if cfg.DDNS.MaxRetries < 1 {
		cfg.DDNS.MaxRetries = 1
	} else if cfg.DDNS.MaxRetries > 10 {
		cfg.DDNS.MaxRetries = 10
	}
	cfg.DDNS.Records = ddns.NormalizeRecords(cfg.DDNS.Records)
	for i := range cfg.DDNS.Records {
		value, err := ddns.ValidateRecordValue(cfg.DDNS.Records[i].Type, cfg.DDNS.Records[i].Value)
		if err != nil {
			return config.Config{}, fmt.Errorf("DDNS record %d: %w", i, err)
		}
		cfg.DDNS.Records[i].Value = value
	}
	clientID, err := cfoauth.NormalizeClientID(cfg.OAuthClientID)
	if err != nil {
		return config.Config{}, err
	}
	cfg.OAuthClientID = clientID
	if strings.TrimSpace(cfg.OAuthRelayCallbackURL) != "" {
		relayURL, err := cfoauth.NormalizeRelayCallbackURL(cfg.OAuthRelayCallbackURL)
		if err != nil {
			return config.Config{}, err
		}
		cfg.OAuthRelayCallbackURL = relayURL
	}
	cfg.S3WebDAV, err = s.s3Svc.ValidateConfig(cfg.S3WebDAV)
	if err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func readConfigBackupMultipart(w http.ResponseWriter, r *http.Request) (configBackupMultipart, error) {
	limit := int64(configbackup.MaxBackupBytes + maxBackupTextFieldBytes + maxBackupMultipartOverhead)
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	reader, err := r.MultipartReader()
	if err != nil {
		return configBackupMultipart{}, configbackup.ErrInvalidBackup
	}
	var request configBackupMultipart
	seen := map[string]bool{}
	textBytes := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isMaxBytesError(err) {
				return configBackupMultipart{}, errConfigBackupTooLarge
			}
			return configBackupMultipart{}, configbackup.ErrInvalidBackup
		}
		name := part.FormName()
		if seen[name] || (name != "file" && name != "password" && name != "sections") {
			_ = part.Close()
			return configBackupMultipart{}, configbackup.ErrInvalidBackup
		}
		seen[name] = true
		switch name {
		case "file":
			request.File, err = readPartLimited(part, configbackup.MaxBackupBytes)
		case "password", "sections":
			remaining := maxBackupTextFieldBytes - textBytes
			if remaining < 0 {
				err = errConfigBackupTooLarge
				break
			}
			var data []byte
			data, err = readPartLimited(part, remaining)
			textBytes += len(data)
			if name == "password" {
				request.Password = string(data)
			} else if err == nil {
				request.SectionsSet = true
				err = decodeStrictJSON(bytes.NewReader(data), &request.Sections)
				if err != nil {
					err = configbackup.ErrInvalidSelection
				}
			}
		}
		_ = part.Close()
		if err != nil {
			if isMaxBytesError(err) || errors.Is(err, errConfigBackupTooLarge) {
				return configBackupMultipart{}, errConfigBackupTooLarge
			}
			return configBackupMultipart{}, err
		}
	}
	if len(request.File) == 0 {
		return configBackupMultipart{}, configbackup.ErrInvalidBackup
	}
	return request, nil
}

func readPartLimited(reader io.Reader, max int) ([]byte, error) {
	if max < 0 {
		return nil, errConfigBackupTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > max {
		return nil, errConfigBackupTooLarge
	}
	return data, nil
}

func decodeStrictJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func requireConfigBackupSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		writeConfigBackupError(w, http.StatusForbidden, "cross_site_request")
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		!strings.EqualFold(parsed.Scheme, configBackupRequestScheme(r)) ||
		!strings.EqualFold(parsed.Host, requestExternalHost(r)) {
		writeConfigBackupError(w, http.StatusForbidden, "cross_site_request")
		return false
	}
	return true
}

func requestExternalHost(r *http.Request) string {
	if forwarded := firstForwardedHeaderValue(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		return forwarded
	}
	return strings.TrimSpace(r.Host)
}

func configBackupRequestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if scheme := strings.ToLower(strings.TrimSpace(r.URL.Scheme)); scheme == "http" || scheme == "https" {
		return scheme
	}
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); strings.EqualFold(forwarded, "https") {
		return "https"
	}
	return "http"
}

func isMaxBytesError(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

func writeConfigBackupMappedError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errConfigBackupTooLarge):
		writeConfigBackupError(w, http.StatusRequestEntityTooLarge, "too_large")
	case errors.Is(err, configbackup.ErrTooLarge):
		writeConfigBackupError(w, http.StatusRequestEntityTooLarge, "too_large")
	case errors.Is(err, configbackup.ErrUnsupportedVersion):
		writeConfigBackupError(w, http.StatusBadRequest, "unsupported_version")
	case errors.Is(err, configbackup.ErrPasswordRequired):
		writeConfigBackupError(w, http.StatusBadRequest, "password_required")
	case errors.Is(err, configbackup.ErrInvalidPasswordOrTampered):
		writeConfigBackupError(w, http.StatusBadRequest, "invalid_password_or_tampered")
	case errors.Is(err, configbackup.ErrInvalidSelection):
		writeConfigBackupError(w, http.StatusBadRequest, "invalid_selection")
	default:
		writeConfigBackupError(w, http.StatusBadRequest, "invalid_backup")
	}
}

func writeConfigBackupError(w http.ResponseWriter, status int, code string) {
	messages := map[string]string{
		"invalid_backup":                            "The backup file or request is invalid.",
		"unsupported_version":                       "The backup version is not supported.",
		"password_required":                         "A password is required for this backup.",
		"invalid_password_or_tampered":              "The password is incorrect or the backup was modified.",
		"plaintext_sensitive_confirmation_required": "Plaintext export of sensitive credentials requires confirmation.",
		"too_large":                                 "The backup exceeds the allowed size.",
		"invalid_selection":                         "Select at least one available backup section.",
		"save_failed":                               "The imported configuration could not be saved.",
		"export_failed":                             "The configuration backup could not be created.",
		"cross_site_request":                        "Cross-site configuration backup requests are not allowed.",
	}
	message := messages[code]
	if strings.TrimSpace(message) == "" {
		message = messages["invalid_backup"]
		code = "invalid_backup"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(configBackupErrorResponse{Error: message, Code: code})
}

func runningTunnelKeys(status func(string) (cloudflared.Status, bool), keys []string) []string {
	var running []string
	for _, key := range keys {
		if value, ok := status(key); ok && value.Running {
			running = append(running, key)
		}
	}
	slices.Sort(running)
	return slices.Compact(running)
}

func runningTunnelSet(status func(string) (cloudflared.Status, bool), tunnels []config.TunnelProfileConfig) map[string]bool {
	running := make(map[string]bool)
	for _, tunnel := range tunnels {
		if value, ok := status(tunnel.Key); ok && value.Running {
			running[tunnel.Key] = true
		}
	}
	return running
}

func filterRunningTunnelKeys(running map[string]bool, keys []string) []string {
	filtered := make([]string, 0, len(keys))
	for _, key := range keys {
		if running[key] {
			filtered = append(filtered, key)
		}
	}
	slices.Sort(filtered)
	return slices.Compact(filtered)
}
