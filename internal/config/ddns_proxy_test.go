package config

import (
	"cfui/internal/persist"
	"testing"
)

func TestDDNSProxySettingPersists(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mgr.Get().DDNS.UseProxy {
		t.Fatal("IP detection should bypass proxies by default")
	}
	for _, useProxy := range []bool{true, false} {
		cfg := mgr.Get()
		cfg.DDNS.UseProxy = useProxy
		if err := mgr.Save(cfg); err != nil {
			t.Fatal(err)
		}
		reloaded, err := NewManager(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := reloaded.Get().DDNS.UseProxy; got != useProxy {
			t.Fatalf("reloaded use_proxy = %v, want %v", got, useProxy)
		}
	}
}

func TestDDNSProxyMigrationDefaultsToBypass(t *testing.T) {
	dir := t.TempDir()
	db, err := persist.OpenRawDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(t.Context(), `CREATE TABLE ddns_settings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		key TEXT NOT NULL UNIQUE,
		enabled BOOL NOT NULL DEFAULT false,
		interval_mins INTEGER NOT NULL DEFAULT 5,
		only_on_change BOOL NOT NULL DEFAULT true,
		max_retries INTEGER NOT NULL DEFAULT 3,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	INSERT INTO ddns_settings (key, enabled, interval_mins, created_at, updated_at)
	VALUES ('default', true, 15, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	client, err := persist.OpenClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	row, err := client.DDNSSetting.Query().Only(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if row.UseProxy || !row.Enabled || row.IntervalMins != 15 {
		t.Fatalf("migration changed existing settings or enabled proxy: %#v", row)
	}
}
