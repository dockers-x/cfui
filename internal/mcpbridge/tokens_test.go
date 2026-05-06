package mcpbridge

import "testing"

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
