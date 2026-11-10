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

	"github.com/AtmosferaSoltec/atm-km-api/internal/integrations/photostore"
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

// MaxPhotoBytes is the largest receipt photo accepted.
const MaxPhotoBytes = app.MaxPhotoBytes

// PhotoRoute takes a receipt photo's raw bytes; main gives it a larger body limit.
const PhotoRoute = fuelhttp.PhotoRoute

// Deps are the module's dependencies.
type Deps struct {
	Tx             tx.Runner
	Clock          clock.Clock
	Location       *time.Location
	Charged        ChargedProvider
	Files          *photostore.Store
	Signer         photostore.Signer
	PhotoRetention time.Duration
}

// Module wires the module with its adapters.
type Module struct {
	service *app.Service
	handler *fuelhttp.Handler
}

// New creates the module.
func New(deps Deps) *Module {
	service := app.NewService(app.Deps{
		Logs: postgres.NewRepository(deps.Tx), Files: deps.Files, Tx: deps.Tx, Charged: deps.Charged,
		Clock: deps.Clock, Location: deps.Location, PhotoRetention: deps.PhotoRetention,
	})
	return &Module{service: service, handler: fuelhttp.NewHandler(service, deps.Signer, deps.Clock, deps.PhotoRetention)}
}

// Routes mounts the module's routes.
func (m *Module) Routes(public, protected gin.IRoutes) { m.handler.Routes(public, protected) }

// CleanupPhotos deletes receipt photos past their retention; it returns how many.
func (m *Module) CleanupPhotos(ctx context.Context) (int, error) { return m.service.CleanupPhotos(ctx) }

// SpentSince is what was spent on fuel since a date.
func (m *Module) SpentSince(ctx context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error) {
	return m.service.SpentSince(ctx, userID, since)
}

// MaxOdometer is the highest reading any fill-up registered.
func (m *Module) MaxOdometer(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return m.service.MaxOdometer(ctx, userID)
}
