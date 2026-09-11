package leaderboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/players"
)

// Sources are the three boards. They never share a ranking: a phone screen, a pad's
// switch travel and a keyboard's scan matrix add different fixed latencies, so one board
// would rank hardware, not people.
var Sources = []string{"touch", "pad", "keyboard"}

// ValidSource reports whether s is one of touch, pad, keyboard.
func ValidSource(s string) bool {
	for _, v := range Sources {
		if v == s {
			return true
		}
	}
	return false
}

// The two board windows.
const (
	WindowAll = "all"
	Window30d = "30d"

	thirtyDays = 30 * 24 * time.Hour
)

// ValidWindow reports whether w is all or 30d.
func ValidWindow(w string) bool { return w == WindowAll || w == Window30d }

// Submission is one row of the submissions table. The ranked fields are the server's
// recompute; the Client* fields keep what the app claimed, for audit only.
type Submission struct {
	ID       int64
	PlayerID string
	ClientID string
	Source   string

	BestMs     float64
	AvgMs      float64
	Accuracy   float64
	Score      int
	AttemptsMs []float64
	Misfires   int

	ClientBestMs   float64
	ClientAvgMs    float64
	ClientAccuracy float64
	ClientScore    int

	AppBuild    string
	Platform    string
	DeviceModel string // "" is stored as NULL
	DeviceLabel string
	CreatedAt   int64
}

