// Package money keeps amounts as decimals end to end: they are stored as
// NUMERIC, computed with shopspring/decimal and travel as text in JSON
// ("51.60"), never through float64.
package money

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Scale is the number of decimals of an amount in soles.
const Scale = 2

// Format renders an amount with exactly two decimals, as the API sends it.
func Format(amount decimal.Decimal) string {
	return amount.StringFixed(Scale)
}

// FormatOptional renders a nullable amount.
func FormatOptional(amount *decimal.Decimal) *string {
	if amount == nil {
		return nil
	}
	text := Format(*amount)
	return &text
}

// Round rounds half away from zero to cents, the way a person charges.
func Round(amount decimal.Decimal) decimal.Decimal {
	return amount.Round(Scale)
}

// Parse reads an amount sent by a client. It rejects anything that is not a
// plain decimal number.
func Parse(text string) (decimal.Decimal, error) {
	amount, err := decimal.NewFromString(text)
	if err != nil {
		return decimal.Zero, fmt.Errorf("invalid amount %q", text)
	}
	return amount, nil
}
