package ddns

import (
	"cfui/internal/config"
	"fmt"
	"net"
	"strings"
)

const (
	AutoIPv4Placeholder = "{IPV4}"
	AutoIPv6Placeholder = "{IPV6}"
)

func DefaultRecordValue(recordType string) string {
	if strings.EqualFold(strings.TrimSpace(recordType), "AAAA") {
		return AutoIPv6Placeholder
	}
	return AutoIPv4Placeholder
}

func NormalizeRecordValue(recordType, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return DefaultRecordValue(recordType)
	}

	switch {
	case strings.EqualFold(trimmed, AutoIPv4Placeholder):
		return AutoIPv4Placeholder
	case strings.EqualFold(trimmed, AutoIPv6Placeholder):
		return AutoIPv6Placeholder
	default:
		return trimmed
	}
}

func UsesAutoValue(recordType, value string) bool {
	return NormalizeRecordValue(recordType, value) == DefaultRecordValue(recordType)
}

func ValidateRecordValue(recordType, value string) (string, error) {
	recordType = strings.ToUpper(strings.TrimSpace(recordType))
	normalized := NormalizeRecordValue(recordType, value)

	switch recordType {
	case "A":
		if normalized == AutoIPv4Placeholder {
			return AutoIPv4Placeholder, nil
		}
		if normalized == AutoIPv6Placeholder {
			return "", fmt.Errorf("IPv4 record cannot use %s", AutoIPv6Placeholder)
		}
	case "AAAA":
		if normalized == AutoIPv6Placeholder {
			return AutoIPv6Placeholder, nil
		}
		if normalized == AutoIPv4Placeholder {
			return "", fmt.Errorf("IPv6 record cannot use %s", AutoIPv4Placeholder)
		}
	default:
		return "", fmt.Errorf("unsupported record type %q", recordType)
	}

	parsed := net.ParseIP(normalized)
	if parsed == nil {
		return "", fmt.Errorf("invalid IP address %q", normalized)
	}

	if recordType == "A" {
		ipv4 := parsed.To4()
		if ipv4 == nil {
			return "", fmt.Errorf("record type A requires a valid IPv4 address")
		}
		return ipv4.String(), nil
	}

	if parsed.To4() != nil {
		return "", fmt.Errorf("record type AAAA requires a valid IPv6 address")
	}
	return parsed.String(), nil
}

func NormalizeRecord(rec config.DDNSRecord) config.DDNSRecord {
	rec.Value = NormalizeRecordValue(rec.Type, rec.Value)
	if rec.TTL <= 0 {
		rec.TTL = 1
	}
	return rec
}

func NormalizeRecords(records []config.DDNSRecord) []config.DDNSRecord {
	if len(records) == 0 {
		return records
	}

	out := make([]config.DDNSRecord, len(records))
	for i, rec := range records {
		out[i] = NormalizeRecord(rec)
	}
	return out
}

func ResolveRecordIP(rec config.DDNSRecord, currentV4, currentV6 string) (string, error) {
	value, err := ValidateRecordValue(rec.Type, rec.Value)
	if err != nil {
		return "", err
	}

	switch value {
	case AutoIPv4Placeholder:
		if strings.TrimSpace(currentV4) == "" {
			return "", fmt.Errorf("no IPv4 address available")
		}
		return currentV4, nil
	case AutoIPv6Placeholder:
		if strings.TrimSpace(currentV6) == "" {
			return "", fmt.Errorf("no IPv6 address available")
		}
		return currentV6, nil
	default:
		return value, nil
	}
}
