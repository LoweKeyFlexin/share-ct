package store

import (
	"context"
	"fmt"
	"github.com/LoweKeyFlexin/share-ct/migrations"
	"testing"
	"testing/fstest"
)

func TestOriginalArcade015PreservesRowsReceiptsSequencesAndCascades(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	old := fstest.MapFS{}
	for _, m := range embedded(t) {
		if m.Version < 15 {
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
	exec(`INSERT INTO players(id,token_hash,display_name,platform,created_at,last_seen_at,player_ref)
      VALUES ('owner','hash','Owner','ios',1,2,'0123456789abcdef0123456789abcdef')`)
	exec(`INSERT INTO arcade_receipts(id,player_id,client_id,body_hash,created_at,publishes_run)
      VALUES (11,'owner','old-arcade','old-body',123,1)`)
	for _, table := range []string{"arcade_bests", "arcade_runs"} {
		exec(`INSERT INTO ` + table + `(id,player_id,client_id,receipt_id,body_hash,drill_id,score,enemies_defeated,total_opponents,completed,elapsed_ms,perfect_opponents,hits,source,app_build,platform,device_model,device_label,created_at,publishes_run)
          VALUES (11,'owner','old-arcade',11,'old-body','whiff-punish',9,1,13,0,120000,0,9,'pad','b1','ios','Phone','Pad',123,1)`)
	}
	exec(`INSERT INTO drill_feed_order(player_id,mode,run_id,created_at) VALUES ('owner','arcade',11,123)`)
	exec(`UPDATE sqlite_sequence SET seq=900 WHERE name IN ('arcade_bests','arcade_runs')`)
	snapshot := func(table string) string {
		t.Helper()
		rows, err := s.db.QueryContext(ctx, `SELECT * FROM `+table+` ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var result string
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			result += fmt.Sprintf("%#v\n", values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := map[string]string{}
	for _, table := range []string{"players", "arcade_receipts", "arcade_bests", "arcade_runs", "drill_feed_order"} {
		before[table] = snapshot(table)
	}
	if result, err := s.Migrate(ctx, migrations.FS); err != nil || result.Applied != len(embedded(t))-14 {
		t.Fatalf("upgrade: %+v %v", result, err)
	}
	for table, expected := range before {
		if got := snapshot(table); got != expected {
			t.Fatalf("%s changed: %s -> %s", table, expected, got)
		}
	}
	if result, err := s.Migrate(ctx, migrations.FS); err != nil || result.Applied != 0 {
		t.Fatalf("restart: %+v %v", result, err)
	}
	for _, table := range []string{"arcade_bests", "arcade_runs"} {
		exec(`INSERT INTO ` + table + `(player_id,client_id,receipt_id,body_hash,drill_id,score,enemies_defeated,total_opponents,completed,elapsed_ms,perfect_opponents,hits,source,app_build,platform,device_model,device_label,created_at,publishes_run)
          VALUES ('owner','new-arcade',12,'new-body','hit-confirm',9,1,14,0,120000,0,9,'pad','b2','ios','Phone','Pad',124,1)`)
		var id int
		if err := s.db.QueryRow(`SELECT id FROM ` + table + ` WHERE client_id='new-arcade'`).Scan(&id); err != nil || id != 901 {
			t.Fatalf("%s reused a pruned ID: %d %v", table, id, err)
		}
		if _, err := s.db.Exec(`UPDATE ` + table + ` SET total_opponents=15 WHERE client_id='new-arcade'`); err == nil {
			t.Fatalf("%s lost upper bound", table)
		}
		if _, err := s.db.Exec(`UPDATE ` + table + ` SET drill_id='parry',total_opponents=12 WHERE client_id='new-arcade'`); err == nil {
			t.Fatalf("%s widened curriculum lower bound", table)
		}
	}
	var indexes int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('ix_arcade_board','ix_arcade_runs_recent','ix_arcade_runs_player','ix_arcade_runs_expiry')`).Scan(&indexes); err != nil || indexes != 4 {
		t.Fatalf("indexes: %d %v", indexes, err)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Error("broken foreign keys")
	}
	rows.Close()
	exec(`DELETE FROM players WHERE id='owner'`)
	for _, table := range []string{"arcade_receipts", "arcade_bests", "arcade_runs", "drill_feed_order"} {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s did not cascade: %d %v", table, count, err)
		}
	}
}
