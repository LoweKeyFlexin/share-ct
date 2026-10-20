package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestMentalStackMigrationPreservesExistingDataIdentityAndCascades(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	old := fstest.MapFS{}
	for _, m := range embedded(t) {
		if m.Version < 10 {
			name := fmt.Sprintf("%03d_%s.sql", m.Version, m.Name)
			old[name] = &fstest.MapFile{Data: migrationBytes(t, name)}
		}
	}
	if _, err := s.Migrate(ctx, old); err != nil {
		t.Fatal(err)
	}
	// The upgrade under test is 010 alone; later migrations have their own tests.
	upTo10 := fstest.MapFS{}
	for _, m := range embedded(t) {
		if m.Version <= 10 {
			name := fmt.Sprintf("%03d_%s.sql", m.Version, m.Name)
			upTo10[name] = &fstest.MapFile{Data: migrationBytes(t, name)}
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO players(id,token_hash,display_name,platform,created_at,last_seen_at,player_ref) VALUES
	 ('owner','hash','Owner','ios',1,2,'0123456789abcdef0123456789abcdef'),
	 ('other','hash2','Other','android',1,2,'abcdef0123456789abcdef0123456789')`)
	for i, drill := range []string{"whiff-punish", "hit-confirm", "anti-air", "crosscut", "drive-impact"} {
		id := i + 11
		client := fmt.Sprint("run", i)
		exec(`INSERT INTO drill_receipts(id,player_id,client_id,body_hash,created_at,publishes_run) VALUES (?,'owner',?,'body',123,1)`, id, client)
		exec(`INSERT INTO drill_bests VALUES (?,'owner',?,?,7,'pad','b1','ios','Phone','Long Controller',123)`, id, drill, client)
		exec(`INSERT INTO drill_runs VALUES (?,'owner',?,?,'body',?,7,'pad','b1','ios','Phone','Long Controller',123)`, id, client, id, drill)
	}
	exec(`INSERT INTO submissions VALUES (40,'owner','reaction','touch',200,220,1,500,'[200,240]',0,200,220,1,500,'b1','ios','Phone','Touch',NULL,123)`)
	exec(`INSERT INTO reports VALUES ('report','other','owner','spam','pending',124)`)
	// Simulate the highest IDs already having been pruned before deployment.
	exec(`UPDATE sqlite_sequence SET seq=900 WHERE name IN ('drill_bests','drill_runs')`)
	snapshot := func(table string) string {
		t.Helper()
		rows, err := s.db.QueryContext(ctx, `SELECT * FROM `+table+` ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var result string
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
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
	for _, table := range []string{"players", "submissions", "reports", "drill_bests", "drill_runs", "drill_receipts"} {
		before[table] = snapshot(table)
	}
	result, err := s.Migrate(ctx, upTo10)
	if err != nil || result.Applied != 1 || result.From != 9 || result.To != 10 {
		t.Fatalf("upgrade %+v: %v", result, err)
	}
	for table, expected := range before {
		if got := snapshot(table); !reflect.DeepEqual(got, expected) {
			t.Fatalf("migration changed %s: before %s after %s", table, expected, got)
		}
	}
	if result, err := s.Migrate(ctx, upTo10); err != nil || result.Applied != 0 {
		t.Fatalf("restart %+v: %v", result, err)
	}
	exec(`INSERT INTO drill_bests(player_id,drill_id,client_id,score,source,app_build,platform,device_model,device_label,created_at)
	 VALUES ('owner','mental-stack','mixed',8,'keyboard','b2','mac','Mac','Keys',125)`)
	exec(`INSERT INTO drill_runs(player_id,client_id,receipt_id,body_hash,drill_id,score,source,app_build,platform,device_model,device_label,created_at)
	 VALUES ('owner','mixed',11,'mixedbody','mental-stack',8,'keyboard','b2','mac','Mac','Keys',125)`)
	for _, table := range []string{"drill_bests", "drill_runs"} {
		var id int
		if err := s.db.QueryRow(`SELECT id FROM ` + table + ` WHERE drill_id='mental-stack'`).Scan(&id); err != nil || id != 901 {
			t.Fatalf("%s reused a pruned identity: %d %v", table, id, err)
		}
		if _, err := s.db.Exec(`UPDATE ` + table + ` SET drill_id='unknown' WHERE drill_id='mental-stack'`); err == nil {
			t.Fatalf("%s lost category constraint", table)
		}
	}
	var indexes int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN
	 ('ix_drill_board','ix_drill_runs_recent','ix_drill_runs_player','ix_drill_runs_expiry','ix_drill_receipt_player')`).Scan(&indexes); err != nil || indexes != 5 {
		t.Fatalf("indexes %d %v", indexes, err)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Error("migration left broken foreign keys")
	}
	rows.Close()
	exec(`DELETE FROM players WHERE id='owner'`)
	for _, table := range []string{"submissions", "reports", "drill_bests", "drill_runs", "drill_receipts"} {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("erase failed for %s: %d %v", table, count, err)
		}
	}
	var other int
	if err := s.db.QueryRow(`SELECT count(*) FROM players WHERE id='other'`).Scan(&other); err != nil || other != 1 {
		t.Fatalf("other player changed: %d %v", other, err)
	}
}
