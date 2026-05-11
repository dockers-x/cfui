package mcpbridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cfui/internal/persist"
	"cfui/internal/persist/ent/mcptoken"
)

func TestTokenStoreCreateListMasksAndVerifies(t *testing.T) {
	store := NewTokenStore(t.TempDir())

	created, err := store.Create("Agent", "cfui_mcp_test_token_123456")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Token != "cfui_mcp_test_token_123456" {
		t.Fatalf("create response must show token once, got %q", created.Token)
	}
	if created.Masked != "cfui...3456" {
		t.Fatalf("unexpected mask: %q", created.Masked)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one token, got %d", len(list))
	}
	if list[0].Masked != "cfui...3456" {
		t.Fatalf("list must only expose masked token, got %#v", list[0])
	}
	if !store.Verify("cfui_mcp_test_token_123456") {
		t.Fatal("expected token to verify")
	}
	if store.Verify("wrong") {
		t.Fatal("unexpected wrong token verification")
	}
}

func TestTokenStoreDelete(t *testing.T) {
	store := NewTokenStore(t.TempDir())
	created, err := store.Create("Agent", "cfui_mcp_delete_token")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if store.Verify("cfui_mcp_delete_token") {
		t.Fatal("deleted token still verifies")
	}
}

func TestTokenStoreMigratesLegacyJSONToDatabase(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "mcp_tokens.json")
	createdAt := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	legacy := []TokenRecord{
		{
			ID:        "legacy-id",
			Name:      "Legacy",
			Token:     "cfui_mcp_legacy_token_123456",
			CreatedAt: createdAt,
		},
	}

	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("Marshal legacy tokens: %v", err)
	}
	if err := os.WriteFile(legacyPath, data, 0600); err != nil {
		t.Fatalf("Write legacy tokens: %v", err)
	}

	store := NewTokenStore(dir)
	list, err := store.List()
	if err != nil {
		t.Fatalf("List after migration: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one migrated token, got %d", len(list))
	}
	if list[0].ID != "legacy-id" || list[0].Name != "Legacy" || list[0].Masked != "cfui...3456" {
		t.Fatalf("unexpected migrated token summary: %#v", list[0])
	}
	if !store.Verify("cfui_mcp_legacy_token_123456") {
		t.Fatal("expected migrated token to verify from database")
	}

	if _, err := os.Stat(persist.DBPath(dir)); err != nil {
		t.Fatalf("expected database file to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mcp_tokens.json.migrated")); err != nil {
		t.Fatalf("expected migrated backup to exist: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy token file to be renamed, stat err = %v", err)
	}

	client, err := persist.OpenClient(dir)
	if err != nil {
		t.Fatalf("OpenClient: %v", err)
	}
	defer client.Close()

	row, err := client.MCPToken.Query().Where(mcptoken.TokenID("legacy-id")).Only(t.Context())
	if err != nil {
		t.Fatalf("Query migrated token: %v", err)
	}
	if row.TokenHash != tokenHash("cfui_mcp_legacy_token_123456") {
		t.Fatalf("unexpected token hash: %q", row.TokenHash)
	}
	if row.Masked != MaskToken("cfui_mcp_legacy_token_123456") {
		t.Fatalf("unexpected masked token: %q", row.Masked)
	}

	if err := os.Remove(filepath.Join(dir, "mcp_tokens.json.migrated")); err != nil {
		t.Fatalf("Remove migrated backup: %v", err)
	}

	reopened := NewTokenStore(dir)
	if !reopened.Verify("cfui_mcp_legacy_token_123456") {
		t.Fatal("expected token to verify after removing migrated json backup")
	}
}
