package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/store"
	"github.com/LoweKeyFlexin/share-ct/migrations"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newApp is the real stack on a temp database: what main.go serves, minus the listener.
func newApp(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	return New(quiet(), db, time.Now), db
}

func do(t *testing.T, h http.Handler, method, path string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec, rec.Body.String()
}

func TestHealthzOK(t *testing.T) {
	h, _ := newApp(t)
	rec, body := do(t, h, "GET", "/healthz")
	if rec.Code != http.StatusOK || body != "ok" {
		t.Fatalf("got %d %q, want 200 ok", rec.Code, body)
	}
}

func TestHealthzDatabaseDown(t *testing.T) {
	h, db := newApp(t)
	db.Close()
	rec, body := do(t, h, "GET", "/healthz")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(body, "closed") {
		t.Fatalf("got %d %q, want 503 without the raw error", rec.Code, body)
	}
}

func TestIndexPage(t *testing.T) {
	h, _ := newApp(t)
	rec, body := do(t, h, "GET", "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type %q, want text/html", ct)
	}
	for _, want := range []string{"Share CT · Reaction Leaderboard", "/v1/board?source=all", "data-view=\"recent\""} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, banned := range []string{"http://", "https://", " src=", "href=", "innerHTML"} {
		if strings.Contains(body, banned) {
			t.Errorf("page must be self-contained and build rows from text: found %q", banned)
		}
	}
	// The one inline script is exactly what the CSP hash admits.
	_, after, ok := strings.Cut(body, "<script>")
	script, _, ok2 := strings.Cut(after, "</script>")
	if !ok || !ok2 || strings.Count(body, "<script") != 1 {
		t.Fatal("page must carry exactly one inline script")
	}
	sum := sha256.Sum256([]byte(script))
	want := "script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, want) || !strings.Contains(csp, "connect-src 'self'") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP %q must admit only the inline script's hash and same-origin fetch", csp)
	}
}

func TestEverythingElseIs404JSON(t *testing.T) {
	h, _ := newApp(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/nope"}, {"GET", "/v1"}, {"GET", "/v1/board/"}, {"POST", "/healthz"}, {"POST", "/"}, {"GET", "/v1/players"},
	} {
		rec, body := do(t, h, c.method, c.path)
		if rec.Code != http.StatusNotFound || body != `{"error":"not_found"}`+"\n" {
			t.Errorf("%s %s: got %d %q, want 404 JSON", c.method, c.path, rec.Code, body)
		}
	}
}

