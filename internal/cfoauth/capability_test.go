package cfoauth

import "testing"

func TestCapabilitiesDeriveReadWriteFromScopes(t *testing.T) {
	matrix := Capabilities("zone.read dns.read dns.write argotunnel.read")

	if !matrix["zones"].Read || matrix["zones"].Write {
		t.Fatalf("zone capability mismatch: %#v", matrix["zones"])
	}
	if !matrix["dns"].Read || !matrix["dns"].Write {
		t.Fatalf("dns capability mismatch: %#v", matrix["dns"])
	}
	if !matrix["tunnels"].Read || matrix["tunnels"].Write {
		t.Fatalf("tunnel capability mismatch: %#v", matrix["tunnels"])
	}
	if matrix["r2"].Read || matrix["r2"].Write {
		t.Fatalf("unexpected r2 capability: %#v", matrix["r2"])
	}
}

func TestCapabilitiesAreCaseInsensitive(t *testing.T) {
	matrix := Capabilities("DNS.READ Cloudflare-Tunnel.WRITE")

	if !matrix["dns"].Read {
		t.Fatalf("expected dns read from mixed-case scope: %#v", matrix["dns"])
	}
	if !matrix["tunnels"].Write {
		t.Fatalf("expected tunnel write from mixed-case scope: %#v", matrix["tunnels"])
	}
}
