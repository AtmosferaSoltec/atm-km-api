package app

import (
	"sync"
	"time"
)

// After failuresBeforeWait consecutive failures with one username, each new
// attempt waits longer instead of locking the account: that way nobody can lock
// someone else out on purpose.
const failuresBeforeWait = 5

var waits = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

const (
	maxTrackedUsernames = 10_000
	forgetAfter         = 30 * time.Minute
)

// loginAttempts counts failures per username, in memory (a single replica).
type loginAttempts struct {
	mu       sync.Mutex
	failures map[string]*failures
}

type failures struct {
	count      int
	last       time.Time
	blockedTil time.Time
}

func newLoginAttempts() *loginAttempts {
	return &loginAttempts{failures: map[string]*failures{}}
}

// wait tells how long until username can try again.
func (a *loginAttempts) wait(username string, now time.Time) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	if f, ok := a.failures[username]; ok && now.Before(f.blockedTil) {
		return f.blockedTil.Sub(now)
	}
	return 0
}

func (a *loginAttempts) failed(username string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Whoever tries thousands of random usernames must not fill the memory.
	if len(a.failures) > maxTrackedUsernames {
		for key, old := range a.failures {
			if now.Sub(old.last) > forgetAfter && now.After(old.blockedTil) {
				delete(a.failures, key)
			}
		}
	}
	f, ok := a.failures[username]
	if !ok {
		f = &failures{}
		a.failures[username] = f
	}
	f.count++
	f.last = now
	if f.count >= failuresBeforeWait {
		step := min(f.count-failuresBeforeWait, len(waits)-1)
		f.blockedTil = now.Add(waits[step])
	}
}

func (a *loginAttempts) succeeded(username string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, username)
}
