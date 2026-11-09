package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func d(text string) decimal.Decimal { return decimal.RequireFromString(text) }

var rates = Rates{Gasoline: d("0.30"), LPG: d("0.10"), Wear: d("0.30")}

func TestBillPerKm(t *testing.T) {
	bill := Bill(d("172"), FuelLPG, rates)
	assert.Equal(t, "0.1", bill.FuelRate.String())
	assert.Equal(t, "17.20", bill.FuelAmount.StringFixed(2))
	assert.Equal(t, "51.60", bill.WearAmount.StringFixed(2))
	assert.Equal(t, "68.80", bill.Total.StringFixed(2))

	gasoline := Bill(d("63"), FuelGasoline, rates)
	assert.Equal(t, "37.80", gasoline.Total.StringFixed(2))
}

func TestBillRoundsEachPartSoTheReceiptAddsUp(t *testing.T) {
	odd := Rates{Gasoline: d("0.333"), LPG: d("0.1"), Wear: d("0.333")}
	bill := Bill(d("10.5"), FuelGasoline, odd)
	assert.Equal(t, "3.50", bill.FuelAmount.StringFixed(2)) // 3.4965
	assert.Equal(t, "3.50", bill.WearAmount.StringFixed(2))
	assert.True(t, bill.Total.Equal(bill.FuelAmount.Add(bill.WearAmount)))
}

func TestValidateReturn(t *testing.T) {
	start := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

	var ok ValidationError
	ValidateReturn(start, d("100"), start, d("100.1"), today, &ok)
	assert.NoError(t, ok.OrNil(), "same day, one more km")

	var problems ValidationError
	ValidateReturn(start, d("100"), start.AddDate(0, 0, -1), d("100"), today, &problems)
	ValidateReturn(start, d("100"), today.AddDate(0, 0, 1), d("150.25"), today, &problems)
	assert.Equal(t, []FieldError{
		{"end_km", CodeNotAfterStart},
		{"return_date", CodeBeforeStart},
		{"end_km", CodeTooPrecise},
		{"return_date", CodeInFuture},
	}, problems.Fields)
}

func TestValidateKm(t *testing.T) {
	var problems ValidationError
	ValidateKm("start_km", d("-1"), &problems)
	ValidateKm("start_km", d("100000000"), &problems)
	ValidateKm("start_km", d("12.5"), &problems)
	assert.Equal(t, []FieldError{{"start_km", CodeNegative}, {"start_km", CodeTooLarge}}, problems.Fields)
}

func TestTripTotals(t *testing.T) {
	trip := Trip{Status: StatusInProgress, StartKm: d("100")}
	assert.Nil(t, trip.DistanceKm())
	assert.True(t, trip.Total().IsZero())

	trip.Status = StatusPending
	trip.Return = &Return{EndKm: d("150"), Billing: Bill(d("50"), FuelGasoline, rates)}
	assert.Equal(t, "50", trip.DistanceKm().String())
	assert.Equal(t, "30.00", trip.Total().StringFixed(2))
}

func TestParsers(t *testing.T) {
	_, ok := ParseStatus("PENDING")
	assert.True(t, ok)
	_, ok = ParseStatus("pending")
	assert.False(t, ok)
	leg, ok := ParseLeg("end")
	assert.True(t, ok)
	assert.Equal(t, LegEnd, leg)
	_, ok = ParseFuelType("DIESEL")
	assert.False(t, ok)
}

func TestValidateVoucherAmountAndBalance(t *testing.T) {
	var problems ValidationError
	ValidateVoucherAmount(decimal.RequireFromString("0"), &problems)
	ValidateVoucherAmount(decimal.RequireFromString("10001"), &problems)
	ValidateVoucherAmount(decimal.RequireFromString("50.005"), &problems)
	ValidateVoucherAmount(decimal.RequireFromString("50.00"), &problems)
	assert.Equal(t, []FieldError{
		{"amount", CodeNotPositive},
		{"amount", CodeTooLarge},
		{"amount", CodeTooPrecise},
	}, problems.Fields)

	// 273 owed, vouchers of 50 + 60 + 70: 93 is left; more vouchers than trips goes negative.
	assert.Equal(t, "93", Balance(decimal.NewFromInt(273), decimal.NewFromInt(180)).String())
	assert.Equal(t, "-10", Balance(decimal.NewFromInt(30), decimal.NewFromInt(40)).String())
}
