// Package store holds the persistence queries for users, their registered
// Spliit servers, the groups they have made available, and web sessions.
//
// Every query uses `?` placeholders and is rebound for the active dialect, so
// the same code runs on SQLite and Postgres. See package db.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/daedaluz/spliit-mcp/internal/db"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a write violates a uniqueness constraint, such
// as reusing an alias or registering the same group twice on one server.
var ErrConflict = errors.New("conflict")

// Store provides access to the application tables.
type Store struct {
	db *db.DB
}

// New returns a Store backed by the given database.
func New(database *db.DB) *Store { return &Store{db: database} }

// User is an authenticated principal, keyed by OIDC subject.
type User struct {
	Sub         string    `db:"sub" json:"sub"`
	Issuer      string    `db:"issuer" json:"issuer"`
	Email       string    `db:"email" json:"email"`
	DisplayName string    `db:"display_name" json:"display_name"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}

// Group is a Spliit group a user has made available to MCP, together with the
// participant that represents them in it.
type Group struct {
	ID      string `db:"id" json:"id"`
	UserSub string `db:"user_sub" json:"-"`
	// BaseURL is the tRPC endpoint of the Spliit instance hosting this group,
	// derived from the link the user pasted rather than configured separately.
	BaseURL string `db:"base_url" json:"base_url"`
	// SpliitGroupID is the ID in the Spliit instance. It is a bearer capability:
	// anyone holding it has full access to the group.
	SpliitGroupID string `db:"spliit_group_id" json:"spliit_group_id"`
	// Alias is the short name MCP tools accept, unique per user.
	Alias string `db:"alias" json:"alias"`
	// ParticipantID pins which participant is "you" in this group.
	ParticipantID   string    `db:"participant_id" json:"participant_id"`
	ParticipantName string    `db:"participant_name" json:"participant_name"`
	GroupName       string    `db:"group_name" json:"group_name"`
	Currency        string    `db:"currency" json:"currency"`
	CreatedAt       time.Time `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time `db:"updated_at" json:"updated_at"`
}

// Session is a config web UI login.
type Session struct {
	ID        string    `db:"id"`
	UserSub   string    `db:"user_sub"`
	ExpiresAt time.Time `db:"expires_at"`
	CreatedAt time.Time `db:"created_at"`
}

func (s *Store) rebind(query string) string { return s.db.Rebind(query) }

// UpsertUser creates or refreshes a user from verified OIDC claims. The stored
// display name is only seeded on insert: it is user-editable afterwards and
// must not be clobbered by the provider on every login.
func (s *Store) UpsertUser(ctx context.Context, sub, issuer, email, displayName string) (*User, error) {
	now := time.Now().UTC()

	existing, err := s.GetUser(ctx, sub)
	switch {
	case err == nil:
		if _, err := s.db.ExecContext(ctx, s.rebind(
			`UPDATE users SET issuer = ?, email = ?, updated_at = ? WHERE sub = ?`,
		), issuer, email, now, sub); err != nil {
			return nil, fmt.Errorf("update user: %w", err)
		}
		existing.Issuer, existing.Email, existing.UpdatedAt = issuer, email, now
		return existing, nil
	case errors.Is(err, ErrNotFound):
		if _, err := s.db.ExecContext(ctx, s.rebind(
			`INSERT INTO users (sub, issuer, email, display_name, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
		), sub, issuer, email, displayName, now, now); err != nil {
			return nil, fmt.Errorf("insert user: %w", err)
		}
		return &User{
			Sub: sub, Issuer: issuer, Email: email, DisplayName: displayName,
			CreatedAt: now, UpdatedAt: now,
		}, nil
	default:
		return nil, err
	}
}

// GetUser looks up a user by OIDC subject.
func (s *Store) GetUser(ctx context.Context, sub string) (*User, error) {
	var u User
	err := s.db.GetContext(ctx, &u, s.rebind(`SELECT * FROM users WHERE sub = ?`), sub)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &u, nil
}

// SetDisplayName updates the name used to recognise "you" among participants.
func (s *Store) SetDisplayName(ctx context.Context, sub, displayName string) error {
	res, err := s.db.ExecContext(ctx, s.rebind(
		`UPDATE users SET display_name = ?, updated_at = ? WHERE sub = ?`,
	), displayName, time.Now().UTC(), sub)
	if err != nil {
		return fmt.Errorf("set display name: %w", err)
	}
	return requireAffected(res)
}

// CreateGroup makes a Spliit group available to a user's MCP session.
func (s *Store) CreateGroup(ctx context.Context, g *Group) (*Group, error) {
	now := time.Now().UTC()
	g.ID, g.CreatedAt, g.UpdatedAt = uuid.NewString(), now, now

	_, err := s.db.ExecContext(ctx, s.rebind(
		`INSERT INTO groups (id, user_sub, base_url, spliit_group_id, alias,
		                     participant_id, participant_name, group_name, currency,
		                     created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	), g.ID, g.UserSub, g.BaseURL, g.SpliitGroupID, g.Alias,
		g.ParticipantID, g.ParticipantName, g.GroupName, g.Currency, g.CreatedAt, g.UpdatedAt)
	if err != nil {
		return nil, wrapWriteErr("insert group", err)
	}
	return g, nil
}

// ListGroups returns every group a user has registered, across all servers.
func (s *Store) ListGroups(ctx context.Context, sub string) ([]Group, error) {
	groups := []Group{}
	if err := s.db.SelectContext(ctx, &groups, s.rebind(
		`SELECT * FROM groups WHERE user_sub = ? ORDER BY alias`,
	), sub); err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	return groups, nil
}

// GetGroup fetches one of the user's groups by internal ID.
func (s *Store) GetGroup(ctx context.Context, sub, id string) (*Group, error) {
	var g Group
	err := s.db.GetContext(ctx, &g, s.rebind(
		`SELECT * FROM groups WHERE id = ? AND user_sub = ?`,
	), id, sub)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get group: %w", err)
	}
	return &g, nil
}

// ResolveGroup finds a group by alias, by internal ID, or by Spliit group ID —
// always scoped to the calling user. This scoping is the authorization boundary
// for MCP tools: a group the user has not registered is simply not found, so a
// leaked group ID from elsewhere cannot be used through this server.
func (s *Store) ResolveGroup(ctx context.Context, sub, ref string) (*Group, error) {
	var g Group
	err := s.db.GetContext(ctx, &g, s.rebind(
		`SELECT * FROM groups
		 WHERE user_sub = ? AND (alias = ? OR id = ? OR spliit_group_id = ?)
		 ORDER BY CASE WHEN alias = ? THEN 0 ELSE 1 END
		 LIMIT 1`,
	), sub, ref, ref, ref, ref)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve group: %w", err)
	}
	return &g, nil
}

