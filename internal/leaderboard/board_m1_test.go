package leaderboard

// The M1 server delta, ported from the app repo's
// docs/plans/leaderboard/m1-server/board_m1_test.go onto this package's harness. The
// builder's fixtures carried the app mock's score numbers (6431, 9000), which the
// server's recompute refuses as score_mismatch, so every body here is built by trial(),
// whose numbers are honest; the shape and intent of each case are kept. These are table
// tests on purpose: the sort orders and the delete codes are both places where the wrong
// answer is a plausible-looking one.

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// named creates a player whose display name the assertions can read back off the board.
func (hs *harness) named(name string) (id, token string) {
	hs.t.Helper()
	p, token, err := hs.pl.Store().Create(context.Background(), name, "ios")
	if err != nil {
		hs.t.Fatal(err)
	}
	return p.ID, token
}

// submitAt posts body with the clock at `at`, so created_at is that instant rather than
// whatever the test's speed made it, and fails the test unless a row was created.
func (hs *harness) submitAt(token string, body map[string]any, at time.Time) {
	hs.t.Helper()
	hs.now = at
	if rec, _ := hs.submit(token, body); rec.Code != http.StatusCreated {
		hs.t.Fatalf("submit %v at %s: %d %s", body["client_id"], at.Format(time.RFC3339), rec.Code, rec.Body.String())
	}
}

// on is a trial() body played on a named device.
func on(device string, body map[string]any) map[string]any {
	body["device_label"] = device
	return body
}

// --- 1. the two new board fields ---

// The board's row must carry the label and time OF THE WINNING SUBMISSION, not of the
// player's latest. A join on the player alone passes a naive test and shows the wrong pad.
func TestBoardEntryCarriesDeviceLabelAndCreatedAtOfTheWinningRow(t *testing.T) {
	hs := newHarness(t)
	_, tok := hs.named("AARON")

	// The winning trial, on a pad: the faster single AND the higher score, so it wins
	// under either sort.
	hs.submitAt(tok, on("DualSense Wireless Controller", trial("win", "pad", []float64{191.4, 240.2, 267.4}, 0)),
		time.Date(2026, 9, 11, 3, 0, 0, 0, time.UTC))
	// A LATER, WORSE trial on a different device. If the board joins on the player's
	// most recent row instead of the winning one, the entry below will say "Touch".
	hs.submitAt(tok, on("Touch", trial("late", "pad", []float64{480, 500, 520}, 0)),
		time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC))

	for _, sort := range []string{"", "&sort=fastest", "&sort=score"} {
		entries := hs.board("?source=pad&window=all" + sort)
		if len(entries) != 1 {
			t.Fatalf("%q: one row per player expected, got %d", sort, len(entries))
		}
		if got := entries[0]["device_label"]; got != "DualSense Wireless Controller" {
			t.Errorf("%q: device_label = %q, want the WINNING row's pad; a later/worse submission's "+
				"label means the board is joining on the wrong submission", sort, got)
		}
		if got := entries[0]["created_at"]; got != "2026-09-11T03:00:00Z" {
			t.Errorf("%q: created_at = %q, want the winning row's timestamp (RFC 3339, UTC)", sort, got)
		}
	}
}

