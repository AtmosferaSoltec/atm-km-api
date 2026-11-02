// Package domain holds the fuel log: what the owner paid to fill the car.
// It is private: the share link never shows it.
package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// FuelType is what was loaded.
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

// Limits: NUMERIC(9,1) for km; no single fill-up costs ten thousand soles.
var (
	maxKm     = decimal.NewFromInt(99_999_999)
	maxAmount = decimal.NewFromInt(10_000)
)

// ErrNotFound: the user has no such log.
var ErrNotFound = errors.New("fuel log not found")

// Log is one fill-up.
type Log struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Date      time.Time
	Odometer  decimal.Decimal
	FuelType  FuelType
	Amount    decimal.Decimal
	IsFull    bool
	CreatedAt time.Time
}

// Field codes the clients know how to show.
const (
	CodeRequired    = "REQUIRED"
	CodeInvalid     = "INVALID"
	CodeNegative    = "NEGATIVE"
	CodeNotPositive = "NOT_POSITIVE"
	CodeTooLarge    = "TOO_LARGE"
	CodeTooPrecise  = "TOO_PRECISE"
	CodeInFuture    = "IN_FUTURE"
)

// FieldError is a field with a problem.
type FieldError struct {
	Field string
	Code  string
}

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

// ValidateOdometer checks a reading: positive, at most one decimal.
func ValidateOdometer(km decimal.Decimal, problems *ValidationError) {
	switch {
	case km.IsNegative():
		problems.Add("odometer", CodeNegative)
	case km.GreaterThan(maxKm):
		problems.Add("odometer", CodeTooLarge)
	case !km.Equal(km.Round(1)):
		problems.Add("odometer", CodeTooPrecise)
	}
}

// ValidateAmount checks what was paid: more than zero, in cents.
func ValidateAmount(amount decimal.Decimal, problems *ValidationError) {
	switch {
	case !amount.IsPositive():
		problems.Add("amount", CodeNotPositive)
	case amount.GreaterThan(maxAmount):
		problems.Add("amount", CodeTooLarge)
	case !amount.Equal(amount.Round(2)):
		problems.Add("amount", CodeTooPrecise)
	}
}

// CostPerKm is the real fuel cost per km, or nil without enough data: at least
// two fill-ups and some km between the first and the last.
func CostPerKm(logs int64, spent, firstOdometer, lastOdometer decimal.Decimal) *decimal.Decimal {
	distance := lastOdometer.Sub(firstOdometer)
	if logs < 2 || !distance.IsPositive() {
		return nil
	}
	cost := spent.Div(distance).Round(4)
	return &cost
}
