package leaderboard

import (
	"context"
	"testing"
)

// Board and Recent rows carry the player's CURRENT avatar, or null, on every row.
func TestBoardRowsCarryTheCurrentAvatarOrNull(t *testing.T) {
	hs := newHarness(t)
	ctx := context.Background()
	withAvatar, tokenA := hs.player()
	_, tokenB := hs.player()
	if rec, _ := hs.submit(tokenA, trial("a1", "touch", []float64{200, 205, 210}, 0)); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec, _ := hs.submit(tokenB, trial("b1", "touch", []float64{230, 240, 250}, 0)); rec.Code != 201 {
		t.Fatal(rec.Code)
	}
	if err := hs.pl.Store().SetAvatar(ctx, withAvatar, strp("1:puppet-riot:1:4")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"?source=touch", "?source=all"} {
		rows := hs.board(path)
		if len(rows) != 2 {
			t.Fatalf("%s: %d rows", path, len(rows))
		}
		if rows[0]["avatar"] != "1:puppet-riot:1:4" {
			t.Errorf("%s: row 1 avatar %v", path, rows[0]["avatar"])
		}
		if v, present := rows[1]["avatar"]; !present || v != nil {
			t.Errorf("%s: a player without an avatar must get an explicit null, got %v (present %v)", path, v, present)
		}
	}
	rec, out := hs.do("GET", "/v1/recent?source=touch", "", "", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	found := false
	for _, e := range out["entries"].([]any) {
		if e.(map[string]any)["avatar"] == "1:puppet-riot:1:4" {
			found = true
		}
	}
	if !found {
		t.Error("recent rows do not carry the avatar")
	}
	// A later change shows on every row: one avatar per player.
	if err := hs.pl.Store().SetAvatar(ctx, withAvatar, nil); err != nil {
		t.Fatal(err)
	}
	if v := hs.board("?source=touch")[0]["avatar"]; v != nil {
		t.Errorf("cleared avatar still served: %v", v)
	}
}

func strp(s string) *string { return &s }
