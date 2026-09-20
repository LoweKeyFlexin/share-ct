package players

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/identity"
)

// ErrNotFound is returned when no player matches an id or token.
var ErrNotFound = errors.New("player not found")

// Player is one row of the players table.
type Player struct {
	ID          string
	Ref         string
	DisplayName string
	Platform    string
	CreatedAt   int64
	LastSeenAt  int64
}

// Short is the player's id tail, see Short.
func (p Player) Short() string { return Short(p.ID) }

// Store reads and writes the players table.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore wraps the shared connection; now stamps created_at and last_seen_at.
func NewStore(db *sql.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

const playerColumns = `id, player_ref, display_name, platform, created_at, last_seen_at`

// Create mints a player and returns it with the one-time bearer token. Only the
// token's sha256 is stored.
func (s *Store) Create(ctx context.Context, displayName, platform string) (Player, string, error) {
	id, err := newUUID()
	if err != nil {
		return Player{}, "", err
	}
	token, err := newToken()
	if err != nil {
		return Player{}, "", err
	}
	ref, err := identity.NewRef()
	if err != nil {
		return Player{}, "", err
	}
	now := s.now().Unix()
	const q = `INSERT INTO players (id, player_ref, token_hash, display_name, platform, created_at, last_seen_at)
	           VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q, id, ref, hashToken(token), displayName, platform, now, now); err != nil {
		return Player{}, "", fmt.Errorf("insert player: %w", err)
	}
	return Player{ID: id, Ref: ref, DisplayName: displayName, Platform: platform, CreatedAt: now, LastSeenAt: now}, token, nil
}

// Get looks a player up by id.
func (s *Store) Get(ctx context.Context, id string) (Player, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+playerColumns+` FROM players WHERE id = ?`, id)
	var p Player
	err := row.Scan(&p.ID, &p.Ref, &p.DisplayName, &p.Platform, &p.CreatedAt, &p.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Player{}, ErrNotFound
	}
	if err != nil {
		return Player{}, fmt.Errorf("select player: %w", err)
	}
	return p, nil
}

// Authenticate resolves a bearer token to its player, or ErrNotFound. The row is found
// by the token's sha256 (the index is on the hash, so nothing about the token itself
// leaks through lookup timing) and the stored hash is then compared in constant time.
func (s *Store) Authenticate(ctx context.Context, token string) (Player, error) {
	h := hashToken(token)
	row := s.db.QueryRowContext(ctx, `SELECT `+playerColumns+`, token_hash FROM players WHERE token_hash = ?`, h)
	var p Player
	var stored string
	err := row.Scan(&p.ID, &p.Ref, &p.DisplayName, &p.Platform, &p.CreatedAt, &p.LastSeenAt, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Player{}, ErrNotFound
	}
	if err != nil {
		return Player{}, fmt.Errorf("select player by token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(h)) != 1 {
		return Player{}, ErrNotFound
	}
	return p, nil
}

// Rename changes the display name and bumps last_seen_at.
func (s *Store) Rename(ctx context.Context, id, displayName string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE players SET display_name = ?, last_seen_at = ? WHERE id = ?`,
		displayName, s.now().Unix(), id)
	return affectedOne(res, err)
}

// Delete erases the player. Every feature table references players(id) with
// ON DELETE CASCADE and the connection runs with foreign keys on, so the player's
// rows go with it: this is "delete my data".
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM players WHERE id = ?`, id)
	return affectedOne(res, err)
}

// Touch bumps last_seen_at.
func (s *Store) Touch(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE players SET last_seen_at = ? WHERE id = ?`, s.now().Unix(), id)
	return affectedOne(res, err)
}

func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
