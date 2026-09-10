package store

import (
	"context"
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

	first, err := s.Migrate(ctx, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if first.Applied != 2 || first.From != 0 || first.To != 2 {
		t.Fatalf("first run = %+v, want applied 2, 0 -> 2", first)
	}

	second, err := s.Migrate(ctx, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if second.Applied != 0 || second.From != 2 || second.To != 2 {
		t.Fatalf("second run = %+v, want applied 0, 2 -> 2", second)
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
	later := fstest.MapFS{
		"001_init.sql":    {Data: []byte("SELECT 'never run again';")},
		"002_players.sql": {Data: []byte("SELECT 'never run again';")},
		"003_more.sql":    {Data: []byte("CREATE TABLE more (id INTEGER PRIMARY KEY);")},
	}
	res, err := s.Migrate(ctx, later)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.From != 2 || res.To != 3 {
		t.Fatalf("got %+v, want applied 1, 2 -> 3", res)
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
