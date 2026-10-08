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
	ws, err := store.Init(root, workspaceDefaults())
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

	load := func(ctx context.Context) (query.Snapshot, error) {
		return query.Load(ctx, ws, consoleHarnesses(), consoleTools())
	}
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

// TestConsoleHarnessPickerPutsTheWorkspaceDefaultFirst is the picker on
// the real wire: a workspace whose workspace.yaml says `mate_harness: codex`,
// read through query.Load, must open the create-a-Mate picker with codex
// first and under the cursor, so Enter alone creates the Mate the workspace
// is configured for.
func TestConsoleHarnessPickerPutsTheWorkspaceDefaultFirst(t *testing.T) {
	root := t.TempDir()
	ws, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "shop", ".git"), 0o755); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := ws.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}}); err != nil {
		t.Fatalf("add project: %v", err)
	}
	raw, err := os.ReadFile(ws.WorkspaceFile())
	if err != nil {
		t.Fatalf("read workspace.yaml: %v", err)
	}
	edited := strings.Replace(string(raw), "mate_harness: claude", "mate_harness: codex", 1)
	if edited == string(raw) {
		t.Fatalf("setup: workspace.yaml has no mate_harness line to edit:\n%s", raw)
	}
	if err := os.WriteFile(ws.WorkspaceFile(), []byte(edited), 0o644); err != nil {
		t.Fatalf("write workspace.yaml: %v", err)
	}

	var got []console.ActionRequest
	load := func(ctx context.Context) (query.Snapshot, error) {
		return query.Load(ctx, ws, consoleHarnesses(), consoleTools())
	}
	act := func(_ context.Context, req console.ActionRequest) (string, error) {
		got = append(got, req)
		return "ok", nil
	}
	var model tea.Model = console.New(load, act)
	step := func(msg tea.Msg) tea.Cmd {
		t.Helper()
		var cmd tea.Cmd
		model, cmd = model.Update(msg)
		return cmd
	}
	step(tea.WindowSizeMsg{Width: 40, Height: 36})
	step(model.Init()())
	step(tea.KeyMsg{Type: tea.KeyEnter})                     // into shop; its Mate row
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}) // create: the harness picker
	frame := model.View()
	codex, claude := strings.Index(frame, "codex"), strings.Index(frame, "claude")
	if codex < 0 || claude < 0 || codex > claude {
		t.Fatalf("picker order: codex at %d, claude at %d; want the workspace default codex first:\n%s", codex, claude, frame)
	}
	cmd := step(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on the picker dispatched nothing:\n%s", model.View())
	}
	cmd()
	if len(got) != 1 || got[0].Harness != query.HarnessKind("codex") {
		t.Fatalf("requests = %+v, want one create carrying the workspace default codex", got)
	}
}

// TestConsoleRefusesANonTerminal: `mate <dir> > file` must refuse with a
// usage error instead of starting Bubble Tea against a pipe.
func TestConsoleRefusesANonTerminal(t *testing.T) {
	root := t.TempDir()
	if _, err := store.Init(root, workspaceDefaults()); err != nil {
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
