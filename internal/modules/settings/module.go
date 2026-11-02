// Package settings is each owner's car, borrower, per-km rates and share link.
//
// It is the module's public API: other modules and main only use what is here.
package settings

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	settingshttp "github.com/AtmosferaSoltec/atm-km-api/internal/modules/settings/adapters/http"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/settings/adapters/postgres"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/settings/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/settings/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/tx"
)

// Settings are one owner's.
type Settings = domain.Settings

// Rates are soles per km.
type Rates = domain.Rates

// ErrNotFound: no settings carry that share token.
var ErrNotFound = domain.ErrNotFound

// Module wires the module with its adapters.
type Module struct {
	service *app.Service
	handler *settingshttp.Handler
}

// New creates the module.
func New(db tx.Runner) *Module {
	service := app.NewService(postgres.NewRepository(db))
	return &Module{service: service, handler: settingshttp.NewHandler(service)}
}

// Routes mounts the module's routes.
func (m *Module) Routes(protected gin.IRoutes) { m.handler.Routes(protected) }

// Get returns the user's settings, creating the defaults on first use.
func (m *Module) Get(ctx context.Context, userID uuid.UUID) (Settings, error) {
	return m.service.Get(ctx, userID)
}

// Rates returns the user's current rates.
func (m *Module) Rates(ctx context.Context, userID uuid.UUID) (Rates, error) {
	settings, err := m.service.Get(ctx, userID)
	return settings.Rates, err
}

// ByShareToken finds whose share link it is; ErrNotFound if nobody's.
func (m *Module) ByShareToken(ctx context.Context, token uuid.UUID) (Settings, error) {
	return m.service.ByShareToken(ctx, token)
}