func TestPanicBecomes500(t *testing.T) {
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
	rec, body := do(t, recoverer(quiet(), boom), "GET", "/v1/board")
	if rec.Code != http.StatusInternalServerError || body != `{"error":"internal"}`+"\n" {
		t.Fatalf("got %d %q, want 500 JSON", rec.Code, body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h, _ := newApp(t)
	rec, _ := do(t, h, "GET", "/")
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("index page missing Content-Security-Policy")
	}
}

// TestRoundTrip is the CI smoke's shape through the real stack: register, submit a
// known trial, read it back on the board and the profile, erase, gone.
func TestRoundTrip(t *testing.T) {
	h, _ := newApp(t)
	call := func(method, path, body, token string) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		r := httptest.NewRequest(method, path, rd)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		if rec.Body.Len() > 0 {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("%s %s: %d %q", method, path, rec.Code, rec.Body.String())
			}
		}
		return rec.Code, out
	}

	code, reg := call("POST", "/v1/players", `{"display_name":"Aaron","platform":"ios"}`, "")
	if code != 201 {
		t.Fatalf("register: %d %v", code, reg)
	}
	id, token := reg["player_id"].(string), reg["token"].(string)

	const body = `{"client_id":"smoke-1","source":"touch","best_ms":200,"avg_ms":205,"accuracy":1,"score":2100,
	  "attempts_ms":[200,205,210],"misfires":0,"app_build":"2214","platform":"ios","device_model":"iPhone17,2","device_label":"Touch"}`
	if code, out := call("POST", "/v1/scores", body, token); code != 201 || out["rank"] != float64(1) {
		t.Fatalf("submit: %d %v", code, out)
	}
	if code, out := call("POST", "/v1/scores", strings.Replace(body, `"score":2100`, `"score":2101`, 1), token); code != 422 || out["reason"] != "score_mismatch" {
		t.Fatalf("mismatched score: %d %v", code, out)
	}
	code, board := call("GET", "/v1/board?source=touch", "", "")
	entries := board["entries"].([]any)
	if code != 200 || len(entries) != 1 {
		t.Fatalf("board: %d %v", code, board)
	}
	// Rank 1 on a one-row board: LEGEND by STANDING, not by 200 ms (which is 12.0f =
	// MASTER on the time ladder). See TierNameForRow.
	if e := entries[0].(map[string]any); e["score"] != float64(2100) || e["tier"] != "LEGEND" || e["display_name"] != "Aaron" {
		t.Errorf("entry %v", e)
	}
	if code, prof := call("GET", "/v1/players/"+id, "", ""); code != 200 || prof["submissions"] != float64(1) {
		t.Errorf("profile: %d %v", code, prof)
	}
	if code, _ := call("DELETE", "/v1/players/"+id, "", token); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if _, board := call("GET", "/v1/board?source=touch", "", ""); len(board["entries"].([]any)) != 0 {
		t.Errorf("board after delete: %v", board)
	}
}

// TestIndexPageNamesTheProductAndNotTheHost pins the page's visible copy.
//
// Nothing pinned it before, which is how it went stale twice at once: it named the
// app "Controller Tester FGC" ten days after Aaron renamed the product to Fighter CT
// (branding/fighter-ct-icon-pack/ADOPTION.md, 2026-08-30), and it described the host
// as "a friend on a Raspberry Pi" a day after he ruled that wording out. Neither was
// a code defect and neither could fail a build, so both survived until he read the
// page himself.
//
// The banned list is the point. A rename that only fixes the occurrence someone
// noticed is the failure this test exists to stop.
func TestIndexPageNamesTheProductAndNotTheHost(t *testing.T) {
	_, body := do(t, mustApp(t), "GET", "/")

	for _, want := range []string{
		"Share CT",   // the service's name, in the title and the masthead
		"Fighter CT", // the app it serves, named in the description line
		"Ver .0",     // the page's own revision, top right
		"opt-in",     // the promise that predates the leaderboard
		"13 frames",  // what the score actually rewards, not an adjective
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page must say %q", want)
		}
	}

	// Retired copy only, matched case-insensitively against the exact phrases that were
	// actually wrong. Deliberately NOT a bare "Controller Tester" or "Will": the iOS App
	// Store listing is still titled Controller Tester FGC (bundle lowe.controllertester),
	// so "opt in from Controller Tester FGC on the App Store" is a sentence this page may
	// legitimately want, and "will" is an ordinary English word. A banned list that can fire
	// on correct copy gets deleted by the next person rather than argued with, which would
	// cost more than the drift it was guarding.
	// Only phrases that can never be correct copy. "controller tester fgc" is NOT here:
	// the iOS App Store listing is still titled that (bundle lowe.controllertester), so
	// "opt in from Controller Tester FGC on the App Store" is legitimate and a ban would
	// fire on it — verified by writing exactly that sentence and watching the ban trip.
	// The rename is covered by the positive assertion that "Fighter CT" is present, which
	// catches a page that names only the old title without catching one that names both.
	lower := strings.ToLower(body)
	for _, banned := range []string{
		"raspberry",
		"hosted by a friend",
	} {
		if strings.Contains(lower, banned) {
			t.Errorf("page must not contain %q", banned)
		}
	}
}

