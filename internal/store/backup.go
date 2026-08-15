package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Backup writes a transactionally consistent standalone SQLite database using
// VACUUM INTO and verifies its integrity before returning. It refuses to
// overwrite an existing destination.
func (s *Store) Backup(ctx context.Context, destination string) error {
	if strings.TrimSpace(destination) == "" || destination == ":memory:" || strings.HasPrefix(destination, "file:") {
		return errors.New("store: backup destination must be a filesystem path")
	}
	abs, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("store: resolve backup destination: %w", err)
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("store: backup destination %q already exists", abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: inspect backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return fmt.Errorf("store: create backup directory: %w", err)
	}
	// VACUUM does not accept a bind parameter in this position. SQLite string
	// literals escape a single quote by doubling it.
	literal := "'" + strings.ReplaceAll(abs, "'", "''") + "'"
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO "+literal); err != nil {
		_ = os.Remove(abs)
		return fmt.Errorf("store: backup database: %w", err)
	}
	if err := verifyBackup(ctx, abs); err != nil {
		_ = os.Remove(abs)
		return err
	}
	if err := os.Chmod(abs, 0o600); err != nil {
		return fmt.Errorf("store: protect backup: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE settings SET last_backup_at = ? WHERE id = 1", formatTime(s.now())); err != nil {
		return fmt.Errorf("store: record backup time: %w", err)
	}
	return nil
}

func verifyBackup(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("store: open backup for verification: %w", err)
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("store: verify backup: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("store: backup integrity check: %s", integrity)
	}
	return nil
}
