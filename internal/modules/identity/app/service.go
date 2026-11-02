package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/ids"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/logger"
)

// Reasons a session is revoked (sessions.revoked_reason).
const (
	revokedLogout      = "logout"
	revokedReuse       = "reuse"
	revokedPassword    = "password_changed"
	revokedDeactivated = "deactivated"
)

const (
	refreshTokenBytes = 32
	userAgentMaxRunes = 255
	// refreshRaceWindow: a refresh token used again this soon after its rotation
	// is a parallel request from the same device (two tabs, a double render), not
	// a thief. It gets a retryable error instead of revoking the session.
	refreshRaceWindow = 30 * time.Second
)

// Config holds the token lifetimes.
type Config struct {
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

// Deps are the ports the service needs.
type Deps struct {
	Users    Users
	Sessions Sessions
	Hasher   PasswordHasher
	Tokens   AccessTokens
	Tx       Transactor
	Clock    clock.Clock
	Config   Config
}

// Service holds the account and session use cases.
type Service struct {
	deps      Deps
	attempts  *loginAttempts
	dummyHash string
}

// NewService creates the service. It computes a dummy hash so that logging in
// with a username that does not exist takes as long as with one that does.
func NewService(deps Deps) (*Service, error) {
	dummy, err := deps.Hasher.Hash("not-a-real-password")
	if err != nil {
		return nil, fmt.Errorf("compute dummy hash: %w", err)
	}
	return &Service{deps: deps, attempts: newLoginAttempts(), dummyHash: dummy}, nil
}

// Tokens are what the client receives when logging in or refreshing.
type Tokens struct {
	AccessToken      string
	ExpiresIn        time.Duration
	RefreshToken     string
	RefreshExpiresIn time.Duration
}

// AuthResult is the answer of a login.
type AuthResult struct {
	User   domain.User
	Tokens Tokens
}

// LoginInput is the login form.
type LoginInput struct {
	Username  string
	Password  string
	UserAgent string
	Web       bool
}

// Login checks username and password and opens a session. "No such user",
// "wrong password" and "deactivated" give the same error and take the same time.
func (s *Service) Login(ctx context.Context, in LoginInput) (AuthResult, error) {
	username := domain.NormalizeUsername(in.Username)
	now := s.deps.Clock.Now()
	if wait := s.attempts.wait(username, now); wait > 0 {
		return AuthResult{}, &domain.TooManyAttemptsError{RetryAfter: wait}
	}

	user, err := s.deps.Users.ByUsername(ctx, username)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		_, _ = s.deps.Hasher.Verify(in.Password, s.dummyHash)
		return AuthResult{}, s.loginFailed(ctx, username, now)
	case err != nil:
		return AuthResult{}, err
	}

	ok, err := s.deps.Hasher.Verify(in.Password, user.PasswordHash)
	if err != nil {
		return AuthResult{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok || !user.IsActive {
		return AuthResult{}, s.loginFailed(ctx, username, now)
	}
	s.attempts.succeeded(username)

	var tokens Tokens
	err = s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.deps.Users.TouchLastLogin(ctx, user.ID); err != nil {
			return err
		}
		tokens, err = s.openSession(ctx, user.ID, in.UserAgent, now, in.Web)
		return err
	})
	if err != nil {
		return AuthResult{}, err
	}
	logger.From(ctx).Info("security: login succeeded", "user_id", user.ID)
	return AuthResult{User: user, Tokens: tokens}, nil
}

func (s *Service) loginFailed(ctx context.Context, username string, now time.Time) error {
	s.attempts.failed(username, now)
	logger.From(ctx).Warn("security: login failed")
	return domain.ErrInvalidCredentials
}

