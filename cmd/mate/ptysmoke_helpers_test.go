package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// ptysmokeBinary builds the pty driver. It is a real terminal - the frames
// it prints went through an emulator, not a byte recorder - which is what
// makes a mouse sequence written into it indistinguishable from one a
// terminal sent.
func ptysmokeBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ptysmoke")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/nguyenngocanh94/mate/scripts/ptysmoke")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ptysmoke: %v\n%s", err, out)
	}
	return bin
}

// runPtysmoke drives one whole Console run and returns the frame it had
// drawn. -no-quit is not optional here: ptysmoke's default exit types a "q",
// and under terminal focus that is a character the Mate would receive.
func runPtysmoke(t *testing.T, smoke, binary, workspace, script string, settle time.Duration) string {
	t.Helper()
	cmd := exec.Command(smoke, "-no-quit", "-settle", settle.String(), "-in", script, binary, workspace)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ptysmoke -in %q: %v\n%s", script, err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// frameRowOf is the 0-based row a substring appears on.
func frameRowOf(frame, want string) (int, bool) {
	for i, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, want) {
			return i, true
		}
	}
	return 0, false
}

// frameColOf is the 0-based column a substring starts at on one row. It
// counts display cells rather than bytes, because the rail's own glyphs are
// multi-byte and a byte offset would aim the click a few cells to the left.
func frameColOf(frame string, row int, want string) (int, bool) {
	lines := strings.Split(frame, "\n")
	if row < 0 || row >= len(lines) {
		return 0, false
	}
	at := strings.Index(lines[row], want)
	if at < 0 {
		return 0, false
	}
	return len([]rune(lines[row][:at])), true
}

func assertFrameSays(t *testing.T, frame string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(frame, want) {
			t.Fatalf("the frame does not say %q:\n%s", want, frame)
		}
	}
}

func sentEntries(t *testing.T, w *store.Workspace, project string) []store.SentEntry {
	t.Helper()
	entries, _, err := w.ReadSent(project, 0)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadSent: %v", err)
	}
	return entries
}
