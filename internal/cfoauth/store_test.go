package cfoauth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cfui/internal/persist"
)

func TestStorePersistsOAuthStateAndSessionInSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store := NewStore(dir)
	pending := PendingState{
		State:        "state-1",
		CodeVerifier: "verifier-1",
		RedirectURI:  "https://oauth.example.test/oauth/callback",
		Scope:        "dns.read dns.write",
		ExpiresAt:    time.Now().UTC().Add(time.Minute),
	}
	if err := store.SaveState(ctx, pending); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	session := Session{
		ID:           "session-1",
		Label:        "me@example.com",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
		Scope:        "dns.read dns.write",
		Current:      true,
	}
	if err := store.SaveSession(ctx, session); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	closeStore(t, store)
	assertOAuthRowsInSQLite(t, dir, session, pending)

	reloaded := NewStore(dir)
	t.Cleanup(func() { closeStore(t, reloaded) })

	consumed, err := reloaded.ConsumeState(ctx, pending.State)
	if err != nil {
		t.Fatalf("ConsumeState after reload: %v", err)
	}
	if consumed.CodeVerifier != pending.CodeVerifier || consumed.RedirectURI != pending.RedirectURI || consumed.Scope != pending.Scope {
		t.Fatalf("unexpected consumed state: %#v", consumed)
	}
	if _, err := reloaded.ConsumeState(ctx, pending.State); err != ErrStateExpired {
		t.Fatalf("expected consumed state to be deleted, got %v", err)
	}

	current, err := reloaded.CurrentSession(ctx)
	if err != nil {
		t.Fatalf("CurrentSession after reload: %v", err)
	}
	if current.ID != session.ID || current.AccessToken != session.AccessToken || current.RefreshToken != session.RefreshToken {
		t.Fatalf("unexpected current session: %#v", current)
	}

	assertNoJSONFiles(t, dir)
}

func assertOAuthRowsInSQLite(t *testing.T, dir string, session Session, pending PendingState) {
	t.Helper()

	db, err := persist.OpenRawDB(dir)
	if err != nil {
		t.Fatalf("OpenRawDB: %v", err)
	}
	defer db.Close()

	var sessionRows int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM oauth_sessions WHERE session_id = ? AND access_token = ? AND refresh_token = ?`,
		session.ID,
		session.AccessToken,
		session.RefreshToken,
	).Scan(&sessionRows); err != nil {
		t.Fatalf("query oauth_sessions: %v", err)
	}
	if sessionRows != 1 {
		t.Fatalf("expected OAuth session to be stored in SQLite, got %d rows", sessionRows)
	}

	var stateRows int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM oauth_states WHERE state = ? AND code_verifier = ? AND redirect_uri = ?`,
		pending.State,
		pending.CodeVerifier,
		pending.RedirectURI,
	).Scan(&stateRows); err != nil {
		t.Fatalf("query oauth_states: %v", err)
	}
	if stateRows != 1 {
		t.Fatalf("expected OAuth state to be stored in SQLite, got %d rows", stateRows)
	}
}

func TestStoreSaveSessionKeepsExactlyOneCurrentSession(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	first := Session{
		ID:          "first",
		Label:       "First",
		AccessToken: "access-1",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Scope:       "zone.read",
	}
	second := Session{
		ID:          "second",
		Label:       "Second",
		AccessToken: "access-2",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Scope:       "dns.read",
	}
	if err := store.SaveSession(ctx, first); err != nil {
		t.Fatalf("SaveSession first: %v", err)
	}
	if err := store.SaveSession(ctx, second); err != nil {
		t.Fatalf("SaveSession second: %v", err)
	}

	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %#v", sessions)
	}
	currentCount := 0
	for _, session := range sessions {
		if session.Current {
			currentCount++
			if session.ID != "second" {
				t.Fatalf("expected second session current, got %#v", session)
			}
		}
	}
	if currentCount != 1 {
		t.Fatalf("expected exactly one current session, got %d in %#v", currentCount, sessions)
	}
}