// Store reads and writes the submissions table.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore wraps the shared connection; now stamps created_at and bounds the 30d window.
func NewStore(db *sql.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

// Insert stores sub unless the player already submitted this client_id, in which case
// sub takes the existing row's id, source and created_at and created is false: a retried
// submission is idempotent. Either way the player's last_seen_at is bumped. Everything
// runs on the transaction, because the pool holds exactly one connection.
func (s *Store) Insert(ctx context.Context, sub *Submission) (created bool, err error) {
	attempts, err := json.Marshal(sub.AttemptsMs)
	if err != nil {
		return false, err
	}
	now := s.now().Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	const insert = `INSERT INTO submissions (
	    player_id, client_id, source, best_ms, avg_ms, accuracy, score, attempts_ms, misfires,
	    client_best_ms, client_avg_ms, client_accuracy, client_score,
	    app_build, platform, device_model, device_label, created_at)
	  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	  ON CONFLICT (player_id, client_id) DO NOTHING`
	res, err := tx.ExecContext(ctx, insert,
		sub.PlayerID, sub.ClientID, sub.Source, sub.BestMs, sub.AvgMs, sub.Accuracy, sub.Score, string(attempts), sub.Misfires,
		sub.ClientBestMs, sub.ClientAvgMs, sub.ClientAccuracy, sub.ClientScore,
		sub.AppBuild, sub.Platform, nullable(sub.DeviceModel), nullable(sub.DeviceLabel), now)
	if err != nil {
		return false, fmt.Errorf("insert submission: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 1 {
		id, err := res.LastInsertId()
		if err != nil {
			return false, err
		}
		sub.ID, sub.CreatedAt, created = id, now, true
	} else {
		const existing = `SELECT id, source, created_at FROM submissions WHERE player_id = ? AND client_id = ?`
		row := tx.QueryRowContext(ctx, existing, sub.PlayerID, sub.ClientID)
		if err := row.Scan(&sub.ID, &sub.Source, &sub.CreatedAt); err != nil {
			return false, fmt.Errorf("select existing submission: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET last_seen_at = ? WHERE id = ?`, now, sub.PlayerID); err != nil {
		return false, fmt.Errorf("touch player: %w", err)
	}
	return created, tx.Commit()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// The two board orders (M1 delta §2). Fastest is the default: Aaron's pick A ranks the
// fastest single attempt, on the app's local board and here alike, so a bare /v1/board
// and the app agree about who is first. Score keeps the M0 order.
const (
	SortFastest = "fastest"
	SortScore   = "score"
)

// ValidSort reports whether s is fastest or score.
func ValidSort(s string) bool { return s == SortFastest || s == SortScore }

// orderBy is the ORDER BY list for sort over the columns of alias (a table alias with
// its dot, or "" for bare columns). Callers check ValidSort first; anything but score is
// fastest here. Ties break the same way under both: the other measure, then the earlier
// created_at, then id.
func orderBy(sort, alias string) string {
	if sort == SortScore {
		return alias + "score DESC, " + alias + "best_ms ASC, " + alias + "created_at ASC, " + alias + "id ASC"
	}
	return alias + "best_ms ASC, " + alias + "score DESC, " + alias + "created_at ASC, " + alias + "id ASC"
}

// Entry is one board row (design brief §D, M1 delta §1). DeviceLabel and CreatedAt are
// the winning submission's, not the player's latest: the row says which pad set the
// score, and when.
type Entry struct {
	Rank        int     `json:"rank"`
	PlayerShort string  `json:"player_short"`
	DisplayName string  `json:"display_name"`
	Score       int     `json:"score"`
	BestMs      float64 `json:"best_ms"`
	AvgMs       float64 `json:"avg_ms"`
	Accuracy    float64 `json:"accuracy"`
	Tier        string  `json:"tier"`
	Platform    string  `json:"platform"`
	DeviceLabel string  `json:"device_label"`
	CreatedAt   string  `json:"created_at"` // RFC 3339, UTC
}

// bestPerPlayer numbers each player's submissions on one source from best to worst in
// sort's order; row 1 is the one the board shows. The same order ranks the board itself,
// so the row shown is the row the ranking is about: under fastest a player's slower,
// higher-scoring trial is not their entry, and neither is its pad.
func bestPerPlayer(sort string) string {
	return `
	SELECT s.id, s.player_id, s.score, s.best_ms, s.avg_ms, s.accuracy, s.platform, s.device_label, s.created_at,
	       ROW_NUMBER() OVER (PARTITION BY s.player_id ORDER BY ` + orderBy(sort, "s.") + `) AS rn
	FROM submissions s
	WHERE s.source = ? AND s.created_at >= ?`
}

// Board is the top limit players on source within window, one row per player, in
// sort's order. An unknown sort is an error, never a fallback.
func (s *Store) Board(ctx context.Context, source, window, sort string, limit int) ([]Entry, error) {
	if !ValidSort(sort) {
		return nil, fmt.Errorf("board: unknown sort %q", sort)
	}
	q := `WITH best AS (` + bestPerPlayer(sort) + `)
	  SELECT b.player_id, p.display_name, b.score, b.best_ms, b.avg_ms, b.accuracy, b.platform, b.device_label, b.created_at
	  FROM best b JOIN players p ON p.id = b.player_id
	  WHERE b.rn = 1
	  ORDER BY ` + orderBy(sort, "b.") + `
	  LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, source, s.since(window), limit)
	if err != nil {
		return nil, fmt.Errorf("board: %w", err)
	}
	defer rows.Close()
	entries := []Entry{} // never nil: the app decodes [] with no special case
	for rows.Next() {
		var e Entry
		var playerID string
		var label sql.NullString // "" was stored as NULL
		var created int64
		if err := rows.Scan(&playerID, &e.DisplayName, &e.Score, &e.BestMs, &e.AvgMs, &e.Accuracy, &e.Platform, &label, &created); err != nil {
			return nil, err
		}
		e.Rank = len(entries) + 1
		e.PlayerShort = players.Short(playerID)
		e.Tier = TierName(e.BestMs)
		e.DeviceLabel = label.String
		e.CreatedAt = time.Unix(created, 0).UTC().Format(time.RFC3339)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// since is the created_at floor for a window.
func (s *Store) since(window string) int64 {
	if window == Window30d {
		return s.now().Add(-thirtyDays).Unix()
	}
	return 0
}

// Rank is the player's position on source's all-time board in the default (fastest)
// order, and the board's size; rank is 0 when the player has no submission there. It is
// what POST /v1/scores answers, so it has to agree with a bare GET /v1/board.
func (s *Store) Rank(ctx context.Context, source, playerID string) (rank, size int, err error) {
	q := `WITH best AS (` + bestPerPlayer(SortFastest) + `),
	  top AS (SELECT player_id, ROW_NUMBER() OVER (ORDER BY ` + orderBy(SortFastest, "") + `) AS pos
	          FROM best WHERE rn = 1)
	  SELECT (SELECT count(*) FROM top), COALESCE((SELECT pos FROM top WHERE player_id = ?), 0)`
	if err := s.db.QueryRowContext(ctx, q, source, 0, playerID).Scan(&size, &rank); err != nil {
		return 0, 0, fmt.Errorf("rank: %w", err)
	}
	return rank, size, nil
}

// Bests is a player's best submission per source and their submission count; it
// backs GET /v1/players/{id}.
func (s *Store) Bests(ctx context.Context, playerID string) (players.Bests, int, error) {
	const q = `WITH best AS (
	    SELECT source, score, best_ms, avg_ms, accuracy, platform, created_at,
	           ROW_NUMBER() OVER (PARTITION BY source
	                              ORDER BY score DESC, best_ms ASC, created_at ASC, id ASC) AS rn
	    FROM submissions WHERE player_id = ?)
	  SELECT source, score, best_ms, avg_ms, accuracy, platform, created_at FROM best WHERE rn = 1`
	var b players.Bests
	if err := s.eachRow(ctx, q, []any{playerID}, func(rows *sql.Rows) error {
		var source string
		var sum players.ScoreSummary
		if err := rows.Scan(&source, &sum.Score, &sum.BestMs, &sum.AvgMs, &sum.Accuracy, &sum.Platform, &sum.CreatedAt); err != nil {
			return err
		}
		sum.Tier = TierName(sum.BestMs)
		b.Set(source, &sum)
		return nil
	}); err != nil {
		return players.Bests{}, 0, fmt.Errorf("bests: %w", err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM submissions WHERE player_id = ?`, playerID).Scan(&n); err != nil {
		return players.Bests{}, 0, fmt.Errorf("count submissions: %w", err)
	}
	return b, n, nil
}

// eachRow runs q and calls fn per row, closing the rows before returning so the single
// connection is free for the next statement.
func (s *Store) eachRow(ctx context.Context, q string, args []any, fn func(*sql.Rows) error) error {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
