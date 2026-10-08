package chain

import (
	"context"
	"errors"
	"time"
)

// The circuit breaker: after BreakerFailures failures of the primary in a
// row, the chain stops asking it for BreakerCooldown and answers every
// snapshot with the fallback's reading, Reason "jev: circuit open". When
// the cooldown has passed, one request is let through: if it succeeds the
// circuit closes, if it fails the circuit stays open another cooldown. The
// log says `breaker open` once when the circuit opens and `breaker closed`
// once when it closes, so a day's log shows each outage once rather than a
// failed request per pane change for its whole length.
const (
	BreakerFailures = 3
	BreakerCooldown = 60 * time.Second
)

// The breaker's two log events.
const (
	BreakerOpen   = "open"
	BreakerClosed = "closed"
)

// circuitOpen is the Reason of every Observation the open circuit returns.
const circuitOpen = "jev: circuit open"

// breaker is the circuit's state; Chain.mu guards it.
type breaker struct {
	failures int
	open     bool
	// until is when the open circuit next lets a request through.
	until time.Time
}

// admit reports whether the primary may be asked now, and whether this is
// the one request let through an open circuit. That request holds the
// circuit open for every other caller until it answers.
func (c *Chain) admit() (ask, trial bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.breaker.open {
		return true, false
	}
	now := c.now()
	if now.Before(c.breaker.until) {
		return false, false
	}
	c.breaker.until = now.Add(BreakerCooldown)
	return true, true
}

// record counts one answer of the primary and returns the event it caused,
// if any. A request its caller cancelled says nothing about the primary and
// is not counted; one that ran out of time is a failure.
func (c *Chain) record(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b := &c.breaker
	if err == nil {
		b.failures = 0
		if b.open {
			b.open = false
			return BreakerClosed
		}
		return ""
	}
	b.failures++
	switch {
	case b.open:
		b.until = c.now().Add(BreakerCooldown)
	case b.failures >= BreakerFailures:
		b.open, b.until = true, c.now().Add(BreakerCooldown)
		return BreakerOpen
	}
	return ""
}
