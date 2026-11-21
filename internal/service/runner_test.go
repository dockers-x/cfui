package service

import (
	"testing"

	"cfui/internal/config"
)

func TestProfileStatusResolvesActiveAndDeletedInstances(t *testing.T) {
	manager, err := config.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "office-west"
	if _, err := manager.SaveTunnelProfile(key, config.DefaultTunnelProfileConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ActivateTunnelProfile(key); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(manager)
	if _, err := r.instanceFor(key); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"", key, " Office_West "} {
		if _, ok := r.ProfileStatus(alias); !ok {
			t.Fatalf("no status for %q", alias)
		}
	}
	if _, err := manager.DeleteTunnelProfile(key); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.ProfileStatus(""); ok {
		t.Fatal("legacy status still refers to deleted profile")
	}
	if err := r.RemoveProfile("Office_West"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.ProfileStatus(key); ok {
		t.Fatal("removed instance still registered")
	}
}
