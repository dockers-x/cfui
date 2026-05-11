package ddns

import (
	"cfui/internal/config"
	"testing"
)

func TestNormalizeRecordValueFallsBackForHistoricalRecords(t *testing.T) {
	if got := NormalizeRecordValue("A", ""); got != AutoIPv4Placeholder {
		t.Fatalf("NormalizeRecordValue(A, empty) = %q, want %q", got, AutoIPv4Placeholder)
	}
	if got := NormalizeRecordValue("AAAA", ""); got != AutoIPv6Placeholder {
		t.Fatalf("NormalizeRecordValue(AAAA, empty) = %q, want %q", got, AutoIPv6Placeholder)
	}
}

func TestValidateRecordValueSupportsPlaceholderAndFixedIP(t *testing.T) {
	tests := []struct {
		recordType string
		value      string
		want       string
	}{
		{recordType: "A", value: "{ipv4}", want: AutoIPv4Placeholder},
		{recordType: "AAAA", value: "{IPv6}", want: AutoIPv6Placeholder},
		{recordType: "A", value: "1.2.3.4", want: "1.2.3.4"},
		{recordType: "AAAA", value: "2001:db8::1", want: "2001:db8::1"},
	}

	for _, tt := range tests {
		got, err := ValidateRecordValue(tt.recordType, tt.value)
		if err != nil {
			t.Fatalf("ValidateRecordValue(%q, %q) error = %v", tt.recordType, tt.value, err)
		}
		if got != tt.want {
			t.Fatalf("ValidateRecordValue(%q, %q) = %q, want %q", tt.recordType, tt.value, got, tt.want)
		}
	}
}

func TestResolveRecordIPUsesFixedValueAndAutoFallback(t *testing.T) {
	ip, err := ResolveRecordIP(config.DDNSRecord{Type: "A", Value: "8.8.8.8"}, "1.1.1.1", "")
	if err != nil {
		t.Fatalf("ResolveRecordIP fixed IPv4 error = %v", err)
	}
	if ip != "8.8.8.8" {
		t.Fatalf("ResolveRecordIP fixed IPv4 = %q, want %q", ip, "8.8.8.8")
	}

	ip, err = ResolveRecordIP(config.DDNSRecord{Type: "AAAA"}, "", "2001:db8::8")
	if err != nil {
		t.Fatalf("ResolveRecordIP auto IPv6 error = %v", err)
	}
	if ip != "2001:db8::8" {
		t.Fatalf("ResolveRecordIP auto IPv6 = %q, want %q", ip, "2001:db8::8")
	}
}
