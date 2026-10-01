package codex

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

func startupFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "startup", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestStartupDialogLayoutsOfOneScreenShareTheirAnswer holds the rule that
// lets one screen have several measured layouts: the settle presses one
// answer for the screen, so every layout of it must take the same keys to
// the same option position.
func TestStartupDialogLayoutsOfOneScreenShareTheirAnswer(t *testing.T) {
	t.Parallel()
	p := codexStartup()
	first := map[harness.StartupScreen]harness.StartupDialog{}
	for _, d := range p.Dialogs {
		f, ok := first[d.Screen]
		if !ok {
			first[d.Screen] = d
			continue
		}
		if !slices.Equal(d.SelectKeys, f.SelectKeys) || d.Target != f.Target {
			t.Fatalf("%s layouts disagree: %v/%d vs %v/%d", d.Screen, d.SelectKeys, d.Target, f.SelectKeys, f.Target)
		}
	}
}
