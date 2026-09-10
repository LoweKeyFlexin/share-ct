// Package store opens the one SQLite file behind Share CT and applies its migrations.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, CGO_ENABLED=0)
)

// Store is the single-writer SQLite handle.
type Store struct {
	db *sql.DB
}

// dsn builds the driver DSN: WAL journaling and a 5 s busy timeout, as the brief fixes.
// The -wal and -shm files sit beside the database, so the directory must be writable.
func dsn(path string) string {
	return "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
}

// Open opens (creating if needed) the database at path. The pool is capped at one
// connection: SQLite has one writer, and one connection makes that explicit instead of
// leaning on busy_timeout to serialise. The directory must already exist; the server
// never creates directories, because the only writable places are /data and /tmp.
func Open(ctx context.Context, path string) (*Store, error) {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("database directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("database directory %s: not a directory", dir)
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &Store{db: db}
	if err := s.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.requireWAL(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// requireWAL fails loudly if the pragma did not take, rather than running on a rollback
// journal nobody asked for.
func (s *Store) requireWAL(ctx context.Context) error {
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("read journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("journal_mode is %q, want wal", mode)
	}
	return nil
}

// Ping runs SELECT 1; it backs /healthz.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	if one != 1 {
		return fmt.Errorf("SELECT 1 returned %d", one)
	}
	return nil
}

// Close releases the connection. WAL contents are checkpointed by SQLite on close.
func (s *Store) Close() error {
	return s.db.Close()
}