// UpdateGroup persists edits to a group's alias, pinned participant, and the
// cached name/currency read back from Spliit.
func (s *Store) UpdateGroup(ctx context.Context, g *Group) error {
	g.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, s.rebind(
		`UPDATE groups
		 SET alias = ?, participant_id = ?, participant_name = ?,
		     group_name = ?, currency = ?, updated_at = ?
		 WHERE id = ? AND user_sub = ?`,
	), g.Alias, g.ParticipantID, g.ParticipantName, g.GroupName, g.Currency,
		g.UpdatedAt, g.ID, g.UserSub)
	if err != nil {
		return wrapWriteErr("update group", err)
	}
	return requireAffected(res)
}

// DeleteGroup unlinks a group locally. It never touches the Spliit instance.
func (s *Store) DeleteGroup(ctx context.Context, sub, id string) error {
	res, err := s.db.ExecContext(ctx, s.rebind(
		`DELETE FROM groups WHERE id = ? AND user_sub = ?`,
	), id, sub)
	if err != nil {
		return wrapWriteErr("delete group", err)
	}
	return requireAffected(res)
}

// CreateSession starts a config web UI session.
func (s *Store) CreateSession(ctx context.Context, sub string, ttl time.Duration) (*Session, error) {
	now := time.Now().UTC()
	sess := &Session{
		ID: uuid.NewString(), UserSub: sub,
		ExpiresAt: now.Add(ttl), CreatedAt: now,
	}
	if _, err := s.db.ExecContext(ctx, s.rebind(
		`INSERT INTO sessions (id, user_sub, expires_at, created_at) VALUES (?, ?, ?, ?)`,
	), sess.ID, sess.UserSub, sess.ExpiresAt, sess.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}
	return sess, nil
}

// GetSession returns a live session, or ErrNotFound if it is missing or expired.
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	var sess Session
	err := s.db.GetContext(ctx, &sess, s.rebind(
		`SELECT * FROM sessions WHERE id = ? AND expires_at > ?`,
	), id, time.Now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &sess, nil
}

// DeleteSession logs a session out.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, s.rebind(
		`DELETE FROM sessions WHERE id = ?`,
	), id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions purges sessions that are past their expiry.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, s.rebind(
		`DELETE FROM sessions WHERE expires_at <= ?`,
	), time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil // Not all drivers report this; a missing count is not an error.
	}
	return n, nil
}

func requireAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return nil // Driver does not report it; assume success.
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// wrapWriteErr maps driver-specific uniqueness violations onto ErrConflict.
// The two engines word these differently, and neither exposes a portable code,
// so this matches on the message text.
func wrapWriteErr(op string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, marker := range []string{
		"UNIQUE constraint failed", "duplicate key value", // uniqueness
		"FOREIGN KEY constraint failed", "violates foreign key constraint", // referential
	} {
		if strings.Contains(msg, marker) {
			return fmt.Errorf("%s: %w", op, ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}
