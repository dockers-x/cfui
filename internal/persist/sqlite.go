package persist

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"cfui/internal/persist/ent"

	_ "github.com/lib-x/entsqlite"
)

const (
	// DBFilename is the default SQLite database filename.
	DBFilename = "data.db"

	sqlitePragmas = "cache=shared&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)"
)

// DBPath returns the SQLite database path under the configured data directory.
func DBPath(dir string) string {
	return filepath.Join(dir, DBFilename)
}

// OpenClient opens the SQLite database, auto-creates the file if missing, and
// applies the latest schema migrations.
func OpenClient(dir string) (*ent.Client, error) {
	dbPath, err := filepath.Abs(DBPath(dir))
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}

	if err := ensureDatabaseFile(dbPath); err != nil {
		return nil, fmt.Errorf("create database file: %w", err)
	}

	client, err := ent.Open("sqlite3", sqliteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if err := client.Schema.Create(context.Background()); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("apply sqlite schema: %w", err)
	}

	return client, nil
}

func ensureDatabaseFile(dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return err
	}

	file, err := os.OpenFile(dbPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}

	return file.Close()
}

func sqliteDSN(dbPath string) string {
	return (&url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(dbPath),
		RawQuery: sqlitePragmas,
	}).String()
}

// MarkLegacyMigrated renames a legacy file after a successful DB import while
// keeping it as a backup for manual recovery.
func MarkLegacyMigrated(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}

	backupPath := path + ".migrated"
	if _, err := os.Stat(backupPath); err == nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return removeErr
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.Rename(path, backupPath)
}
