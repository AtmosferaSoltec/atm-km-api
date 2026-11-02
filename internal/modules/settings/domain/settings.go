// Package domain holds each owner's settings: the car, who borrows it, the
// per-km rates and the share link.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Limits. A rate above 100 soles per km is a typo, not a price.
const (
	CarNameMaxLength      = 60
	BorrowerNameMaxLength = 40
	rateScale             = 3
)

var maxRate = decimal.NewFromInt(100)

// Field codes the clients know how to show.
const (
	CodeRequired   = "REQUIRED"
	CodeTooLong    = "TOO_LONG"
	CodeInvalid    = "INVALID"
	CodeNegative   = "NEGATIVE"
	CodeTooLarge   = "TOO_LARGE"
	CodeTooPrecise = "TOO_PRECISE"
)

// ErrNotFound: no settings for that share token.
var ErrNotFound = errors.New("not found")

// Rates are soles per km. A trip adds the fuel rate of the fuel it came back
// with and the wear rate.
type Rates struct {
	Gasoline decimal.Decimal
	LPG      decimal.Decimal
	Wear     decimal.Decimal
}

// Settings are one owner's.
type Settings struct {
	UserID       uuid.UUID
	CarName      string
	BorrowerName string
	Rates        Rates
	ShareToken   uuid.UUID
}

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

// ParseRate reads a rate sent as text ("0.30").
func ParseRate(field, text string, problems *ValidationError) decimal.Decimal {
	text = strings.TrimSpace(text)
	if text == "" {
		problems.Add(field, CodeRequired)
		return decimal.Zero
	}
	rate, err := decimal.NewFromString(text)
	switch {
	case err != nil:
		problems.Add(field, CodeInvalid)
	case rate.IsNegative():
		problems.Add(field, CodeNegative)
	case rate.GreaterThan(maxRate):
		problems.Add(field, CodeTooLarge)
	case rate.Exponent() < -rateScale && !rate.Equal(rate.Round(rateScale)):
		problems.Add(field, CodeTooPrecise)
	}
	return rate
}

// NormalizeName collapses inner spaces and trims.
func NormalizeName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// ValidateName checks a required name of at most maxLength characters.
func ValidateName(field, name string, maxLength int, problems *ValidationError) {
	switch {
	case name == "":
		problems.Add(field, CodeRequired)
	case utf8.RuneCountInString(name) > maxLength:
		problems.Add(field, CodeTooLong)
	}
}