// Refresh trades a refresh token for a new one (rotation). Every refresh gives
// a full new lifetime, so a session only dies after REFRESH_TOKEN_TTL_DAYS
// without opening the app. A token used again after the race window means
// someone else has it: the whole session is revoked.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	now := s.deps.Clock.Now()
	var tokens Tokens
	var reused *LockedRefreshToken
	err := s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		stored, err := s.deps.Sessions.LockRefreshToken(ctx, hashToken(refreshToken))
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrSessionExpired
		}
		if err != nil {
			return err
		}
		if stored.SessionRevoked || !stored.UserActive || !now.Before(stored.ExpiresAt) {
			return domain.ErrSessionExpired
		}
		if stored.UsedAt != nil {
			if now.Sub(*stored.UsedAt) < refreshRaceWindow {
				return domain.ErrRefreshRace
			}
			// The revocation is saved (no error) and then answered with a 401.
			reused = &stored
			return s.deps.Sessions.Revoke(ctx, stored.SessionID, revokedReuse)
		}
		if err := s.deps.Sessions.MarkRefreshTokenUsed(ctx, stored.ID, now); err != nil {
			return err
		}
		if err := s.deps.Sessions.Touch(ctx, stored.SessionID); err != nil {
			return err
		}
		tokens, err = s.issueTokens(ctx, stored.UserID, stored.SessionID, now)
		return err
	})
	if err != nil {
		return Tokens{}, err
	}
	if reused != nil {
		logger.From(ctx).Warn("security: refresh token reused, session revoked",
			"user_id", reused.UserID, "session_id", reused.SessionID)
		return Tokens{}, domain.ErrSessionExpired
	}
	return tokens, nil
}

// Logout closes the refresh token's session. It never fails on an unknown
// token: logging out must always work.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	return s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		stored, err := s.deps.Sessions.LockRefreshToken(ctx, hashToken(refreshToken))
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		logger.From(ctx).Info("security: logout", "user_id", stored.UserID)
		return s.deps.Sessions.Revoke(ctx, stored.SessionID, revokedLogout)
	})
}

// Prune clears expired refresh tokens and dead sessions. It runs from a
// background job; nothing depends on it for security.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	return s.deps.Sessions.Prune(ctx, s.deps.Clock.Now(), s.deps.Config.RefreshTokenTTL)
}

// Me returns the account making the request.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (domain.User, error) {
	return s.deps.Users.ByID(ctx, userID)
}

// Authenticate validates an access token and that its session is still alive:
// logging out or deactivating the user cuts access at once, without waiting
// for the token to expire.
func (s *Service) Authenticate(ctx context.Context, accessToken string) (auth.Principal, error) {
	claims, err := s.deps.Tokens.Verify(accessToken, s.deps.Clock.Now())
	if err != nil {
		return auth.Principal{}, auth.ErrUnauthorized
	}
	active, err := s.deps.Sessions.IsActive(ctx, claims.SessionID, claims.UserID)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("check session: %w", err)
	}
	if !active {
		return auth.Principal{}, auth.ErrUnauthorized
	}
	return auth.Principal{UserID: claims.UserID, SessionID: claims.SessionID}, nil
}

func (s *Service) openSession(ctx context.Context, userID uuid.UUID, userAgent string, now time.Time, web bool) (Tokens, error) {
	sessionID := ids.New()
	session := Session{ID: sessionID, UserID: userID, Web: web, UserAgent: truncate(userAgent, userAgentMaxRunes)}
	if err := s.deps.Sessions.Create(ctx, session); err != nil {
		return Tokens{}, err
	}
	return s.issueTokens(ctx, userID, sessionID, now)
}

func (s *Service) issueTokens(ctx context.Context, userID, sessionID uuid.UUID, now time.Time) (Tokens, error) {
	access, err := s.deps.Tokens.Issue(AccessClaims{UserID: userID, SessionID: sessionID}, now)
	if err != nil {
		return Tokens{}, fmt.Errorf("issue access token: %w", err)
	}
	refresh, err := newRefreshToken()
	if err != nil {
		return Tokens{}, err
	}
	ttl := s.deps.Config.RefreshTokenTTL
	err = s.deps.Sessions.CreateRefreshToken(ctx, RefreshToken{
		ID:        ids.New(),
		SessionID: sessionID,
		Hash:      hashToken(refresh),
		ExpiresAt: now.Add(ttl),
	})
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: access, ExpiresIn: s.deps.Config.AccessTokenTTL, RefreshToken: refresh, RefreshExpiresIn: ttl}, nil
}

func newRefreshToken() (string, error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func truncate(text string, maxRunes int) string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	return string([]rune(text)[:maxRunes])
}
