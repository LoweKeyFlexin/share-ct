package drills

import (
	"context"
	"database/sql"
	"github.com/LoweKeyFlexin/share-ct/internal/players"
	"time"
)

// ActivityEntry identifies a public run's board without turning chronology into rank.
type ActivityEntry struct {
	Entry
	DrillID string `json:"drill_id"`
	Mode    string `json:"mode"`
}

// RecentActivity reads only explicit run-feed publications, never best-only rows.
// Each indexed source is capped before the union, so at most 2*limit rows join
// player presentation. Existing per-player retention and erase ownership remain.
func (m *Module) RecentActivity(ctx context.Context, limit int) ([]ActivityEntry, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}
	cutoff := m.now().Add(-RunRetention).Unix()
	rows, err := m.db.QueryContext(ctx, `WITH
 endless AS (SELECT id,player_id,client_id,drill_id,score,source,platform,device_label,created_at FROM drill_runs WHERE created_at>=? ORDER BY created_at DESC,id DESC LIMIT ?),
 arcade AS (SELECT * FROM arcade_runs WHERE created_at>=? AND publishes_run=1 ORDER BY created_at DESC,id DESC LIMIT ?),
 pool AS (
  SELECT id,player_id,client_id,drill_id,score,source,platform,device_label,created_at,'endless' AS mode,0 AS enemies,0 AS total,0 AS completed,0 AS elapsed,0 AS perfect,0 AS hits FROM endless
  UNION ALL
  SELECT id,player_id,client_id,drill_id,score,source,platform,device_label,created_at,'arcade',enemies_defeated,total_opponents,completed,elapsed_ms,perfect_opponents,hits FROM arcade)
 SELECT p.id,p.player_ref,p.display_name,b.drill_id,b.mode,b.score,b.source,b.platform,b.device_label,b.created_at,b.client_id,COALESCE(p.avatar_complete,p.avatar),b.enemies,b.total,b.completed,b.elapsed,b.perfect,b.hits
 FROM pool b JOIN players p ON p.id=b.player_id ORDER BY b.created_at DESC,b.id DESC,b.mode LIMIT ?`, cutoff, limit, cutoff, limit, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ActivityEntry{}
	for rows.Next() {
		var e ActivityEntry
		var player, client string
		var stamp int64
		var avatar sql.NullString
		var stats ArcadeStats
		if err := rows.Scan(&player, &e.PlayerRef, &e.DisplayName, &e.DrillID, &e.Mode, &e.Score, &e.Source, &e.Platform, &e.DeviceLabel, &stamp, &client, &avatar,
			&stats.EnemiesDefeated, &stats.TotalOpponents, &stats.Completed, &stats.ElapsedMS, &stats.PerfectOpponents, &stats.Hits); err != nil {
			return nil, err
		}
		e.PlayerShort = players.Short(player)
		e.DisplayName = players.Display(e.DisplayName)
		e.CreatedAt = time.Unix(stamp, 0).UTC().Format(time.RFC3339)
		e.Avatar = players.StoredAvatar(avatar)
		e.RunRef = publicRunRef(e.PlayerRef, client)
		if e.Mode == "arcade" {
			stats.ConsistencyBonus, stats.TimeBonus = stats.bonuses()
			e.ArcadeStats = &stats
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
