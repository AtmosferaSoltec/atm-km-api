// Package app holds the trips use cases and the ports they need. It knows
// nothing about Gin, Postgres or the disk.
package app

import (
	"context"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/domain"
)

// Repository stores trips. Every method is scoped to the user.
type Repository interface {
	// Insert returns domain.ErrAlreadyLent if the user already has a trip in progress.
	Insert(ctx context.Context, trip domain.Trip) error
	// Get and Lock return domain.ErrNotFound if the user has no such trip.
	Get(ctx context.Context, userID, id uuid.UUID) (domain.Trip, error)
	Lock(ctx context.Context, userID, id uuid.UUID) (domain.Trip, error)
	// Open returns nil if the car is home.
	Open(ctx context.Context, userID uuid.UUID) (*domain.Trip, error)
	List(ctx context.Context, userID uuid.UUID, status *domain.Status, limit, offset int) ([]domain.Trip, int64, error)
	ByStatus(ctx context.Context, userID uuid.UUID, statuses []domain.Status, limit int) ([]domain.Trip, error)
	SaveReturn(ctx context.Context, userID, id uuid.UUID, ret domain.Return) error
	SetStatus(ctx context.Context, userID, id uuid.UUID, status domain.Status, paidDate *time.Time) error
	// Pay marks the given pending trips paid and returns which ones it marked.
	Pay(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, paidDate time.Time) ([]uuid.UUID, error)
	Delete(ctx context.Context, userID, id uuid.UUID) error
	BillableSince(ctx context.Context, userID uuid.UUID, since time.Time) (Totals, error)
	FuelChargedByMonth(ctx context.Context, userID uuid.UUID, since time.Time) ([]MonthAmount, error)
	MaxOdometer(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	LastFuelType(ctx context.Context, userID uuid.UUID) (*domain.FuelType, error)
}

// Totals add up billable trips.
type Totals struct {
	DistanceKm decimal.Decimal
	Charged    decimal.Decimal
	Trips      int64
}

// MonthAmount is an amount for a calendar month (its first day).
type MonthAmount struct {
	Month  time.Time
	Amount decimal.Decimal
}

// Photos stores the photos' metadata.
type Photos interface {
	OfTrips(ctx context.Context, tripIDs []uuid.UUID) (map[uuid.UUID]map[domain.Leg]domain.Photo, error)
	// Get and OfLeg return domain.ErrNoPhoto if there is none.
	Get(ctx context.Context, id uuid.UUID) (domain.Photo, error)
	OfLeg(ctx context.Context, tripID uuid.UUID, leg domain.Leg) (domain.Photo, error)
	Insert(ctx context.Context, photo domain.Photo) error
	Delete(ctx context.Context, id uuid.UUID) error
	Expired(ctx context.Context, before time.Time, limit int) ([]domain.Photo, error)
}

// Files keeps the photo files (integrations/photostore).
type Files interface {
	Save(key string, data []byte) error
	Open(key string) (*os.File, error)
	Delete(key string) error
	SweepOlderThan(cutoff time.Time) (int, error)
}

// RatesProvider gives the user's current rates (the settings module).
type RatesProvider interface {
	Rates(ctx context.Context, userID uuid.UUID) (domain.Rates, error)
}

// Transactor opens a transaction (platform/tx.Runner).
type Transactor interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}
