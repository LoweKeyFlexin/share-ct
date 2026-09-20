package moderation_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/identity"
	"github.com/LoweKeyFlexin/share-ct/internal/leaderboard"
	"github.com/LoweKeyFlexin/share-ct/internal/moderation"
	"github.com/LoweKeyFlexin/share-ct/internal/players"
	"github.com/LoweKeyFlexin/share-ct/internal/server"
	"github.com/LoweKeyFlexin/share-ct/internal/store"
	"github.com/LoweKeyFlexin/share-ct/migrations"
)

type fixture struct {
	t     *testing.T
	db    *store.Store
	clock time.Time
	log   bytes.Buffer
	h     http.Handler
}

func makeFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "reports.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background(), migrations.FS); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: db, clock: time.Unix(1_800_000_000, 0)}
	f.h = server.New(slog.New(slog.NewTextHandler(&f.log, nil)), db, func() time.Time { return f.clock })
	return f
}

func (f *fixture) player() (players.Player, string) {
	f.t.Helper()
	p, token, err := players.NewStore(f.db.DB(), func() time.Time { return f.clock }).Create(context.Background(), "Player", "ios")
	if err != nil {
		f.t.Fatal(err)
	}
	return p, token
}

func (f *fixture) post(body, token string) (*httptest.ResponseRecorder, map[string]string) {
	f.t.Helper()
	r := httptest.NewRequest("POST", "/v1/reports", strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	var result map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		f.t.Fatal(err)
	}
	return rec, result
}

func reportBody(ref, reason string) string {
	b, _ := json.Marshal(map[string]string{"target_player_ref": ref, "reason": reason})
	return string(b)
}

