// Package drills owns aggregate, full-speed Endless scores, separate from Reaction timings.
package drills

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
	"github.com/LoweKeyFlexin/share-ct/internal/players"
	"github.com/LoweKeyFlexin/share-ct/internal/ratelimit"
)

type Request struct {
	ClientID    string `json:"client_id"`
	DrillID     string `json:"drill_id"`
	Score       *int   `json:"score"`
	Misses      int    `json:"misses"`
	Source      string `json:"source"`
	AppBuild    string `json:"app_build"`
	Platform    string `json:"platform"`
	DeviceModel string `json:"device_model"`
	DeviceLabel string `json:"device_label"`
}
type Entry struct {
	Rank        int    `json:"rank"`
	PlayerRef   string `json:"player_ref"`
	PlayerShort string `json:"player_short"`
	DisplayName string `json:"display_name"`
	Score       int    `json:"score"`
	Source      string `json:"source"`
	Platform    string `json:"platform"`
	DeviceLabel string `json:"device_label"`
	CreatedAt   string `json:"created_at"`
	RunRef      string `json:"run_ref,omitempty"`
}
type Board struct {
	DrillID   string  `json:"drill_id"`
	Entries   []Entry `json:"entries"`
	Own       *Entry  `json:"own"`
	BoardSize int     `json:"board_size"`
	Sort      string  `json:"sort,omitempty"`
}
type Receipt struct {
	SubmissionID int64 `json:"submission_id"`
	Rank         int   `json:"rank"`
	BoardSize    int   `json:"board_size"`
}
type Module struct {
	db        *sql.DB
	now       func() time.Time
	perPlayer *ratelimit.Limiter
	perIP     *ratelimit.Limiter
}

func New(db *sql.DB, now func() time.Time) *Module {
	if now == nil {
		now = time.Now
	}
	return &Module{db: db, now: now, perPlayer: ratelimit.New(10, time.Minute, now), perIP: ratelimit.New(60, time.Minute, now)}
}
func (m *Module) Register(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/drills/scores", auth(http.HandlerFunc(m.submit)))
	mux.Handle("POST /v1/drills/runs", auth(http.HandlerFunc(m.submitRun)))
	// The public website reads anonymously; an app bearer also adds its own standing.
	mux.Handle("GET /v1/drills/board", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			auth(http.HandlerFunc(m.board)).ServeHTTP(w, r)
			return
		}
		m.board(w, r)
	}))
}
func validDrill(id string) bool {
	switch id {
	case "whiff-punish", "hit-confirm", "anti-air", "crosscut", "drive-impact":
		return true
	}
	return false
}
func validate(r Request) string {
	switch {
	case r.ClientID == "" || len(r.ClientID) > 64 || strings.TrimSpace(r.ClientID) != r.ClientID:
		return "client_id"
	case !validDrill(r.DrillID):
		return "drill_id"
	case r.Score == nil || *r.Score < 0 || *r.Score > 1000000:
		return "score"
	case r.Misses != 3:
		return "misses"
	case r.Source != "touch" && r.Source != "pad" && r.Source != "keyboard":
		return "source"
	case !players.ValidPlatform(r.Platform):
		return "platform"
	case strings.TrimSpace(r.AppBuild) == "" || len(r.AppBuild) > 32:
		return "app_build"
	case len(r.DeviceModel) > 64 || len(r.DeviceLabel) > 64:
		return "device"
	}
	return ""
}
func (m *Module) submit(w http.ResponseWriter, r *http.Request) {
	m.submitWithIntent(w, r, false)
}
func (m *Module) submitRun(w http.ResponseWriter, r *http.Request) {
	m.submitWithIntent(w, r, true)
}
func (m *Module) submitWithIntent(w http.ResponseWriter, r *http.Request, publishesRun bool) {
	p, ok := players.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "unauthorized")
		return
	}
	var req Request
	if !httpx.DecodeJSONStrict(w, r, &req) {
		return
	}
	if reason := validate(req); reason != "" {
		httpx.WriteInvalid(w, reason)
		return
	}
	if ok, retry := m.perPlayer.Allow(p.ID); !ok {
		httpx.WriteRateLimited(w, retry)
		return
	}
	if ok, retry := m.perIP.Allow(httpx.ClientIP(r)); !ok {
		httpx.WriteRateLimited(w, retry)
		return
	}
	id, repeated, err := m.saveWithIntent(r.Context(), p.ID, req, publishesRun)
	if errors.Is(err, errConflict) {
		httpx.WriteInvalid(w, "client_id_conflict")
		return
	}
	if err != nil {
		httpx.WriteError(w, 500, "internal")
		return
	}
	b, err := m.read(r.Context(), req.DrillID, p.ID, 1)
	if err != nil {
		httpx.WriteError(w, 500, "internal")
		return
	}
	rank := 0
	if b.Own != nil {
		rank = b.Own.Rank
	}
	status := 201
	if repeated {
		status = 200
	}
	httpx.WriteJSON(w, status, Receipt{id, rank, b.BoardSize})
}

var errConflict = errors.New("client id already used for different body")

