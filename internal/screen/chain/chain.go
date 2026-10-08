// Package chain is the screen.Observer that asks Jev first and the fixture
// observer when Jev is unsure (docs/plans/jev-observer-2026-10-08.md
// sections 4.1, 4.3 and 4.5).
//
// The fixture observer reads every snapshot, Jev every new one. The
// fixture's reading is the only source of what Jev cannot answer (Draft,
// Highlight, Startup, Evidence, Deterministic), so the checks that stand
// between an Observation and a key press - send's string compare before
// Enter, settle's highlight confirmation - are made on the fixture's
// reading whatever Jev said.
//
// On the composer Jev may veto, never enable: the composer is the
// fixture's, except that Jev, sure of itself, may call a composer the
// fixture reads as empty a draft or a turn in flight. Jev never turns a
// composer the fixture reads as anything else into an empty one, so an
// Observation's Composer is ComposerEmpty only when its Deterministic is.
// Jev decides Dialog and Notice when it answers at or above the threshold
// and the fixture has not recognised a startup dialog on the screen.
package chain

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/screen"
)

// TTL is how long one answer from Jev stands for the same screen of the
// same harness.
const TTL = 60 * time.Second

// Why the chain returned the fallback's Observation, as the log names it.
const (
	FallbackError     = "error"
	FallbackThreshold = "below-threshold"
	FallbackDialog    = "recognised-dialog"
)

// Option configures a Chain.
type Option func(*Chain)

// WithClock sets the clock the dedup cache ages by and the log stamps with.
func WithClock(now func() time.Time) Option { return func(c *Chain) { c.now = now } }

// WithLog makes the chain hand one formatted line (log.go) to appendLine
// for every request it sends to the primary observer. The caller owns the
// file and its lock; an append that fails is dropped, never an
// observation's failure.
func WithLog(appendLine func(line string) error) Option {
	return func(c *Chain) { c.appendLine = appendLine }
}

// Chain is the chained observer.
type Chain struct {
	primary, fallback screen.Observer
	threshold         float64
	now               func() time.Time
	appendLine        func(string) error

	mu      sync.Mutex
	cache   map[cacheKey]cached
	breaker breaker
}

type cacheKey struct {
	kind string
	hash [sha256.Size]byte
}

type cached struct {
	obs screen.Observation
	err error
	at  time.Time
}

