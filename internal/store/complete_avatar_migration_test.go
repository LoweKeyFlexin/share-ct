package store

import (
	"context"
	"fmt"
	"github.com/LoweKeyFlexin/share-ct/migrations"
	"testing"
	"testing/fstest"
)

func TestCompleteAvatarMigrationPreservesLegacyPlayersAndErasure(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	old := fstest.MapFS{}
	for _, m := range embedded(t) {
		if m.Version < 14 {
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
	exec(`INSERT INTO players(id,token_hash,display_name,platform,created_at,last_seen_at,player_ref,avatar) VALUES
 ('owner','hash','Owner','ios',1,2,'0123456789abcdef0123456789abcdef','1:puppet:1:0'),
 ('other','hash2','Other','android',1,2,'abcdef0123456789abcdef0123456789',NULL)`)
	exec(`INSERT INTO reports VALUES ('report','other','owner','spam','pending',124)`)
	if _, err := s.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	var avatar, ref, hash string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(avatar_complete,avatar),player_ref,token_hash FROM players WHERE id='owner'`).Scan(&avatar, &ref, &hash); err != nil {
		t.Fatal(err)
	}
	if avatar != "1:puppet:1:0" || ref != "0123456789abcdef0123456789abcdef" || hash != "hash" {
		t.Fatal("legacy identity/look changed")
	}
	complete := "3:puppet-heavy:1:0:scouter-visor,greatsword,lunch-break:t123456780:r,w2,c"
	exec(`UPDATE players SET avatar=NULL, avatar_complete=? WHERE id='owner'`, complete)
	exec(`DELETE FROM players WHERE id='owner'`)
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM reports`).Scan(&n); err != nil || n != 0 {
		t.Fatal("existing erasure cascade lost", err, n)
	}
}
