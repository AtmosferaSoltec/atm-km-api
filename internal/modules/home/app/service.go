// Package app builds the home screen in one round trip: whether the car is
// out, what is owed, the last 30 days and the latest trips.
package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/dates"
)

// Window and list sizes.
const (
	// RecentDays: "the last 30 days" instead of "this month", which reads zero
	// early in the month.
	RecentDays  = 30
	recentTrips = 4
	maxPending  = 200
)

// Trips is what home reads from the trips module.
type Trips interface {
	Open(ctx context.Context, userID uuid.UUID) (*trips.Trip, error)
	ByStatus(ctx context.Context, userID uuid.UUID, statuses []trips.Status, limit int) ([]trips.Trip, error)
	BillableSince(ctx context.Context, userID uuid.UUID, since time.Time) (trips.Totals, error)
	MaxOdometer(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	LastFuelType(ctx context.Context, userID uuid.UUID) (*trips.FuelType, error)
}

// Fuel is what home reads from the fuel module.
type Fuel interface {
	SpentSince(ctx context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error)
	MaxOdometer(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// Home is the home screen.
type Home struct {
	OpenTrip     *trips.Trip
	Pending      []trips.Trip
	PendingTotal decimal.Decimal
	Since        time.Time
	Recent       trips.Totals
	FuelSpent    decimal.Decimal
	RecentTrips  []trips.Trip
	LastOdometer decimal.Decimal
	LastFuelType *trips.FuelType
}

// Service builds the home screen.
type Service struct {
	trips    Trips
	fuel     Fuel
	clock    clock.Clock
	location *time.Location
}

// NewService creates the service.
func NewService(t Trips, f Fuel, c clock.Clock, loc *time.Location) *Service {
	return &Service{trips: t, fuel: f, clock: c, location: loc}
}

// Get builds the home screen for the user.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Home, error) {
	since := dates.Today(s.clock.Now(), s.location).AddDate(0, 0, -(RecentDays - 1))
	home := Home{Since: since}
	var err error
	if home.OpenTrip, err = s.trips.Open(ctx, userID); err != nil {
		return Home{}, err
	}
	if home.Pending, err = s.trips.ByStatus(ctx, userID, []trips.Status{trips.StatusPending}, maxPending); err != nil {
		return Home{}, err
	}
	for _, trip := range home.Pending {
		home.PendingTotal = home.PendingTotal.Add(trip.Total())
	}
	if home.Recent, err = s.trips.BillableSince(ctx, userID, since); err != nil {
		return Home{}, err
	}
	if home.FuelSpent, err = s.fuel.SpentSince(ctx, userID, since); err != nil {
		return Home{}, err
	}
	closed := []trips.Status{trips.StatusPending, trips.StatusPaid, trips.StatusCancelled}
	if home.RecentTrips, err = s.trips.ByStatus(ctx, userID, closed, recentTrips); err != nil {
		return Home{}, err
	}
	tripKm, err := s.trips.MaxOdometer(ctx, userID)
	if err != nil {
		return Home{}, err
	}
	fuelKm, err := s.fuel.MaxOdometer(ctx, userID)
	if err != nil {
		return Home{}, err
	}
	home.LastOdometer = decimal.Max(tripKm, fuelKm)
	if home.LastFuelType, err = s.trips.LastFuelType(ctx, userID); err != nil {
		return Home{}, err
	}
	return home, nil
}
