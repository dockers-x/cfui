package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDDNSRecordsCreateStoresAutoAndFixedValues(t *testing.T) {
	s := newServerTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/ddns/records", strings.NewReader(`{
		"subdomain":"home",
		"zone_id":"zone-1",
		"zone_name":"example.com",
		"ipv4":true,
		"ipv6":true,
		"ipv4_value":"{IPV4}",
		"ipv6_value":"2001:db8::10",
		"proxied":true,
		"ttl":120
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleDDNSRecords(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create record status %d: %s", rec.Code, rec.Body.String())
	}

	cfg := s.cfgMgr.Get()
	if len(cfg.DDNS.Records) != 2 {
		t.Fatalf("expected 2 DDNS records, got %d", len(cfg.DDNS.Records))
	}

	if cfg.DDNS.Records[0].Type != "A" || cfg.DDNS.Records[0].Value != "{IPV4}" {
		t.Fatalf("unexpected IPv4 record: %#v", cfg.DDNS.Records[0])
	}
	if cfg.DDNS.Records[1].Type != "AAAA" || cfg.DDNS.Records[1].Value != "2001:db8::10" {
		t.Fatalf("unexpected IPv6 record: %#v", cfg.DDNS.Records[1])
	}

	var resp struct {
		Records []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"records"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Records) != 2 || resp.Records[0].Value != "{IPV4}" || resp.Records[1].Value != "2001:db8::10" {
		t.Fatalf("unexpected response payload: %#v", resp.Records)
	}
}

func TestDDNSRecordUpdateStoresEditedValue(t *testing.T) {
	s := newServerTestServer(t)

	createReq := httptest.NewRequest(http.MethodPost, "/api/ddns/records", strings.NewReader(`{
		"subdomain":"office",
		"zone_id":"zone-1",
		"zone_name":"example.com",
		"ipv4":true,
		"ipv6":false,
		"ipv4_value":"{IPV4}",
		"proxied":false,
		"ttl":1
	}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	s.handleDDNSRecords(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create record status %d: %s", createRec.Code, createRec.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/ddns/records/0", strings.NewReader(`{
		"subdomain":"office",
		"zone_id":"zone-1",
		"zone_name":"example.com",
		"value":"8.8.8.8",
		"proxied":true,
		"ttl":300
	}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	s.handleDDNSRecord(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update record status %d: %s", updateRec.Code, updateRec.Body.String())
	}

	cfg := s.cfgMgr.Get()
	if len(cfg.DDNS.Records) != 1 {
		t.Fatalf("expected 1 DDNS record, got %d", len(cfg.DDNS.Records))
	}
	got := cfg.DDNS.Records[0]
	if got.Name != "office.example.com" || got.Value != "8.8.8.8" || !got.Proxied || got.TTL != 300 {
		t.Fatalf("unexpected updated record: %#v", got)
	}
}
