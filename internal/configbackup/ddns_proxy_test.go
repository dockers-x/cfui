package configbackup

import (
	"cfui/internal/config"
	"slices"
	"testing"
	"time"
)

func TestDDNSProxyBackupRoundTrip(t *testing.T) {
	for _, useProxy := range []bool{true, false} {
		cfg := config.DefaultConfig()
		cfg.DDNS.UseProxy = useProxy
		payload, err := Build(cfg, ExportOptions{Sections: []Section{SectionDDNS}}, "dev", time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		data, err := Encode(payload, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(data, "")
		if err != nil {
			t.Fatal(err)
		}
		current := config.DefaultConfig()
		current.DDNS.UseProxy = !useProxy
		result, err := Apply(current, decoded.Payload, []Section{SectionDDNS})
		if err != nil {
			t.Fatal(err)
		}
		if result.Config.DDNS.UseProxy != useProxy {
			t.Errorf("restored use_proxy = %v, want %v", result.Config.DDNS.UseProxy, useProxy)
		}
		if !slices.Contains(result.ChangedSections, SectionDDNS) {
			t.Error("proxy-only change was not detected")
		}
	}
}
