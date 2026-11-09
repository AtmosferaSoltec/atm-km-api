package app

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips"
)

func day(text string) time.Time {
	date, err := time.Parse(time.DateOnly, text)
	if err != nil {
		panic(err)
	}
	return date
}

func km(text string) decimal.Decimal { return decimal.RequireFromString(text) }

func returnedTrip(start, startKm, end, endKm string) trips.Trip {
	return trips.Trip{
		Status: trips.StatusPending, StartDate: day(start), StartKm: km(startKm), CreatedAt: day(start),
		Return: &trips.Return{Date: day(end), EndKm: km(endKm)},
	}
}

// 2026-11-08 is a Sunday: weeks start on 11-01, 11-08.
func TestComputeUsage(t *testing.T) {
	today := day("2026-11-11") // Wednesday
	// The brother takes the car at 5000 and returns it at 5050; the owner
	// drives 25 km and lends it again at 5075, which comes back at 5100.
	history := []trips.Trip{
		returnedTrip("2026-11-03", "5000", "2026-11-05", "5050"),
		returnedTrip("2026-11-09", "5075", "2026-11-10", "5100"),
	}

	usage := ComputeUsage(history, nil, km("5110"), today, 2)

	assert.Len(t, usage.Weeks, 2)
	assert.Equal(t, day("2026-11-08"), usage.Weeks[0].Start)
	assert.Equal(t, "25", usage.Weeks[0].BorrowerKm.String())
	assert.Equal(t, "35", usage.Weeks[0].OwnerKm.String(), "25 counted when the car left again (11-09) + 10 of the stretch still open")
	assert.Equal(t, "50", usage.Weeks[1].BorrowerKm.String())
	assert.Equal(t, "0", usage.Weeks[1].OwnerKm.String())
	assert.Equal(t, "35", usage.OwnerKm.String())
	assert.Equal(t, "75", usage.BorrowerKm.String())
	assert.Equal(t, 32, usage.OwnerSharePct)
}

func TestComputeUsageWhileLentAndOutOfWindow(t *testing.T) {
	today := day("2026-11-11")
	old := returnedTrip("2026-09-01", "100", "2026-09-02", "150")
	open := trips.Trip{Status: trips.StatusInProgress, StartDate: day("2026-11-10"), StartKm: km("200"), CreatedAt: day("2026-11-10")}
	back := returnedTrip("2026-11-02", "160", "2026-11-03", "180")

	usage := ComputeUsage([]trips.Trip{old, back}, &open, km("999"), today, 2)

	assert.Equal(t, "20", usage.BorrowerKm.String(), "the old trip is outside the window")
	assert.Equal(t, "30", usage.OwnerKm.String(), "150 to 160 and 180 to 200; nothing is added while the car is lent")
}

func TestComputeUsageIgnoresNegativeGapsAndEmptyHistory(t *testing.T) {
	today := day("2026-11-11")
	history := []trips.Trip{
		returnedTrip("2026-11-02", "100", "2026-11-03", "150"),
		returnedTrip("2026-11-04", "140", "2026-11-05", "160"),
	}
	usage := ComputeUsage(history, nil, km("160"), today, 1)
	assert.True(t, usage.OwnerKm.IsZero())

	empty := ComputeUsage(nil, nil, km("0"), today, 2)
	assert.Len(t, empty.Weeks, 2)
	assert.Zero(t, empty.OwnerSharePct)
}
