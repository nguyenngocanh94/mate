package fixture_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/fixture"
)

// startupCapture loads one of a harness's committed startup captures.
func startupCapture(t *testing.T, kind harness.Kind, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "harness", string(kind), "testdata", "startup", name))
	if err != nil {
		t.Fatalf("read startup capture %s: %v", name, err)
	}
	return string(data)
}

// TestFixtureObservesRepresentativeScreens runs the fixture observer over one
// screen of each shape the callers' policies tell apart.
func TestFixtureObservesRepresentativeScreens(t *testing.T) {
	tests := []struct {
		name   string
		kind   harness.Kind
		screen string
		want   screen.Observation
	}{
		{
			name:   "an empty composer",
			kind:   claude.KindClaude,
			screen: capture(t, "claude_empty"),
			want: screen.Observation{Composer: screen.ComposerEmpty, Evidence: "❯",
				Dialog: screen.DialogNone, Startup: harness.StartupScreenReady, Highlight: -1, Confidence: 1},
		},
		{
			name:   "a draft in the composer",
			kind:   claude.KindClaude,
			screen: capture(t, "claude_pending"),
			want: screen.Observation{Composer: screen.ComposerDraft, Draft: "half typed",
				Evidence: "❯ half typed", Dialog: screen.DialogNone,
				Startup: harness.StartupScreenUnrecognized, Highlight: -1, Confidence: 1},
		},
		{
			name:   "a turn in flight",
			kind:   codex.KindCodex,
			screen: capture(t, "codex_busy"),
			want: screen.Observation{Composer: screen.ComposerBusy, Evidence: "• Working (2s • esc to interrupt)",
				Dialog: screen.DialogNone, Startup: harness.StartupScreenReady, Highlight: -1, Confidence: 1},
		},
		{
			name:   "the trust dialog with the highlight on the option mate confirms",
			kind:   claude.KindClaude,
			screen: startupCapture(t, claude.KindClaude, "claude-2.1.270-trust-dialog-accept-selected.txt"),
			want: screen.Observation{Composer: screen.ComposerUnknown, Evidence: "harness directory-trust dialog",
				Dialog: screen.DialogTrust, Startup: harness.StartupScreenTrustDialog, Highlight: 1, Confidence: 1,
				Reason: "no composer: harness directory-trust dialog"},
		},
		{
			name:   "the trust dialog with the highlight still on its default",
			kind:   claude.KindClaude,
			screen: startupCapture(t, claude.KindClaude, "claude-2.1.270-trust-dialog.txt"),
			want: screen.Observation{Composer: screen.ComposerUnknown, Evidence: "harness directory-trust dialog",
				Dialog: screen.DialogTrust, Startup: harness.StartupScreenTrustDialog, Highlight: -1, Confidence: 1,
				Reason: "no composer: harness directory-trust dialog"},
		},
		{
			name:   "a screen the profile cannot name",
			kind:   claude.KindClaude,
			screen: "user@host ~/shop $ ls\nREADME.md  go.mod\nuser@host ~/shop $ ",
			want: screen.Observation{Composer: screen.ComposerUnknown, Dialog: screen.DialogUnknown,
				Startup: harness.StartupScreenUnrecognized, Highlight: -1, Confidence: 0,
				Reason: "the harness profile recognises no composer, busy line or startup dialog on screen"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fixture.New().Observe(context.Background(), screenOf(tc.kind), tc.screen)
			if err != nil {
				t.Fatalf("Observe: %v", err)
			}
			tc.want.Source = fixture.Source
			if got != tc.want {
				t.Fatalf("Observe =\n  %+v\nwant\n  %+v", got, tc.want)
			}
		})
	}
}

// TestFixtureObserveAgreesWithTheProfileOnEveryCapture pins that observing
// changes no classification: on every committed composer capture the
// Observation carries ClassifyComposer's verdict and ClassifyStartup's, as
// the profile returns them.
func TestFixtureObserveAgreesWithTheProfileOnEveryCapture(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "screens"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		kind := claude.KindClaude
		if strings.HasPrefix(e.Name(), "codex_") {
			kind = codex.KindCodex
		}
		pane := captureFile(t, e.Name())
		profile := screenOf(kind)
		got, err := fixture.New().Observe(context.Background(), profile, pane)
		if err != nil {
			t.Fatalf("%s: Observe: %v", e.Name(), err)
		}
		cls := fixture.ClassifyComposer(profile, pane)
		if got.Composer != cls.State || got.Draft != cls.Pending || got.Evidence != cls.Evidence {
			t.Errorf("%s: composer %q/%q/%q, ClassifyComposer says %q/%q/%q", e.Name(),
				got.Composer, got.Draft, got.Evidence, cls.State, cls.Pending, cls.Evidence)
		}
		if want := profile.ClassifyStartup(fixture.StripSGR(pane)); got.Startup != want {
			t.Errorf("%s: startup %q, ClassifyStartup says %q", e.Name(), got.Startup, want)
		}
	}
}
