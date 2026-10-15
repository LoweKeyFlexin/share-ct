package drills

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"github.com/LoweKeyFlexin/share-ct/internal/players"
	"log/slog"
	"time"
)

const RunCap = 200
const RunRetention = 90 * 24 * time.Hour

// PruneExpired runs at startup and hourly, and before a Recent read or accepted
// submission. Expired runs never appear publicly while awaiting routine cleanup.
func PruneExpired(ctx context.Context, db *sql.DB, now time.Time) error {
	_, err := db.ExecContext(ctx, `DELETE FROM drill_runs WHERE created_at<?`, now.Add(-RunRetention).Unix())
	return err
}

// MaintainRetention belongs to the process context and stops on normal shutdown.
func MaintainRetention(ctx context.Context, db *sql.DB, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := PruneExpired(ctx, db, now); err != nil {
				log.Error("drills retention cleanup failed", "error", err.Error())
			}
		}
	}
}

// publicRunRef contains neither private player identity nor authentication data.
func publicRunRef(playerRef, clientID string) string {
	digest := sha256.Sum256([]byte(playerRef + "\x00" + clientID))
	return hex.EncodeToString(digest[:])
}

func (m *Module) readRecent(ctx context.Context, drill, player string, limit int) (Board, error) {
	if err := PruneExpired(ctx, m.db, m.now()); err != nil {
		return Board{}, err
	}
	// Own retains the actual all-time standing; the feed itself has no ranking.
	board, err := m.read(ctx, drill, player, 1)
	if err != nil {
		return Board{}, err
	}
	board.Sort = "recent"
	board.Entries = []Entry{}
	rows, err := m.db.QueryContext(ctx, `SELECT p.id,p.player_ref,p.display_name,r.client_id,r.score,r.source,r.platform,r.device_label,r.created_at
 FROM drill_runs r JOIN players p ON p.id=r.player_id WHERE r.drill_id=? AND r.created_at>=?
 ORDER BY r.created_at DESC,r.id DESC LIMIT ?`, drill, m.now().Add(-RunRetention).Unix(), limit)
	if err != nil {
		return Board{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry Entry
		var playerID, clientID string
		var stamp int64
		if err = rows.Scan(&playerID, &entry.PlayerRef, &entry.DisplayName, &clientID, &entry.Score, &entry.Source, &entry.Platform, &entry.DeviceLabel, &stamp); err != nil {
			return Board{}, err
		}
		entry.PlayerShort = players.Short(playerID)
		entry.DisplayName = players.Display(entry.DisplayName)
		entry.CreatedAt = time.Unix(stamp, 0).UTC().Format(time.RFC3339)
		entry.RunRef = publicRunRef(entry.PlayerRef, clientID)
		board.Entries = append(board.Entries, entry)
	}
	return board, rows.Err()
}
