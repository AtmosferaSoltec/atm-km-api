// Package dates handles date-only values (a trip's start, a fuel log's day).
// They are kept as midnight UTC in a time.Time, matching Postgres DATE, so no
// hours or DST changes ever leak into a comparison.
package dates

import (
	"fmt"
	"time"
)

// Layout is the wire format of a date: "2026-11-02".
const Layout = time.DateOnly

// Today is the calendar date at instant now in loc, as midnight UTC. The owner
// lives in Peru: at 9 p.m. in Lima it is already tomorrow in UTC.
func Today(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

// Parse reads a "YYYY-MM-DD" date.
func Parse(text string) (time.Time, error) {
	date, err := time.Parse(Layout, text)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q", text)
	}
	return date, nil
}

// Format writes a date as "YYYY-MM-DD".
func Format(date time.Time) string {
	return date.UTC().Format(Layout)
}

// FormatOptional writes a nullable date.
func FormatOptional(date *time.Time) *string {
	if date == nil {
		return nil
	}
	text := Format(*date)
	return &text
}

// MonthStart is the first day of date's month.
func MonthStart(date time.Time) time.Time {
	return time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.UTC)
}
