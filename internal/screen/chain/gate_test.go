package chain_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/chain"
	"github.com/nguyenngocanh94/mate/internal/screen/fixture"
	"github.com/nguyenngocanh94/mate/internal/screen/jev"
)

const moduleRoot = "../../.."

// harnessOf is the harness a corpus file was captured from, the way
// scripts/jeveval labels it: its directory under internal/harness, else
// the prefix of its name. A notice fixture belongs to none.
func harnessOf(reg harness.Registry, file string) (harness.Kind, bool) {
	if rest, ok := strings.CutPrefix(file, "internal/harness/"); ok {
		k, _, _ := strings.Cut(rest, "/")
		return harness.Kind(k), true
	}
	if strings.Contains(file, "#") {
		return "", false
	}
	for _, k := range reg.Kinds() {
		if strings.HasPrefix(filepath.Base(file), string(k)+"_") {
			return k, true
		}
	}
	return "", false
}

var startupDialogs = map[harness.StartupScreen]bool{
	harness.StartupScreenTrustDialog:  true,
	harness.StartupScreenUpdateDialog: true,
	harness.StartupScreenHooksReview:  true,
	harness.StartupScreenBypassDialog: true,
}

// The ruling's proof (threshold 0.85, the safer side wins, a dialog the
// fixture recognises is the fixture's, below the threshold the fixture): every harness screen of the 2026-10-08 corpus,
// answered by Jev from its cassette, goes through the chain, and the chain
// never calls a composer empty that the fixture reads as a draft or a turn
// in flight, and never says no dialog where the fixture recognises one.
// The notice fixtures have no harness and so no screen profile; the chain
// is never asked about a screen without one.
func TestChainGateOnTheCorpus(t *testing.T) {
	replay := jev.Replay{Dir: filepath.Join(moduleRoot, "scripts", "jeveval", "testdata", "cassette")}
	screens, err := replay.Screens(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	observer := chain.New(jev.New(notice.New("test-key").WithTransport(replay)), fixture.New(), 0.85,
		chain.WithLog(func(line string) error { lines = append(lines, line); return nil }))
	reg := catalog.Default()
	ctx := context.Background()
	counts := map[string]int{}
	var dangerous []string
	chained, noHarness, jevMissedDialog := 0, 0, 0
	for _, s := range screens {
		kind, ok := harnessOf(reg, s.File)
		if !ok {
			noHarness++
			continue
		}
		p, err := reg.Lookup(kind)
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		profile := p.Screen()
		det, err := fixture.New().Observe(ctx, profile, s.Text)
		if err != nil {
			t.Fatal(err)
		}
		obs, err := observer.Observe(ctx, profile, s.Text)
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		chained++
		if len(lines) != chained {
			t.Fatalf("%s: %d log lines after %d screens", s.File, len(lines), chained)
		}
		logged, err := chain.ParseLogLine(lines[len(lines)-1])
		if err != nil {
			t.Fatal(err)
		}
		switch logged.Fallback {
		case "":
			counts["jev"]++
		case chain.FallbackSaferSide:
			counts["safer-side"]++
		default:
			counts["fixture ("+logged.Fallback+")"]++
			if logged.Fallback == chain.FallbackDialog && logged.Dialog == screen.DialogNone {
				jevMissedDialog++
			}
		}
		if obs.Composer == screen.ComposerEmpty && (det.Composer == screen.ComposerDraft || det.Composer == screen.ComposerBusy) {
			dangerous = append(dangerous, fmt.Sprintf("%s: composer empty, fixture %s (%s)", s.File, det.Composer, obs.Reason))
		}
		if obs.Dialog == screen.DialogNone && startupDialogs[det.Startup] {
			dangerous = append(dangerous, fmt.Sprintf("%s: dialog none, fixture %s (%s)", s.File, det.Startup, obs.Reason))
		}
		if obs.Draft != det.Draft || obs.Highlight != det.Highlight || obs.Startup != det.Startup || obs.Evidence != det.Evidence {
			t.Errorf("%s: %+v does not carry the fixture's %+v", s.File, obs, det)
		}
	}
	t.Logf("chain(jev cassette, fixture, 0.85) on %d harness screens (%d notice fixtures have no harness):", chained, noHarness)
	t.Logf("  %-28s %3d", "jev", counts["jev"])
	t.Logf("  %-28s %3d", "fixture ("+chain.FallbackThreshold+")", counts["fixture ("+chain.FallbackThreshold+")"])
	t.Logf("  %-28s %3d", "fixture ("+chain.FallbackDialog+")", counts["fixture ("+chain.FallbackDialog+")"])
	t.Logf("  %-28s %3d", "  of which jev said none", jevMissedDialog)
	t.Logf("  %-28s %3d", "fixture ("+chain.FallbackError+")", counts["fixture ("+chain.FallbackError+")"])
	t.Logf("  %-28s %3d", "safer-side", counts["safer-side"])
	t.Logf("  %-28s %3d", "dangerous", len(dangerous))
	if chained != 78 || noHarness != 12 {
		t.Fatalf("%d harness screens and %d without a harness, want the corpus's 78 and 12", chained, noHarness)
	}
	// A request the cassette cannot answer falls back to the fixture, which
	// is never dangerous by this test's measure: the gate would pass
	// without measuring Jev at all.
	if n := counts["fixture ("+chain.FallbackError+")"]; n != 0 {
		t.Fatalf("%d requests failed: the gate measured the fixture, not the chain", n)
	}
	for _, d := range dangerous {
		t.Errorf("dangerous: %s", d)
	}
}
