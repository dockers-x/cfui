package cfoauth

import (
	"os"
	"strings"
)

const (
	defaultAuthorizationURL = "https://dash.cloudflare.com/oauth2/auth"
	defaultLogoutURL        = "https://dash.cloudflare.com/logout"
	defaultTokenURL         = "https://dash.cloudflare.com/oauth2/token"
	defaultRevokeURL        = "https://dash.cloudflare.com/oauth2/revoke"
	defaultUserInfoURL      = "https://dash.cloudflare.com/oauth2/userinfo"
	defaultRelayCallbackURL = "https://oauth.omarchy.qzz.io/oauth/callback"
)

type Config struct {
	ClientID          string `json:"client_id"`
	RelayCallbackURL  string `json:"relay_callback_url"`
	LocalCallbackPath string `json:"local_callback_path"`
	AuthorizationURL  string `json:"authorization_url"`
	LogoutURL         string `json:"logout_url"`
	TokenURL          string `json:"token_url"`
	RevokeURL         string `json:"revoke_url"`
	UserInfoURL       string `json:"userinfo_url"`
	Scopes            string `json:"scopes"`
	Configured        bool   `json:"configured"`
}

func ConfigFromEnv() Config {
	cfg := Config{
		ClientID:          strings.TrimSpace(os.Getenv("CFUI_OAUTH_CLIENT_ID")),
		RelayCallbackURL:  firstEnv("CFUI_OAUTH_RELAY_URL", "CFUI_OAUTH_REDIRECT_URI", defaultRelayCallbackURL),
		LocalCallbackPath: "/oauth/callback",
		AuthorizationURL:  firstEnv("CFUI_OAUTH_AUTH_URL", defaultAuthorizationURL),
		LogoutURL:         firstEnv("CFUI_OAUTH_LOGOUT_URL", defaultLogoutURL),
		TokenURL:          firstEnv("CFUI_OAUTH_TOKEN_URL", defaultTokenURL),
		RevokeURL:         firstEnv("CFUI_OAUTH_REVOKE_URL", defaultRevokeURL),
		UserInfoURL:       firstEnv("CFUI_OAUTH_USERINFO_URL", defaultUserInfoURL),
		Scopes:            firstEnv("CFUI_OAUTH_SCOPES", DefaultScopes()),
	}
	cfg.Configured = cfg.ClientID != "" && cfg.RelayCallbackURL != ""
	return cfg
}

func firstEnv(keysAndDefault ...string) string {
	if len(keysAndDefault) == 0 {
		return ""
	}
	defaultValue := keysAndDefault[len(keysAndDefault)-1]
	for _, key := range keysAndDefault[:len(keysAndDefault)-1] {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return defaultValue
}