func count(t *testing.T, db *store.Store, table string) int {
	t.Helper()
	var n int
	if err := db.DB().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReportHTTPValidationCommitAndSafeLogs(t *testing.T) {
	f := makeFixture(t)
	reporter, token := f.player()
	target, _ := f.player()
	for _, tc := range []struct {
		body, token string
		code        int
		reason      string
	}{
		{reportBody(target.Ref, "spam"), "", 401, ""},
		{reportBody(target.Ref, "spam"), "invalid", 401, ""},
		{reportBody("bad-ref", "spam"), token, 422, "target_player_ref"},
		{reportBody(strings.Repeat("0", 32), "spam"), token, 404, ""},
		{reportBody(reporter.Ref, "spam"), token, 422, "self_report"},
		{reportBody(target.Ref, "other"), token, 422, "reason"},
		{`{"target_player_ref":"` + target.Ref + `","reason":"spam","extra":1}`, token, 422, "unknown_field"},
	} {
		rec, out := f.post(tc.body, tc.token)
		if rec.Code != tc.code || (tc.reason != "" && out["reason"] != tc.reason) {
			t.Errorf("%s: got %d %v, want %d %s", tc.body, rec.Code, out, tc.code, tc.reason)
		}
	}
	if count(t, f.db, "reports") != 0 {
		t.Fatal("invalid requests persisted")
	}
	good := reportBody(target.Ref, "spam")
	rec, out := f.post(good, token)
	if rec.Code != 201 || !identity.ValidRef(out["report_id"]) {
		t.Fatalf("accept = %d %v", rec.Code, out)
	}
	rec2, out2 := f.post(good, token)
	if rec2.Code != 202 || out2["report_id"] != out["report_id"] {
		t.Fatalf("dedup = %d %v", rec2.Code, out2)
	}
	if count(t, f.db, "reports") != 1 || count(t, f.db, "report_alert_outbox") != 1 {
		t.Fatal("report/outbox not atomic or deduped")
	}
	for _, secret := range []string{token, good, target.Ref, reporter.ID} {
		if strings.Contains(f.log.String(), secret) {
			t.Fatalf("log contains sensitive value %q", secret)
		}
	}
	// A failed outbox insertion must roll the report back and never answer accepted.
	if _, err := f.db.DB().Exec(`CREATE TRIGGER fail_alert BEFORE INSERT ON report_alert_outbox BEGIN SELECT RAISE(ABORT, 'outbox unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	other, _ := f.player()
	rec, _ = f.post(reportBody(other.Ref, "spam"), token)
	if rec.Code != 500 || count(t, f.db, "reports") != 1 {
		t.Fatalf("outbox failure accepted report: %d", rec.Code)
	}
}

func TestDurableQuotaAfterRestart(t *testing.T) {
	f := makeFixture(t)
	reporter, token := f.player()
	_ = reporter
	var refs []string
	for i := 0; i < moderation.ReportsPerDay+1; i++ {
		p, _ := f.player()
		refs = append(refs, p.Ref)
	}
	for i := 0; i < moderation.ReportsPerDay; i++ {
		if rec, _ := f.post(reportBody(refs[i], "spam"), token); rec.Code != 201 {
			t.Fatalf("report %d: %d", i, rec.Code)
		}
	}
	// Reconstruct the handler, as after a server restart; quota is in SQLite.
	f.h = server.New(slog.New(slog.NewTextHandler(io.Discard, nil)), f.db, func() time.Time { return f.clock })
	rec, out := f.post(reportBody(refs[len(refs)-1], "spam"), token)
	if rec.Code != 429 || out["error"] != "rate_limited" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("quota = %d %v", rec.Code, out)
	}
	// Exact duplicate remains idempotent without consuming another quota slot.
	if rec, _ := f.post(reportBody(refs[0], "spam"), token); rec.Code != 202 {
		t.Fatalf("dedup after restart = %d", rec.Code)
	}
	f.clock = f.clock.Add(24*time.Hour + time.Second)
	if rec, _ := f.post(reportBody(refs[len(refs)-1], "spam"), token); rec.Code != 201 {
		t.Fatalf("quota did not expire: %d", rec.Code)
	}
}

func TestDedupExpiresAndClosedCaseCanBeReportedAgain(t *testing.T) {
	f := makeFixture(t)
	_, token := f.player()
	target, _ := f.player()
	body := reportBody(target.Ref, "harassment")
	first, old := f.post(body, token)
	if first.Code != 201 {
		t.Fatalf("first report: %d", first.Code)
	}
	if _, err := f.db.DB().Exec(`UPDATE reports SET state = 'dismissed' WHERE id = ?`, old["report_id"]); err != nil {
		t.Fatal(err)
	}
	second, fresh := f.post(body, token)
	if second.Code != 201 || fresh["report_id"] == old["report_id"] {
		t.Fatalf("closed case reused: %d %v", second.Code, fresh)
	}
	if duplicate, same := f.post(body, token); duplicate.Code != 202 || same["report_id"] != fresh["report_id"] {
		t.Fatalf("pending case did not deduplicate: %d %v", duplicate.Code, same)
	}
	f.clock = f.clock.Add(24*time.Hour + time.Second)
	third, later := f.post(body, token)
	if third.Code != 201 || later["report_id"] == fresh["report_id"] {
		t.Fatalf("older incident reused: %d %v", third.Code, later)
	}
	if count(t, f.db, "reports") != 3 || count(t, f.db, "report_alert_outbox") != 3 {
		t.Fatal("new incident did not enqueue a new alert")
	}
}

type sender struct {
	calls []moderation.Alert
	fail  bool
}

type heldSender struct {
	entered  chan moderation.Alert
	release  chan struct{}
	deadline chan time.Time
}

func (s *heldSender) Send(ctx context.Context, a moderation.Alert) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return fmt.Errorf("provider context has no deadline")
	}
	s.deadline <- deadline
	s.entered <- a
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// The provider may complete an already-started ID-only email after the target is
// erased. The claim is committed before Send, so erasure must finish while Send is
// held, and finishing that send must not recreate the deleted report or outbox item.
func TestEraseRacesWithInFlightAlertWithoutResurrection(t *testing.T) {
	f := makeFixture(t)
	_, token := f.player()
	target, _ := f.player()
	rec, out := f.post(reportBody(target.Ref, "spam"), token)
	if rec.Code != 201 {
		t.Fatal(rec.Code)
	}
	s := moderation.NewStore(f.db.DB(), func() time.Time { return f.clock })
	mail := &heldSender{entered: make(chan moderation.Alert, 1), release: make(chan struct{}), deadline: make(chan time.Time, 1)}
	released := false
	defer func() {
		if !released {
			close(mail.release)
		}
	}()
	type result struct {
		attempted bool
		err       error
	}
	done := make(chan result, 1)
	go func() { attempted, err := s.DispatchDue(context.Background(), mail); done <- result{attempted, err} }()
	select {
	case alert := <-mail.entered:
		if alert.ReportID != out["report_id"] {
			t.Fatalf("wrong alert: %+v", alert)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("send never started")
	}
	deadline := <-mail.deadline
	if left := time.Until(deadline); left <= 0 || left > 10*time.Second {
		t.Fatalf("provider deadline is not bounded to ten seconds: %v", left)
	}
	eraseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := players.NewStore(f.db.DB(), nil).Delete(eraseCtx, target.ID); err != nil {
		t.Fatalf("erase blocked by provider call: %v", err)
	}
	if count(t, f.db, "reports") != 0 || count(t, f.db, "report_alert_outbox") != 0 {
		t.Fatal("erase left report or alert while provider is in flight")
	}
	close(mail.release)
	released = true
	select {
	case result := <-done:
		if !result.attempted || result.err != nil {
			t.Fatalf("dispatch finish: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch did not finish")
	}
	if count(t, f.db, "reports") != 0 || count(t, f.db, "report_alert_outbox") != 0 {
		t.Fatal("send completion resurrected deleted data")
	}
	f.clock = f.clock.Add(time.Minute)
	if attempted, err := s.DispatchDue(context.Background(), &sender{}); attempted || err != nil {
		t.Fatalf("deleted report is retryable: %v %v", attempted, err)
	}
}

func TestDispatchHonorsProviderDeadlineAndReleasesLease(t *testing.T) {
	f := makeFixture(t)
	_, token := f.player()
	target, _ := f.player()
	if rec, _ := f.post(reportBody(target.Ref, "spam"), token); rec.Code != 201 {
		t.Fatal(rec.Code)
	}
	s := moderation.NewStore(f.db.DB(), func() time.Time { return f.clock })
	mail := &heldSender{entered: make(chan moderation.Alert, 1), release: make(chan struct{}), deadline: make(chan time.Time, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	attempted, err := s.DispatchDue(ctx, mail)
	if !attempted || err != moderation.ErrDelivery {
		t.Fatalf("timed send = %v %v", attempted, err)
	}
	if len(mail.entered) != 1 || len(mail.deadline) != 1 {
		t.Fatal("sender did not receive bounded context")
	}
	var attempts, leaseUntil int
	var leaseToken sql.NullString
	if err := f.db.DB().QueryRow(`SELECT attempts, lease_token, lease_until FROM report_alert_outbox`).Scan(&attempts, &leaseToken, &leaseUntil); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || leaseToken.Valid || leaseUntil != 0 {
		t.Fatalf("canceled send stranded lease: attempts=%d token=%v until=%d", attempts, leaseToken, leaseUntil)
	}
}

func (s *sender) Send(_ context.Context, a moderation.Alert) error {
	s.calls = append(s.calls, a)
	if s.fail {
		return fmt.Errorf("provider failed")
	}
	return nil
}

func TestOutboxRetryAndErasureFromEitherSide(t *testing.T) {
	for _, erase := range []string{"reporter", "target"} {
		t.Run(erase, func(t *testing.T) {
			f := makeFixture(t)
			reporter, token := f.player()
			target, _ := f.player()
			body := reportBody(target.Ref, "harassment")
			if rec, _ := f.post(body, token); rec.Code != 201 {
				t.Fatal(rec.Code)
			}
			s := moderation.NewStore(f.db.DB(), func() time.Time { return f.clock })
			mail := &sender{fail: true}
			attempted, err := s.DispatchDue(context.Background(), mail)
			if !attempted || err == nil || len(mail.calls) != 1 || !identity.ValidRef(mail.calls[0].ReportID) {
				t.Fatalf("failed send = %v %v %+v", attempted, err, mail.calls)
			}
			if attempted, err := s.DispatchDue(context.Background(), mail); attempted || err != nil {
				t.Fatalf("retry too early = %v %v", attempted, err)
			}
			var attempts int
			if err := f.db.DB().QueryRow(`SELECT attempts FROM report_alert_outbox`).Scan(&attempts); err != nil || attempts != 1 {
				t.Fatalf("retry not durable: %d %v", attempts, err)
			}
			victim := reporter.ID
			if erase == "target" {
				victim = target.ID
			}
			if err := players.NewStore(f.db.DB(), nil).Delete(context.Background(), victim); err != nil {
				t.Fatal(err)
			}
			if count(t, f.db, "reports") != 0 || count(t, f.db, "report_alert_outbox") != 0 {
				t.Fatal("erase left report or alert")
			}
			f.clock = f.clock.Add(2 * time.Minute)
			mail.fail = false
			if attempted, err := s.DispatchDue(context.Background(), mail); attempted || err != nil || len(mail.calls) != 1 {
				t.Fatalf("erased report retried: %v %v %+v", attempted, err, mail.calls)
			}
		})
	}
}

func TestOutboxRetrySurvivesDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "persist.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	pl := players.NewStore(db.DB(), func() time.Time { return now })
	reporter, _, err := pl.Create(ctx, "Reporter", "ios")
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := pl.Create(ctx, "Target", "ios")
	if err != nil {
		t.Fatal(err)
	}
	s := moderation.NewStore(db.DB(), func() time.Time { return now })
	id, made, err := s.Accept(ctx, reporter.ID, target.Ref, "spam")
	if err != nil || !made {
		t.Fatalf("accept: %v %v", made, err)
	}
	mail := &sender{fail: true}
	if _, err := s.DispatchDue(ctx, mail); err == nil {
		t.Fatal("wanted failed send")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if res, err := db.Migrate(ctx, migrations.FS); err != nil || res.Applied != 0 {
		t.Fatalf("reopen migration %+v %v", res, err)
	}
	now = now.Add(2 * time.Minute)
	s = moderation.NewStore(db.DB(), func() time.Time { return now })
	mail.fail = false
	if attempted, err := s.DispatchDue(ctx, mail); !attempted || err != nil {
		t.Fatalf("retry: %v %v", attempted, err)
	}
	if len(mail.calls) != 2 || mail.calls[1].ReportID != id {
		t.Fatalf("retry alert mismatch: %+v", mail.calls)
	}
	var sentAt int64
	if err := db.DB().QueryRow(`SELECT sent_at FROM report_alert_outbox WHERE report_id = ?`, id).Scan(&sentAt); err != nil || sentAt != now.Unix() {
		t.Fatalf("sent_at: %d %v", sentAt, err)
	}
}

func TestOldDatabaseBackfillAndBoardRefStable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{}
	for n := 1; n <= 5; n++ {
		name := fmt.Sprintf("%03d_", n)
		entries, _ := fs.ReadDir(migrations.FS, ".")
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), name) {
				b, _ := fs.ReadFile(migrations.FS, e.Name())
				old[e.Name()] = &fstest.MapFile{Data: b}
			}
		}
	}
	if _, err := db.Migrate(ctx, old); err != nil {
		t.Fatal(err)
	}
	const id = "a0000000-0000-4000-8000-000000000001"
	if _, err := db.DB().Exec(`INSERT INTO players (id, token_hash, display_name, platform, created_at, last_seen_at) VALUES (?, ?, ?, 'ios', 1, 1)`, id, strings.Repeat("a", 64), "Old Name"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	var ref string
	if err := db.DB().QueryRow(`SELECT player_ref FROM players WHERE id = ?`, id).Scan(&ref); err != nil || !identity.ValidRef(ref) || ref == id {
		t.Fatalf("backfill ref %q %v", ref, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := db.DB().QueryRow(`SELECT player_ref FROM players WHERE id = ?`, id).Scan(&after); err != nil || after != ref {
		t.Fatalf("ref changed on restart: %q %v", after, err)
	}
	if err := players.NewStore(db.DB(), nil).Rename(ctx, id, "New Name"); err != nil {
		t.Fatal(err)
	}
	lb := leaderboard.NewStore(db.DB(), nil)
	_, err = lb.Insert(ctx, &leaderboard.Submission{PlayerID: id, ClientID: "old-run", Source: "pad", BestMs: 200, AvgMs: 205, Accuracy: 1, Score: 2100, AttemptsMs: []float64{200, 205, 210}, ClientBestMs: 200, ClientAvgMs: 205, ClientAccuracy: 1, ClientScore: 2100, AppBuild: "test", Platform: "ios"})
	if err != nil {
		t.Fatal(err)
	}
	for _, entries := range [][]leaderboard.Entry{mustBoard(t, lb), mustRecent(t, lb)} {
		if len(entries) != 1 || entries[0].PlayerRef != ref || entries[0].DisplayName != "New Name" {
			t.Fatalf("public ref/name = %+v", entries)
		}
	}
	h := server.New(slog.New(slog.NewTextHandler(io.Discard, nil)), db, nil)
	for _, path := range []string{"/v1/board?source=pad", "/v1/recent?source=pad"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		var envelope struct {
			Entries []struct {
				PlayerRef string `json:"player_ref"`
			} `json:"entries"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &envelope) != nil || len(envelope.Entries) != 1 || envelope.Entries[0].PlayerRef != ref {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

func mustBoard(t *testing.T, s *leaderboard.Store) []leaderboard.Entry {
	t.Helper()
	e, err := s.Board(context.Background(), "pad", "all", "fastest", 50)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func mustRecent(t *testing.T, s *leaderboard.Store) []leaderboard.Entry {
	t.Helper()
	e, err := s.Recent(context.Background(), "pad", 50)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