// Which row wins depends on the sort. One player, two rows that disagree: the trial
// with a misfire is the faster single but the lower score. Under fastest the board
// shows it, and its pad; under score it shows the other. A board that picked the winner
// one way and ordered the other would list a pad the ranking is not about.
func TestBoardWinningRowFollowsTheSort(t *testing.T) {
	hs := newHarness(t)
	_, tok := hs.named("AARON")
	fast := trial("fast", "pad", []float64{180, 500}, 1)          // best 180, accuracy 2/3
	steady := trial("steady", "pad", []float64{200, 205, 210}, 0) // best 200, the clean 2100
	if fast["score"].(int) >= steady["score"].(int) || fast["best_ms"].(float64) >= steady["best_ms"].(float64) {
		t.Fatalf("fixture must disagree: fast %v ms/%v, steady %v ms/%v",
			fast["best_ms"], fast["score"], steady["best_ms"], steady["score"])
	}
	hs.submitAt(tok, on("Hit Box", fast), time.Date(2026, 9, 11, 3, 0, 0, 0, time.UTC))
	hs.submitAt(tok, on("DualSense Wireless Controller", steady), time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC))

	cases := []struct {
		query, label, created string
		attempts              []any
		misfires              float64
	}{
		{"?source=pad", "Hit Box", "2026-09-11T03:00:00Z", []any{float64(180), float64(500)}, 1},
		{"?source=pad&sort=fastest", "Hit Box", "2026-09-11T03:00:00Z", []any{float64(180), float64(500)}, 1},
		{"?source=pad&sort=score", "DualSense Wireless Controller", "2026-09-11T04:00:00Z", []any{float64(200), float64(205), float64(210)}, 0},
	}
	for _, c := range cases {
		entries := hs.board(c.query)
		if len(entries) != 1 {
			t.Fatalf("%s: %d entries, want 1", c.query, len(entries))
		}
		e := entries[0]
		if e["device_label"] != c.label || e["created_at"] != c.created {
			t.Errorf("%s: entry is %v · %v, want %s · %s", c.query, e["device_label"], e["created_at"], c.label, c.created)
		}
		if !reflect.DeepEqual(e["attempts_ms"], c.attempts) || e["misfires"] != c.misfires {
			t.Errorf("%s: attempts are %v + %v misses, want winning row %v + %v misses",
				c.query, e["attempts_ms"], e["misfires"], c.attempts, c.misfires)
		}
	}
}

func TestBoardEntryDeclaresExactlyTheClientContract(t *testing.T) {
	hs := newHarness(t)
	_, tok := hs.named("AARON")
	hs.submitAt(tok, on("Pad", trial("a", "pad", []float64{200, 205, 210}, 0)), hs.now)
	_, bare := hs.named("NOLABEL")
	hs.submitAt(bare, on("", trial("b", "pad", []float64{250, 255, 260}, 0)), hs.now.Add(time.Second))

	entries := hs.board("?source=pad&window=all")
	if len(entries) != 2 {
		t.Fatalf("%d entries, want 2", len(entries))
	}
	want := []string{
		"rank", "player_short", "display_name", "score", "best_ms", "avg_ms",
		"accuracy", "tier", "platform", "device_label", "created_at", "source",
		"attempts_ms", "misfires",
	}
	slices.Sort(want)
	for _, e := range entries {
		got := make([]string, 0, len(e))
		for k := range e {
			got = append(got, k)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("board entry %v declares %v, want exactly %v; the client and server must move together",
				e["display_name"], got, want)
		}
	}
	// A submission without a label (stored as NULL) still carries the key, as "".
	if entries[1]["display_name"] != "NOLABEL" || entries[1]["device_label"] != "" {
		t.Errorf("row without a label = %v, want device_label \"\"", entries[1])
	}
	if !reflect.DeepEqual(entries[0]["attempts_ms"], []any{float64(200), float64(205), float64(210)}) {
		t.Errorf("AARON attempts = %v, want all three winning-run times", entries[0]["attempts_ms"])
	}
}

// --- 2. sort ---

func TestBoardSortOrders(t *testing.T) {
	// Two players where score order and fastest order DISAGREE. If they agreed, this test
	// would pass whatever the handler did.
	//   SLOWPOKE: 200/205/210, no misfires  -> best 200, the clean 2100
	//   QUICK:    180/500 plus a misfire    -> best 180, a lower score
	slowpoke, quick := []float64{200, 205, 210}, []float64{180, 500}
	if ScoreTrial(quick, 1).Score >= ScoreTrial(slowpoke, 0).Score {
		t.Fatalf("fixture must disagree: QUICK scores %d, SLOWPOKE %d", ScoreTrial(quick, 1).Score, ScoreTrial(slowpoke, 0).Score)
	}
	cases := []struct {
		query string
		want  []string // display names, in order
		why   string
	}{
		{"?source=pad&window=all", []string{"QUICK", "SLOWPOKE"},
			"no sort= must default to FASTEST (pick A): a bare board and the app must agree"},
		{"?source=pad&window=all&sort=fastest", []string{"QUICK", "SLOWPOKE"},
			"sort=fastest orders by best_ms ASC"},
		{"?source=pad&window=all&sort=score", []string{"SLOWPOKE", "QUICK"},
			"sort=score orders by score DESC"},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			hs := newHarness(t)
			_, slow := hs.named("SLOWPOKE")
			_, fast := hs.named("QUICK")
			hs.submitAt(slow, on("Pad", trial("s", "pad", slowpoke, 0)), hs.now)
			hs.submitAt(fast, on("Pad", trial("q", "pad", quick, 1)), hs.now.Add(time.Second))
			got := hs.board(c.query)
			if len(got) != 2 {
				t.Fatalf("want 2 entries, got %d", len(got))
			}
			names := []string{got[0]["display_name"].(string), got[1]["display_name"].(string)}
			if !slices.Equal(names, c.want) {
				t.Errorf("order = %v, want %v: %s", names, c.want, c.why)
			}
			if got[0]["rank"] != float64(1) || got[1]["rank"] != float64(2) {
				t.Errorf("ranks %v %v, want 1 2", got[0]["rank"], got[1]["rank"])
			}
		})
	}
}

