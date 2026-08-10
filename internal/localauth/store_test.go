package localauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"cfui/internal/persist"
)

func TestDisabledByDefaultAndSetupRequiresExplicitAction(t *testing.T) {
	store := NewStore(t.TempDir())
	status, err := store.Status(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.Authenticated || status.Username != "" {
		t.Fatalf("new installation must remain unprotected: %#v", status)
	}
	if err := store.Setup(t.Context(), "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	status, err = store.Status(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.Authenticated || status.Username != "admin" {
		t.Fatalf("unexpected enabled status: %#v", status)
	}
}

func TestPasswordAndSessionSecretsNeverAppearInStatus(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	password := "correct horse battery staple"
	if err := store.Setup(t.Context(), "admin", password); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyLogin(t.Context(), "admin", password); err != nil {
		t.Fatalf("valid login failed: %v", err)
	}
	if err := store.VerifyLogin(t.Context(), "admin", "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
	token, err := store.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Authenticated {
		t.Fatal("new session was not accepted")
	}

	db, err := persist.OpenRawDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var storedHash string
	if err := db.QueryRowContext(context.Background(), `SELECT password_hash FROM local_auth_settings WHERE id = 1`).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == password || storedHash == "" {
		t.Fatalf("password was not safely hashed: %q", storedHash)
	}
	var rawCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM local_auth_sessions WHERE token_hash = ?`, token).Scan(&rawCount); err != nil {
		t.Fatal(err)
	}
	if rawCount != 0 {
		t.Fatal("raw session token was stored")
	}
}

func TestChangePasswordRevokesEverySessionAndDisableIsPasswordProtected(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Setup(t.Context(), "admin", "old password value"); err != nil {
		t.Fatal(err)
	}
	first, _ := store.CreateSession(t.Context())
	second, _ := store.CreateSession(t.Context())
	if err := store.ChangePassword(t.Context(), "wrong password", "new password value"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("wrong current password error = %v", err)
	}
	if err := store.ChangePassword(t.Context(), "old password value", "new password value"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first, second} {
		ok, err := store.AuthenticateSession(t.Context(), token)
		if err != nil || ok {
			t.Fatalf("session survived password change: ok=%v err=%v", ok, err)
		}
	}
	if err := store.Disable(t.Context(), "old password value"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("old password disabled protection: %v", err)
	}
	if err := store.Disable(t.Context(), "new password value"); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(t.Context(), "")
	if err != nil || status.Enabled || status.Username != "" {
		t.Fatalf("disable failed: %#v, %v", status, err)
	}
}

func TestRevokeOtherSessionsKeepsCurrentSession(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	current, _ := store.CreateSession(t.Context())
	other, _ := store.CreateSession(t.Context())
	if err := store.RevokeOtherSessions(t.Context(), current, "correct password"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.AuthenticateSession(t.Context(), current); !ok {
		t.Fatal("current session was revoked")
	}
	if ok, _ := store.AuthenticateSession(t.Context(), other); ok {
		t.Fatal("other session was not revoked")
	}
}

func TestSetupRejectsWeakInputs(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Setup(t.Context(), "", "correct password"); err == nil {
		t.Fatal("empty username accepted")
	}
	if err := store.Setup(t.Context(), "admin", "short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestUnknownSessionIsRejectedWithoutDatabaseError(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	ok, err := store.AuthenticateSession(t.Context(), "unknown")
	if err != nil || ok {
		t.Fatalf("unknown session: ok=%v err=%v", ok, err)
	}
}

func TestAuthenticateSessionDoesNotWriteOnEveryRequest(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	db, err := persist.OpenRawDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE local_auth_sessions SET last_seen_at = 123`); err != nil {
		t.Fatal(err)
	}

	if ok, err := store.AuthenticateSession(t.Context(), token); err != nil || !ok {
		t.Fatalf("authenticate session: ok=%v err=%v", ok, err)
	}
	var lastSeen int64
	if err := db.QueryRowContext(t.Context(), `SELECT last_seen_at FROM local_auth_sessions`).Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if lastSeen != 123 {
		t.Fatalf("authentication wrote last_seen_at = %d; polling requests must stay read-only", lastSeen)
	}
}

func TestConcurrentPasswordChangesCannotBothUseOldPassword(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Setup(t.Context(), "admin", "old password value"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, password := range []string{"first new password", "second new password"} {
		wg.Add(1)
		go func(newPassword string) {
			defer wg.Done()
			<-start
			errs <- store.ChangePassword(context.Background(), "old password value", newPassword)
		}(password)
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	invalid := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrInvalidPassword):
			invalid++
		default:
			t.Fatalf("unexpected password change error: %v", err)
		}
	}
	if successes != 1 || invalid != 1 {
		t.Fatalf("concurrent changes: successes=%d invalid=%d", successes, invalid)
	}
}

func TestConcurrentLoginCannotSurvivePasswordRotation(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Setup(t.Context(), "admin", "old password value"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	loginResult := make(chan struct {
		token string
		err   error
	}, 1)
	changeResult := make(chan error, 1)
	go func() {
		<-start
		token, err := store.Login(context.Background(), "admin", "old password value")
		loginResult <- struct {
			token string
			err   error
		}{token: token, err: err}
	}()
	go func() {
		<-start
		changeResult <- store.ChangePassword(context.Background(), "old password value", "new password value")
	}()
	close(start)
	login := <-loginResult
	if err := <-changeResult; err != nil {
		t.Fatalf("password rotation failed: %v", err)
	}
	if login.err != nil && !errors.Is(login.err, ErrInvalidCredentials) {
		t.Fatalf("login returned unexpected error: %v", login.err)
	}
	if login.token != "" {
		valid, err := store.AuthenticateSession(t.Context(), login.token)
		if err != nil || valid {
			t.Fatalf("old-password login survived rotation: valid=%v err=%v", valid, err)
		}
	}
}

func TestPasswordRotationCreatesReplacementSessionAtomically(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Setup(t.Context(), "admin", "old password value"); err != nil {
		t.Fatal(err)
	}
	oldToken, err := store.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := store.ChangePasswordAndCreateSession(t.Context(), "old password value", "new password value")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.AuthenticateSession(t.Context(), oldToken); err != nil || ok {
		t.Fatalf("old session survived rotation: ok=%v err=%v", ok, err)
	}
	if ok, err := store.AuthenticateSession(t.Context(), replacement); err != nil || !ok {
		t.Fatalf("replacement session is invalid: ok=%v err=%v", ok, err)
	}
}

func TestRevokeOtherSessionsRejectsRevokedCurrentSession(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Setup(t.Context(), "admin", "correct password"); err != nil {
		t.Fatal(err)
	}
	revoked, _ := store.CreateSession(t.Context())
	current, _ := store.CreateSession(t.Context())
	if err := store.DeleteSession(t.Context(), revoked); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeOtherSessions(t.Context(), revoked, "correct password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked current session error = %v", err)
	}
	if ok, err := store.AuthenticateSession(t.Context(), current); err != nil || !ok {
		t.Fatalf("valid session was removed: ok=%v err=%v", ok, err)
	}
}

func TestDatabaseInitializationRetriesAfterTransientFailure(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "auth-data")
	if err := os.WriteFile(dir, []byte("blocks directory creation"), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	if _, err := store.Status(t.Context(), ""); err == nil {
		t.Fatal("initialization unexpectedly succeeded with file in place of data directory")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(t.Context(), "")
	if err != nil {
		t.Fatalf("initialization did not recover: %v", err)
	}
	if status.Enabled {
		t.Fatalf("unexpected recovered status: %#v", status)
	}
}
