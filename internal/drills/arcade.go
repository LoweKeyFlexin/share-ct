package drills

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/players"
)

// ArcadeStats contains aggregate run facts, never input histories. A completed
// run ranks by time; a partial run ranks by whole opponents, not boss phases.
type ArcadeStats struct {
	EnemiesDefeated  int  `json:"enemies_defeated"`
	TotalOpponents   int  `json:"total_opponents"`
	Completed        bool `json:"completed"`
	ElapsedMS        int  `json:"elapsed_ms"`
	PerfectOpponents int  `json:"perfect_opponents"`
	Hits             int  `json:"hits"`
	ConsistencyBonus int  `json:"consistency_bonus"`
	TimeBonus        int  `json:"time_bonus"`
}

func (a ArcadeStats) bonuses() (int, int) {
	consistency := 5*a.PerfectOpponents + 10*(a.EnemiesDefeated/3)
	speed := 0
	if a.Completed {
		speed = max(0, 300-a.ElapsedMS/1000)
	}
	return consistency, speed
}
func (a ArcadeStats) points() int { c, t := a.bonuses(); return a.Hits + c + t }
func validateArcadeDrill(r Request) string {
	if r.DrillID == "mental-stack" {
		return "drill_id"
	}
	return validateArcade(r)
}
func validateArcade(r Request) string {
	a := r.ArcadeStats
	if a == nil {
		return "arcade"
	}
	// The current one-pass ladder has 13 eligible opponents, or 12 when the
	// selected player removes an additional opponent. No client-defined ladder.
	if a.TotalOpponents < 12 || a.TotalOpponents > 13 {
		return "total_opponents"
	}
	if a.EnemiesDefeated < 0 || a.EnemiesDefeated > a.TotalOpponents {
		return "enemies_defeated"
	}
	if a.Completed != (a.EnemiesDefeated == a.TotalOpponents) {
		return "completed"
	}
	if a.ElapsedMS <= 0 || a.ElapsedMS > 24*60*60*1000 {
		return "elapsed_ms"
	}
	if a.PerfectOpponents < 0 || a.PerfectOpponents > a.EnemiesDefeated {
		return "perfect_opponents"
	}
	if a.Hits < a.EnemiesDefeated || a.Hits > 1000000 {
		return "hits"
	}
	if r.Misses < 0 || r.Misses > 1000000 {
		return "misses"
	}
	c, t := a.bonuses()
	// Optional request bonus fields may be zero/omitted; nonzero claims must agree.
	if a.ConsistencyBonus != 0 && a.ConsistencyBonus != c {
		return "consistency_bonus"
	}
	if a.TimeBonus != 0 && a.TimeBonus != t {
		return "time_bonus"
	}
	if r.Score == nil || *r.Score != a.points() || *r.Score > 1000000 {
		return "score"
	}
	return ""
}

// One order owns both PB selection and public placement. A clear always beats
// a partial. Clear time is primary; partial whole-opponent progress is primary.
const arcadeOrder = `completed DESC,CASE WHEN completed=0 THEN enemies_defeated ELSE 0 END DESC,
 CASE WHEN completed=1 THEN elapsed_ms ELSE 0 END ASC,score DESC,elapsed_ms ASC,created_at ASC,id ASC`
const arcadeBetter = `(excluded.completed,
 CASE WHEN excluded.completed=0 THEN excluded.enemies_defeated ELSE 0 END,
 CASE WHEN excluded.completed=1 THEN -excluded.elapsed_ms ELSE 0 END,excluded.score,-excluded.elapsed_ms) >
 (arcade_bests.completed,CASE WHEN arcade_bests.completed=0 THEN arcade_bests.enemies_defeated ELSE 0 END,
 CASE WHEN arcade_bests.completed=1 THEN -arcade_bests.elapsed_ms ELSE 0 END,arcade_bests.score,-arcade_bests.elapsed_ms)`
const arcadeFields = `player_id,client_id,receipt_id,body_hash,drill_id,score,enemies_defeated,total_opponents,completed,elapsed_ms,perfect_opponents,hits,source,app_build,platform,device_model,device_label,created_at`

