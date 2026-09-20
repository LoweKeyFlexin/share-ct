package store

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/identity"
)

// Migration is one migrations/NNN_name.sql file.
type Migration struct {
	Version int
	Name    string // the file name without its NNN_ prefix and .sql suffix
	SQL     string
}

// Result reports what Migrate did.
type Result struct {
	From    int // highest version recorded before this run; 0 on a fresh file
	To      int // highest version recorded after this run
	Applied int // migrations applied by this run; 0 on every restart
}

var migrationName = regexp.MustCompile(`^(\d{3})_([a-z0-9_]+)\.sql$`)

// Load reads every NNN_name.sql at the root of fsys, in version order. Anything else in
// the directory is an error: a typo in a file name must not silently skip a migration.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	seen := map[int]string{}
	var ms []Migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %q: name must match NNN_name.sql", e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		if version == 0 {
			return nil, fmt.Errorf("migration %q: versions start at 001", e.Name())
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %03d", prev, e.Name(), version)
		}
		seen[version] = e.Name()
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		ms = append(ms, Migration{Version: version, Name: m[2], SQL: string(body)})
	}
	slices.SortFunc(ms, func(a, b Migration) int { return cmp.Compare(a.Version, b.Version) })
	return ms, nil
}

// Migrate applies, in version order and each in its own transaction, every migration in
// fsys whose version is not yet recorded in schema_migrations. Re-running is a no-op.
// The runner never creates schema_migrations itself: 001_init.sql does, and a missing
// table means "nothing applied yet".
func (s *Store) Migrate(ctx context.Context, fsys fs.FS) (Result, error) {
	ms, err := Load(fsys)
	if err != nil {
		return Result{}, err
	}
	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return Result{}, err
	}
	res := Result{From: maxVersion(applied)}
	res.To = res.From
	for _, m := range ms {
		if applied[m.Version] {
			continue
		}
		if err := s.apply(ctx, m); err != nil {
			return res, fmt.Errorf("migration %03d_%s: %w", m.Version, m.Name, err)
		}
		res.Applied++
		res.To = max(res.To, m.Version)
	}
	return res, nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[int]bool, error) {
	const exists = `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`
	var n int
	if err := s.db.QueryRowContext(ctx, exists).Scan(&n); err != nil {
		return nil, fmt.Errorf("look up schema_migrations: %w", err)
	}
	versions := map[int]bool{}
	if n == 0 {
		return versions, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		versions[v] = true
	}
	return versions, rows.Err()
}

// apply runs one migration and records it, atomically: SQLite DDL is transactional.
func (s *Store) apply(ctx context.Context, m Migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if m.Version == 6 && m.Name == "player_refs_reports" {
		if err := backfillPlayerRefs(ctx, tx); err != nil {
			return fmt.Errorf("backfill player refs: %w", err)
		}
	}
	const record = `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`
	if _, err := tx.ExecContext(ctx, record, m.Version, m.Name, time.Now().Unix()); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return tx.Commit()
}

func backfillPlayerRefs(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM players WHERE player_ref IS NULL`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		ref, err := identity.NewRef()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE players SET player_ref = ? WHERE id = ?`, ref, id); err != nil {
			return err
		}
	}
	return nil
}

func maxVersion(applied map[int]bool) int {
	v := 0
	for k := range applied {
		v = max(v, k)
	}
	return v
}
