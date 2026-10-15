package drills

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMentalStackHTTPRunIdentityTopRecentAndErase(t *testing.T) {
	m, pl, h := harness(t)
	ctx := context.Background()
	p, token, err := pl.Create(ctx, "Mixed", "ios")
	if err != nil {
		t.Fatal(err)
	}
	q, otherToken, err := pl.Create(ctx, "Other", "android")
	if err != nil {
		t.Fatal(err)
	}
	post := func(payload Request, want int, bearer string) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		w := call(h, "POST", "/v1/drills/runs", string(body), bearer)
		if w.Code != want {
			t.Fatalf("POST %s: %d %s", payload.ClientID, w.Code, w.Body.String())
		}
	}
	first := req("mixed-best", "mental-stack", 12)
	post(first, 401, "")
	post(first, 201, token)
	post(first, 200, token)
	post(req("mixed-low", "mental-stack", 2), 201, token)
	post(req("mixed-zero", "mental-stack", 0), 201, token)
	post(req("mixed-other", "mental-stack", 8), 201, otherToken)
	conflict := first
	*conflict.Score = 13
	post(conflict, 422, token)
	post(req("invalid", "mental-stack-custom", 50), 422, token)
	board := func(sort string) Board {
		t.Helper()
		w := call(h, "GET", "/v1/drills/board?drill_id=mental-stack&sort="+sort, "", token)
		var b Board
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || w.Code != 200 {
			t.Fatalf("GET %s: %d %s %v", sort, w.Code, w.Body.String(), err)
		}
		return b
	}
	top := board("top")
	if top.BoardSize != 2 || len(top.Entries) != 2 || top.Own == nil || top.Own.Score != 12 || top.Own.Rank != 1 {
		t.Fatalf("top %+v", top)
	}
	recent := board("recent")
	if recent.Sort != "recent" || len(recent.Entries) != 4 || recent.Entries[0].Score != 8 || recent.Entries[1].Score != 0 {
		t.Fatalf("recent %+v", recent)
	}
	seen := map[string]bool{}
	for _, row := range recent.Entries {
		if row.Rank != 0 || row.RunRef == "" || seen[row.RunRef] {
			t.Fatalf("invalid run identity/rank %+v", row)
		}
		seen[row.RunRef] = true
	}
	if err := pl.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	post(req("erased-token", "mental-stack", 99), 401, token)
	for _, table := range []string{"drill_bests", "drill_runs", "drill_receipts"} {
		var count int
		if err := m.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE player_id=?`, p.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("erased %s: %d %v", table, count, err)
		}
	}
	remaining, err := m.readRecent(ctx, "mental-stack", q.ID, 50)
	if err != nil || len(remaining.Entries) != 1 || remaining.Own == nil || remaining.Own.Score != 8 || remaining.Own.Rank != 1 {
		t.Fatalf("other player %+v %v", remaining, err)
	}
}
