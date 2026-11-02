package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoginAttemptsBackOff(t *testing.T) {
	attempts := newLoginAttempts()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	for range failuresBeforeWait - 1 {
		attempts.failed("joel", now)
	}
	assert.Zero(t, attempts.wait("joel", now), "four failures do not block")

	attempts.failed("joel", now)
	assert.Equal(t, time.Minute, attempts.wait("joel", now))
	assert.Zero(t, attempts.wait("joel", now.Add(time.Minute)))

	attempts.failed("joel", now.Add(time.Minute))
	assert.Equal(t, 5*time.Minute, attempts.wait("joel", now.Add(time.Minute)))

	for range 5 {
		attempts.failed("joel", now.Add(10*time.Minute))
	}
	assert.Equal(t, 15*time.Minute, attempts.wait("joel", now.Add(10*time.Minute)), "capped")

	assert.Zero(t, attempts.wait("ana", now), "per username")
	attempts.succeeded("joel")
	assert.Zero(t, attempts.wait("joel", now.Add(10*time.Minute)))
}