// Under fastest, a tie on best_ms goes to the higher score, then to the earlier row.
func TestBoardFastestTieBreaks(t *testing.T) {
	hs := newHarness(t)
	_, early := hs.named("EARLY")
	_, late := hs.named("LATE")
	_, lower := hs.named("LOWER")
	base := hs.now
	hs.submitAt(lower, on("Pad", trial("c", "pad", []float64{200, 300, 400}, 0)), base) // best 200, the lowest score, first in
	hs.submitAt(early, on("Pad", trial("a", "pad", []float64{200, 205, 210}, 0)), base.Add(time.Second))
	hs.submitAt(late, on("Pad", trial("b", "pad", []float64{200, 205, 210}, 0)), base.Add(2*time.Second))

	got := hs.board("?source=pad&sort=fastest")
	var names []string
	for _, e := range got {
		names = append(names, e["display_name"].(string))
	}
	if want := []string{"EARLY", "LATE", "LOWER"}; !slices.Equal(names, want) {
		t.Errorf("order = %v, want %v: equal best_ms breaks to the higher score, then the earlier created_at", names, want)
	}
	// The POST reply's rank is on the same default order.
	for i, name := range []string{"EARLY", "LATE", "LOWER"} {
		var id string
		if err := hs.db.DB().QueryRow(`SELECT id FROM players WHERE display_name = ?`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		rank, size, err := hs.lb.Rank(context.Background(), "pad", id)
		if err != nil || rank != i+1 || size != 3 {
			t.Errorf("Rank(%s) = %d/%d %v, want %d/3", name, rank, size, err, i+1)
		}
	}
}

func TestUnknownSortIsRejectedRatherThanSilentlyDefaulted(t *testing.T) {
	hs := newHarness(t)
	for _, sort := range []string{"fastst", "FASTEST", "best_ms", "score,fastest"} {
		rec, out := hs.do("GET", "/v1/board?source=pad&window=all&sort="+sort, "", "", "")
		if rec.Code != http.StatusBadRequest || out["error"] != "bad_request" || out["reason"] != "sort" {
			t.Errorf("sort=%s gave %d %s, want 400 bad_request reason sort. A typo that quietly returns "+
				"a different ordering is indistinguishable from the board being wrong.", sort, rec.Code, rec.Body.String())
		}
	}
	// The store refuses too: there is no layer at which an unknown sort becomes a default.
	if _, err := hs.lb.Board(context.Background(), "pad", WindowAll, "fastst", DefaultLimit); err == nil {
		t.Error("Store.Board accepted an unknown sort instead of returning an error")
	}
}

// --- 3. delete: the status codes the client's retry loop depends on ---

func TestDeletePlayerStatusCodes(t *testing.T) {
	t.Run("known player is 204 and erases the submissions too", func(t *testing.T) {
		hs := newHarness(t)
		pid, tok := hs.named("AARON")
		hs.submitAt(tok, on("Pad", trial("a", "pad", []float64{200, 205, 210}, 0)), hs.now)
		if rec, _ := hs.do("DELETE", "/v1/players/"+pid, "", tok, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("delete gave %d %s, want 204", rec.Code, rec.Body.String())
		}
		if entries := hs.board("?source=pad&window=all"); len(entries) != 0 {
			t.Errorf("board still has %d entries after the player was deleted; a player row erased "+
				"with submissions left behind leaves scores nobody can delete", len(entries))
		}
	})

	t.Run("unknown player is 404, not 204 and not 500", func(t *testing.T) {
		hs := newHarness(t)
		_, tok := hs.named("AARON")
		rec, out := hs.do("DELETE", "/v1/players/does-not-exist", "", tok, "")
		if rec.Code != http.StatusNotFound || out["error"] != "not_found" {
			t.Errorf("delete of an unknown player gave %d %s, want 404 not_found. The client treats 404 as "+
				"ALREADY ERASED and stops retrying; anything else keeps the player id and token on the "+
				"phone forever for a player that does not exist.", rec.Code, rec.Body.String())
		}
	})

	t.Run("a second delete of the same player is 404, not an error", func(t *testing.T) {
		hs := newHarness(t)
		pid, tok := hs.named("AARON")
		if rec, _ := hs.do("DELETE", "/v1/players/"+pid, "", tok, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("first delete gave %d, want 204", rec.Code)
		}
		rec, _ := hs.do("DELETE", "/v1/players/"+pid, "", tok, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("second delete gave %d, want 404: the retry IS the normal case here, because "+
				"the client repeats the request until it is confirmed", rec.Code)
		}
	})

	t.Run("a wrong token on a live player is 401 or 403, never 404", func(t *testing.T) {
		hs := newHarness(t)
		pid, _ := hs.named("AARON")
		_, other := hs.named("SOMEONE")
		tokens := map[string]string{
			"no": "", "malformed": "not-the-token", "unknown": strings.Repeat("B", 43), "another player's": other,
		}
		for name, tok := range tokens {
			rec, _ := hs.do("DELETE", "/v1/players/"+pid, "", tok, "")
			if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
				t.Errorf("%s token gave %d, want 401 or 403. Answering 404 would tell the client the "+
					"data is gone when it is not, and it would stop retrying.", name, rec.Code)
			}
		}
		if _, err := hs.pl.Store().Get(context.Background(), pid); err != nil {
			t.Errorf("the player must survive every refused delete: %v", err)
		}
	})
}

