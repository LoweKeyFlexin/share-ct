package moderation

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrDelivery omits provider errors, which may contain recipient addresses or payloads.
var ErrDelivery = errors.New("alert delivery failed")

// Alert is the minimum information an email adapter needs. A provider and private
// recipient are deliberately not configured in this release-blocked draft.
type Alert struct {
	ReportID        string
	Reason          string
	TargetPlayerRef string
}

type AlertSender interface {
	Send(context.Context, Alert) error
}

// DispatchDue attempts one due alert. The transaction keeps player erasure from
// overtaking an in-flight send. A deleted report has no outbox row to select.
// Delivery is at least once: a crash after provider acceptance can retry the same ID.
func (s *Store) DispatchDue(ctx context.Context, sender AlertSender) (attempted bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var alert Alert
	var attempts int
	now := s.now().Unix()
	err = tx.QueryRowContext(ctx, `SELECT r.id, r.reason, p.player_ref, o.attempts
		FROM report_alert_outbox o JOIN reports r ON r.id = o.report_id
		JOIN players p ON p.id = r.target_id
		WHERE o.sent_at IS NULL AND o.next_attempt_at <= ?
		ORDER BY o.next_attempt_at, r.created_at LIMIT 1`, now).Scan(&alert.ReportID, &alert.Reason, &alert.TargetPlayerRef, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sendErr := sender.Send(ctx, alert)
	if sendErr == nil {
		_, err = tx.ExecContext(ctx, `UPDATE report_alert_outbox SET attempts = attempts + 1, sent_at = ? WHERE report_id = ?`, now, alert.ReportID)
	} else {
		// Exponential retry capped at one hour; no provider error text is persisted.
		backoff := time.Minute * time.Duration(1<<min(attempts, 6))
		if backoff > time.Hour {
			backoff = time.Hour
		}
		_, err = tx.ExecContext(ctx, `UPDATE report_alert_outbox SET attempts = attempts + 1, next_attempt_at = ? WHERE report_id = ?`, now+int64(backoff.Seconds()), alert.ReportID)
	}
	if err != nil {
		return true, err
	}
	if err := tx.Commit(); err != nil {
		return true, err
	}
	if sendErr != nil {
		return true, ErrDelivery
	}
	return true, nil
}
