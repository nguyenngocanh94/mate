package chain_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/chain"
)

// The chain writes one line per request in the documented shape: Jev's
// answer, which reading was returned and why the fixture's was.
func TestChainLogLine(t *testing.T) {
	now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	tick := now
	clock := func() time.Time { t := tick; tick = tick.Add(237 * time.Millisecond); return t }
	var lines []string
	log := chain.WithLog(func(line string) error { lines = append(lines, line); return errors.New("disk full") })
	jev := &stub{obs: jevSays(screen.ComposerBusy, screen.DialogNone, 0.62)}
	c := chain.New(jev, &stub{obs: fixtureDraft}, 0.85, chain.WithClock(clock), log)
	if _, err := c.Observe(context.Background(), claudeScreen, "screen"); err != nil {
		t.Fatalf("a log that cannot be written failed the observation: %v", err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("screen")))[:12]
	want := "2026-10-08T02:00:00Z claude " + hash + " 237 fixture composer=busy dialog=none conf=0.62 fallback=below-threshold"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("log %q\nwant %q", lines, want)
	}
	jev.obs, jev.err = screen.Observation{}, errors.New("offline")
	if _, err := c.Observe(context.Background(), claudeScreen, "other"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(lines[1], " fixture composer=- dialog=- conf=0.00 fallback=error") {
		t.Fatalf("failed request logged as %q", lines[1])
	}
}

func TestParseLogLineReadsWhatStringWrote(t *testing.T) {
	for _, l := range []chain.LogLine{
		{Time: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC), Kind: "codex", Hash: "0123456789ab", Latency: 284 * time.Millisecond,
			Source: "jev", Composer: screen.ComposerEmpty, Dialog: screen.DialogNone, Confidence: 0.91},
		{Time: time.Date(2026, 10, 8, 9, 0, 1, 0, time.UTC), Kind: "pi", Hash: "ba9876543210", Latency: 8 * time.Second,
			Source: "fixture", Composer: screen.ComposerDraft, Dialog: screen.DialogHooksReview, Confidence: 0.5, Fallback: chain.FallbackThreshold},
	} {
		got, err := chain.ParseLogLine(l.String())
		if err != nil || got != l {
			t.Fatalf("ParseLogLine(%q) = %+v, %v", l.String(), got, err)
		}
	}
	for _, bad := range []string{"", "2026-10-08T09:00:00Z claude abc", "yesterday claude abc 1 jev composer=empty dialog=none conf=0.9",
		"2026-10-08T09:00:00Z claude abc -1 jev composer=empty dialog=none conf=0.9",
		"2026-10-08T09:00:00Z claude abc 1 jev composer=empty dialog=none confidence=0.9"} {
		if _, err := chain.ParseLogLine(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// A day's log comes down to the three numbers the evidence needs: requests,
// latency p50/p95 and fallbacks. A line caught mid-append is not read.
func TestSummarizeALog(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		l := chain.LogLine{Time: time.Date(2026, 10, 8, 9, 0, i, 0, time.UTC), Kind: "claude", Hash: "0123456789ab",
			Latency: time.Duration(i*10) * time.Millisecond, Source: "jev", Composer: screen.ComposerEmpty, Dialog: screen.DialogNone, Confidence: 0.9}
		switch {
		case i%5 == 0:
			l.Source, l.Fallback = "fixture", chain.FallbackThreshold
		case i == 7:
			l.Source, l.Fallback = "fixture", chain.FallbackError
		}
		b.WriteString(l.String() + "\n")
	}
	b.WriteString("not a log line\n")
	b.WriteString("2026-10-08T09:01:00Z claude 0123456789ab 999") // mid-append
	s, err := chain.Summarize(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if s.Calls != 20 || s.P50 != 100*time.Millisecond || s.P95 != 190*time.Millisecond || s.Fallbacks != 5 ||
		s.ByFallback[chain.FallbackThreshold] != 4 || s.ByFallback[chain.FallbackError] != 1 || s.Malformed != 1 {
		t.Fatalf("summary %+v", s)
	}
}