func (m *Module) saveArcade(ctx context.Context, player string, req Request, publishesRun bool) (int64, bool, error) {
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
	var intent bool
	err = tx.QueryRowContext(ctx, `SELECT id,body_hash,publishes_run FROM arcade_receipts WHERE player_id=? AND client_id=?`, player, req.ClientID).Scan(&id, &oldHash, &intent)
	if err == nil {
		if hash != oldHash || intent != publishesRun {
			return 0, false, errConflict
		}
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	for _, table := range []string{"arcade_runs", "arcade_bests"} {
		err = tx.QueryRowContext(ctx, `SELECT receipt_id,body_hash,publishes_run FROM `+table+` WHERE player_id=? AND client_id=?`, player, req.ClientID).Scan(&id, &oldHash, &intent)
		if err == nil {
			if hash != oldHash || intent != publishesRun {
				return 0, false, errConflict
			}
			return id, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, false, err
		}
	}
	now := m.now().Unix()
	if _, err = tx.ExecContext(ctx, `DELETE FROM arcade_runs WHERE created_at<?`, m.now().Add(-RunRetention).Unix()); err != nil {
		return 0, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO arcade_receipts(player_id,client_id,body_hash,created_at,publishes_run) VALUES(?,?,?,?,?)`, player, req.ClientID, hash, now, publishesRun)
	if err != nil {
		return 0, false, err
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	a := req.ArcadeStats
	args := []any{player, req.ClientID, id, hash, req.DrillID, a.points(), a.EnemiesDefeated, a.TotalOpponents, a.Completed, a.ElapsedMS, a.PerfectOpponents, a.Hits, req.Source, req.AppBuild, req.Platform, req.DeviceModel, req.DeviceLabel, now, publishesRun}
	_, err = tx.ExecContext(ctx, `INSERT INTO arcade_bests(`+arcadeFields+`,publishes_run) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(player_id,drill_id) DO UPDATE SET
 client_id=excluded.client_id,receipt_id=excluded.receipt_id,body_hash=excluded.body_hash,score=excluded.score,
 enemies_defeated=excluded.enemies_defeated,total_opponents=excluded.total_opponents,completed=excluded.completed,
 elapsed_ms=excluded.elapsed_ms,perfect_opponents=excluded.perfect_opponents,hits=excluded.hits,
 source=excluded.source,app_build=excluded.app_build,platform=excluded.platform,device_model=excluded.device_model,
 device_label=excluded.device_label,created_at=excluded.created_at,publishes_run=excluded.publishes_run WHERE `+arcadeBetter, args...)
	if err != nil {
		return 0, false, err
	}
	if publishesRun {
		runResult, err := tx.ExecContext(ctx, `INSERT INTO arcade_runs(`+arcadeFields+`,publishes_run) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
		if err != nil {
			return 0, false, err
		}
		runID, err := runResult.LastInsertId()
		if err != nil {
			return 0, false, err
		}
		if err = prunePlayerRuns(ctx, tx, player, "arcade", runID, now); err != nil {
			return 0, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM arcade_receipts WHERE player_id=? AND id NOT IN (SELECT id FROM arcade_receipts WHERE player_id=? ORDER BY id DESC LIMIT 100)`, player, player); err != nil {
		return 0, false, err
	}
	return id, false, tx.Commit()
}

func (m *Module) readArcade(ctx context.Context, drill, player string, limit int, since int64, recent bool) (Board, error) {
	b := Board{Mode: "arcade", DrillID: drill, Entries: []Entry{}}
	if err := PruneExpired(ctx, m.db, m.now()); err != nil {
		return b, err
	}
	// The bounded Recent store and retained PBs jointly supply a window's best.
	pool := `SELECT * FROM arcade_bests WHERE drill_id=?`
	args := []any{drill}
	if since > 0 {
		pool = `SELECT * FROM arcade_bests WHERE drill_id=? AND created_at>=? UNION ALL SELECT * FROM arcade_runs WHERE drill_id=? AND created_at>=?`
		args = []any{drill, since, drill, since}
	}
	query := `WITH pool AS (` + pool + `),picked AS (SELECT pool.*,ROW_NUMBER() OVER(PARTITION BY player_id ORDER BY ` + arcadeOrder + `) AS pick FROM pool),
 ranked AS (SELECT *,ROW_NUMBER() OVER(ORDER BY ` + arcadeOrder + `) AS rank,count(*) OVER() AS board_size FROM picked WHERE pick=1)
 SELECT b.rank,b.board_size,p.id,p.player_ref,p.display_name,b.score,b.source,b.platform,b.device_label,b.created_at,p.avatar,
 b.enemies_defeated,b.total_opponents,b.completed,b.elapsed_ms,b.perfect_opponents,b.hits,b.client_id
 FROM ranked b JOIN players p ON p.id=b.player_id WHERE b.rank<=? OR b.player_id=? ORDER BY b.rank`
	args = append(args, limit, player)
	if recent {
		own, err := m.readArcade(ctx, drill, player, 1, since, false)
		if err != nil {
			return b, err
		}
		b.Own, b.BoardSize, b.Sort = own.Own, own.BoardSize, "recent"
		query = `SELECT 0,0,p.id,p.player_ref,p.display_name,b.score,b.source,b.platform,b.device_label,b.created_at,p.avatar,
 b.enemies_defeated,b.total_opponents,b.completed,b.elapsed_ms,b.perfect_opponents,b.hits,b.client_id
 FROM arcade_runs b JOIN players p ON p.id=b.player_id WHERE b.drill_id=? AND b.created_at>=? ORDER BY b.created_at DESC,b.id DESC LIMIT ?`
		args = []any{drill, max(since, m.now().Add(-RunRetention).Unix()), limit}
	}
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		a := ArcadeStats{}
		e.ArcadeStats = &a
		var pid, client string
		var stamp int64
		var avatar sql.NullString
		var size int
		if err = rows.Scan(&e.Rank, &size, &pid, &e.PlayerRef, &e.DisplayName, &e.Score, &e.Source, &e.Platform, &e.DeviceLabel, &stamp, &avatar,
			&a.EnemiesDefeated, &a.TotalOpponents, &a.Completed, &a.ElapsedMS, &a.PerfectOpponents, &a.Hits, &client); err != nil {
			return b, err
		}
		a.ConsistencyBonus, a.TimeBonus = a.bonuses()
		e.Avatar = players.StoredAvatar(avatar)
		e.PlayerShort, e.DisplayName, e.CreatedAt = players.Short(pid), players.Display(e.DisplayName), time.Unix(stamp, 0).UTC().Format(time.RFC3339)
		if recent {
			e.RunRef = publicRunRef(e.PlayerRef, "arcade\x00"+client)
		} else {
			b.BoardSize = size
		}
		if recent || e.Rank <= limit {
			b.Entries = append(b.Entries, e)
		}
		if !recent && pid == player {
			own := e
			b.Own = &own
		}
	}
	return b, rows.Err()
}
