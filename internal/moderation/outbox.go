package moderation

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/identity"
)

const (
	providerTimeout = 10 * time.Second
	claimLease      = 30 * time.Second // longer than the provider timeout
	finalizeTimeout = 2 * time.Second
)

// ErrDelivery omits provider errors, which may contain recipient addresses or payloads.
var ErrDelivery = errors.New("alert delivery failed")

// Alert contains only an opaque report ID. The future adapter can construct a secure
// review URL from its own deployment config; no player data belongs in an email.
type Alert struct {
	ReportID string
}

// AlertSender must return when ctx is canceled. A production adapter must also set its
// own network timeout; context cancellation alone cannot interrupt every SMTP client.
type AlertSender interface {
	Send(context.Context, Alert) error
}

// DispatchDue claims one due alert in SQLite, commits, and then calls the provider with
// a 10-second deadline. A 30-second lease prevents another worker from picking the same
// alert during that call; expiry recovers a crashed worker. Completion updates only the
// row with this lease token, so player erasure cannot resurrect an outbox item.
//
// The database and mail provider cannot commit atomically. An email whose send already
// started may still arrive after erasure begins, and a crash after provider acceptance
// may resend the same ID. Deleted reports cannot be claimed for a later attempt.
func (s *Store) DispatchDue(ctx context.Context, sender AlertSender) (attempted bool, err error) {
	id, token, attempts, err := s.claimDue(ctx)
	if err != nil || id == "" {
		return false, err
	}

	sendCtx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	sendErr := sender.Send(sendCtx, Alert{ReportID: id})
	// Persist the result even when the caller canceled the send context. A bounded
	// independent context prevents a canceled request from stranding the lease.
	finishCtx, finishCancel := context.WithTimeout(context.Background(), finalizeTimeout)
	defer finishCancel()
	if err := s.finishAttempt(finishCtx, id, token, attempts, sendErr == nil); err != nil {
		return true, err
	}
	if sendErr != nil {
		return true, ErrDelivery
	}
	return true, nil
}

func (s *Store) claimDue(ctx context.Context) (id, token string, attempts int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().Unix()
	err = tx.QueryRowContext(ctx, `SELECT report_id, attempts FROM report_alert_outbox
        WHERE sent_at IS NULL AND next_attempt_at <= ? AND lease_until <= ?
        ORDER BY next_attempt_at, report_id LIMIT 1`, now, now).Scan(&id, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", 0, nil
	}
	if err != nil {
		return "", "", 0, err
	}
	token, err = identity.NewRef()
	if err != nil {
		return "", "", 0, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE report_alert_outbox
        SET lease_token = ?, lease_until = ?
        WHERE report_id = ? AND sent_at IS NULL AND lease_until <= ?`, token, now+int64(claimLease.Seconds()), id, now)
	if err != nil {
		return "", "", 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", "", 0, err
	}
	if n != 1 {
		return "", "", 0, nil // another worker claimed or erased the row
	}
	if err := tx.Commit(); err != nil {
		return "", "", 0, err
	}
	return id, token, attempts, nil
}

func (s *Store) finishAttempt(ctx context.Context, id, token string, attempts int, sent bool) error {
	now := s.now().Unix()
	if sent {
		_, err := s.db.ExecContext(ctx, `UPDATE report_alert_outbox
            SET attempts = attempts + 1, sent_at = ?, lease_token = NULL, lease_until = 0
            WHERE report_id = ? AND lease_token = ?`, now, id, token)
		return err
	}
	// Exponential retry capped at one hour; no provider error text is persisted.
	backoff := time.Minute * time.Duration(1<<min(attempts, 6))
	if backoff > time.Hour {
		backoff = time.Hour
	}
	_, err := s.db.ExecContext(ctx, `UPDATE report_alert_outbox
        SET attempts = attempts + 1, next_attempt_at = ?, lease_token = NULL, lease_until = 0
        WHERE report_id = ? AND lease_token = ?`, now+int64(backoff.Seconds()), id, token)
	return err
}
