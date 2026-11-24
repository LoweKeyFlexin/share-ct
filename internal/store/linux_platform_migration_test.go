package store

import (
	"context"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/LoweKeyFlexin/share-ct/migrations"
)

func TestLinuxPlatformMigrationPreservesRowsAndAcceptsLinuxAcrossScoreTables(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	before18 := fstest.MapFS{}
	for _, m := range embedded(t) {
		if m.Version < 18 {
			name := fmt.Sprintf("%03d_%s.sql", m.Version, m.Name)
			before18[name] = &fstest.MapFile{Data: migrationBytes(t, name)}
		}
	}
	if _, err := s.Migrate(ctx, before18); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO players(id,token_hash,display_name,platform,created_at,last_seen_at,player_ref) VALUES ('ios-owner','h1','IOS','ios',1,2,'0123456789abcdef0123456789abcdef')`)
	exec(`INSERT INTO players(id,token_hash,display_name,platform,created_at,last_seen_at,player_ref) VALUES ('deck-owner','h2','DECK','linux',1,2,'fedcba9876543210fedcba9876543210')`)

	// Seed pre-018 rows in all five score tables, including reaction_crown_earned = 1 from 017.
	exec(`INSERT INTO submissions(id,player_id,client_id,source,best_ms,avg_ms,accuracy,score,attempts_ms,misfires,client_best_ms,client_avg_ms,client_accuracy,client_score,app_build,platform,device_model,device_label,trace,created_at,reaction_crown_earned) VALUES (41,'ios-owner','rx-ios','touch',180.0,190.0,1,2100,'[180,190,200]',0,180.0,190.0,1,2100,'1.4','ios','iPhone17,2','Touch',NULL,100,1)`)
	exec(`INSERT INTO drill_bests(id,player_id,drill_id,client_id,score,source,app_build,platform,device_model,device_label,created_at) VALUES (51,'ios-owner','whiff-punish','db-ios',20,'pad','1.4','ios','iPhone17,2','DualSense',100)`)
	exec(`INSERT INTO drill_runs(id,player_id,client_id,receipt_id,body_hash,drill_id,score,source,app_build,platform,device_model,device_label,created_at) VALUES (61,'ios-owner','dr-ios',61,'h61','whiff-punish',20,'pad','1.4','ios','iPhone17,2','DualSense',100)`)
	exec(`INSERT INTO arcade_bests(id,player_id,client_id,receipt_id,body_hash,drill_id,score,enemies_defeated,total_opponents,completed,elapsed_ms,perfect_opponents,hits,source,app_build,platform,device_model,device_label,created_at,publishes_run) VALUES (71,'ios-owner','ab-ios',71,'h71','whiff-punish',500,12,12,1,45000,6,30,'pad','1.4','ios','iPhone17,2','DualSense',100,1)`)
	exec(`INSERT INTO arcade_runs(id,player_id,client_id,receipt_id,body_hash,drill_id,score,enemies_defeated,total_opponents,completed,elapsed_ms,perfect_opponents,hits,source,app_build,platform,device_model,device_label,created_at,publishes_run) VALUES (81,'ios-owner','ar-ios',81,'h81','whiff-punish',500,12,12,1,45000,6,30,'pad','1.4','ios','iPhone17,2','DualSense',100,1)`)

	// Retention can delete the highest ID before an upgrade. These counters must
	// survive even when they exceed every retained row, or a feed can reuse IDs.
	const highWater = 500
	autoincrementTables := []string{"drill_bests", "drill_runs", "arcade_bests", "arcade_runs"}
	for _, table := range autoincrementTables {
		exec(`UPDATE sqlite_sequence SET seq = ? WHERE name = ?`, highWater, table)
	}

	// Before migration 018, inserting platform='linux' into submissions fails CHECK.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO submissions(player_id,client_id,source,best_ms,avg_ms,accuracy,score,attempts_ms,misfires,client_best_ms,client_avg_ms,client_accuracy,client_score,app_build,platform,device_model,device_label,trace,created_at) VALUES ('deck-owner','rx-pre','pad',204.0,215.6,1,1200,'[215,228,204]',0,204.0,215.6,1,1200,'1.4.0','linux','Steam Deck','Steam Deck',NULL,101)`); err == nil {
		t.Fatal("expected pre-018 CHECK constraint to reject linux")
	}

	res, err := s.Migrate(ctx, migrations.FS)
	if err != nil || res.Applied != 1 || res.To != 18 {
		t.Fatalf("migrate to 18: %+v %v", res, err)
	}

	// Verify existing crown row and all 4 drill/arcade rows survived intact.
	var crown int
	if err := s.db.QueryRowContext(ctx, `SELECT reaction_crown_earned FROM submissions WHERE id = 41`).Scan(&crown); err != nil || crown != 1 {
		t.Fatalf("preserved crown = %d err=%v", crown, err)
	}

	// Now platform='linux' succeeds across all five score tables.
	exec(`INSERT INTO submissions(player_id,client_id,source,best_ms,avg_ms,accuracy,score,attempts_ms,misfires,client_best_ms,client_avg_ms,client_accuracy,client_score,app_build,platform,device_model,device_label,trace,created_at) VALUES ('deck-owner','rx-deck','pad',204.0,215.6,1,1200,'[215,228,204]',0,204.0,215.6,1,1200,'1.4.0','linux','Steam Deck','Steam Deck',NULL,101)`)
	exec(`INSERT INTO drill_bests(player_id,drill_id,client_id,score,source,app_build,platform,device_model,device_label,created_at) VALUES ('deck-owner','whiff-punish','db-deck',12,'pad','1.4.0','linux','Steam Deck','Steam Deck',101)`)
	exec(`INSERT INTO drill_runs(player_id,client_id,receipt_id,body_hash,drill_id,score,source,app_build,platform,device_model,device_label,created_at) VALUES ('deck-owner','dr-deck',62,'h62','whiff-punish',12,'pad','1.4.0','linux','Steam Deck','Steam Deck',101)`)
	exec(`INSERT INTO arcade_bests(player_id,client_id,receipt_id,body_hash,drill_id,score,enemies_defeated,total_opponents,completed,elapsed_ms,perfect_opponents,hits,source,app_build,platform,device_model,device_label,created_at,publishes_run) VALUES ('deck-owner','ab-deck',72,'h72','whiff-punish',400,12,12,1,52000,4,25,'pad','1.4.0','linux','Steam Deck','Steam Deck',101,1)`)
	exec(`INSERT INTO arcade_runs(player_id,client_id,receipt_id,body_hash,drill_id,score,enemies_defeated,total_opponents,completed,elapsed_ms,perfect_opponents,hits,source,app_build,platform,device_model,device_label,created_at,publishes_run) VALUES ('deck-owner','ar-deck',82,'h82','whiff-punish',400,12,12,1,52000,4,25,'pad','1.4.0','linux','Steam Deck','Steam Deck',101,1)`)

	for _, table := range autoincrementTables {
		var nextID int
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM `+table+` WHERE player_id = 'deck-owner'`).Scan(&nextID); err != nil || nextID != highWater+1 {
			t.Fatalf("%s generated id %d after migration, want %d (err=%v)", table, nextID, highWater+1, err)
		}
	}

	// Unknown platform is still rejected by CHECK.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO submissions(player_id,client_id,source,best_ms,avg_ms,accuracy,score,attempts_ms,misfires,client_best_ms,client_avg_ms,client_accuracy,client_score,app_build,platform,device_model,device_label,trace,created_at) VALUES ('deck-owner','rx-bad','pad',204.0,215.6,1,1200,'[215,228,204]',0,204.0,215.6,1,1200,'1.4.0','web','Browser','Web',NULL,102)`); err == nil {
		t.Fatal("CHECK constraint admitted unknown platform 'web'")
	}

	// ON DELETE CASCADE still removes all five score tables when the player is erased.
	exec(`DELETE FROM players WHERE id = 'deck-owner'`)
	for _, tbl := range []string{"submissions", "drill_bests", "drill_runs", "arcade_bests", "arcade_runs"} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+tbl+` WHERE player_id = 'deck-owner'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("table %s retained %d rows after player delete (err=%v)", tbl, n, err)
		}
		var ios int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+tbl+` WHERE player_id = 'ios-owner'`).Scan(&ios); err != nil || ios != 1 {
			t.Fatalf("table %s lost ios-owner row (got %d, err=%v)", tbl, ios, err)
		}
	}

	if result, err := s.Migrate(ctx, migrations.FS); err != nil || result.Applied != 0 || result.To != 18 {
		t.Fatalf("second migration: %+v %v", result, err)
	}
	var integrity string
	if err := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity check: %q (err=%v)", integrity, err)
	}
	var violations int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("foreign key violations: %d (err=%v)", violations, err)
	}
}
