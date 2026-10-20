package drills

import (
	"context"
	"encoding/json"
	"testing"
)

// Drills Top and Recent rows carry each player's current avatar, or an explicit null.
func TestDrillsRowsCarryTheAvatarOrNull(t *testing.T) {
	_, pl, h := harness(t)
	ctx := context.Background()
	a, tokenA, _ := pl.Create(ctx, "Alpha", "ios")
	_, tokenB, _ := pl.Create(ctx, "Bravo", "ios")
	for i, token := range []string{tokenA, tokenB} {
		data, _ := json.Marshal(req("run"+string(rune('a'+i)), "whiff-punish", 10-i))
		if w := call(h, "POST", "/v1/drills/runs", string(data), token); w.Code/100 != 2 {
			t.Fatalf("submit %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	avatar := "1:puppet-oni:1:7:pirate-hat"
	if err := pl.SetAvatar(ctx, a.ID, &avatar); err != nil {
		t.Fatal(err)
	}
	for _, sort := range []string{"top", "recent"} {
		w := call(h, "GET", "/v1/drills/board?drill_id=whiff-punish&sort="+sort, "", tokenA)
		if w.Code != 200 {
			t.Fatalf("%s: %d", sort, w.Code)
		}
		var out struct {
			Entries []map[string]any `json:"entries"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Entries) != 2 {
			t.Fatalf("%s: %v %s", sort, err, w.Body.String())
		}
		got := map[any]any{}
		for _, e := range out.Entries {
			v, present := e["avatar"]
			if !present {
				t.Fatalf("%s: row without an avatar key", sort)
			}
			got[e["display_name"]] = v
		}
		if got["Alpha"] != avatar || got["Bravo"] != nil {
			t.Errorf("%s: %v", sort, got)
		}
	}
}
