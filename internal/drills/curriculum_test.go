package drills

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewCurriculaHaveIndependentArcadeAndEndlessBoards(t *testing.T) {
	_, players, h := harness(t)
	for i, id := range []string{"whiff-punish-ii", "hit-confirm-ii", "anti-air-ii", "crosscut-ii", "drive-impact-ii", "parry"} {
		t.Run(id, func(t *testing.T) {
			_, token, err := players.Create(context.Background(), "Player"+string(rune('A'+i)), "ios")
			if err != nil {
				t.Fatal(err)
			}
			endless := req("endless", id, 17)
			data, _ := json.Marshal(endless)
			for _, code := range []int{201, 200} {
				w := call(h, "POST", "/v1/drills/scores", string(data), token)
				if w.Code != code {
					t.Fatalf("endless: %d %s", w.Code, w.Body.String())
				}
			}
			arcade := arcadeReq("arcade", 14, 14, 90000, 10, 50)
			arcade.DrillID = id
			postArcade(t, h, token, "/v1/drills/scores", arcade, 201)
			for _, mode := range []string{"arcade", "endless"} {
				w := call(h, "GET", "/v1/drills/board?drill_id="+id+"&mode="+mode, "", token)
				if w.Code != 200 {
					t.Fatalf("%s: %d %s", mode, w.Code, w.Body.String())
				}
				var board Board
				if err := json.Unmarshal(w.Body.Bytes(), &board); err != nil {
					t.Fatal(err)
				}
				expected := 17
				if mode == "arcade" {
					expected = *arcade.Score
				}
				if board.Own == nil || board.Own.Score != expected || board.BoardSize != 1 {
					t.Fatalf("mixed category/mode: %+v", board)
				}
			}
			for _, total := range []int{12, 15} {
				invalid := arcadeReq("invalid", total, total, 90000, 0, 50)
				invalid.DrillID = id
				if validate(invalid) != "total_opponents" {
					t.Fatalf("accepted new ladder size%d", total)
				}
			}
		})
	}
	original := arcadeReq("original", 14, 14, 90000, 0, 50)
	if got := validate(original); got != "" {
		t.Fatalf("original ladders also field fourteen opponents: %s", got)
	}
	if w := call(h, "GET", "/v1/drills/board?drill_id=hit-confirm&mode=endless", "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	} else {
		var b Board
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.BoardSize != 0 {
			t.Fatalf("new scores leaked into original: %+v %v", b, err)
		}
	}
}
