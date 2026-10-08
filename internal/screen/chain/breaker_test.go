package chain_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness/harnesstest"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/chain"
)

// breakerLines are the breaker's own lines of a log.
func breakerLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if f := strings.Fields(l); len(f) == 3 && f[1] == "breaker" {
			out = append(out, f[2])
		}
	}
	return out
}

// Three failures of Jev in a row open the circuit: for a minute Jev is not
// asked, every observation is the fixture's with Reason "jev: circuit
// open", and the log says so once. After the minute one request goes
// through; a failure keeps the circuit open another minute, a success
// closes it, which the log says once.
func TestChainOpensTheCircuitAfterThreeFailures(t *testing.T) {
	now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	var lines []string
	jev := &stub{err: errors.New("Jev unavailable (HTTP 503)")}
	fix := screen.Observation{Composer: screen.ComposerEmpty, Dialog: screen.DialogNone, Highlight: -1, Confidence: 1, Source: "fixture"}
	c := chain.New(jev, &stub{obs: fix}, 0.85, chain.WithClock(func() time.Time { return now }),
		chain.WithLog(func(line string) error { lines = append(lines, line); return nil }))
	screens := 0
	observe := func() screen.Observation {
		t.Helper()
		screens++ // a new screen every time: the cache answers nothing here
		obs, err := c.Observe(context.Background(), claudeScreen, fmt.Sprintf("screen %d", screens))
		if err != nil {
			t.Fatal(err)
		}
		return obs
	}

	for i := 1; i <= chain.BreakerFailures; i++ {
		if obs := observe(); obs.Reason != "jev: Jev unavailable (HTTP 503)" {
			t.Fatalf("failure %d: reason %q", i, obs.Reason)
		}
	}
	if got := breakerLines(lines); len(got) != 1 || got[0] != chain.BreakerOpen {
		t.Fatalf("after three failures the log says %q, want one open", got)
	}
	if !strings.HasPrefix(lines[len(lines)-1], "2026-10-08T09:00:00Z breaker open") {
		t.Fatalf("breaker line %q", lines[len(lines)-1])
	}

	asked, logged := jev.calls, len(lines)
	for range 5 {
		obs := observe()
		want := fix
		want.Reason = "jev: circuit open"
		if obs != want {
			t.Fatalf("while open: %+v, want the fixture's reading with the circuit's reason", obs)
		}
		now = now.Add(11 * time.Second)
	}
	if jev.calls != asked || len(lines) != logged {
		t.Fatalf("while open Jev was asked %d times and %d lines logged, want none", jev.calls-asked, len(lines)-logged)
	}

	now = time.Date(2026, 10, 8, 9, 1, 0, 0, time.UTC) // the cooldown has passed
	if obs := observe(); jev.calls != asked+1 || obs.Reason != "jev: Jev unavailable (HTTP 503)" {
		t.Fatalf("after the cooldown: %d calls, reason %q; want one failed request", jev.calls-asked, obs.Reason)
	}
	if obs := observe(); jev.calls != asked+1 || obs.Reason != "jev: circuit open" {
		t.Fatalf("after a failed retry: %d calls, reason %q; want the circuit open again", jev.calls-asked, obs.Reason)
	}
	now = now.Add(chain.BreakerCooldown - time.Second)
	if observe(); jev.calls != asked+1 {
		t.Fatal("asked again before a full cooldown after the failed retry")
	}

	now = now.Add(time.Second)
	jev.err, jev.obs = nil, jevSays(screen.ComposerBusy, screen.DialogNone, 0.97)
	if obs := observe(); obs.Source != "jev" || obs.Composer != screen.ComposerBusy {
		t.Fatalf("a retry that succeeded: %+v, want Jev's reading", obs)
	}
	if got := breakerLines(lines); len(got) != 2 || got[1] != chain.BreakerClosed {
		t.Fatalf("log says %q, want open then closed", got)
	}
	before := jev.calls
	observe()
	if jev.calls != before+1 {
		t.Fatal("Jev not asked once the circuit closed")
	}
	if got := breakerLines(lines); len(got) != 2 {
		t.Fatalf("log says %q, want no further breaker line", got)
	}
}

// Only failures in a row open the circuit, and a request the caller
// cancelled is not Jev's failure.
func TestChainCountsOnlyFailuresInARow(t *testing.T) {
	jev := &stub{}
	c := chain.New(jev, &stub{obs: fixtureDraft}, 0.85)
	n := 0
	observe := func(ctx context.Context, err error) screen.Observation {
		t.Helper()
		jev.err = err
		n++
		obs, oerr := c.Observe(ctx, claudeScreen, fmt.Sprintf("screen %d", n))
		if oerr != nil {
			t.Fatal(oerr)
		}
		return obs
	}
	down := errors.New("offline")
	observe(context.Background(), down)
	observe(context.Background(), down)
	observe(context.Background(), nil)
	observe(context.Background(), down)
	observe(context.Background(), down)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	observe(cancelled, context.Canceled)
	if obs := observe(context.Background(), nil); obs.Reason == "jev: circuit open" || jev.calls != 7 {
		t.Fatalf("%d calls, reason %q: the circuit opened on failures that were not three in a row", jev.calls, obs.Reason)
	}
}

// The cache and the log name a harness by its screen profile's kind, also
// for a profile wrapped to read another source.
func TestChainNamesTheHarnessByItsKind(t *testing.T) {
	var lines []string
	c := chain.New(&stub{obs: jevSays(screen.ComposerEmpty, screen.DialogNone, 0.9)}, &stub{obs: fixtureDraft}, 0.85,
		chain.WithLog(func(line string) error { lines = append(lines, line); return nil }))
	if _, err := c.Observe(context.Background(), harnesstest.ScreenReadingVisible(codexScreen), "screen"); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || strings.Fields(lines[0])[1] != "codex" {
		t.Fatalf("log %q, want the codex kind", lines)
	}
}