// TestIndexPageShowsWhatTheBoardAlreadyReturns pins the fields a row renders.
//
// Every one of these was already in the /v1/board payload and the page discarded it,
// so a global row was a name and a time while the app's own row showed the device,
// the moment and the accuracy. Aaron's words: "it doesn't feel very real the way it
// is." Asserting the script reads each field stops a future edit quietly dropping one
// again — the payload carrying a field is not the same as the page showing it.
func TestIndexPageShowsWhatTheBoardAlreadyReturns(t *testing.T) {
	_, body := do(t, mustApp(t), "GET", "/")
	for _, field := range []string{"device_label", "created_at", "avg_ms", "accuracy"} {
		if !strings.Contains(body, "e."+field) {
			t.Errorf("a board row must render %s; it is in the payload already", field)
		}
	}
	// Player-supplied text reaches the page as text, never as markup.
	if strings.Contains(body, "innerHTML") {
		t.Error("rows must be built with textContent")
	}
}

func mustApp(t *testing.T) http.Handler {
	t.Helper()
	h, _ := newApp(t)
	return h
}

// TestIndexPageOffersBothRankings pins the sort control.
//
// Aaron asked three times why the page showed only fastest times. The server has
// selected a different submission per player per metric since M1 — bestPerPlayer()
// passes sort into the ROW_NUMBER partition, so a score board shows each player's
// best-SCORING run, not their fastest run's score. Verified against the running
// binary with one player and two submissions: sort=fastest returned best_ms 188.62
// score 1095, sort=score returned best_ms 193 score 2128.
//
// The orchestrator asserted twice that this needed server work first and that a
// client-only chip would mislabel a row. That was wrong, from reading orderBy()
// without reading bestPerPlayer(). This test exists so the capability is visible in
// the page rather than rediscovered from the query.
func TestIndexPageOffersBothRankings(t *testing.T) {
	_, body := do(t, mustApp(t), "GET", "/")
	for _, want := range []string{
		`data-view="fastest"`,
		`data-view="score"`,
		"HIGH SCORE",
		"&sort=' + state.view", // the chosen view must reach the request
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// The per-input boards still exist on the API and are simply not offered at the top
	// level yet — Aaron: "Let's remove the Filter by Touch, Pad and Keyboard for now."
	// The page asks for the mixed board on both rankings.
	if !strings.Contains(body, "'/v1/board?source=all&window=all&sort='") {
		t.Error("both rankings must read the mixed board, all-time")
	}
	for _, gone := range []string{`data-source="touch"`, `data-source="pad"`, `data-source="keyboard"`} {
		if strings.Contains(body, gone) {
			t.Errorf("the per-input filter is withdrawn for now; found %q", gone)
		}
	}
}

// TestBoardIsCardsAndControlsAreAboveIt pins the shape, and one reversal.
//
// The board is a list of <details> cards - the app's own score-log shape, which Aaron
// called readable - so a reader can open a row for the run behind it.
//
// The controls sit ABOVE it, compact and sticky. An earlier revision collapsed them BELOW
// the board on his "make the selection and filtering tools not the most prominent thing on
// the page", and a test here pinned that. He overruled it on seeing it: "I like your idea
// of hiding the filter below the scores, but that would make it completely hidden if we got
// 10 scores." Both instructions are satisfied by small-and-adjacent, not by far-away: a
// filter has to be visible at the moment the list it governs is, and its effect has to be
// seen without hunting for the control.
//
// Recorded because the previous rule was also written down, and without the reason the next
// reader has two contradictory tests in the history and no way to tell which won.
func TestBoardIsCardsAndControlsAreAboveIt(t *testing.T) {
	_, body := do(t, mustApp(t), "GET", "/")

	for _, want := range []string{
		`<ol id="board" class="board"`, // a list, not a table
		`<div class="controls">`,       // one compact toolbar
		"el('details', cls)",           // each entry is a card, built at runtime
		"var cls = 'row';",             // whose classes carry the rank colours
		`data-view="recent"`,           // the mixed board exists
		".controls { position:sticky",  // and stays reachable down a long list
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "<table") || strings.Contains(body, "<thead") {
		t.Error("the board is a list of cards; a table came back")
	}
	if strings.Contains(body, `<details class="filters"`) {
		t.Error("controls must not be collapsed below the board; a long list hides them")
	}
	if strings.Index(body, `<div class="controls">`) > strings.Index(body, `<ol id="board"`) {
		t.Error("controls must sit above the board")
	}
	// RECENT is the default, and the reason has moved twice. Single-source boards are each
	// empty until someone plays on that input, which is what made the page look broken; the
	// mixed board fixed that. But a board of any kind is one row per player, so a session of
	// five trials still shows as one line — Aaron: "I submitted more than one Touch score
	// today and it's just showing my single attempt." A feed is the only view where activity
	// is visible.
	if !strings.Contains(body, "view: 'recent'") {
		t.Error("the feed must be the default view")
	}
	if !strings.Contains(body, "/v1/recent") {
		t.Error("the feed must fetch the feed endpoint")
	}
	// RECENT sits left of the two rankings.
	if strings.Index(body, `data-view="recent"`) > strings.Index(body, `data-view="fastest"`) {
		t.Error("RECENT must come first")
	}
	for _, m := range []string{"row.m1", "row.m2", "row.m3"} {
		if !strings.Contains(body, "."+m) {
			t.Errorf("missing medal style for %s", m)
		}
	}
	// The time-window control is withdrawn - Aaron: "let's remove the 30 days metric from
	// the leaderboard for now". The API still takes the parameter and the page still sends
	// window=all, so the control can come back without a server change; what must not come
	// back by accident is the control itself.
	for _, gone := range []string{`data-window=`, "30 DAYS", "ALL TIME", "tabs minor"} {
		if strings.Contains(body, gone) {
			t.Errorf("the time-window control is withdrawn for now; found %q", gone)
		}
	}
	// The rule that makes [hidden] work on a flex container stays: an author display rule
	// beats the UA stylesheet, so .tabs{display:flex} silently defeated [hidden] until
	// .tabs[hidden] was added, and the next hideable tab row would rediscover it.
	if !strings.Contains(body, ".tabs[hidden] { display:none; }") {
		t.Error("hidden must actually hide: .tabs{display:flex} overrides the UA [hidden] rule")
	}

	// Every attempt of the run is rendered, not just the best one.
	if !strings.Contains(body, "e.attempts_ms") {
		t.Error("a row must show the run behind it")
	}
}

// TestTheRankColoursTheNumberThatRanksTheRow pins Aaron's ask - "Scores should be the
// color of the Rank. Gold score for gold, the diamond color for Diamond" - and the
// mechanism that keeps it from silently reverting.
//
// The mechanism matters as much as the result. Tier colours were applied by a class that
// set `color`, which meant a bare .t-gold at specificity (0,1,0) lost to .detail dd at
// (0,1,1) and every tier rendered grey while carrying the right class - a failure only
// visible by reading the COMPUTED colour. The fix is that the tier class now declares a
// custom PROPERTY and the colour is declared once, where it is read. Two rules that
// declare different properties cannot out-specify each other, so pinning "the tier class
// sets --tier, never color" is pinning the thing that made the bug impossible.
func TestTheRankColoursTheNumberThatRanksTheRow(t *testing.T) {
	_, body := do(t, mustApp(t), "GET", "/")

	// One declaration, resolved in priority order: podium metal, then tier, then plain ink.
	if !strings.Contains(body, "color:var(--headline, var(--tier, var(--ink)))") {
		t.Error("the headline number must read --headline then --tier then --ink")
	}
	// Every rung of the server's ladder has a colour, and each rule declares the variable.
	for _, tier := range []string{"legend", "ultimate", "highmaster", "master", "diamond",
		"platinum", "gold", "silver", "bronze", "rookie"} {
		rule := ".row.t-" + tier
		i := strings.Index(body, rule)
		if i < 0 {
			t.Errorf("no colour rule for tier %q", tier)
			continue
		}
		decl := body[i:]
		if end := strings.Index(decl, "}"); end >= 0 {
			decl = decl[:end]
		}
		if !strings.Contains(decl, "--tier:") {
			t.Errorf("%s must declare --tier, not a colour: %q", rule, decl)
		}
		if strings.Contains(decl, "color:") && !strings.Contains(decl, "--tier:") {
			t.Errorf("%s declares colour directly; that is the specificity trap: %q", rule, decl)
		}
	}
	// The tier NAME reads the same variable, so the colour on the number always has a
	// legend somewhere in the row.
	if !strings.Contains(body, ".detail dd.tier { font-weight:700; letter-spacing:.06em; color:var(--tier, var(--dim)); }") {
		t.Error("the tier name must read --tier too, or the coloured number has no legend")
	}
	// The tier is shown on every board now, score included - a coloured number whose
	// colour is never named is decoration.
	if strings.Contains(body, "if (!byScore) def(dl, 'tier'") {
		t.Error("the tier name must appear on every board, not only where it ranks")
	}
}

// TestFastestIsATopTenAndBothPodiumsAreStruckMetal pins the two podium asks.
//
// Aaron: "The Fastest should display the top 10 if it doesn't already and top 3 should get
// special legend colors for their score. think of something cool (no emojis) to do for the
// High score top 3."
func TestFastestIsATopTenAndBothPodiumsAreStruckMetal(t *testing.T) {
	_, body := do(t, mustApp(t), "GET", "/")

	if !strings.Contains(body, "var LIMITS = { recent: 50, fastest: 10, score: 50 };") {
		t.Error("FASTEST must be a top ten")
	}
	if !strings.Contains(body, "&limit=' + LIMITS[state.view]") {
		t.Error("the limit must come from the view, or FASTEST silently returns fifty again")
	}

	// The podium metals override the tier colour on the headline, and are struck as a
	// gradient clipped to the glyphs: flat fill made silver indistinguishable from ink.
	for _, want := range []string{
		"--headline:var(--gold)", "--headline:var(--silver)", "--headline:var(--bronze)",
		"-webkit-background-clip:text; background-clip:text;",
		"background-size:150% 100%; background-position:50% 0;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("podium lacks %q", want)
		}
	}
	// Only HIGH SCORE's podium moves - that is what tells the two podiums apart.
	if !strings.Contains(body, ".row.foil .big b { animation:sheen 5s linear infinite; }") {
		t.Error("the HIGH SCORE podium must carry the sheen")
	}
	if !strings.Contains(body, "if (podium && byScore) { cls += ' foil'; }") {
		t.Error("only the score board's podium gets the sheen")
	}
	// A transparent fill with no clip support would erase the number outright.
	if !strings.Contains(body, "@supports not ((-webkit-background-clip: text) or (background-clip: text))") {
		t.Error("the foil needs a fallback, or an unsupporting browser renders no score at all")
	}
	// Motion is the flourish; the metal is the point.
	if !strings.Contains(body, "@media (prefers-reduced-motion: reduce)") {
		t.Error("the sheen must stop for reduced motion")
	}
	// No emoji anywhere: Aaron asked for this explicitly, and the podium is exactly where a
	// medal glyph would have been reached for. U+2100 is above every punctuation mark the
	// page legitimately uses (the chevron is U+203A, the separator U+00B7) and below the
	// symbol and emoji blocks, so no exception list is needed - and an exception list that
	// never excludes anything is a guard that looks stricter than it is. Positive control:
	// appending U+1F947 to this body fires the branch.
	for _, r := range body {
		if r >= 0x2100 {
			t.Errorf("symbol %q (U+%04X) in the page; the podium must be type, not glyphs", r, r)
			break
		}
	}
}
