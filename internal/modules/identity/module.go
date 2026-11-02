// Package identity is user accounts and their sessions: login, refresh,
// logout, authenticating each request, and the account management the owner
// runs from the server's command line.
//
// It is the module's public API: other modules and main only use what is here.
package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	identityhttp "github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/adapters/http"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/adapters/postgres"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/adapters/security"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/tx"
)

// User is an account.
type User = domain.User

// CreateUserInput is a new account.
type CreateUserInput = app.CreateUserInput

// ValidationError lists a form's problems.
type ValidationError = domain.ValidationError

// Errors the command line reports.
var (
	ErrUsernameTaken = domain.ErrUsernameTaken
	ErrNotFound      = domain.ErrNotFound
)

// PasswordParams are the argon2id parameters.
type PasswordParams = security.Argon2Params

// FastPasswordParams lower argon2id's cost: tests only, which measure the logic
// and not the hash's strength.
var FastPasswordParams = PasswordParams{MemoryKiB: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

// Deps are the module's dependencies.
type Deps struct {
	Tx              tx.Runner
	Clock           clock.Clock
	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	// PasswordParams lowers argon2's cost in tests; nil uses production's.
	PasswordParams *PasswordParams
	// SecureCookies marks the web's session cookie Secure (all but development).
	SecureCookies bool
}

// Module wires the module with its adapters.
type Module struct {
	service *app.Service
	handler *identityhttp.Handler
}

// New creates the module.
func New(deps Deps) (*Module, error) {
	params := security.DefaultArgon2Params
	if deps.PasswordParams != nil {
		params = *deps.PasswordParams
	}
	service, err := app.NewService(app.Deps{
		Users:    postgres.NewUsers(deps.Tx),
		Sessions: postgres.NewSessions(deps.Tx),
		Hasher:   security.Argon2Hasher{Params: params},
		Tokens:   security.JWTTokens{Secret: []byte(deps.JWTSecret), TTL: deps.AccessTokenTTL},
		Tx:       deps.Tx,
		Clock:    deps.Clock,
		Config:   app.Config{AccessTokenTTL: deps.AccessTokenTTL, RefreshTokenTTL: deps.RefreshTokenTTL},
	})
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	cookie := identityhttp.CookieOptions{Secure: deps.SecureCookies, MaxAge: deps.RefreshTokenTTL}
	return &Module{service: service, handler: identityhttp.NewHandler(service, cookie)}, nil
}

// Routes mounts the module's routes.
func (m *Module) Routes(public, limited, protected gin.IRoutes) {
	m.handler.Routes(public, limited, protected)
}

// Authenticator validates access tokens for the auth.Require middleware.
func (m *Module) Authenticator() auth.Authenticator { return m.service }

// ActiveDisplayName returns the account's name, for the share page. A
// deactivated account answers ErrNotFound: its link stops working too.
func (m *Module) ActiveDisplayName(ctx context.Context, userID uuid.UUID) (string, error) {
	user, err := m.service.Me(ctx, userID)
	if err != nil {
		return "", err
	}
	if !user.IsActive {
		return "", ErrNotFound
	}
	return user.DisplayName, nil
}

// CreateUser creates an account (command line only).
func (m *Module) CreateUser(ctx context.Context, in CreateUserInput) (User, error) {
	return m.service.CreateUser(ctx, in)
}

// ChangePassword sets a new password and closes every session (command line only).
func (m *Module) ChangePassword(ctx context.Context, username, password string) error {
	return m.service.ChangePassword(ctx, username, password)
}

// SetActive turns an account on or off (command line only).
func (m *Module) SetActive(ctx context.Context, username string, active bool) error {
	return m.service.SetActive(ctx, username, active)
}

// Prune clears expired refresh tokens and dead sessions.
func (m *Module) Prune(ctx context.Context) (int64, error) { return m.service.Prune(ctx) }
