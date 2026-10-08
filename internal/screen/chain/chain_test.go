package chain_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/chain"
	"github.com/nguyenngocanh94/mate/internal/screen/fixture"
)

// stub answers every screen with one Observation or one error, and counts.
type stub struct {
	obs   screen.Observation
	err   error
	calls int
}

func (s *stub) Observe(context.Context, harness.ScreenProfile, string) (screen.Observation, error) {
	s.calls++
	return s.obs, s.err
}

var (
	claudeScreen = claude.Claude{}.Screen()
	codexScreen  = codex.Codex{}.Screen()
)

// fixtureDraft is a fixture reading that is sure of a draft.
var fixtureDraft = screen.Observation{Composer: screen.ComposerDraft, Draft: "half typed", Evidence: "❯ half typed",
	Dialog: screen.DialogNone, Startup: harness.StartupScreenUnrecognized, Highlight: -1, Confidence: 1, Source: "fixture"}

func jevSays(composer screen.ComposerState, dialog screen.DialogKind, conf float64) screen.Observation {
	return screen.Observation{Composer: composer, Dialog: dialog, Highlight: -1, Confidence: conf, Source: "jev",
		Notice: "quota_warning", Reason: "composer and dialog answers"}
}

func TestChainRules(t *testing.T) {
	fixtureTrust := screen.Observation{Composer: screen.ComposerUnknown, Evidence: "", Dialog: screen.DialogTrust,
		Startup: harness.StartupScreenTrustDialog, Highlight: 1, Confidence: 1, Source: "fixture"}
	fixtureUnrecognised := screen.Observation{Composer: screen.ComposerUnknown, Dialog: screen.DialogUnknown,
		Startup: harness.StartupScreenUnrecognized, Highlight: -1, Confidence: 0, Source: "fixture", Reason: "nothing recognised"}
	tests := []struct {
		name       string
		fixture    screen.Observation
		jev        screen.Observation
		jevErr     error
		want       screen.Observation
		wantReason string
	}{
		{
			name: "jev fails: the fixture's reading whole", fixture: fixtureDraft, jevErr: errors.New("Jev unavailable (HTTP 503)"),
			want: fixtureDraft, wantReason: "jev: Jev unavailable (HTTP 503)",
		},
		{
			name: "jev below the threshold: the fixture's reading whole", fixture: fixtureUnrecognised,
			jev: jevSays(screen.ComposerEmpty, screen.DialogNone, 0.84), want: fixtureUnrecognised,
			wantReason: "jev: confidence 0.84 below 0.85 (composer and dialog answers); nothing recognised",
		},
		{
			name: "jev calls a sure draft empty: the safer side wins", fixture: fixtureDraft,
			jev: jevSays(screen.ComposerEmpty, screen.DialogNone, 0.99), want: fixtureDraft,
			wantReason: "jev: composer empty, the fixture reads draft; the safer side wins",
		},
		{
			name: "jev names a dialog the fixture does not recognise: no highlight", fixture: fixtureUnrecognised,
			jev: jevSays(screen.ComposerUnknown, screen.DialogHooksReview, 0.95),
			want: screen.Observation{Composer: screen.ComposerUnknown, Dialog: screen.DialogHooksReview, Notice: "quota_warning",
				Startup: harness.StartupScreenUnrecognized, Highlight: -1, Confidence: 0.95, Source: "jev"},
			wantReason: "composer and dialog answers",
		},
		{
			name: "a dialog the fixture recognises is the fixture's, even where jev agrees", fixture: fixtureTrust,
			jev: jevSays(screen.ComposerUnknown, screen.DialogTrust, 0.9), want: fixtureTrust,
			wantReason: "fixture recognised trust_dialog",
		},
		{
			name: "otherwise jev decides; what it cannot answer is the fixture's", fixture: screen.Observation{
				Composer: screen.ComposerEmpty, Evidence: "›", Dialog: screen.DialogNone, Startup: harness.StartupScreenReady,
				Highlight: -1, Confidence: 1, Source: "fixture"},
			jev: jevSays(screen.ComposerBusy, screen.DialogNone, 0.9),
			want: screen.Observation{Composer: screen.ComposerBusy, Evidence: "›", Dialog: screen.DialogNone, Notice: "quota_warning",
				Startup: harness.StartupScreenReady, Highlight: -1, Confidence: 0.9, Source: "jev"},
			wantReason: "composer and dialog answers",
		},
		{
			name: "jev's draft stands over the fixture's empty; the draft text is the fixture's", fixture: screen.Observation{
				Composer: screen.ComposerEmpty, Evidence: "❯", Dialog: screen.DialogNone, Startup: harness.StartupScreenReady,
				Highlight: -1, Confidence: 1, Source: "fixture"},
			jev: jevSays(screen.ComposerDraft, screen.DialogNone, 0.9),
			want: screen.Observation{Composer: screen.ComposerDraft, Evidence: "❯", Dialog: screen.DialogNone, Notice: "quota_warning",
				Startup: harness.StartupScreenReady, Highlight: -1, Confidence: 0.9, Source: "jev"},
			wantReason: "composer and dialog answers",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := chain.New(&stub{obs: tc.jev, err: tc.jevErr}, &stub{obs: tc.fixture}, 0.85)
			got, err := c.Observe(context.Background(), claudeScreen, "screen")
			if err != nil {
				t.Fatal(err)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("reason %q, want %q", got.Reason, tc.wantReason)
			}
			got.Reason, tc.want.Reason = "", ""
			if got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// One screen of one harness is asked of Jev once a minute however often it
// is observed; another screen, another harness or an older answer asks
// again. Every request, and only a request, is logged.
func TestChainAsksJevOncePerScreenAMinute(t *testing.T) {
	now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	jev := &stub{obs: jevSays(screen.ComposerEmpty, screen.DialogNone, 0.9)}
	var lines []string
	c := chain.New(jev, &stub{obs: screen.Observation{Composer: screen.ComposerEmpty, Confidence: 1, Source: "fixture"}}, 0.85,
		chain.WithClock(func() time.Time { return now }),
		chain.WithLog(func(line string) error { lines = append(lines, line); return nil }))
	observe := func(profile harness.ScreenProfile, pane string) screen.Observation {
		t.Helper()
		obs, err := c.Observe(context.Background(), profile, pane)
		if err != nil {
			t.Fatal(err)
		}
		return obs
	}
	steps := []struct {
		advance time.Duration
		profile harness.ScreenProfile
		pane    string
		calls   int
	}{
		{0, claudeScreen, "a", 1},
		{59 * time.Second, claudeScreen, "a", 1},
		{0, claudeScreen, "b", 2},
		{0, codexScreen, "a", 3},
		{time.Second, claudeScreen, "a", 4},
		{0, claudeScreen, "a", 4},
	}
	for i, s := range steps {
		now = now.Add(s.advance)
		if obs := observe(s.profile, s.pane); obs.Source != "jev" {
			t.Fatalf("step %d: %+v, want jev's reading, cached or not", i, obs)
		}
		if jev.calls != s.calls || len(lines) != s.calls {
			t.Fatalf("step %d: %d calls, %d log lines, want %d", i, jev.calls, len(lines), s.calls)
		}
	}
	if !strings.Contains(lines[2], " codex ") || !strings.Contains(lines[0], " claude ") {
		t.Fatalf("log lines do not name the harness: %q", lines)
	}
}

// A failed request is remembered like an answer, so a pane Jev cannot read
// is not asked again every poll; a request the caller cancelled is not.
func TestChainRemembersFailuresButNotCancellations(t *testing.T) {
	jev := &stub{err: errors.New("offline")}
	c := chain.New(jev, &stub{obs: fixtureDraft}, 0.85)
	for range 2 {
		if obs, err := c.Observe(context.Background(), claudeScreen, "a"); err != nil || obs.Source != "fixture" {
			t.Fatalf("%+v, %v", obs, err)
		}
	}
	if jev.calls != 1 {
		t.Fatalf("%d calls for one failing screen", jev.calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 2 {
		if _, err := c.Observe(ctx, claudeScreen, "b"); err != nil {
			t.Fatal(err)
		}
	}
	if jev.calls != 3 {
		t.Fatalf("%d calls, want a cancelled request asked again", jev.calls)
	}
}

// A fixture that fails is the chain's failure: there is nothing to fall
// back to.
func TestChainFailsWhenTheFixtureDoes(t *testing.T) {
	c := chain.New(&stub{obs: jevSays(screen.ComposerEmpty, screen.DialogNone, 1)}, &stub{err: errors.New("broken")}, 0.85)
	if _, err := c.Observe(context.Background(), claudeScreen, "a"); err == nil {
		t.Fatal("no error")
	}
}

// Rule 3b: on a Codex trust dialog the fixture recognises, a Jev sure there
// is no dialog and an empty composer changes nothing. The dialog, its
// highlight and the composer are the fixture's, so send refuses and settle
// answers the dialog as measured.
func TestChainKeepsADialogTheFixtureRecognises(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(moduleRoot, "internal", "harness", "codex", "testdata", "startup", "codex-0.156.1-trust-dialog.txt"))
	if err != nil {
		t.Fatal(err)
	}
	jev := &stub{obs: screen.Observation{Composer: screen.ComposerEmpty, Dialog: screen.DialogNone, Highlight: -1,
		Confidence: 0.99, Source: "jev", Reason: "composer empty 0.99, dialog none 0.99"}}
	var lines []string
	c := chain.New(jev, fixture.New(), 0.85, chain.WithLog(func(l string) error { lines = append(lines, l); return nil }))
	got, err := c.Observe(context.Background(), codexScreen, string(data))
	if err != nil {
		t.Fatal(err)
	}
	if got.Dialog != screen.DialogTrust || got.Highlight != 0 || got.Startup != harness.StartupScreenTrustDialog ||
		got.Composer != screen.ComposerUnknown || got.Source != "fixture" {
		t.Fatalf("got %+v, want the fixture's trust dialog with its highlight on option 0", got)
	}
	if !strings.HasPrefix(got.Reason, "fixture recognised trust_dialog") {
		t.Fatalf("reason %q", got.Reason)
	}
	if len(lines) != 1 || !strings.HasSuffix(lines[0], " fallback="+chain.FallbackDialog) {
		t.Fatalf("log %q", lines)
	}
}
