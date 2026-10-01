package pi

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

func capture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "screens", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// Every measured screen reads as what it showed. The catalog contract suite
// checks the same captures through send.ClassifyComposer; this pins what
// the profile itself says.
func TestPiScreensReadAsMeasured(t *testing.T) {
	cases := []struct {
		file  string
		busy  bool
		draft string
		ready bool
	}{
		{file: "run1-empty-composer.visible.txt", ready: true},
		{file: "run6-idle-after-turn.visible.txt", ready: true},
		{file: "run1-after-ctrl-u.txt", ready: true},
		{file: "run1-busy-10.txt", ready: true},
		{file: "run8-offline-startup.txt", ready: true},
		{file: "run2-resume-startup.txt", ready: true},
		{file: "run3-sessid-startup.txt", ready: true},
		{file: "trust-c5-no-approve-startup.txt", ready: true},
		{file: "trust-c5-after-session-trust.visible.txt", ready: true},
		{file: "run1-busy-1.txt", busy: true},
		{file: "run1-busy-3.txt", busy: true},
		{file: "run8-busy-reads.visible.txt", busy: true},
		{file: "run6-draft.visible.txt", draft: "draft text not sent"},
	}
	s := piScreen{}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			screen := capture(t, tc.file)
			lines := strings.Split(screen, "\n")
			text, ok := s.Composer(lines)
			if !ok {
				t.Fatal("no composer found")
			}
			if _, busy := s.Busy(lines); busy != tc.busy {
				t.Fatalf("busy = %v, want %v", busy, tc.busy)
			}
			if tc.draft != "" && text != tc.draft {
				t.Fatalf("composer = %q, want the draft %q", text, tc.draft)
			}
			if tc.draft == "" && text != "" {
				t.Fatalf("composer = %q, want it empty", text)
			}
			if ready := s.ClassifyStartup(screen) == harness.StartupScreenReady; ready != tc.ready {
				t.Fatalf("ready = %v, want %v", ready, tc.ready)
			}
		})
	}
}

// Through recent-unwrapped pi's rules and composer row can come back joined
// into one line. That screen must not read as an empty composer, or a draft
// would be typed over.
func TestPiJoinedComposerIsNoComposer(t *testing.T) {
	lines := strings.Split(capture(t, "run6-draft.recent-unwrapped.txt"), "\n")
	if text, ok := (piScreen{}).Composer(lines); ok {
		t.Fatalf("the joined screen read as a composer holding %q", text)
	}
}

func TestPiTrustDialog(t *testing.T) {
	s := piScreen{}
	start := capture(t, "trust-c5-startup.txt")
	if got := s.ClassifyStartup(start); got != harness.StartupScreenTrustDialog {
		t.Fatalf("trust-c5-startup classifies as %s", got)
	}
	if s.StartupTargetSelected(harness.StartupScreenTrustDialog, start) {
		t.Fatal("the default highlight is on Trust, but the target read as selected")
	}
	third := capture(t, "trust-c5-dialog-third-option.visible.txt")
	if got := s.ClassifyStartup(third); got != harness.StartupScreenTrustDialog {
		t.Fatalf("the third-option screen classifies as %s", got)
	}
	if !s.StartupTargetSelected(harness.StartupScreenTrustDialog, third) {
		t.Fatal("the highlight on \"Trust (this session only)\" did not read as the target")
	}
	ans, err := s.StartupAnswer(harness.StartupScreenTrustDialog)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ans.SelectKeys, []string{"down", "down"}) || ans.TargetLabel != "Trust (this session only)" {
		t.Fatalf("answer = %+v, want down down onto the session-only trust", ans)
	}
}

func TestPiReadyScreenIsReady(t *testing.T) {
	s := piScreen{}
	ready := s.ReadyScreen()
	if got := s.ClassifyStartup(ready); got != harness.StartupScreenReady {
		t.Fatalf("ReadyScreen classifies as %s", got)
	}
}
