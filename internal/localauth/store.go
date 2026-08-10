// Package localauth stores and verifies the optional local UI access guard.
package localauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cfui/internal/persist"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 19 * 1024
	argonIterations  = 2
	argonParallelism = 1
	argonSaltLength  = 16
	argonKeyLength   = 32
	sessionLifetime  = 7 * 24 * time.Hour
)

var (
	ErrAlreadyEnabled     = errors.New("local access protection is already enabled")
	ErrDisabled           = errors.New("local access protection is disabled")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInvalidPassword    = errors.New("invalid password")
)

// Status is safe to expose through the API. It intentionally contains no
// password hash or session material.
type Status struct {
	Enabled       bool   `json:"enabled"`
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
}

// Store owns the local-auth SQLite tables. It uses the same database as the
// rest of cfui while keeping authentication secrets outside exported config.
type Store struct {
	dir    string
	db     *sql.DB
	initMu sync.Mutex
	mu     sync.Mutex
}

func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) ensureDB() error {
	s.initMu.Lock()
	defer s.initMu.Unlock()
	if s.db != nil {
		return nil
	}
	db, err := persist.OpenRawDB(s.dir)
	if err != nil {
		return err
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS local_auth_settings (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				enabled INTEGER NOT NULL DEFAULT 0,
				username TEXT NOT NULL DEFAULT '',
				password_hash TEXT NOT NULL DEFAULT '',
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL,
				password_updated_at INTEGER NOT NULL DEFAULT 0
			)`,
		`CREATE TABLE IF NOT EXISTS local_auth_sessions (
				token_hash TEXT PRIMARY KEY,
				created_at INTEGER NOT NULL,
				expires_at INTEGER NOT NULL,
				last_seen_at INTEGER NOT NULL
			)`,
		`CREATE INDEX IF NOT EXISTS local_auth_sessions_expires_at ON local_auth_sessions(expires_at)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			_ = db.Close()
			return fmt.Errorf("initialize local auth database: %w", err)
		}
	}
	s.db = db
	return nil
}

