package tunnelmgr

import (
	"cfui/internal/config"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

const (
	ruleCheckTimeout   = 5 * time.Second
	dnsCheckWorkers    = 3
	originCheckWorkers = 3
	dnsRecordsPageSize = 100
)

type RuleCheckRequest struct {
	Indexes []int `json:"indexes,omitempty"`
}

type RuleCheckResponse struct {
	TunnelKey string            `json:"tunnel_key,omitempty"`
	TunnelID  string            `json:"tunnel_id"`
	Version   int               `json:"version"`
	Results   []RuleCheckResult `json:"results"`
	Summary   RuleCheckSummary  `json:"summary"`
}

type RuleCheckResult struct {
	Index    int               `json:"index"`
	Hostname string            `json:"hostname"`
	DNS      DNSCheckResult    `json:"dns"`
	Origin   OriginCheckResult `json:"origin"`
}

type DNSCheckResult struct {
	Status  string `json:"status"`
	Type    string `json:"type,omitempty"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

type OriginCheckResult struct {
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

type RuleCheckSummary struct {
	Total         int `json:"total"`
	DNSOK         int `json:"dns_ok"`
	DNSMissing    int `json:"dns_missing"`
	DNSConflict   int `json:"dns_conflict"`
	DNSError      int `json:"dns_error"`
	OriginOK      int `json:"origin_ok"`
	OriginError   int `json:"origin_error"`
	OriginSkipped int `json:"origin_skipped"`
}

// CheckRulesFor performs read-only DNS and origin checks against the current
// remote ingress configuration. It never creates, updates, or deletes a
// Cloudflare resource.
func (m *Manager) CheckRulesFor(ctx context.Context, tunnelKey string, req RuleCheckRequest) (RuleCheckResponse, error) {
	cfg, client, err := m.clientFor(tunnelKey)
	if err != nil {
		return RuleCheckResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	current, err := client.GetTunnelConfiguration(ctx, cloudflare.AccountIdentifier(cfg.AccountID), cfg.TunnelID)
	if err != nil {
		return RuleCheckResponse{}, err
	}
	entries, err := selectRuleChecks(toConfigurationResponse(current).Entries, req.Indexes)
	if err != nil {
		return RuleCheckResponse{}, err
	}
	results := make([]RuleCheckResult, len(entries))
	for idx, entry := range entries {
		results[idx] = RuleCheckResult{
			Index:    entry.Index,
			Hostname: normalizeHostname(entry.Hostname),
			DNS:      DNSCheckResult{Status: "missing"},
			Origin:   OriginCheckResult{Status: "skipped"},
		}
	}
	m.checkRuleDNS(ctx, client, cfg, results)
	if err := ctx.Err(); err != nil {
		return RuleCheckResponse{}, fmt.Errorf("rule DNS checks: %w", err)
	}
	checkRuleOrigins(ctx, entries, results)
	if err := ctx.Err(); err != nil {
		return RuleCheckResponse{}, fmt.Errorf("rule origin checks: %w", err)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Index < results[j].Index })
	tunnelID := strings.TrimSpace(current.TunnelID)
	if tunnelID == "" {
		tunnelID = strings.TrimSpace(cfg.TunnelID)
	}
	return RuleCheckResponse{
		TunnelKey: strings.TrimSpace(tunnelKey),
		TunnelID:  tunnelID,
		Version:   current.Version,
		Results:   results,
		Summary:   summarizeRuleChecks(results),
	}, nil
}

func selectRuleChecks(entries []IngressRule, indexes []int) ([]IngressRule, error) {
	byIndex := make(map[int]IngressRule, len(entries))
	for _, entry := range entries {
		byIndex[entry.Index] = entry
	}
	if len(indexes) == 0 {
		selected := make([]IngressRule, 0, len(entries))
		for _, entry := range entries {
			if strings.TrimSpace(entry.Hostname) != "" {
				selected = append(selected, entry)
			}
		}
		return selected, nil
	}
	seen := make(map[int]bool, len(indexes))
	selected := make([]IngressRule, 0, len(indexes))
	for _, index := range indexes {
		entry, ok := byIndex[index]
		if !ok || strings.TrimSpace(entry.Hostname) == "" {
			return nil, fmt.Errorf("entry index %d is not a public hostname rule", index)
		}
		if !seen[index] {
			selected = append(selected, entry)
			seen[index] = true
		}
	}
	return selected, nil
}

func (m *Manager) checkRuleDNS(ctx context.Context, client cloudflareClient, cfg config.TunnelManagementConfig, results []RuleCheckResult) {
	if len(results) == 0 {
		return
	}
	zonesResp, err := client.ListZonesContext(ctx, cloudflare.WithZoneFilters("", cfg.AccountID, ""))
	if err != nil {
		for idx := range results {
			results[idx].DNS = DNSCheckResult{Status: "error", Error: err.Error()}
		}
		return
	}
	type dnsJob struct {
		position int
		zoneID   string
		hostname string
	}
	jobsList := make([]dnsJob, 0, len(results))
	for idx := range results {
		zone := findZoneForHostname(results[idx].Hostname, zonesResp.Result)
		if zone == nil {
			results[idx].DNS = DNSCheckResult{Status: "missing"}
			continue
		}
		jobsList = append(jobsList, dnsJob{position: idx, zoneID: zone.ID, hostname: results[idx].Hostname})
	}
	expected := strings.ToLower(strings.TrimSpace(cfg.TunnelID)) + ".cfargotunnel.com"
	if len(jobsList) == 0 {
		return
	}
	jobs := make(chan dnsJob)
	var wg sync.WaitGroup
	workers := min(dnsCheckWorkers, len(jobsList))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				records, err := listDNSRecordsByName(ctx, client, job.zoneID, job.hostname)
				if err != nil {
					results[job.position].DNS = DNSCheckResult{Status: "error", Error: err.Error()}
					continue
				}
				results[job.position].DNS = classifyDNSRecords(records, expected)
			}
		}()
	}
	for _, job := range jobsList {
		select {
		case jobs <- job:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

func listDNSRecordsByName(ctx context.Context, client cloudflareClient, zoneID, hostname string) ([]cloudflare.DNSRecord, error) {
	var records []cloudflare.DNSRecord
	for page := 1; ; page++ {
		batch, info, err := client.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{
			Name:       hostname,
			ResultInfo: cloudflare.ResultInfo{Page: page, PerPage: dnsRecordsPageSize},
		})
		if err != nil {
			return nil, err
		}
		records = append(records, batch...)
		if info == nil || info.TotalPages <= page || info.TotalPages == 0 {
			break
		}
	}
	return records, nil
}

func classifyDNSRecords(records []cloudflare.DNSRecord, expected string) DNSCheckResult {
	if len(records) == 0 {
		return DNSCheckResult{Status: "missing"}
	}
	first := records[0]
	result := DNSCheckResult{Status: "conflict", Type: first.Type, Content: first.Content}
	if len(records) != 1 || !strings.EqualFold(strings.TrimSpace(first.Type), "CNAME") ||
		!strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(first.Content), "."), expected) || first.Proxied == nil || !*first.Proxied {
		return result
	}
	result.Status = "ok"
	return result
}

func checkRuleOrigins(ctx context.Context, entries []IngressRule, results []RuleCheckResult) {
	if len(entries) == 0 {
		return
	}
	type job struct {
		position int
		entry    IngressRule
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	workers := min(originCheckWorkers, len(entries))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				results[item.position].Origin = checkOrigin(ctx, item.entry)
			}
		}()
	}
	for idx, entry := range entries {
		jobs <- job{position: idx, entry: entry}
	}
	close(jobs)
	wg.Wait()
}

func checkOrigin(parent context.Context, entry IngressRule) OriginCheckResult {
	service := strings.TrimSpace(entry.Service)
	parsed, err := url.Parse(service)
	if err != nil || parsed.Scheme == "" {
		return OriginCheckResult{Status: "error", Error: "invalid origin service"}
	}
	if parsed.Scheme == "http_status" {
		return OriginCheckResult{Status: "skipped"}
	}
	ctx, cancel := context.WithTimeout(parent, ruleCheckTimeout)
	defer cancel()
	started := time.Now()
	var result OriginCheckResult
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		result = checkHTTPOrigin(ctx, parsed, entry)
	} else {
		result = checkSocketOrigin(ctx, parsed)
	}
	result.LatencyMS = max(1, time.Since(started).Milliseconds())
	return result
}

func checkHTTPOrigin(ctx context.Context, target *url.URL, entry IngressRule) OriginCheckResult {
	tlsConfig := &tls.Config{InsecureSkipVerify: entry.NoTLSVerify} //nolint:gosec // explicit per-rule setting
	if name := strings.TrimSpace(entry.OriginServerName); name != "" {
		tlsConfig.ServerName = name
	}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: ruleCheckTimeout}).DialContext,
		TLSClientConfig:     tlsConfig,
		TLSHandshakeTimeout: ruleCheckTimeout,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   ruleCheckTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	if err != nil {
		return classifyOriginError(err)
	}
	if host := strings.TrimSpace(entry.HTTPHostHeader); host != "" {
		req.Host = host
	}
	resp, err := client.Do(req)
	if err != nil {
		return classifyOriginError(err)
	}
	resp.Body.Close()
	return OriginCheckResult{Status: "ok", HTTPStatus: resp.StatusCode}
}

func checkSocketOrigin(ctx context.Context, target *url.URL) OriginCheckResult {
	network := "tcp"
	address := target.Host
	if target.Scheme == "unix" {
		network = "unix"
		address = target.Path
	}
	if address == "" {
		return OriginCheckResult{Status: "error", Error: "origin address is empty"}
	}
	conn, err := (&net.Dialer{Timeout: ruleCheckTimeout}).DialContext(ctx, network, address)
	if err != nil {
		return classifyOriginError(err)
	}
	conn.Close()
	return OriginCheckResult{Status: "ok"}
}

func classifyOriginError(err error) OriginCheckResult {
	result := OriginCheckResult{Status: "error", Error: err.Error()}
	var dnsErr *net.DNSError
	var unknownAuthority x509.UnknownAuthorityError
	var certificateInvalid x509.CertificateInvalidError
	var hostnameErr x509.HostnameError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		result.Status = "timeout"
	case errors.As(err, &dnsErr):
		result.Status = "dns_error"
	case errors.As(err, &unknownAuthority), errors.As(err, &certificateInvalid), errors.As(err, &hostnameErr), strings.Contains(strings.ToLower(err.Error()), "tls"):
		result.Status = "tls_error"
	case errors.Is(err, syscall.ECONNREFUSED), strings.Contains(strings.ToLower(err.Error()), "connection refused"):
		result.Status = "connection_refused"
	default:
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			result.Status = "timeout"
		}
	}
	return result
}

func summarizeRuleChecks(results []RuleCheckResult) RuleCheckSummary {
	summary := RuleCheckSummary{Total: len(results)}
	for _, result := range results {
		switch result.DNS.Status {
		case "ok":
			summary.DNSOK++
		case "missing":
			summary.DNSMissing++
		case "conflict":
			summary.DNSConflict++
		default:
			summary.DNSError++
		}
		if result.Origin.Status == "ok" {
			summary.OriginOK++
		} else if result.Origin.Status == "skipped" {
			summary.OriginSkipped++
		} else {
			summary.OriginError++
		}
	}
	return summary
}
