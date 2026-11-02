// Package trips is lending the car: the trip's life cycle, per-km billing and
// the odometer photos, which are deleted after PHOTO_RETENTION_DAYS.
//
// It is the module's public API: other modules and main only use what is here.
package trips

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/integrations/photostore"
	tripshttp "github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/adapters/http"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/adapters/postgres"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/tx"
)

// Public types for the modules that show trips (home, share).
type (
	Trip         = domain.Trip
	Status       = domain.Status
	FuelType     = domain.FuelType
	Rates        = domain.Rates
	Totals       = app.Totals
	MonthAmount  = app.MonthAmount
	TripResponse = tripshttp.TripResponse
)

// Statuses.
const (
	StatusInProgress = domain.StatusInProgress
	StatusPending    = domain.StatusPending
	StatusPaid       = domain.StatusPaid
	StatusCancelled  = domain.StatusCancelled
)

// MaxPhotoBytes is the largest photo accepted.
const MaxPhotoBytes = app.MaxPhotoBytes

// PhotoUploadRoute takes a photo's raw bytes; main gives it a larger body limit.
const PhotoUploadRoute = tripshttp.PhotoUploadRoute

// RatesFunc adapts a function (the settings module) to the rates port.
type RatesFunc func(ctx context.Context, userID uuid.UUID) (Rates, error)

// Rates calls f.
func (f RatesFunc) Rates(ctx context.Context, userID uuid.UUID) (Rates, error) { return f(ctx, userID) }

// Deps are the module's dependencies.
type Deps struct {
	Tx             tx.Runner
	Clock          clock.Clock
	Location       *time.Location
	Rates          RatesFunc
	Files          *photostore.Store
	Signer         photostore.Signer
	PhotoRetention time.Duration
}

// Module wires the module with its adapters.
type Module struct {
	service   *app.Service
	handler   *tripshttp.Handler
	presenter tripshttp.Presenter
}

// New creates the module.
func New(deps Deps) *Module {
	service := app.NewService(app.Deps{
		Trips:          postgres.NewTrips(deps.Tx),
		Photos:         postgres.NewPhotos(deps.Tx),
		Files:          deps.Files,
		Rates:          deps.Rates,
		Tx:             deps.Tx,
		Clock:          deps.Clock,
		Location:       deps.Location,
		PhotoRetention: deps.PhotoRetention,
	})
	presenter := tripshttp.Presenter{Signer: deps.Signer, Clock: deps.Clock, Retention: deps.PhotoRetention}
	return &Module{service: service, handler: tripshttp.NewHandler(service, presenter), presenter: presenter}
}

// Routes mounts the module's routes.
func (m *Module) Routes(public, protected gin.IRoutes) { m.handler.Routes(public, protected) }

// Present renders trips as the API sends them, with signed photo URLs.
func (m *Module) Present(trips []Trip) []TripResponse { return m.presenter.Trips(trips) }

// PresentOne renders one trip.
func (m *Module) PresentOne(trip Trip) TripResponse { return m.presenter.Trip(trip) }

// Open returns the trip in progress, or nil if the car is home.
func (m *Module) Open(ctx context.Context, userID uuid.UUID) (*Trip, error) {
	return m.service.Open(ctx, userID)
}

// ByStatus returns the newest trips with any of the statuses.
func (m *Module) ByStatus(ctx context.Context, userID uuid.UUID, statuses []Status, limit int) ([]Trip, error) {
	return m.service.ByStatus(ctx, userID, statuses, limit)
}

// BillableSince adds up the billable trips that started since a date.
func (m *Module) BillableSince(ctx context.Context, userID uuid.UUID, since time.Time) (Totals, error) {
	return m.service.BillableSince(ctx, userID, since)
}

// FuelChargedByMonth is the fuel charged per month since a date.
func (m *Module) FuelChargedByMonth(ctx context.Context, userID uuid.UUID, since time.Time) ([]MonthAmount, error) {
	return m.service.FuelChargedByMonth(ctx, userID, since)
}

// MaxOdometer is the highest reading any trip registered.
func (m *Module) MaxOdometer(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return m.service.MaxOdometer(ctx, userID)
}

// LastFuelType is what the car came back with last time.
func (m *Module) LastFuelType(ctx context.Context, userID uuid.UUID) (*FuelType, error) {
	return m.service.LastFuelType(ctx, userID)
}

// CleanupPhotos deletes photos past their retention; it returns how many.
func (m *Module) CleanupPhotos(ctx context.Context) (int, error) {
	result, err := m.service.CleanupPhotos(ctx)
	return result.Photos + result.Orphan, err
}