// New chains primary (Jev) in front of fallback (the fixture observer).
// threshold is the confidence, in [0, 1], below which primary's answer is
// not used.
func New(primary, fallback screen.Observer, threshold float64, opts ...Option) screen.Observer {
	c := &Chain{primary: primary, fallback: fallback, threshold: threshold, now: time.Now, cache: map[cacheKey]cached{}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Observe reads one snapshot through both observers and keeps, in order:
//
//  1. Draft, Highlight, Startup, Evidence and Deterministic from the
//     fallback, always.
//  2. The fallback's Observation whole when the primary fails or answers
//     below the threshold.
//  3. The fallback's Observation whole when the fallback is sure of a
//     startup dialog it recognises (Startup names one): whatever the
//     primary says, the dialog, its highlight and the composer it hides
//     are the fallback's, so nothing is typed into a dialog Jev missed.
//  4. Otherwise the primary's Dialog, Notice and Confidence, and the
//     fallback's Composer, except that a fallback ComposerEmpty becomes the
//     primary's ComposerDraft or ComposerBusy when it says one (the veto:
//     the more conservative reading wins). The primary never makes a
//     composer empty, and never names one the fallback cannot read.
//  5. With the primary's Dialog, Highlight -1 when it names a dialog and
//     the fallback recognises no startup screen: settle refuses such a
//     screen, naming Jev's dialog.
//
// The primary is asked once per harness and screen hash within TTL; a
// repeat is answered from memory. After BreakerFailures failures in a row
// it is not asked at all for BreakerCooldown (breaker.go): the fallback's
// reading is returned whole, Reason "jev: circuit open", even for a screen
// whose earlier answer is still in memory - a cache hit costs no request,
// but an open circuit answers from the fixture alone. A screen the primary
// skips (a skipper with no text to send) is not asked, not counted by the
// breaker and not logged: the fallback's reading is returned whole. The
// call is synchronous, so an answer always belongs to the snapshot that was
// hashed:
// a pane that changes while Jev is asked is a new snapshot on the caller's
// next read, never this answer's. The answer can be up to Jev's deadline old
// when it returns, so a caller that acts on it (send before typing, settle
// before the confirm key) reads the pane again first.
func (c *Chain) Observe(ctx context.Context, profile harness.ScreenProfile, pane string) (screen.Observation, error) {
	fix, err := c.fallback.Observe(ctx, profile, pane)
	if err != nil {
		return fix, err
	}
	if s, ok := c.primary.(skipper); ok && s.Skips(pane) {
		return withReason(fix, "jev: no terminal text to observe; not asked"), nil
	}
	ask, trial := c.admit()
	if !ask {
		return withReason(fix, circuitOpen), nil
	}
	key := cacheKey{kind: kindOf(profile), hash: sha256.Sum256([]byte(pane))}
	if !trial {
		if hit, ok := c.lookup(key); ok {
			obs, _ := decide(fix, hit.obs, hit.err, c.threshold)
			return obs, nil
		}
	}
	start := c.now()
	primary, perr := c.primary.Observe(ctx, profile, pane)
	latency := c.now().Sub(start)
	event := c.record(ctx, perr)
	if ctx.Err() == nil {
		c.store(key, cached{obs: primary, err: perr, at: c.now()})
	}
	obs, fallback := decide(fix, primary, perr, c.threshold)
	if c.appendLine != nil {
		_ = c.appendLine(LogLine{Time: start, Kind: key.kind, Hash: fmt.Sprintf("%x", key.hash[:6]), Latency: latency,
			Source: obs.Source, Composer: primary.Composer, Dialog: primary.Dialog, Confidence: primary.Confidence,
			Caller: screen.CallerOf(ctx), Used: used(fallback), Fallback: fallback}.String())
		if event != "" {
			_ = c.appendLine(BreakerLine(c.now(), event))
		}
	}
	return obs, nil
}

// skipper is a primary that can say, without a request, that a screen has
// nothing to ask it about (jev.Observer.Skips).
type skipper interface {
	Skips(pane string) bool
}

// decide applies Observe's rules to one pair of readings. It names the
// fallback rule that applied, or "" when the primary's reading stands.
func decide(fix, primary screen.Observation, perr error, threshold float64) (screen.Observation, string) {
	switch {
	case perr != nil:
		return withReason(fix, "jev: "+perr.Error()), FallbackError
	case primary.Confidence < threshold:
		return withReason(fix, fmt.Sprintf("jev: confidence %.2f below %.2f (%s)", primary.Confidence, threshold, primary.Reason)), FallbackThreshold
	case fix.Confidence == 1 && recognisedDialog(fix.Startup):
		return withReason(fix, "fixture recognised "+string(fix.Startup)), FallbackDialog
	}
	out := fix
	out.Composer = veto(fix.Composer, primary.Composer)
	out.Dialog, out.Notice = primary.Dialog, primary.Notice
	out.Confidence, out.Source, out.Reason = primary.Confidence, primary.Source, primary.Reason
	if primary.Dialog != screen.DialogNone && (fix.Startup == "" || fix.Startup == harness.StartupScreenUnrecognized) {
		out.Highlight = -1
	}
	return out, ""
}

// veto is the composer the chain returns: the fixture's, unless the
// fixture reads it empty and Jev says a draft or a turn in flight is there.
func veto(fixture, jev screen.ComposerState) screen.ComposerState {
	if fixture == screen.ComposerEmpty && (jev == screen.ComposerDraft || jev == screen.ComposerBusy) {
		return jev
	}
	return fixture
}

// used is the log's word for whether the primary's answer is in the
// reading returned: it is whenever no fallback rule applied.
func used(fallback string) string {
	if fallback == "" {
		return UsedYes
	}
	return UsedNo
}

// recognisedDialog reports whether a startup classification names a
// dialog: not the ready composer, not a screen the profile cannot name.
func recognisedDialog(startup harness.StartupScreen) bool {
	return startup != "" && startup != harness.StartupScreenReady && startup != harness.StartupScreenUnrecognized
}

// withReason is the fallback's Observation with why the primary's was not
// used put first in its Reason.
func withReason(fix screen.Observation, why string) screen.Observation {
	if fix.Reason != "" {
		why += "; " + fix.Reason
	}
	fix.Reason = why
	return fix
}

func (c *Chain) lookup(key cacheKey) (cached, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hit, ok := c.cache[key]
	if !ok || c.now().Sub(hit.at) >= TTL {
		return cached{}, false
	}
	return hit, true
}

// store remembers one answer and forgets every expired one, so the cache
// holds at most the screens of the last TTL.
func (c *Chain) store(key cacheKey, entry cached) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.cache {
		if entry.at.Sub(v.at) >= TTL {
			delete(c.cache, k)
		}
	}
	c.cache[key] = entry
}

// kindOf names the harness a screen profile belongs to, "-" for none.
func kindOf(profile harness.ScreenProfile) string {
	if profile == nil {
		return "-"
	}
	return string(profile.Kind())
}
