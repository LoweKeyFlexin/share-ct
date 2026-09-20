// Package moderation accepts player reports and keeps email alerts in a durable outbox.
package moderation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/httpx"
	"github.com/LoweKeyFlexin/share-ct/internal/identity"
	"github.com/LoweKeyFlexin/share-ct/internal/players"
)

const (
	ReportsPerDay = 5
	reportWindow  = 24 * time.Hour
)

var (
	ErrTargetGone = errors.New("target gone")
	ErrSelfReport = errors.New("self report")
)

// RateLimited carries a durable quota's time to its oldest report's expiry.
type RateLimited struct{ RetryAfter time.Duration }

func (e RateLimited) Error() string { return "report rate limited" }

// Store owns reports and their email outbox on the same SQLite connection.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

// Accept commits a report and its alert together. An identical report from the same
// player is idempotent for life: it returns the original ID without another alert.
func (s *Store) Accept(ctx context.Context, reporterID, targetRef, reason string) (id string, created bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()
	var targetID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM players WHERE player_ref = ?`, targetRef).Scan(&targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrTargetGone
	}
	if err != nil {
		return "", false, err
	}
	if targetID == reporterID {
		return "", false, ErrSelfReport
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM reports WHERE reporter_id = ? AND target_id = ? AND reason = ?`, reporterID, targetID, reason).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	now := s.now().Unix()
	var count int
	var oldest sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT count(*), min(created_at) FROM reports WHERE reporter_id = ? AND created_at > ?`, reporterID, now-int64(reportWindow.Seconds())).Scan(&count, &oldest); err != nil {
		return "", false, err
	}
	if count >= ReportsPerDay {
		return "", false, RateLimited{RetryAfter: time.Duration(oldest.Int64+int64(reportWindow.Seconds())-now+1) * time.Second}
	}
	id, err = identity.NewRef()
	if err != nil {
		return "", false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO reports (id, reporter_id, target_id, reason, created_at) VALUES (?, ?, ?, ?, ?)`, id, reporterID, targetID, reason, now); err != nil {
		return "", false, fmt.Errorf("insert report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO report_alert_outbox (report_id, next_attempt_at) VALUES (?, ?)`, id, now); err != nil {
		return "", false, fmt.Errorf("enqueue alert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return id, true, nil
}

type request struct {
	TargetPlayerRef string `json:"target_player_ref"`
	Reason          string `json:"reason"`
}
type response struct {
	ReportID string `json:"report_id"`
}

type Module struct {
	store *Store
	log   *slog.Logger
}

func New(log *slog.Logger, db *sql.DB, now func() time.Time) *Module {
	return &Module{store: NewStore(db, now), log: log}
}
func (m *Module) Register(mux *http.ServeMux, requireBearer func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/reports", requireBearer(http.HandlerFunc(m.create)))
}
func (m *Module) create(w http.ResponseWriter, r *http.Request) {
	p, ok := players.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req request
	if !httpx.DecodeJSONStrict(w, r, &req) {
		return
	}
	if !identity.ValidRef(req.TargetPlayerRef) {
		httpx.WriteInvalid(w, "target_player_ref")
		return
	}
	switch req.Reason {
	case "spam", "harassment", "inappropriate":
	default:
		httpx.WriteInvalid(w, "reason")
		return
	}
	id, created, err := m.store.Accept(r.Context(), p.ID, req.TargetPlayerRef, req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, ErrTargetGone):
			httpx.WriteError(w, http.StatusNotFound, "not_found")
		case errors.Is(err, ErrSelfReport):
			httpx.WriteInvalid(w, "self_report")
		default:
			var limited RateLimited
			if errors.As(err, &limited) {
				httpx.WriteRateLimited(w, limited.RetryAfter)
				return
			}
			// Database errors can contain bound data in driver text. Keep reports and
			// credentials out of both application and request logs.
			m.log.Error("accept report failed")
			httpx.WriteError(w, http.StatusInternalServerError, "internal")
		}
		return
	}
	status := http.StatusAccepted
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, response{ReportID: id})
}