func (s *Store) Status(ctx context.Context, sessionToken string) (Status, error) {
	if err := s.ensureDB(); err != nil {
		return Status{}, err
	}
	var status Status
	err := s.db.QueryRowContext(ctx, `SELECT enabled, username FROM local_auth_settings WHERE id = 1`).Scan(&status.Enabled, &status.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	if !status.Enabled {
		status.Username = ""
		return status, nil
	}
	if status.Enabled && sessionToken != "" {
		status.Authenticated, err = s.AuthenticateSession(ctx, sessionToken)
	}
	return status, err
}

func (s *Store) Enabled(ctx context.Context) (bool, error) {
	status, err := s.Status(ctx, "")
	return status.Enabled, err
}

// Setup enables protection and replaces any credentials left from an earlier
// disabled configuration. Existing installations remain unaffected until this
// method is explicitly called.
func (s *Store) Setup(ctx context.Context, username, password string) error {
	username, password, err := validateCredentials(username, password)
	if err != nil {
		return err
	}
	if err := s.ensureDB(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var enabled bool
	err = tx.QueryRowContext(ctx, `SELECT enabled FROM local_auth_settings WHERE id = 1`).Scan(&enabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if enabled {
		return ErrAlreadyEnabled
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Unix()
	_, err = tx.ExecContext(ctx, `INSERT INTO local_auth_settings
		(id, enabled, username, password_hash, created_at, updated_at, password_updated_at)
		VALUES (1, 1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET enabled = 1, username = excluded.username,
		password_hash = excluded.password_hash, updated_at = excluded.updated_at,
		password_updated_at = excluded.password_updated_at`, username, hash, now, now, now)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM local_auth_sessions`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) VerifyLogin(ctx context.Context, username, password string) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifyLogin(ctx, username, password)
}

func (s *Store) verifyLogin(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	var enabled bool
	var savedUsername, passwordHash string
	err := s.db.QueryRowContext(ctx, `SELECT enabled, username, password_hash FROM local_auth_settings WHERE id = 1`).Scan(&enabled, &savedUsername, &passwordHash)
	if errors.Is(err, sql.ErrNoRows) || !enabled {
		return ErrDisabled
	}
	if err != nil {
		return err
	}
	validHash, err := verifyPassword(password, passwordHash)
	if err != nil {
		return ErrInvalidCredentials
	}
	validUser := subtle.ConstantTimeCompare([]byte(username), []byte(savedUsername)) == 1
	if !validUser || !validHash {
		return ErrInvalidCredentials
	}
	return nil
}

func (s *Store) VerifyPassword(ctx context.Context, password string) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifyPassword(ctx, password)
}

func (s *Store) verifyPassword(ctx context.Context, password string) error {
	var enabled bool
	var passwordHash string
	err := s.db.QueryRowContext(ctx, `SELECT enabled, password_hash FROM local_auth_settings WHERE id = 1`).Scan(&enabled, &passwordHash)
	if errors.Is(err, sql.ErrNoRows) || !enabled {
		return ErrDisabled
	}
	if err != nil {
		return err
	}
	ok, err := verifyPassword(password, passwordHash)
	if err != nil || !ok {
		return ErrInvalidPassword
	}
	return nil
}

func (s *Store) ChangePassword(ctx context.Context, currentPassword, newPassword string) error {
	if _, _, err := validateCredentials("user", newPassword); err != nil {
		return err
	}
	if err := s.ensureDB(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.verifyPassword(ctx, currentPassword); err != nil {
		return err
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.updatePasswordAndRevoke(ctx, hash, "")
}

// ChangePasswordAndCreateSession rotates the password, revokes every existing
// session, and creates the caller's replacement session in one mutation
// critical section and one database transaction.
func (s *Store) ChangePasswordAndCreateSession(ctx context.Context, currentPassword, newPassword string) (string, error) {
	if _, _, err := validateCredentials("user", newPassword); err != nil {
		return "", err
	}
	if err := s.ensureDB(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.verifyPassword(ctx, currentPassword); err != nil {
		return "", err
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return "", err
	}
	token, err := newSessionToken()
	if err != nil {
		return "", err
	}
	if err := s.updatePasswordAndRevoke(ctx, hash, token); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) updatePasswordAndRevoke(ctx context.Context, hash, replacementToken string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `UPDATE local_auth_settings SET password_hash = ?, updated_at = ?, password_updated_at = ? WHERE id = 1 AND enabled = 1`, hash, now, now)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return err
		}
		return ErrDisabled
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM local_auth_sessions`); err != nil {
		return err
	}
	if replacementToken != "" {
		nowTime := time.Unix(now, 0).UTC()
		if _, err = tx.ExecContext(ctx, `INSERT INTO local_auth_sessions(token_hash, created_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?)`,
			sessionHash(replacementToken), now, nowTime.Add(sessionLifetime).Unix(), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Disable(ctx context.Context, password string) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.verifyPassword(ctx, password); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE local_auth_settings SET enabled = 0, updated_at = ? WHERE id = 1`, time.Now().UTC().Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM local_auth_sessions`); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateSession returns a random bearer token. Only its SHA-256 digest is
// persisted; callers must place the raw value in an HttpOnly cookie.
func (s *Store) CreateSession(ctx context.Context) (string, error) {
	if err := s.ensureDB(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createSession(ctx)
}

// Login verifies the current credentials and creates their session while
// holding the same mutation lock. A password change therefore either revokes
// the newly-created session or completes before the old password is checked.
func (s *Store) Login(ctx context.Context, username, password string) (string, error) {
	if err := s.ensureDB(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.verifyLogin(ctx, username, password); err != nil {
		return "", err
	}
	return s.createSession(ctx)
}

func (s *Store) createSession(ctx context.Context) (string, error) {
	token, err := newSessionToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM local_auth_settings WHERE id = 1`).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrDisabled
		}
		return "", err
	}
	if !enabled {
		return "", ErrDisabled
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM local_auth_sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO local_auth_sessions(token_hash, created_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?)`,
		sessionHash(token), now.Unix(), now.Add(sessionLifetime).Unix(), now.Unix()); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) AuthenticateSession(ctx context.Context, token string) (bool, error) {
	if err := s.ensureDB(); err != nil {
		return false, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}
	hash := sessionHash(token)
	var expiresAt int64
	err := s.db.QueryRowContext(ctx, `SELECT expires_at FROM local_auth_sessions WHERE token_hash = ?`, hash).Scan(&expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := time.Now().UTC().Unix()
	if expiresAt <= now {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM local_auth_sessions WHERE token_hash = ?`, hash)
		return false, nil
	}
	return true, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	if err := s.ensureDB(); err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM local_auth_sessions WHERE token_hash = ?`, sessionHash(token))
	return err
}

func (s *Store) RevokeOtherSessions(ctx context.Context, currentToken, password string) error {
	if strings.TrimSpace(currentToken) == "" {
		return ErrInvalidCredentials
	}
	if err := s.ensureDB(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.verifyPassword(ctx, password); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	var expiresAt int64
	err = tx.QueryRowContext(ctx, `SELECT expires_at FROM local_auth_sessions WHERE token_hash = ?`, sessionHash(currentToken)).Scan(&expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if expiresAt <= now {
		return ErrInvalidCredentials
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM local_auth_sessions WHERE token_hash <> ?`, sessionHash(currentToken)); err != nil {
		return err
	}
	return tx.Commit()
}

func newSessionToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func validateCredentials(username, password string) (string, string, error) {
	username = strings.TrimSpace(username)
	if len(username) < 1 || len(username) > 64 {
		return "", "", fmt.Errorf("username must be between 1 and 64 characters")
	}
	if len(password) < 8 || len(password) > 256 {
		return "", "", fmt.Errorf("password must be between 8 and 256 characters")
	}
	return username, password, nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func verifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("invalid password hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, fmt.Errorf("invalid argon2 version")
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, fmt.Errorf("invalid argon2 parameters")
	}
	if memory < 8*1024 || memory > 256*1024 || iterations < 1 || iterations > 10 || parallelism < 1 || parallelism > 8 {
		return false, fmt.Errorf("unsafe argon2 parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, fmt.Errorf("invalid argon2 salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false, fmt.Errorf("invalid argon2 hash")
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func sessionHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
