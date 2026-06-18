package cfoauth

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	maxOAuthScopeCount = 64
	maxOAuthScopeBytes = 2048
)

var oauthScopeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Capability struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
}

type CapabilityMatrix map[string]Capability

type FeatureScope struct {
	ID          string
	ReadScopes  []string
	WriteScopes []string
}

var featureScopes = []FeatureScope{
	{ID: "account", ReadScopes: []string{"account-settings.read"}},
	{ID: "zones", ReadScopes: []string{"zone.read"}, WriteScopes: []string{"zone.write"}},
	{ID: "dns", ReadScopes: []string{"dns.read"}, WriteScopes: []string{"dns.write"}},
	{ID: "workers", ReadScopes: []string{"workers-scripts.read"}, WriteScopes: []string{"workers-scripts.write"}},
	{ID: "workers_tail", ReadScopes: []string{"workers-tail.read"}},
	{ID: "snippets", ReadScopes: []string{"snippets.read"}, WriteScopes: []string{"snippets.write"}},
	{ID: "r2", ReadScopes: []string{"workers-r2.read"}, WriteScopes: []string{"workers-r2.write"}},
	{ID: "d1", ReadScopes: []string{"d1.read"}, WriteScopes: []string{"d1.write"}},
	{ID: "kv", ReadScopes: []string{"workers-kv-storage.read"}, WriteScopes: []string{"workers-kv-storage.write"}},
	{
		ID: "tunnels",
		ReadScopes: []string{
			"argotunnel.read",
			"cloudflare-one-connectors.read",
			"cloudflare-one-connector-cloudflared.read",
			"cloudflare-tunnel.read",
		},
		WriteScopes: []string{
			"argotunnel.write",
			"cloudflare-one-connectors.write",
			"cloudflare-one-connector-cloudflared.write",
			"cloudflare-tunnel.write",
		},
	},
	{ID: "waf", ReadScopes: []string{"zone-waf.read"}, WriteScopes: []string{"zone-waf.write"}},
	{ID: "zone_settings", ReadScopes: []string{"zone-settings.read"}, WriteScopes: []string{"zone-settings.write", "cache.purge"}},
	{ID: "analytics", ReadScopes: []string{"account-analytics.read", "analytics.read"}},
}

func Capabilities(scope string) CapabilityMatrix {
	granted := scopeSet(scope)
	matrix := make(CapabilityMatrix, len(featureScopes))
	for _, feature := range featureScopes {
		matrix[feature.ID] = Capability{
			Read:  hasAnyScope(granted, feature.ReadScopes),
			Write: hasAnyScope(granted, feature.WriteScopes),
		}
	}
	return matrix
}

func scopeSet(scope string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range strings.Fields(scope) {
		out[strings.ToLower(strings.TrimSpace(item))] = struct{}{}
	}
	return out
}

func hasAnyScope(granted map[string]struct{}, scopes []string) bool {
	if len(scopes) == 0 {
		return false
	}
	for _, scope := range scopes {
		if _, ok := granted[strings.ToLower(scope)]; ok {
			return true
		}
	}
	return false
}

func DefaultScopes() string {
	return strings.Join([]string{
		"account-settings.read",
		"zone.read",
		"dns.read",
		"dns.write",
		"argotunnel.read",
	}, " ")
}

func NormalizeRequestedScopes(scope string) (string, error) {
	if len(scope) > maxOAuthScopeBytes {
		return "", fmt.Errorf("oauth scopes are too long")
	}
	seen := map[string]string{}
	for _, item := range strings.Fields(scope) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !oauthScopeTokenPattern.MatchString(item) {
			return "", fmt.Errorf("invalid oauth scope %q", item)
		}
		seen[strings.ToLower(item)] = item
	}
	if len(seen) == 0 {
		return "", fmt.Errorf("oauth scopes are required")
	}
	if len(seen) > maxOAuthScopeCount {
		return "", fmt.Errorf("too many oauth scopes")
	}
	scopes := make([]string, 0, len(seen))
	for _, scope := range seen {
		scopes = append(scopes, scope)
	}
	sort.Slice(scopes, func(i, j int) bool {
		return strings.ToLower(scopes[i]) < strings.ToLower(scopes[j])
	})
	return strings.Join(scopes, " "), nil
}
