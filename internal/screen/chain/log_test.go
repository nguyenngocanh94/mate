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
// answer, which reading was returned, who asked, whether Jev's answer is in
// the reading and why the fixture's was.
func TestChainLogLine(t *testing.T) {
	now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	tick := now
	clock := func() time.Time { t := tick; tick = tick.Add(237 * time.Millisecond); return t }
	var lines []string
	log := chain.WithLog(func(line string) error { lines = append(lines, line); return errors.New("disk full") })
	jev := &stub{obs: jevSays(screen.ComposerBusy, screen.DialogNone, 0.62)}
	c := chain.New(jev, &stub{obs: fixtureDraft}, 0.85, chain.WithClock(clock), log)
	if _, err := c.Observe(screen.WithCaller(context.Background(), screen.CallerWatch), claudeScreen, "screen"); err != nil {
		t.Fatalf("a log that cannot be written failed the observation: %v", err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("screen")))[:12]
	want := "2026-10-08T02:00:00Z claude " + hash + " 237 fixture composer=busy dialog=none conf=0.62 caller=watch used=no fallback=below-threshold"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("log %q\nwant %q", lines, want)
	}
	jev.obs, jev.err = screen.Observation{}, errors.New("offline")
	if _, err := c.Observe(context.Background(), claudeScreen, "other"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(lines[1], " fixture composer=- dialog=- conf=0.00 caller=- used=no fallback=error") {
		t.Fatalf("failed request logged as %q", lines[1])
	}
	jev.obs, jev.err = jevSays(screen.ComposerBusy, screen.DialogNone, 0.9), nil
	if _, err := c.Observe(screen.WithCaller(context.Background(), screen.CallerSend), claudeScreen, "third"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(lines[2], " jev composer=busy dialog=none conf=0.90 caller=send used=yes") {
		t.Fatalf("a used answer logged as %q", lines[2])
	}
}

func TestParseLogLineReadsWhatStringWrote(t *testing.T) {
	for _, l := range []chain.LogLine{
		{Time: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC), Kind: "codex", Hash: "0123456789ab", Latency: 284 * time.Millisecond,
			Source: "jev", Composer: screen.ComposerEmpty, Dialog: screen.DialogNone, Confidence: 0.91},
		{Time: time.Date(2026, 10, 8, 9, 0, 1, 0, time.UTC), Kind: "pi", Hash: "ba9876543210", Latency: 8 * time.Second,
			Source: "fixture", Composer: screen.ComposerDraft, Dialog: screen.DialogHooksReview, Confidence: 0.5, Fallback: chain.FallbackThreshold},
		{Time: time.Date(2026, 10, 8, 9, 0, 2, 0, time.UTC), Kind: "claude", Hash: "ba9876543210", Latency: time.Second,
			Source: "jev", Composer: screen.ComposerBusy, Dialog: screen.DialogNone, Confidence: 0.9, Caller: "watch", Used: chain.UsedYes},
		{Time: time.Date(2026, 10, 8, 9, 0, 3, 0, time.UTC), Kind: "claude", Hash: "ba9876543210", Latency: time.Second,
			Source: "fixture", Composer: screen.ComposerBusy, Dialog: screen.DialogNone, Confidence: 0.5, Used: chain.UsedNo,
			Fallback: chain.FallbackThreshold},
	} {
		got, err := chain.ParseLogLine(l.String())
		if err != nil || got != l {
			t.Fatalf("ParseLogLine(%q) = %+v, %v", l.String(), got, err)
		}
	}
	for _, bad := range []string{"", "2026-10-08T09:00:00Z claude abc", "yesterday claude abc 1 jev composer=empty dialog=none conf=0.9",
		"2026-10-08T09:00:00Z claude abc -1 jev composer=empty dialog=none conf=0.9",
		"2026-10-08T09:00:00Z claude abc 1 jev composer=empty dialog=none confidence=0.9",
		"2026-10-08T09:00:00Z claude abc 1 jev composer=empty dialog=none conf=0.9 caller=watch used=maybe",
		"2026-10-08T09:00:00Z claude abc 1 jev composer=empty dialog=none conf=0.9 caller=watch"} {
		if _, err := chain.ParseLogLine(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// A day's log comes down to the numbers the evidence needs: requests,
// latency p50/p95, fallbacks, and requests used and unused. A line caught mid-append is not read.
func TestSummarizeALog(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		l := chain.LogLine{Time: time.Date(2026, 10, 8, 9, 0, i, 0, time.UTC), Kind: "claude", Hash: "0123456789ab",
			Latency: time.Duration(i*10) * time.Millisecond, Source: "jev", Composer: screen.ComposerEmpty, Dialog: screen.DialogNone, Confidence: 0.9}
		l.Caller, l.Used = "watch", chain.UsedYes
		switch {
		case i%5 == 0:
			l.Source, l.Fallback, l.Used = "fixture", chain.FallbackThreshold, chain.UsedNo
		case i == 7:
			l.Source, l.Fallback, l.Used, l.Caller = "fixture", chain.FallbackError, chain.UsedNo, "send"
		case i == 9:
			l.Caller, l.Used = "", "" // a line from before caller and used were logged
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
		s.ByFallback[chain.FallbackThreshold] != 4 || s.ByFallback[chain.FallbackError] != 1 || s.Malformed != 1 ||
		s.Used != 14 || s.Unused != 5 || s.ByCaller["watch"] != 18 || s.ByCaller["send"] != 1 {
		t.Fatalf("summary %+v", s)
	}
}
