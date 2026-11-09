// Package server wires the modules into one Gin router. main uses it to serve
// and the end-to-end tests use it to run the real API against km_test.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AtmosferaSoltec/atm-km-api/internal/integrations/photostore"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/fuel"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/home"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/settings"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/share"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/config"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/health"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/httpserver"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/middleware"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/tx"
)

// Options tune what tests need to change.
type Options struct {
	Clock clock.Clock
	// PasswordParams lowers argon2's cost in tests; nil uses production's.
	PasswordParams *identity.PasswordParams
}

// App is the wired API.
type App struct {
	Router   *gin.Engine
	Identity *identity.Module
	Trips    *trips.Module
}

// Build wires every module on pool.
func Build(cfg config.Config, pool *pgxpool.Pool, log *slog.Logger, opts Options) (*App, error) {
	if opts.Clock == nil {
		opts.Clock = clock.System{}
	}
	router, api, err := httpserver.NewRouter(httpserver.Options{
		Production:         cfg.IsProduction(),
		Logger:             log,
		CORSOrigins:        cfg.CORSOrigins,
		RateLimitPerMinute: cfg.RateLimitPerMinute,
		// A photo travels as raw bytes, with a little room over its own limit so
		// the use case (not the reader) explains what is wrong.
		LargeBodyRoutes: map[string]int64{
			trips.PhotoUploadRoute:  trips.MaxPhotoBytes + 1<<10,
			trips.VoucherPhotoRoute: trips.MaxPhotoBytes + 1<<10,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("router: %w", err)
	}
	health.Register(api, pool)

	runner := tx.NewRunner(pool)
	identityModule, err := identity.New(identity.Deps{
		Tx:              runner,
		Clock:           opts.Clock,
		JWTSecret:       cfg.JWTAccessSecret,
		AccessTokenTTL:  cfg.AccessTokenTTL,
		RefreshTokenTTL: cfg.RefreshTokenTTL,
		PasswordParams:  opts.PasswordParams,
		SecureCookies:   cfg.Env == config.Production,
	})
	if err != nil {
		return nil, err
	}

	files, err := photostore.New(cfg.Photos.Dir)
	if err != nil {
		return nil, err
	}
	settingsModule := settings.New(runner)
	tripsModule := trips.New(trips.Deps{
		Tx:       runner,
		Clock:    opts.Clock,
		Location: cfg.Location,
		Rates: func(ctx context.Context, userID uuid.UUID) (trips.Rates, error) {
			rates, err := settingsModule.Rates(ctx, userID)
			return trips.Rates{Gasoline: rates.Gasoline, LPG: rates.LPG, Wear: rates.Wear}, err
		},
		Files:          files,
		Signer:         photostore.NewSigner(cfg.JWTAccessSecret),
		PhotoRetention: cfg.Photos.Retention,
	})
	fuelModule := fuel.New(fuel.Deps{
		Tx:       runner,
		Clock:    opts.Clock,
		Location: cfg.Location,
		Charged: func(ctx context.Context, userID uuid.UUID, since time.Time) ([]fuel.MonthAmount, error) {
			months, err := tripsModule.FuelChargedByMonth(ctx, userID, since)
			out := make([]fuel.MonthAmount, len(months))
			for i, month := range months {
				out[i] = fuel.MonthAmount{Month: month.Month, Amount: month.Amount}
			}
			return out, err
		},
	})
	homeModule := home.New(tripsModule, fuelModule, opts.Clock, cfg.Location)
	shareModule := share.New(settingsModule, identityModule, tripsModule)

	// Login gets a narrower per-IP limit.
	limited := api.Group("", middleware.NewRateLimiter(cfg.LoginRateLimitPerMinute).Middleware())
	protected := api.Group("", auth.Require(identityModule.Authenticator()))
	identityModule.Routes(api, limited, protected)
	settingsModule.Routes(protected)
	tripsModule.Routes(api, protected)
	fuelModule.Routes(protected)
	homeModule.Routes(protected)
	shareModule.Routes(api)

	return &App{Router: router, Identity: identityModule, Trips: tripsModule}, nil
}

// Housekeeping deletes photos past their retention and dead sessions: shortly
// after start, then every interval, until ctx ends.
func (a *App) Housekeeping(ctx context.Context, log *slog.Logger, interval time.Duration) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if _, err := a.Trips.CleanupPhotos(ctx); err != nil {
			log.Error("photo cleanup failed", "error", err)
		}
		if _, err := a.Identity.Prune(ctx); err != nil {
			log.Error("session prune failed", "error", err)
		}
		timer.Reset(interval)
	}
}
