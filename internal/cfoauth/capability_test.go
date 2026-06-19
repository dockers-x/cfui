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

func TestFeatureScopesReturnsDefensiveCopy(t *testing.T) {
	scopes := FeatureScopes()
	if len(scopes) == 0 {
		t.Fatal("expected feature scopes")
	}
	scopes[0].ReadScopes[0] = "mutated.read"

	matrix := Capabilities("account-settings.read")
	if !matrix["account"].Read {
		t.Fatalf("feature scope mutation leaked into capability matrix: %#v", matrix["account"])
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

func TestWorkersCapabilityIsReadOnlyUntilDeployUXExists(t *testing.T) {
	matrix := Capabilities("workers-scripts.read workers-scripts.write")

	if !matrix["workers"].Read {
		t.Fatalf("expected workers read capability: %#v", matrix["workers"])
	}
	if matrix["workers"].Write {
		t.Fatalf("workers write should stay disabled until worker deploy UX exists: %#v", matrix["workers"])
	}
}

func TestZoneSettingsAndCachePurgeCapabilitiesAreIndependent(t *testing.T) {
	matrix := Capabilities("zone-settings.read zone-settings.write cache_purge.write")

	if !matrix["zone_settings"].Read || !matrix["zone_settings"].Write {
		t.Fatalf("expected zone settings read/write: %#v", matrix["zone_settings"])
	}
	if !matrix["cache_purge"].Write {
		t.Fatalf("expected cache purge write: %#v", matrix["cache_purge"])
	}

	settingsOnly := Capabilities("zone-settings.read zone-settings.write")
	if !settingsOnly["zone_settings"].Write || settingsOnly["cache_purge"].Write {
		t.Fatalf("zone settings write should not imply cache purge: zone=%#v cache=%#v", settingsOnly["zone_settings"], settingsOnly["cache_purge"])
	}

	purgeOnly := Capabilities("cache_purge.write")
	if purgeOnly["zone_settings"].Write || !purgeOnly["cache_purge"].Write {
		t.Fatalf("cache purge should not imply zone settings write: zone=%#v cache=%#v", purgeOnly["zone_settings"], purgeOnly["cache_purge"])
	}
}

func TestCachePurgeCapabilityAcceptsLegacyScopeName(t *testing.T) {
	matrix := Capabilities("cache.purge")

	if !matrix["cache_purge"].Write {
		t.Fatalf("expected legacy cache.purge to enable cache purge write: %#v", matrix["cache_purge"])
	}
}