// --- 4. the accepted body is exactly the twelve keys ---

func TestASubmissionWithAnUndeclaredFieldIsRejected(t *testing.T) {
	hs := newHarness(t)
	_, tok := hs.named("AARON")
	body := on("Pad", trial("x", "pad", []float64{191.4, 240.2, 267.4}, 0))
	body["latitude"] = 51.5
	rec, out := hs.submit(tok, body)
	if rec.Code != http.StatusUnprocessableEntity || out["error"] != "invalid" || out["reason"] != "unknown_field" {
		t.Errorf("a body with an undeclared field gave %d %s, want 422 invalid/unknown_field. Rejecting is "+
			"what keeps the client's disclosure list and the server's field set from drifting quietly, "+
			"and `latitude` is exactly the kind of field the agreement says is never sent.", rec.Code, rec.Body.String())
	}
	var n int
	if err := hs.db.DB().QueryRow(`SELECT count(*) FROM submissions`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows stored from a rejected body", n)
	}
	// The declared body, and only it, is accepted.
	hs.now = hs.now.Add(time.Minute)
	if rec, _ := hs.submit(tok, on("Pad", trial("y", "pad", []float64{191.4, 240.2, 267.4}, 0))); rec.Code != http.StatusCreated {
		t.Errorf("the declared body gave %d %s, want 201", rec.Code, rec.Body.String())
	}
}

// The app's LeaderboardContractTests assert its encoded keys against the README's set,
// and DecodeJSONStrict refuses anything else, so a key added on either side alone fails
// a test. This pins the server's side of that agreement to the struct that decodes it.
func TestSubmitRequestDeclaresExactlyTheTwelveKeys(t *testing.T) {
	want := []string{
		"client_id", "source", "best_ms", "avg_ms", "accuracy", "score", "attempts_ms", "misfires",
		"app_build", "platform", "device_model", "device_label",
	}
	rt := reflect.TypeOf(SubmitRequest{})
	var got []string
	for i := 0; i < rt.NumField(); i++ {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		got = append(got, key)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("SubmitRequest declares %v, want exactly %v; a change here is a change to what the app "+
			"promises its users it sends", got, want)
	}
}