func TestStoreDeleteCurrentPromotesOldestRemainingSession(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	first := Session{ID: "first", Label: "First", AccessToken: "access-1", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	second := Session{ID: "second", Label: "Second", AccessToken: "access-2", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.SaveSession(ctx, first); err != nil {
		t.Fatalf("SaveSession first: %v", err)
	}
	time.Sleep(time.Millisecond)
	if err := store.SaveSession(ctx, second); err != nil {
		t.Fatalf("SaveSession second: %v", err)
	}

	if err := store.DeleteSession(ctx, "second"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	current, err := store.CurrentSession(ctx)
	if err != nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	if current.ID != "first" {
		t.Fatalf("expected first session to be promoted, got %#v", current)
	}
}

func TestStoreSwitchSessionKeepsExactlyOneCurrentSession(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	first := Session{ID: "first", Label: "First", AccessToken: "access-1", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	second := Session{ID: "second", Label: "Second", AccessToken: "access-2", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.SaveSession(ctx, first); err != nil {
		t.Fatalf("SaveSession first: %v", err)
	}
	if err := store.SaveSession(ctx, second); err != nil {
		t.Fatalf("SaveSession second: %v", err)
	}
	if err := store.SwitchSession(ctx, "first"); err != nil {
		t.Fatalf("SwitchSession: %v", err)
	}
	current, err := store.CurrentSession(ctx)
	if err != nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	if current.ID != "first" {
		t.Fatalf("expected first current, got %#v", current)
	}
	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	currentCount := 0
	for _, session := range sessions {
		if session.Current {
			currentCount++
		}
	}
	if currentCount != 1 {
		t.Fatalf("expected exactly one current session, got %d in %#v", currentCount, sessions)
	}
	if err := store.SwitchSession(ctx, "missing"); err != ErrNotLoggedIn {
		t.Fatalf("expected ErrNotLoggedIn for missing session, got %v", err)
	}
}

func TestStoreUpdateSessionLabelOnlyChangesTargetLabel(t *testing.T) {
	ctx := context.Background()
	store := NewStore(t.TempDir())
	t.Cleanup(func() { closeStore(t, store) })

	first := Session{ID: "first", Label: "First", AccessToken: "access-1", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	second := Session{ID: "second", Label: "Second", AccessToken: "access-2", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.SaveSession(ctx, first); err != nil {
		t.Fatalf("SaveSession first: %v", err)
	}
	if err := store.SaveSession(ctx, second); err != nil {
		t.Fatalf("SaveSession second: %v", err)
	}

	if err := store.UpdateSessionLabel(ctx, "first", "  Primary Cloudflare  "); err != nil {
		t.Fatalf("UpdateSessionLabel: %v", err)
	}
	updated, err := store.Session(ctx, "first")
	if err != nil {
		t.Fatalf("Session first: %v", err)
	}
	if updated.Label != "Primary Cloudflare" || updated.AccessToken != first.AccessToken {
		t.Fatalf("unexpected updated session: %#v", updated)
	}
	untouched, err := store.Session(ctx, "second")
	if err != nil {
		t.Fatalf("Session second: %v", err)
	}
	if untouched.Label != "Second" {
		t.Fatalf("unexpected second session label: %#v", untouched)
	}
	if err := store.UpdateSessionLabel(ctx, "missing", "Name"); err != ErrNotLoggedIn {
		t.Fatalf("expected ErrNotLoggedIn for missing session, got %v", err)
	}
}

func closeStore(t *testing.T, store *Store) {
	t.Helper()
	if store != nil && store.client != nil {
		if err := store.client.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

func assertNoJSONFiles(t *testing.T, dir string) {
	t.Helper()
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			t.Fatalf("OAuth store must not write JSON files, found %s", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}
