package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LoweKeyFlexin/share-ct/migrations"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func TestOpenRequiresExistingDirectory(t *testing.T) {
	_, err := Open(context.Background(), filepath.Join(t.TempDir(), "missing", "x.db"))
	if err == nil {
		t.Fatal("Open must not create directories: the host allows writes only under /data and /tmp")
	}
}

// embedded returns the real migrations, so the counts below follow the directory
// instead of being retyped every time a feature adds a file.
func embedded(t *testing.T) []Migration {
	t.Helper()
	ms, err := Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 2 {
		t.Fatalf("only %d embedded migrations; expected at least init + players", len(ms))
	}
	return ms
}

func TestOpenUsesWALAndPings(t *testing.T) {
	s, dir := openTemp(t)
	ctx := context.Background()
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d (err %v), want 1: cascades depend on it", fk, err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "test.db") {
			t.Errorf("unexpected file beside the database: %s", e.Name())
		}
	}
}

func TestMigrateAppliesOnceThenNoOps(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	ms := embedded(t)
	n, last := len(ms), ms[len(ms)-1].Version

	first, err := s.Migrate(ctx, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if first.Applied != n || first.From != 0 || first.To != last {
		t.Fatalf("first run = %+v, want applied %d, 0 -> %d", first, n, last)
	}

	second, err := s.Migrate(ctx, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if second.Applied != 0 || second.From != last || second.To != last {
		t.Fatalf("second run = %+v, want applied 0, %d -> %d", second, last, last)
	}

	for _, table := range []string{"schema_migrations", "players"} {
		var n int
		const q = `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
		if err := s.db.QueryRowContext(ctx, q, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: found %d (err %v), want 1", table, n, err)
		}
	}
}

func TestMigrateResumesFromRecordedVersion(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	ms := embedded(t)
	last := ms[len(ms)-1].Version
	later := fstest.MapFS{}
	for _, m := range ms {
		later[fmt.Sprintf("%03d_%s.sql", m.Version, m.Name)] = &fstest.MapFile{Data: []byte("SELECT 'never run again';")}
	}
	later[fmt.Sprintf("%03d_more.sql", last+1)] = &fstest.MapFile{Data: []byte("CREATE TABLE more (id INTEGER PRIMARY KEY);")}
	res, err := s.Migrate(ctx, later)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.From != last || res.To != last+1 {
		t.Fatalf("got %+v, want applied 1, %d -> %d", res, last, last+1)
	}
}

func TestMigrateFailureRollsBackAndRecordsNothing(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	bad := fstest.MapFS{
		"001_init.sql": {Data: migrationBytes(t, "001_init.sql")},
		"002_bad.sql":  {Data: []byte("CREATE TABLE half (id INTEGER); CREATE TABLE half (id INTEGER);")},
	}
	res, err := s.Migrate(ctx, bad)
	if err == nil || !strings.Contains(err.Error(), "002_bad") {
		t.Fatalf("err = %v, want a failure naming 002_bad", err)
	}
	if res.Applied != 1 {
		t.Errorf("applied = %d, want 1 (001 before the failure)", res.Applied)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'half'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("table half exists after a failed migration (n=%d err=%v)", n, err)
	}
}

func TestLoadRejectsBadNames(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"typo":      {"01_init.sql": {Data: []byte("")}},
		"uppercase": {"001_Init.sql": {Data: []byte("")}},
		"zero":      {"000_init.sql": {Data: []byte("")}},
		"duplicate": {"001_a.sql": {Data: []byte("")}, "001_b.sql": {Data: []byte("")}},
		"stray":     {"001_init.sql": {Data: []byte("")}, "notes.txt": {Data: []byte("")}},
	}
	for name, fsys := range cases {
		if _, err := Load(fsys); err == nil {
			t.Errorf("%s: Load accepted %v", name, fsys)
		}
	}
}

func TestLoadOrdersByVersion(t *testing.T) {
	ms, err := Load(fstest.MapFS{
		"010_c.sql": {Data: []byte("")}, "002_b.sql": {Data: []byte("")}, "001_a.sql": {Data: []byte("")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 3 || ms[0].Version != 1 || ms[1].Version != 2 || ms[2].Version != 10 || ms[2].Name != "c" {
		t.Fatalf("got %+v", ms)
	}
}

func migrationBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
