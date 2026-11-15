package leaderboard

import (
	"context"
	"database/sql"
	"time"
)

// ReactionCrownReceipt is private to the authenticated board reader. It identifies
// an already-held submission, never creates a run or exposes private player IDs.
type ReactionCrownReceipt struct {
	SubmissionID int64  `json:"submission_id"`
	ClientID     string `json:"client_id"`
}

// ReactionCrown restores an earned receipt, or certifies an existing leader even
// if they have not submitted since the reward shipped. The three canonical source
// boards are checked independently of the visible board's filter/order/window.
func (s *Store) ReactionCrown(ctx context.Context, playerID string) (*ReactionCrownReceipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var receipt ReactionCrownReceipt
	err = tx.QueryRowContext(ctx, `SELECT id, client_id FROM submissions WHERE player_id = ? AND reaction_crown_earned = 1 ORDER BY id LIMIT 1`, playerID).Scan(&receipt.SubmissionID, &receipt.ClientID)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil {
		return &receipt, tx.Commit()
	}
	since := s.now().Unix() - int64(thirtyDays/time.Second)
	for _, source := range Sources {
		q := `WITH best AS (` + bestPerPlayer(SortFastest, source) + `)
          SELECT b.player_id, b.id FROM best b WHERE b.rn = 1
          ORDER BY ` + orderBy(SortFastest, "b.") + ` LIMIT 1`
		var winner string
		var submissionID int64
		err = tx.QueryRowContext(ctx, q, source, since).Scan(&winner, &submissionID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		if winner != playerID {
			continue
		}
		if err = tx.QueryRowContext(ctx, `SELECT id, client_id FROM submissions WHERE id = ? AND player_id = ?`, submissionID, playerID).Scan(&receipt.SubmissionID, &receipt.ClientID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE submissions SET reaction_crown_earned = 1 WHERE id = ? AND player_id = ?`, receipt.SubmissionID, playerID); err != nil {
			return nil, err
		}
		return &receipt, tx.Commit()
	}
	return nil, tx.Commit()
}
