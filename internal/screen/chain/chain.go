// Package chain is the screen.Observer that asks Jev first and the fixture
// observer when Jev is unsure (docs/plans/jev-observer-2026-10-08.md
// sections 4.1, 4.3 and 4.5).
//
// The fixture observer reads every snapshot, Jev every new one. The
// fixture's reading is the only source of what Jev cannot answer (Draft,
// Highlight, Startup, Evidence), so
// the checks that stand between an Observation and a key press - send's
// string compare before Enter, settle's highlight confirmation - are made on
// the fixture's reading whatever Jev said. Jev decides Composer, Dialog and
// Notice only when it answers at or above the threshold and does not call a
// composer empty that the fixture is sure holds text or a turn in flight.
package chain

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path"
	"reflect"
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
	FallbackSaferSide = "safer-side"
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

	mu    sync.Mutex
	cache map[cacheKey]cached
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
//  1. Draft, Highlight, Startup and Evidence from the fallback, always.
//  2. The fallback's Observation whole when the primary fails or answers
//     below the threshold.
//  3. The fallback's Observation whole when the primary calls the composer
//     empty and the fallback is sure (Confidence 1) it holds a draft or a
//     turn in flight: the safer side wins.
//  4. The primary's Dialog, with Highlight -1, when the fallback recognises
//     no startup screen: settle refuses such a screen, naming Jev's dialog.
//  5. Otherwise the primary's Composer, Dialog, Notice and Confidence.
//
// The primary is asked once per harness and screen hash within TTL; a
// repeat is answered from memory. The call is synchronous, so an answer
// always belongs to the snapshot that was hashed: a pane that changes while
// Jev is asked is a new snapshot on the caller's next read, never this
// answer's.
func (c *Chain) Observe(ctx context.Context, profile harness.ScreenProfile, pane string) (screen.Observation, error) {
	fix, err := c.fallback.Observe(ctx, profile, pane)
	if err != nil {
		return fix, err
	}
	key := cacheKey{kind: kindOf(profile), hash: sha256.Sum256([]byte(pane))}
	if hit, ok := c.lookup(key); ok {
		obs, _ := decide(fix, hit.obs, hit.err, c.threshold)
		return obs, nil
	}
	start := c.now()
	primary, perr := c.primary.Observe(ctx, profile, pane)
	latency := c.now().Sub(start)
	if ctx.Err() == nil {
		c.store(key, cached{obs: primary, err: perr, at: c.now()})
	}
	obs, fallback := decide(fix, primary, perr, c.threshold)
	if c.appendLine != nil {
		_ = c.appendLine(LogLine{Time: start, Kind: key.kind, Hash: fmt.Sprintf("%x", key.hash[:6]), Latency: latency,
			Source: obs.Source, Composer: primary.Composer, Dialog: primary.Dialog, Confidence: primary.Confidence,
			Fallback: fallback}.String())
	}
	return obs, nil
}

// decide applies Observe's rules to one pair of readings. It names the
// fallback rule that applied, or "" when the primary's reading stands.
func decide(fix, primary screen.Observation, perr error, threshold float64) (screen.Observation, string) {
	switch {
	case perr != nil:
		return withReason(fix, "jev: "+perr.Error()), FallbackError
	case primary.Confidence < threshold:
		return withReason(fix, fmt.Sprintf("jev: confidence %.2f below %.2f (%s)", primary.Confidence, threshold, primary.Reason)), FallbackThreshold
	case primary.Composer == screen.ComposerEmpty && fix.Confidence == 1 &&
		(fix.Composer == screen.ComposerDraft || fix.Composer == screen.ComposerBusy):
		return withReason(fix, fmt.Sprintf("jev: composer empty, the fixture reads %s; the safer side wins", fix.Composer)), FallbackSaferSide
	}
	out := fix
	out.Composer, out.Dialog, out.Notice = primary.Composer, primary.Dialog, primary.Notice
	out.Confidence, out.Source, out.Reason = primary.Confidence, primary.Source, primary.Reason
	if primary.Dialog != screen.DialogNone && (fix.Startup == "" || fix.Startup == harness.StartupScreenUnrecognized) {
		out.Highlight = -1
	}
	return out, ""
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

// kindOf names the harness a screen profile belongs to: every harness's
// profile lives in the harness's own package, named for its kind
// (internal/harness/claude, codex, grok, pi).
func kindOf(profile harness.ScreenProfile) string {
	if profile == nil {
		return "-"
	}
	t := reflect.TypeOf(profile)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return path.Base(t.PkgPath())
}
