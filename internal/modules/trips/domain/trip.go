// Package domain holds the rules of lending the car: the trip's life cycle
// (lent, returned, paid, cancelled) and how a returned trip is billed per km.
package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Status is where a trip is in its life cycle.
type Status string

// Trip statuses.
const (
	StatusInProgress Status = "IN_PROGRESS"
	StatusPending    Status = "PENDING"
	StatusPaid       Status = "PAID"
	StatusCancelled  Status = "CANCELLED"
)

// ParseStatus reads a status filter.
func ParseStatus(text string) (Status, bool) {
	switch status := Status(text); status {
	case StatusInProgress, StatusPending, StatusPaid, StatusCancelled:
		return status, true
	}
	return "", false
}

// Billable tells whether the trip has an amount (returned, paid or not).
func (s Status) Billable() bool { return s == StatusPending || s == StatusPaid }

// FuelType is what the car ran on while lent.
type FuelType string

// Fuel types.
const (
	FuelGasoline FuelType = "GASOLINE"
	FuelLPG      FuelType = "LPG"
)

// ParseFuelType reads a fuel type.
func ParseFuelType(text string) (FuelType, bool) {
	switch fuel := FuelType(text); fuel {
	case FuelGasoline, FuelLPG:
		return fuel, true
	}
	return "", false
}

// Leg is one end of the trip: when the car left or when it came back.
type Leg string

// Legs.
const (
	LegStart Leg = "START"
	LegEnd   Leg = "END"
)

// ParseLeg reads a leg from the URL ("start" or "end").
func ParseLeg(text string) (Leg, bool) {
	switch text {
	case "start":
		return LegStart, true
	case "end":
		return LegEnd, true
	}
	return "", false
}

// Odometer limits: NUMERIC(9,1).
var maxKm = decimal.NewFromInt(99_999_999)

// Errors of the trip's life cycle.
var (
	ErrNotFound = errors.New("trip not found")
	// ErrAlreadyLent: the car is already out; it cannot be lent twice.
	ErrAlreadyLent = errors.New("car already lent")
	// ErrWrongStatus: the action does not apply to the trip's current status.
	ErrWrongStatus = errors.New("action not allowed in the trip's status")
	// ErrNoPhoto: the trip has no photo for that leg.
	ErrNoPhoto = errors.New("photo not found")
	// ErrPhotoLegClosed: the end photo needs the car to be back.
	ErrPhotoLegClosed = errors.New("end photo needs a returned trip")
)

// Rates are the user's per-km rates when the trip comes back.
type Rates struct {
	Gasoline decimal.Decimal
	LPG      decimal.Decimal
	Wear     decimal.Decimal
}

// Billing is what a returned trip charges. Rates are frozen on the trip, so
// editing them later never changes an amount already registered.
type Billing struct {
	FuelRate   decimal.Decimal
	WearRate   decimal.Decimal
	FuelAmount decimal.Decimal
	WearAmount decimal.Decimal
	Total      decimal.Decimal
}

// Bill charges distance km: each part is rounded to cents and the total is
// their sum, so the receipt always adds up.
func Bill(distance decimal.Decimal, fuel FuelType, rates Rates) Billing {
	fuelRate := rates.Gasoline
	if fuel == FuelLPG {
		fuelRate = rates.LPG
	}
	fuelAmount := distance.Mul(fuelRate).Round(2)
	wearAmount := distance.Mul(rates.Wear).Round(2)
	return Billing{
		FuelRate:   fuelRate,
		WearRate:   rates.Wear,
		FuelAmount: fuelAmount,
		WearAmount: wearAmount,
		Total:      fuelAmount.Add(wearAmount),
	}
}

// Return is the car coming back.
type Return struct {
	Date     time.Time
	EndKm    decimal.Decimal
	FuelType FuelType
	Billing  Billing
}

// Photo is an odometer photo's metadata; the file lives in the photo store.
type Photo struct {
	ID          uuid.UUID
	TripID      uuid.UUID
	UserID      uuid.UUID
	Leg         Leg
	StorageKey  string
	ContentType string
	SizeBytes   int64
	CreatedAt   time.Time
}

// Trip is one time the car was lent.
type Trip struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Status    Status
	StartDate time.Time
	StartKm   decimal.Decimal
	Return    *Return
	PaidDate  *time.Time
	CreatedAt time.Time
	Photos    map[Leg]Photo
}

// DistanceKm is how far the car went; nil while it is out.
func (t Trip) DistanceKm() *decimal.Decimal {
	if t.Return == nil {
		return nil
	}
	distance := t.Return.EndKm.Sub(t.StartKm)
	return &distance
}

// Total is what the trip charges; zero while it has no amount.
func (t Trip) Total() decimal.Decimal {
	if t.Return == nil || !t.Status.Billable() {
		return decimal.Zero
	}
	return t.Return.Billing.Total
}

// FieldError is a field with a problem.
type FieldError struct {
	Field string
	Code  string
}

// Field codes the clients know how to show.
const (
	CodeRequired      = "REQUIRED"
	CodeInvalid       = "INVALID"
	CodeNegative      = "NEGATIVE"
	CodeTooLarge      = "TOO_LARGE"
	CodeInFuture      = "IN_FUTURE"
	CodeBeforeStart   = "BEFORE_START"
	CodeNotAfterStart = "NOT_AFTER_START"
	CodeTooPrecise    = "TOO_PRECISE"
	CodeTooMany       = "TOO_MANY"
	CodeUnsupported   = "UNSUPPORTED_TYPE"
	CodeEmpty         = "EMPTY"
)

// ValidationError gathers a form's problems.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string { return fmt.Sprintf("validation failed: %v", e.Fields) }

// Add records a problem.
func (e *ValidationError) Add(field, code string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Code: code})
}

// OrNil returns the error only if there are problems.
func (e *ValidationError) OrNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

// ValidateKm checks an odometer reading: positive, at most one decimal.
func ValidateKm(field string, km decimal.Decimal, problems *ValidationError) {
	switch {
	case km.IsNegative():
		problems.Add(field, CodeNegative)
	case km.GreaterThan(maxKm):
		problems.Add(field, CodeTooLarge)
	case !km.Equal(km.Round(1)):
		problems.Add(field, CodeTooPrecise)
	}
}

// ValidateDate checks a date is not after today.
func ValidateDate(field string, date, today time.Time, problems *ValidationError) {
	if date.After(today) {
		problems.Add(field, CodeInFuture)
	}
}

// ValidateReturn checks the car came back after it left.
func ValidateReturn(startDate time.Time, startKm decimal.Decimal, returnDate time.Time, endKm decimal.Decimal, today time.Time, problems *ValidationError) {
	ValidateKm("end_km", endKm, problems)
	if !endKm.GreaterThan(startKm) {
		problems.Add("end_km", CodeNotAfterStart)
	}
	ValidateDate("return_date", returnDate, today, problems)
	if returnDate.Before(startDate) {
		problems.Add("return_date", CodeBeforeStart)
	}
}
