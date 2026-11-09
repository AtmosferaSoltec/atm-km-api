package app

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/dates"
)

// Usage window.
const (
	DefaultUsageWeeks = 8
	MaxUsageWeeks     = 52
	// maxUsageTrips bounds what one usage request reads; a person lends the car
	// a few times a week.
	maxUsageTrips = 1000
)

// WeekUsage is how far the car went in a week (Sunday to Saturday), by whom.
type WeekUsage struct {
	Start      time.Time
	OwnerKm    decimal.Decimal
	BorrowerKm decimal.Decimal
}

// Usage compares the owner's km with the borrower's, week by week, newest first.
type Usage struct {
	Weeks         []WeekUsage
	OwnerKm       decimal.Decimal
	BorrowerKm    decimal.Decimal
	OwnerSharePct int
}

// Usage splits the car's km between the borrower (what the trips billed) and
// the owner (what the odometer moved while the car was home).
func (s *Service) Usage(ctx context.Context, userID uuid.UUID, weeks int) (Usage, error) {
	weeks = min(max(weeks, 1), MaxUsageWeeks)
	returned, err := s.trips.ByStatus(ctx, userID, []trips.Status{trips.StatusPending, trips.StatusPaid}, maxUsageTrips)
	if err != nil {
		return Usage{}, err
	}
	open, err := s.trips.Open(ctx, userID)
	if err != nil {
		return Usage{}, err
	}
	odometer, err := s.lastOdometer(ctx, userID)
	if err != nil {
		return Usage{}, err
	}
	today := dates.Today(s.clock.Now(), s.location)
	return ComputeUsage(returned, open, odometer, today, weeks), nil
}

// ComputeUsage is the usage calculation without any I/O.
//
// The borrower's km are each returned trip's distance, counted on the day it
// came back. The owner's km are the odometer's movement between a return and
// the next lending, counted on the day the car left again; while the car is
// home, the stretch since the last return runs up to the latest odometer
// reading and counts today. Gaps that make no sense (a negative one) are ignored.
func ComputeUsage(returned []trips.Trip, open *trips.Trip, odometer decimal.Decimal, today time.Time, weeks int) Usage {
	chrono := append([]trips.Trip(nil), returned...)
	if open != nil {
		chrono = append(chrono, *open)
	}
	sort.SliceStable(chrono, func(i, j int) bool {
		if !chrono[i].StartDate.Equal(chrono[j].StartDate) {
			return chrono[i].StartDate.Before(chrono[j].StartDate)
		}
		return chrono[i].CreatedAt.Before(chrono[j].CreatedAt)
	})

	first := weekStart(today).AddDate(0, 0, -7*(weeks-1))
	byWeek := map[time.Time]*WeekUsage{}
	add := func(date time.Time, owner, borrower decimal.Decimal) {
		week := weekStart(date)
		if week.Before(first) || !owner.IsPositive() && !borrower.IsPositive() {
			return
		}
		if byWeek[week] == nil {
			byWeek[week] = &WeekUsage{Start: week}
		}
		byWeek[week].OwnerKm = byWeek[week].OwnerKm.Add(owner)
		byWeek[week].BorrowerKm = byWeek[week].BorrowerKm.Add(borrower)
	}

	for i, trip := range chrono {
		if trip.Return != nil {
			add(trip.Return.Date, decimal.Zero, trip.Return.EndKm.Sub(trip.StartKm))
		}
		if i == 0 || chrono[i-1].Return == nil {
			continue
		}
		add(trip.StartDate, trip.StartKm.Sub(chrono[i-1].Return.EndKm), decimal.Zero)
	}
	if n := len(chrono); n > 0 && open == nil && chrono[n-1].Return != nil {
		add(today, odometer.Sub(chrono[n-1].Return.EndKm), decimal.Zero)
	}

	usage := Usage{Weeks: []WeekUsage{}}
	for week := weekStart(today); !week.Before(first); week = week.AddDate(0, 0, -7) {
		entry := WeekUsage{Start: week}
		if found := byWeek[week]; found != nil {
			entry = *found
		}
		usage.Weeks = append(usage.Weeks, entry)
		usage.OwnerKm = usage.OwnerKm.Add(entry.OwnerKm)
		usage.BorrowerKm = usage.BorrowerKm.Add(entry.BorrowerKm)
	}
	if total := usage.OwnerKm.Add(usage.BorrowerKm); total.IsPositive() {
		usage.OwnerSharePct = int(usage.OwnerKm.Mul(decimal.NewFromInt(100)).Div(total).Round(0).IntPart())
	}
	return usage
}

// weekStart is the Sunday on or before date, the week the app groups trips by.
func weekStart(date time.Time) time.Time {
	return date.AddDate(0, 0, -int(date.Weekday()))
}

func (s *Service) lastOdometer(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	tripKm, err := s.trips.MaxOdometer(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}
	fuelKm, err := s.fuel.MaxOdometer(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}
	return decimal.Max(tripKm, fuelKm), nil
}
