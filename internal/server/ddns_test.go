package server

import (
	"cfui/internal/config"
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
		"comment":"home ddns",
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

	if cfg.DDNS.Records[0].Type != "A" || cfg.DDNS.Records[0].Value != "{IPV4}" || cfg.DDNS.Records[0].Comment != "home ddns" {
		t.Fatalf("unexpected IPv4 record: %#v", cfg.DDNS.Records[0])
	}
	if cfg.DDNS.Records[1].Type != "AAAA" || cfg.DDNS.Records[1].Value != "2001:db8::10" || cfg.DDNS.Records[1].Comment != "home ddns" {
		t.Fatalf("unexpected IPv6 record: %#v", cfg.DDNS.Records[1])
	}

	var resp struct {
		Records []struct {
			Type    string `json:"type"`
			Value   string `json:"value"`
			Comment string `json:"comment"`
		} `json:"records"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Records) != 2 || resp.Records[0].Value != "{IPV4}" || resp.Records[0].Comment != "home ddns" || resp.Records[1].Value != "2001:db8::10" {
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
		"comment":"office ddns",
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
	if got.Name != "office.example.com" || got.Value != "8.8.8.8" || got.Comment != "office ddns" || !got.Proxied || got.TTL != 300 {
		t.Fatalf("unexpected updated record: %#v", got)
	}
}

func TestDDNSRecordsDefaultComment(t *testing.T) {
	s := newServerTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/ddns/records", strings.NewReader(`{
		"subdomain":"auto",
		"zone_id":"zone-1",
		"zone_name":"example.com",
		"ipv4":true,
		"ipv6":false,
		"ipv4_value":"{IPV4}",
		"ttl":1
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleDDNSRecords(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create record status %d: %s", rec.Code, rec.Body.String())
	}

	cfg := s.cfgMgr.Get()
	if len(cfg.DDNS.Records) != 1 || cfg.DDNS.Records[0].Comment != "cfui" {
		t.Fatalf("expected default comment, got %#v", cfg.DDNS.Records)
	}
}

func TestDDNSRecordDeleteCanKeepCloudflareRecord(t *testing.T) {
	s := newServerTestServer(t)
	cfg := s.cfgMgr.Get()
	cfg.DDNS.Records = []config.DDNSRecord{{
		Name: "home.example.com", ZoneID: "zone-1", ZoneName: "example.com",
		Type: "A", Value: "{IPV4}", Comment: "cfui", TTL: 1,
	}}
	if err := s.cfgMgr.Save(cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	invalidReq := httptest.NewRequest(http.MethodDelete, "/api/ddns/records/0?delete_remote=invalid", nil)
	invalidRec := httptest.NewRecorder()
	s.handleDDNSRecord(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid delete_remote status %d: %s", invalidRec.Code, invalidRec.Body.String())
	}
	if got := s.cfgMgr.Get().DDNS.Records; len(got) != 1 {
		t.Fatalf("invalid request changed DDNS records: %#v", got)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/ddns/records/0?delete_remote=false", nil)
	deleteRec := httptest.NewRecorder()
	s.handleDDNSRecord(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete record status %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
	if got := s.cfgMgr.Get().DDNS.Records; len(got) != 0 {
		t.Fatalf("DDNS config still contains deleted record: %#v", got)
	}
}
