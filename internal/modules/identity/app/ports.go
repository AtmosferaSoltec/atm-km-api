// Package app holds the account and session use cases and the ports
// (interfaces) they need. It knows nothing about Gin or Postgres.
package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/domain"
)

// Users stores accounts.
type Users interface {
	// Create returns domain.ErrUsernameTaken if the username exists.
	Create(ctx context.Context, user domain.User) error
	// ByUsername and ByID return domain.ErrNotFound if there is no such user.
	ByUsername(ctx context.Context, username string) (domain.User, error)
	ByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	TouchLastLogin(ctx context.Context, id uuid.UUID) error
	SetPassword(ctx context.Context, id uuid.UUID, hash string) error
	SetActive(ctx context.Context, id uuid.UUID, active bool) error
}

// Session is a device with an open session.
type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Web       bool
	UserAgent string
}

// RefreshToken is what gets stored of a refresh token: only its hash.
type RefreshToken struct {
	ID        uuid.UUID
	SessionID uuid.UUID
	Hash      []byte
	ExpiresAt time.Time
}

// LockedRefreshToken is a refresh token locked for use until the transaction ends.
type LockedRefreshToken struct {
	ID             uuid.UUID
	SessionID      uuid.UUID
	UserID         uuid.UUID
	Web            bool
	ExpiresAt      time.Time
	UsedAt         *time.Time
	SessionRevoked bool
	UserActive     bool
}

// Sessions stores sessions and refresh tokens.
type Sessions interface {
	Create(ctx context.Context, session Session) error
	// IsActive is false for a revoked session or a deactivated user.
	IsActive(ctx context.Context, sessionID, userID uuid.UUID) (bool, error)
	Revoke(ctx context.Context, sessionID uuid.UUID, reason string) error
	Touch(ctx context.Context, sessionID uuid.UUID) error
	CreateRefreshToken(ctx context.Context, token RefreshToken) error
	// LockRefreshToken returns domain.ErrNotFound if the hash does not exist.
	LockRefreshToken(ctx context.Context, hash []byte) (LockedRefreshToken, error)
	MarkRefreshTokenUsed(ctx context.Context, id uuid.UUID, now time.Time) error
	// RevokeAll closes every session of the user.
	RevokeAll(ctx context.Context, userID uuid.UUID, reason string) error
	// Prune deletes expired refresh tokens and sessions unused for staleAfter
	// that have no token left.
	Prune(ctx context.Context, now time.Time, staleAfter time.Duration) (int64, error)
}

// PasswordHasher computes and checks password hashes.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, hash string) (bool, error)
}

// AccessClaims is what an access token carries.
type AccessClaims struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

// AccessTokens signs and verifies access tokens.
type AccessTokens interface {
	Issue(claims AccessClaims, now time.Time) (string, error)
	// Verify returns auth.ErrUnauthorized if the token is no good.
	Verify(token string, now time.Time) (AccessClaims, error)
}

// Transactor opens a transaction (platform/tx.Runner).
type Transactor interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}
