// Package clock puts the current time behind an interface so tests can pin
// "now" (and therefore "today") without depending on the system clock.
package clock

import "time"

// Clock returns the current instant.
type Clock interface {
	Now() time.Time
}

// System is the real clock, always in UTC.
type System struct{}

// Now returns the system time in UTC.
func (System) Now() time.Time { return time.Now().UTC() }

// Fixed is a clock stopped at an instant, for tests.
type Fixed struct {
	At time.Time
}

// Now always returns the same instant.
func (f Fixed) Now() time.Time { return f.At }
