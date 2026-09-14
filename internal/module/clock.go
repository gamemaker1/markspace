package module

import (
	"sync"
	"time"
)

// ManualClock is a clock for tests. It starts at the time passed to
// NewManualClock and moves only on Advance and Set. Pass its Now
// method wherever an option takes func() time.Time.
type ManualClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewManualClock returns a clock set to t in UTC.
func NewManualClock(t time.Time) *ManualClock {
	return &ManualClock{t: t.UTC()}
}

// Now returns the current manual time.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance moves the clock forward by d and returns the new time.
func (c *ManualClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	return c.t
}

// Set moves the clock to t.
func (c *ManualClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t.UTC()
}

// utcNow is the default clock.
func utcNow() time.Time { return time.Now().UTC() }