func (m *Module) save(ctx context.Context, player string, req Request) (int64, bool, error) {
	return m.saveWithIntent(ctx, player, req, false)
}
func (m *Module) saveWithIntent(ctx context.Context, player string, req Request, publishesRun bool) (int64, bool, error) {
	data, _ := json.Marshal(req)
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var id int64
	var oldHash string
	var oldIntent bool
	err = tx.QueryRowContext(ctx, `SELECT id,body_hash,publishes_run FROM drill_receipts WHERE player_id=? AND client_id=?`, player, req.ClientID).Scan(&id, &oldHash, &oldIntent)
	if err == nil {
		if oldHash != hash || oldIntent != publishesRun {
			return 0, false, errConflict
		}
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	// Run rows retain deduplication proof even after the smaller retry-receipt
	// ring rolls over. Cross-route retries never promote best-only intent.
	err = tx.QueryRowContext(ctx, `SELECT receipt_id,body_hash FROM drill_runs WHERE player_id=? AND client_id=?`, player, req.ClientID).Scan(&id, &oldHash)
	if err == nil {
		if !publishesRun || oldHash != hash {
			return 0, false, errConflict
		}
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	// A retained all-time best is durable proof that this client ID already
	// existed, even if both bounded recent rings have since expired.
	if publishesRun {
		var retainedBest bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM drill_bests WHERE player_id=? AND client_id=?)`, player, req.ClientID).Scan(&retainedBest); err != nil {
			return 0, false, err
		}
		if retainedBest {
			return 0, false, errConflict
		}
	}
	now := m.now().Unix()
	if _, err = tx.ExecContext(ctx, `DELETE FROM drill_runs WHERE created_at<?`, m.now().Add(-RunRetention).Unix()); err != nil {
		return 0, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO drill_receipts(player_id,client_id,body_hash,created_at,publishes_run) VALUES(?,?,?,?,?)`, player, req.ClientID, hash, now, publishesRun)
	if err != nil {
		return 0, false, err
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO drill_bests(player_id,drill_id,client_id,score,source,app_build,platform,device_model,device_label,created_at)
 VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(player_id,drill_id) DO UPDATE SET
 client_id=excluded.client_id,score=excluded.score,source=excluded.source,app_build=excluded.app_build,
 platform=excluded.platform,device_model=excluded.device_model,device_label=excluded.device_label,created_at=excluded.created_at
 WHERE excluded.score>drill_bests.score`, player, req.DrillID, req.ClientID, *req.Score, req.Source, req.AppBuild, req.Platform, req.DeviceModel, req.DeviceLabel, now)
	if err != nil {
		return 0, false, err
	}
	if publishesRun {
		_, err = tx.ExecContext(ctx, `INSERT INTO drill_runs(player_id,client_id,receipt_id,body_hash,drill_id,score,source,app_build,platform,device_model,device_label,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, player, req.ClientID, id, hash, req.DrillID, *req.Score, req.Source, req.AppBuild, req.Platform, req.DeviceModel, req.DeviceLabel, now)
		if err != nil {
			return 0, false, err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM drill_runs WHERE player_id=? AND id NOT IN (SELECT id FROM drill_runs WHERE player_id=? ORDER BY id DESC LIMIT ?)`, player, player, RunCap)
		if err != nil {
			return 0, false, err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM drill_receipts WHERE player_id=? AND id NOT IN
 (SELECT id FROM drill_receipts WHERE player_id=? ORDER BY id DESC LIMIT 100)`, player, player)
	if err != nil {
		return 0, false, err
	}
	return id, false, tx.Commit()
}

const rankedSQL = `WITH ranked AS (SELECT b.*,ROW_NUMBER() OVER(ORDER BY b.score DESC,b.created_at ASC,b.id ASC) AS rank,
 count(*) OVER() AS board_size FROM drill_bests b WHERE b.drill_id=?)
 SELECT b.rank,p.id,p.player_ref,p.display_name,b.score,b.source,b.platform,b.device_label,b.created_at,b.board_size
 FROM ranked b JOIN players p ON p.id=b.player_id WHERE b.rank<=? OR b.player_id=? ORDER BY b.rank`

func (m *Module) read(ctx context.Context, drill, player string, limit int) (Board, error) {
	b := Board{DrillID: drill, Entries: []Entry{}}
	rows, err := m.db.QueryContext(ctx, rankedSQL, drill, limit, player)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		var pid string
		var stamp int64
		if err = rows.Scan(&e.Rank, &pid, &e.PlayerRef, &e.DisplayName, &e.Score, &e.Source, &e.Platform, &e.DeviceLabel, &stamp, &b.BoardSize); err != nil {
			return b, err
		}
		e.DisplayName = players.Display(e.DisplayName)
		e.PlayerShort = players.Short(pid)
		e.CreatedAt = time.Unix(stamp, 0).UTC().Format(time.RFC3339)
		if e.Rank <= limit {
			b.Entries = append(b.Entries, e)
		}
		if pid == player {
			own := e
			b.Own = &own
		}
	}
	return b, rows.Err()
}
func (m *Module) board(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	drill := q.Get("drill_id")
	if !validDrill(drill) {
		httpx.WriteBadRequest(w, "drill_id")
		return
	}
	sort := q.Get("sort")
	if sort != "" && sort != "top" && sort != "recent" {
		httpx.WriteBadRequest(w, "sort")
		return
	}
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			httpx.WriteBadRequest(w, "limit")
			return
		}
		limit = v
		if limit > 100 {
			limit = 100
		}
	}
	player := ""
	if p, ok := players.FromContext(r.Context()); ok {
		player = p.ID
	}
	var b Board
	var err error
	if sort == "recent" {
		if limit > 50 {
			limit = 50
		}
		b, err = m.readRecent(r.Context(), drill, player, limit)
	} else {
		b, err = m.read(r.Context(), drill, player, limit)
	}
	if err != nil {
		httpx.WriteError(w, 500, "internal")
		return
	}
	httpx.WriteJSON(w, 200, b)
}
