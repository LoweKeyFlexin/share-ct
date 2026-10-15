package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LoweKeyFlexin/share-ct/internal/drills"
	"github.com/LoweKeyFlexin/share-ct/internal/leaderboard"
)

// The user-facing REMOVE MY SCORES erases one complete Share CT identity. This
// proves the existing route handles both products while ordinary Drills requests
// cannot mutate Reaction and another player's identity remains usable.
func TestGlobalEraseBothScoreTypesAndReregister(t *testing.T) {
	h, db := newApp(t)
	request := func(method, path string, body any, token string, want int) []byte {
		t.Helper()
		raw := ""
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			raw = string(data)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(raw))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type identity struct {
		ID    string `json:"player_id"`
		Token string `json:"token"`
	}
	register := func(name string) identity {
		t.Helper()
		var p identity
		data := request("POST", "/v1/players", map[string]string{"display_name": name, "platform": "ios"}, "", 201)
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	reaction := func(p identity, id string) {
		t.Helper()
		samples := []float64{200, 205, 210}
		s := leaderboard.ScoreTrial(samples, 0)
		request("POST", "/v1/scores", leaderboard.SubmitRequest{ClientID: id, Source: "pad", BestMs: s.BestMs, AvgMs: s.AvgMs, Accuracy: s.Accuracy, Score: s.Score, AttemptsMs: samples, Misfires: 0, AppBuild: "1", Platform: "ios", DeviceModel: "iPhone", DeviceLabel: "Pad"}, p.Token, 201)
	}
	endless := func(p identity, id string) {
		t.Helper()
		score := 6
		request("POST", "/v1/drills/runs", drills.Request{ClientID: id, DrillID: "whiff-punish", Score: &score, Misses: 3, Source: "pad", AppBuild: "1", Platform: "ios", DeviceModel: "iPhone", DeviceLabel: "Pad"}, p.Token, 201)
	}
	a, b := register("Alpha"), register("Beta")
	reaction(a, "reaction-a")
	reaction(b, "reaction-b")
	before := string(request("GET", "/v1/board?source=pad", nil, "", 200))
	endless(a, "endless-a")
	endless(b, "endless-b")
	after := string(request("GET", "/v1/board?source=pad", nil, "", 200))
	if before != after {
		t.Fatal("Drills submissions changed Reaction board")
	}
	var refs [2]string
	for i, p := range []identity{a, b} {
		if err := db.DB().QueryRow(`SELECT player_ref FROM players WHERE id=?`, p.ID).Scan(&refs[i]); err != nil {
			t.Fatal(err)
		}
	}
	request("POST", "/v1/reports", map[string]string{"target_player_ref": refs[1], "reason": "spam"}, a.Token, 201)
	request("POST", "/v1/reports", map[string]string{"target_player_ref": refs[0], "reason": "harassment"}, b.Token, 201)
	// Another authenticated player cannot erase Alpha.
	request("DELETE", "/v1/players/"+a.ID, nil, b.Token, http.StatusForbidden)
	request("DELETE", "/v1/players/"+a.ID, nil, a.Token, http.StatusNoContent)
	for _, table := range []string{"submissions", "drill_bests", "drill_receipts", "drill_runs"} {
		var removed, retained int
		if err := db.DB().QueryRow(`SELECT count(*) FROM `+table+` WHERE player_id=?`, a.ID).Scan(&removed); err != nil {
			t.Fatal(err)
		}
		if err := db.DB().QueryRow(`SELECT count(*) FROM `+table+` WHERE player_id=?`, b.ID).Scan(&retained); err != nil {
			t.Fatal(err)
		}
		if removed != 0 || retained != 1 {
			t.Fatalf("%s removed=%d retained=%d", table, removed, retained)
		}
	}
	var reports int
	if err := db.DB().QueryRow(`SELECT count(*) FROM reports`).Scan(&reports); err != nil || reports != 0 {
		t.Fatal(reports, err)
	}
	request("DELETE", "/v1/players/"+a.ID, nil, a.Token, http.StatusNotFound)
	request("POST", "/v1/drills/scores", nil, a.Token, http.StatusUnauthorized)
	request("POST", "/v1/drills/runs", nil, a.Token, http.StatusUnauthorized)
	// Beta's bearer remains valid, and both boards retain only Beta's score.
	reaction(b, "reaction-b-next")
	var board drills.Board
	if err := json.Unmarshal(request("GET", "/v1/drills/board?drill_id=whiff-punish", nil, b.Token, 200), &board); err != nil {
		t.Fatal(err)
	}
	if len(board.Entries) != 1 || board.Own == nil || board.Own.PlayerRef != refs[1] {
		t.Fatalf("survivor board %+v", board)
	}
	// The erased installation can enroll afresh and send completed-run payloads to both APIs.
	fresh := register("Alpha")
	if fresh.ID == a.ID {
		t.Fatal("registration reused erased identity")
	}
	reaction(fresh, "reaction-fresh")
	endless(fresh, "endless-fresh")
}
