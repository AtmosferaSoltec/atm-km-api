// Package fuel is the owner's fuel log and how it compares with what the trips
// charged for fuel.
//
// It is the module's public API: other modules and main only use what is here.
package fuel

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	fuelhttp "github.com/AtmosferaSoltec/atm-km-api/internal/modules/fuel/adapters/http"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/fuel/adapters/postgres"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/fuel/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/tx"
)

// MonthAmount is an amount for a calendar month.
type MonthAmount = app.MonthAmount

// ChargedProvider gives the fuel charged on trips per month.
type ChargedProvider = app.ChargedProvider

// Deps are the module's dependencies.
type Deps struct {
	Tx       tx.Runner
	Clock    clock.Clock
	Location *time.Location
	Charged  ChargedProvider
}

// Module wires the module with its adapters.
type Module struct {
	service *app.Service
	handler *fuelhttp.Handler
}

// New creates the module.
func New(deps Deps) *Module {
	service := app.NewService(app.Deps{
		Logs: postgres.NewRepository(deps.Tx), Charged: deps.Charged, Clock: deps.Clock, Location: deps.Location,
	})
	return &Module{service: service, handler: fuelhttp.NewHandler(service)}
}

// Routes mounts the module's routes.
func (m *Module) Routes(protected gin.IRoutes) { m.handler.Routes(protected) }

// SpentSince is what was spent on fuel since a date.
func (m *Module) SpentSince(ctx context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error) {
	return m.service.SpentSince(ctx, userID, since)
}

// MaxOdometer is the highest reading any fill-up registered.
func (m *Module) MaxOdometer(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return m.service.MaxOdometer(ctx, userID)
}
