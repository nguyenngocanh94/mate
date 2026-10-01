package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// TestConsoleGalleryRendersRegisteredProjects is the end of the wire: two
// projects registered through internal/store on a real temporary directory,
// read back through query.Load, and drawn by the Console's own View - the
// same three pieces `mate <dir>` puts together, with only the terminal
// left out.
//
// It asserts the frame contract as well as the content: View must return
// exactly h lines, or the gallery a person sees is not the one this test
// passed on.
func TestConsoleGalleryRendersRegisteredProjects(t *testing.T) {
	root := t.TempDir()
	ws, err := store.Init(root)
	if err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	for _, name := range []string{"shop", "blog"} {
		if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0o755); err != nil {
			t.Fatalf("create repo %s: %v", name, err)
		}
		if err := ws.AddProject(name, store.ProjectConfig{Repos: []store.RepoConfig{{Path: name}}}); err != nil {
			t.Fatalf("add project %s: %v", name, err)
		}
	}

	load := func(ctx context.Context) (query.Snapshot, error) { return query.Load(ctx, ws) }
	// New picks the glyph set from the locale, and the assertions spell the
	// Unicode arrow; force that set so a shell without UTF-8 draws the same.
	t.Setenv("MATE_ASCII", "0")
	m := console.New(load, nil)
	// The Console is the left ~20% of the captain's terminal: 40 columns.
	model, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 36})
	model, _ = model.Update(m.Init()())
	frame := model.View()

	if got := len(strings.Split(frame, "\n")); got != 36 {
		t.Fatalf("frame has %d lines, want 36:\n%s", got, frame)
	}
	for _, want := range []string{"shop", "blog", "!2", "→ next pane"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("gallery does not show %q:\n%s", want, frame)
		}
	}
	// Neither project has a Mate yet, and the gallery must say so rather
	// than leaving the Mate's status blank.
	if strings.Count(frame, "no mate") != 3 {
		t.Fatalf("want both rows and the selected project's detail to say no mate:\n%s", frame)
	}
}

// TestConsoleRefusesANonTerminal: `mate <dir> > file` must refuse with a
// usage error instead of starting Bubble Tea against a pipe.
func TestConsoleRefusesANonTerminal(t *testing.T) {
	root := t.TempDir()
	if _, err := store.Init(root); err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := mainRun([]string{root}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 for a usage refusal; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "needs a real terminal") {
		t.Fatalf("stderr = %q, want it to say a terminal is required", stderr.String())
	}
}
