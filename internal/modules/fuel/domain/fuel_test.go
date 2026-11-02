package domain

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func d(text string) decimal.Decimal { return decimal.RequireFromString(text) }

func TestCostPerKm(t *testing.T) {
	assert.Nil(t, CostPerKm(1, d("50"), d("100"), d("100")), "one fill-up is not enough")
	assert.Nil(t, CostPerKm(3, d("50"), d("100"), d("100")), "no km driven")
	cost := CostPerKm(2, d("50"), d("45000"), d("45460"))
	assert.Equal(t, "0.1087", cost.String())
}

func TestValidateAmountAndOdometer(t *testing.T) {
	var problems ValidationError
	ValidateAmount(d("0"), &problems)
	ValidateAmount(d("10001"), &problems)
	ValidateAmount(d("10.005"), &problems)
	ValidateAmount(d("49.90"), &problems)
	ValidateOdometer(d("-5"), &problems)
	ValidateOdometer(d("45000.5"), &problems)
	assert.Equal(t, []FieldError{
		{"amount", CodeNotPositive},
		{"amount", CodeTooLarge},
		{"amount", CodeTooPrecise},
		{"odometer", CodeNegative},
	}, problems.Fields)
}
