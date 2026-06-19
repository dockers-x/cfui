package cfoauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

const maxSessionLabelRunes = 96

type Service struct {
	cfg        Config
	store      *Store
	httpClient *http.Client
	refreshMu  sync.Mutex
}

type Status struct {
	Config       Config           `json:"config"`
	LoggedIn     bool             `json:"logged_in"`
	Sessions     []SessionSummary `json:"sessions"`
	Current      *SessionSummary  `json:"current,omitempty"`
	Capabilities CapabilityMatrix `json:"capabilities"`
}

type RelayCheck struct {
	RelayCallbackURL      string    `json:"relay_callback_url"`
	HealthURL             string    `json:"health_url"`
	Reachable             bool      `json:"reachable"`
	SupportsStateCallback bool      `json:"supports_state_callback"`
	StatusCode            int       `json:"status_code,omitempty"`
	Message               string    `json:"message,omitempty"`
	CheckedAt             time.Time `json:"checked_at"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type UserInfo struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

type StartURLOptions struct {
	Scopes      string
	FreshLogin  bool
	CallbackURL string
}

func NewService(cfg Config, store *Store) *Service {
	return &Service{
		cfg:        cfg,
		store:      store,
		httpClient: http.DefaultClient,
	}
}

func (s *Service) Config() Config {
	return s.cfg
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	sessions, err := s.store.ListSessions(ctx)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		Config:       s.publicConfig(),
		Sessions:     sessions,
		Capabilities: Capabilities(""),
	}
	for i := range sessions {
		if sessions[i].Current {
			current := sessions[i]
			status.Current = &current
			status.LoggedIn = true
			status.Capabilities = current.Capabilities
			break
		}
	}
	return status, nil
}

func relayHealthURL(relayCallbackURL string) (string, error) {
	raw := strings.TrimSpace(relayCallbackURL)
	if raw == "" {
		return "", fmt.Errorf("oauth relay URL is not configured")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid oauth relay URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("oauth relay URL must use http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("oauth relay URL must include a host")
	}
	u.Path = "/health"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func (s *Service) CheckRelay(ctx context.Context) (RelayCheck, error) {
	check := RelayCheck{
		RelayCallbackURL: s.publicConfig().RelayCallbackURL,
		CheckedAt:        time.Now().UTC(),
	}
	healthURL, err := relayHealthURL(check.RelayCallbackURL)
	if err != nil {
		check.Message = err.Error()
		return check, nil
	}
	check.HealthURL = healthURL

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		check.Message = err.Error()
		return check, nil
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		check.Message = err.Error()
		return check, nil
	}
	defer resp.Body.Close()
	check.StatusCode = resp.StatusCode
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	message := strings.TrimSpace(string(body))
	if len(message) > 240 {
		message = message[:240]
	}
	if message == "" {
		message = resp.Status
	}
	check.Message = message
	check.Reachable = resp.StatusCode >= 200 && resp.StatusCode <= 299
	check.SupportsStateCallback = relaySupportsStateCallback(resp.Header, message)
	return check, nil
}

func relaySupportsStateCallback(header http.Header, message string) bool {
	for _, value := range header.Values("X-CFUI-OAuth-Relay") {
		if strings.EqualFold(strings.TrimSpace(value), "state-v1") {
			return true
		}
	}
	return strings.Contains(strings.ToLower(message), "state-v1")
}

func (s *Service) StartURL(ctx context.Context) (string, error) {
	return s.StartURLWithOptions(ctx, StartURLOptions{})
}

func (s *Service) StartURLWithScopes(ctx context.Context, requestedScopes string) (string, error) {
	return s.StartURLWithOptions(ctx, StartURLOptions{Scopes: requestedScopes})
}

func (s *Service) StartURLWithOptions(ctx context.Context, opts StartURLOptions) (string, error) {
	if !s.cfg.Configured {
		return "", fmt.Errorf("CFUI_OAUTH_CLIENT_ID is required")
	}
	scopes := strings.TrimSpace(s.cfg.Scopes)
	if strings.TrimSpace(opts.Scopes) != "" {
		normalized, err := NormalizeRequestedScopes(opts.Scopes)
		if err != nil {
			return "", err
		}
		scopes = normalized
	}
	if scopes == "" {
		return "", fmt.Errorf("oauth scopes are required")
	}
	stateID, err := randomURLToken(32)
	if err != nil {
		return "", err
	}
	state, err := encodeRelayState(stateID, opts.CallbackURL, s.cfg.LocalCallbackPath)
	if err != nil {
		return "", err
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return "", err
	}
	pending := PendingState{
		State:        state,
		CodeVerifier: verifier,
		RedirectURI:  s.cfg.RelayCallbackURL,
		Scope:        scopes,
		ExpiresAt:    time.Now().UTC().Add(10 * time.Minute),
	}
	if err := s.store.SaveState(ctx, pending); err != nil {
		return "", err
	}
	u, err := url.Parse(s.cfg.AuthorizationURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", s.cfg.ClientID)
	q.Set("redirect_uri", s.cfg.RelayCallbackURL)
	q.Set("scope", scopes)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	authorizeURL := u.String()
	if !opts.FreshLogin {
		return authorizeURL, nil
	}
	logoutURL, err := wrapFreshLoginURL(s.cfg.LogoutURL, authorizeURL)
	if err != nil {
		return "", err
	}
	return logoutURL, nil
}

func wrapFreshLoginURL(logoutURL, authorizeURL string) (string, error) {
	raw := strings.TrimSpace(logoutURL)
	if raw == "" {
		return "", fmt.Errorf("oauth logout URL is required for fresh login")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid oauth logout URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("oauth logout URL must use http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("oauth logout URL must include a host")
	}
	q := u.Query()
	q.Set("to", authorizeURL)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *Service) CompleteCallback(ctx context.Context, code, state string) (SessionSummary, error) {
	pending, err := s.store.ConsumeState(ctx, state)
	if err != nil {
		return SessionSummary{}, err
	}
	token, err := s.requestToken(ctx, map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     s.cfg.ClientID,
		"code":          strings.TrimSpace(code),
		"redirect_uri":  pending.RedirectURI,
		"code_verifier": pending.CodeVerifier,
	})
	if err != nil {
		return SessionSummary{}, err
	}
	sessionID, err := randomURLToken(18)
	if err != nil {
		return SessionSummary{}, err
	}
	scope := strings.TrimSpace(token.Scope)
	if scope == "" {
		scope = pending.Scope
	}
	session := Session{
		ID:           sessionID,
		Label:        "Cloudflare Account",
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second),
		Scope:        scope,
		Current:      true,
	}
	if label := s.fetchIdentityLabel(ctx, token.AccessToken); label != "" {
		session.Label = label
	}
	if err := s.store.SaveSession(ctx, session); err != nil {
		return SessionSummary{}, err
	}
	return summarize(session), nil
}

func (s *Service) Logout(ctx context.Context, sessionID string, revoke bool) error {
	if revoke {
		if sessionID == "" {
			if current, err := s.store.CurrentSession(ctx); err == nil {
				_ = s.revoke(ctx, current)
			}
		} else if session, err := s.store.Session(ctx, sessionID); err == nil {
			_ = s.revoke(ctx, session)
		}
	}
	return s.store.DeleteSession(ctx, sessionID)
}

func (s *Service) SwitchSession(ctx context.Context, sessionID string) (Status, error) {
	if err := s.store.SwitchSession(ctx, sessionID); err != nil {
		return Status{}, err
	}
	return s.Status(ctx)
}

func (s *Service) UpdateSessionLabel(ctx context.Context, sessionID, label string) (Status, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return Status{}, fmt.Errorf("oauth identity label is required")
	}
	if utf8.RuneCountInString(label) > maxSessionLabelRunes {
		return Status{}, fmt.Errorf("oauth identity label must be %d characters or fewer", maxSessionLabelRunes)
	}
	if err := s.store.UpdateSessionLabel(ctx, sessionID, label); err != nil {
		return Status{}, err
	}
	return s.Status(ctx)
}

func (s *Service) CurrentClient(ctx context.Context) (*cloudflare.API, SessionSummary, error) {
	session, err := s.validSession(ctx)
	if err != nil {
		return nil, SessionSummary{}, err
	}
	client, err := cloudflare.NewWithAPIToken(session.AccessToken)
	if err != nil {
		return nil, SessionSummary{}, err
	}
	return client, summarize(session), nil
}

func (s *Service) CurrentAccessToken(ctx context.Context) (string, SessionSummary, error) {
	session, err := s.validSession(ctx)
	if err != nil {
		return "", SessionSummary{}, err
	}
	return session.AccessToken, summarize(session), nil
}

func (s *Service) validSession(ctx context.Context) (Session, error) {
	session, err := s.store.CurrentSession(ctx)
	if err != nil {
		return Session{}, err
	}
	if time.Until(session.ExpiresAt) > 60*time.Second {
		return session, nil
	}
	if strings.TrimSpace(session.RefreshToken) == "" {
		return Session{}, ErrNotLoggedIn
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	session, err = s.store.CurrentSession(ctx)
	if err != nil {
		return Session{}, err
	}
	if time.Until(session.ExpiresAt) > 60*time.Second {
		return session, nil
	}
	token, err := s.requestToken(ctx, map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     s.cfg.ClientID,
		"refresh_token": session.RefreshToken,
	})
	if err != nil {
		return Session{}, err
	}
	session.AccessToken = token.AccessToken
	if strings.TrimSpace(token.RefreshToken) != "" {
		session.RefreshToken = token.RefreshToken
	}
	session.ExpiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	if strings.TrimSpace(token.Scope) != "" {
		session.Scope = token.Scope
	}
	if err := s.store.UpdateToken(ctx, session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Service) requestToken(ctx context.Context, params map[string]string) (TokenResponse, error) {
	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return TokenResponse{}, fmt.Errorf("oauth token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var token TokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return TokenResponse{}, err
	}
	if token.AccessToken == "" {
		return TokenResponse{}, fmt.Errorf("oauth token endpoint returned no access token")
	}
	return token, nil
}

func (s *Service) fetchIdentityLabel(ctx context.Context, accessToken string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.UserInfoURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var info UserInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return ""
	}
	if strings.TrimSpace(info.Email) != "" {
		return strings.TrimSpace(info.Email)
	}
	return strings.TrimSpace(info.Name)
}

func (s *Service) revoke(ctx context.Context, session Session) error {
	token := strings.TrimSpace(session.RefreshToken)
	if token == "" {
		token = strings.TrimSpace(session.AccessToken)
	}
	if token == "" {
		return nil
	}
	_, err := s.requestForm(ctx, s.cfg.RevokeURL, map[string]string{
		"client_id": s.cfg.ClientID,
		"token":     token,
	})
	return err
}

func (s *Service) requestForm(ctx context.Context, target string, params map[string]string) ([]byte, error) {
	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("oauth endpoint returned %d", resp.StatusCode)
	}
	return body, nil
}

func (s *Service) publicConfig() Config {
	cfg := s.cfg
	cfg.ClientID = ""
	return cfg
}

func IsAuthError(err error) bool {
	return errors.Is(err, ErrNotLoggedIn) || errors.Is(err, ErrStateExpired)
}
