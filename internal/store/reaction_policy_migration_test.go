package store

import (
	"context"
	"fmt"
	"github.com/LoweKeyFlexin/share-ct/migrations"
	"testing"
	"testing/fstest"
)

func TestReactionFloorMigrationPreservesRowsRetriesAndErasure(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	old := fstest.MapFS{}
	for _, m := range embedded(t) {
		if m.Version < 16 {
			name := fmt.Sprintf("%03d_%s.sql", m.Version, m.Name)
			old[name] = &fstest.MapFile{Data: migrationBytes(t, name)}
		}
	}
	if _, err := s.Migrate(ctx, old); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO players(id,token_hash,display_name,platform,created_at,last_seen_at,player_ref) VALUES ('owner','hash','Owner','ios',1,2,'0123456789abcdef0123456789abcdef')`)
	insert := `INSERT INTO submissions(id,player_id,client_id,source,best_ms,avg_ms,accuracy,score,attempts_ms,misfires,client_best_ms,client_avg_ms,client_accuracy,client_score,app_build,platform,device_model,device_label,trace,created_at) VALUES (?,'owner',?,'touch',?,?,1,2500,?,0,?,?,1,2500,'b1','ios','Phone','Touch',NULL,123)`
	exec(insert, 41, "old", 200.0, 210.0, "[200,210,220]", 200.0, 210.0)
	snapshot := func() string {
		t.Helper()
		rows, err := s.db.QueryContext(ctx, `SELECT * FROM submissions ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		var out string
		for rows.Next() {
			v := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range v {
				dest[i] = &v[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			out += fmt.Sprintf("%#v", v)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	if result, err := s.Migrate(ctx, migrations.FS); err != nil || result.Applied != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("old row changed: %s -> %s", before, after)
	}
	exec(insert, 42, "six", 100.0, 120.0, "[100,120,140]", 100.0, 120.0)
	if _, err := s.db.ExecContext(ctx, insert, 43, "too-fast", 99.999, 110.0, "[99.999,110,120]", 99.999, 110.0); err == nil {
		t.Fatal("database admitted below-floor time")
	}
	if _, err := s.db.ExecContext(ctx, insert, 44, "six", 100.0, 120.0, "[100,120,140]", 100.0, 120.0); err == nil {
		t.Fatal("retry uniqueness lost")
	}
	if result, err := s.Migrate(ctx, migrations.FS); err != nil || result.Applied != 0 {
		t.Fatalf("restart %+v %v", result, err)
	}
	exec(`DELETE FROM players WHERE id='owner'`)
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM submissions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade count=%d err=%v", count, err)
	}
}
