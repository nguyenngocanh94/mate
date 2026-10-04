package grok

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

func capture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "screens", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGrokScreensReadAsMeasured(t *testing.T) {
	cases := []struct {
		file  string
		busy  string
		draft string
		ready bool
	}{
		{file: "empty.txt", ready: true},
		{file: "idle-after-turn.txt", ready: true},
		{file: "resume.txt", ready: true},
		{file: "busy-waiting.txt", busy: "Waiting for response…"},
		{file: "busy-thinking.txt", busy: "Thinking…"},
		{file: "busy-responding.txt", busy: "Responding…"},
		{file: "draft-pong.txt", draft: "Reply with exactly: pong"},
	}
	s := grokScreen{}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			screen := capture(t, tc.file)
			lines := strings.Split(screen, "\n")
			text, ok := s.Composer(lines)
			if !ok {
				t.Fatal("no composer found")
			}
			evidence, busy := s.Busy(lines)
			if tc.busy == "" && busy {
				t.Fatalf("busy evidence %q on an idle screen", evidence)
			}
			if tc.busy != "" {
				if !busy || !strings.Contains(evidence, tc.busy) {
					t.Fatalf("busy evidence %q, want it to contain %q", evidence, tc.busy)
				}
				r, _ := utf8.DecodeRuneInString(evidence)
				if r < 0x2800 || r > 0x28FF {
					t.Fatalf("busy evidence %q does not start with the spinner", evidence)
				}
			}
			if tc.draft != "" && text != tc.draft {
				t.Fatalf("composer = %q, want %q", text, tc.draft)
			}
			if tc.draft == "" && text != "" {
				t.Fatalf("composer = %q, want it empty", text)
			}
			if ready := s.ClassifyStartup(screen) == harness.StartupScreenReady; ready != tc.ready {
				t.Fatalf("ready = %v, want %v", ready, tc.ready)
			}
			if tc.draft != "" {
				rows, ok := s.ComposerRows(lines)
				if !ok || len(rows) != 1 || rows[0] != tc.draft {
					t.Fatalf("rows = %q, ok %v", rows, ok)
				}
			}
		})
	}
}

func TestGrokQuitToastAndStartupSpinnerAreNotBusy(t *testing.T) {
	s := grokScreen{}
	idle := capture(t, "empty.txt")
	toast := strings.Replace(idle, "Shift+Tab:mode  │  Ctrl+.:shortcuts", "Ctrl+c:press again to quit", 1)
	mcp := "⠴ MCP (5/10)\n" + idle
	thought := "┃  ◆ Thinking…\n" + idle
	for _, screen := range []string{toast, mcp, thought} {
		lines := strings.Split(screen, "\n")
		if evidence, busy := s.Busy(lines); busy {
			t.Fatalf("busy evidence %q on %q", evidence, firstLine(screen))
		}
		if text, ok := s.Composer(lines); !ok || text != "" {
			t.Fatalf("composer = %q, ok %v", text, ok)
		}
		if s.ClassifyStartup(screen) != harness.StartupScreenReady {
			t.Fatalf("%q did not stay ready", firstLine(screen))
		}
	}
}

func TestGrokCancelHintIsBusyWithoutTheSpinner(t *testing.T) {
	s := grokScreen{}
	idle := capture(t, "empty.txt")
	hint := strings.Replace(idle,
		"Shift+Tab:mode  │  Ctrl+.:shortcuts",
		"Shift+Tab:mode  │  Ctrl+c:cancel  │  Ctrl+.:shortcuts", 1)
	lines := strings.Split(hint, "\n")
	evidence, busy := s.Busy(lines)
	if !busy || !strings.Contains(evidence, grokCancelHint) {
		t.Fatalf("busy = %v, evidence %q", busy, evidence)
	}
	if s.ClassifyStartup(hint) == harness.StartupScreenReady {
		t.Fatal("a cancel hint classified as ready")
	}
}

func TestGrokScrollbackGlyphIsNotTheComposer(t *testing.T) {
	screen := capture(t, "resume.txt")
	if strings.Count(screen, grokGlyph) < 2 {
		t.Fatal("the resume capture has no scrollback prompt to ignore")
	}
	text, ok := (grokScreen{}).Composer(strings.Split(screen, "\n"))
	if !ok || text != "" {
		t.Fatalf("composer = %q, ok %v; the scrollback prompt was read as the composer", text, ok)
	}
}

func firstLine(screen string) string {
	line, _, _ := strings.Cut(screen, "\n")
	return line
}
