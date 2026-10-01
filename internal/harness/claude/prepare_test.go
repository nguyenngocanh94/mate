package claude

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/harnesstest"
)

// A Claude Mate loads its manual from CLAUDE.md in its cwd, so the launch
// carries no context flag, and its session and settings go on the argv.
func TestClaudePrepareMate(t *testing.T) {
	req := harnesstest.PrepareFixture(t, harness.RoleMate)
	prep, err := Claude{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if prep.SessionID != harnesstest.SessionID || prep.ContextPath != "" {
		t.Fatalf("session %q, context path %q; want the minted id and no context path", prep.SessionID, prep.ContextPath)
	}
	if got := harnesstest.FileData(t, prep, filepath.Join(req.Cwd, "CLAUDE.md")); string(got) != "@AGENTS.md\n" {
		t.Fatalf("CLAUDE.md = %q, want \"@AGENTS.md\\n\"", got)
	}
	settings := ClaudeSettingsPath(req.Cwd)
	want, err := ClaudeSettings(req.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if got := harnesstest.FileData(t, prep, settings); !bytes.Equal(got, want) {
		t.Fatalf("settings = %s, want ClaudeSettings", got)
	}
	harnesstest.WritePrepared(t, prep)
	spec, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Role: harness.RoleMate, Cwd: req.Cwd, ContextPath: prep.ContextPath, Launch: prep.Launch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Delivery() != harness.DeliveryCwdManual {
		t.Fatalf("delivery = %q, want %q", spec.Delivery(), harness.DeliveryCwdManual)
	}
	args := spec.Args()
	if i := slices.Index(args, "--session-id"); i < 0 || args[i+1] != harnesstest.SessionID {
		t.Fatalf("args %q carry no --session-id %s", args, harnesstest.SessionID)
	}
	if i := slices.Index(args, "--settings"); i < 0 || args[i+1] != settings {
		t.Fatalf("args %q carry no --settings %s", args, settings)
	}

	// A resumed start names the recorded session and mints none.
	req.ResumeSessionID = "22222222-2222-4222-8222-222222222222"
	req.NewSessionID = func() string { t.Fatal("a resumed launch minted a session id"); return "" }
	prep, err = Claude{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	spec, err = Claude{}.Build(context.Background(), harness.AgentSpec{
		Role: harness.RoleMate, Cwd: req.Cwd, Launch: prep.Launch, ResumeSessionID: req.ResumeSessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prep.SessionID != req.ResumeSessionID || slices.Contains(spec.Args(), "--session-id") || !slices.Contains(spec.Args(), "--resume") {
		t.Fatalf("resume: session %q, args %q", prep.SessionID, spec.Args())
	}
}

// An existing settings file keeps its keys; one that is not JSON refuses the
// launch rather than being overwritten.
func TestClaudePrepareMateKeepsTheSettingsFileItFinds(t *testing.T) {
	req := harnesstest.PrepareFixture(t, harness.RoleMate)
	path := ClaudeSettingsPath(req.Cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const done = `{"autoMemoryEnabled":true,"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"x hook mate-session"}]}]}}`
	if err := os.WriteFile(path, []byte(done), 0o644); err != nil {
		t.Fatal(err)
	}
	prep, err := Claude{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := harnesstest.FileData(t, prep, path); string(got) != done {
		t.Fatalf("settings = %s, want the file left byte for byte", got)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (Claude{}).Prepare(context.Background(), req); err == nil {
		t.Fatal("a settings file that is not JSON must refuse the launch")
	}
}

// A Claude Crew takes its brief as the context flag and its settings from
// its state directory: nothing lands in the worktree.
func TestClaudePrepareCrewWritesNothingInTheWorktree(t *testing.T) {
	req := harnesstest.PrepareFixture(t, harness.RoleCrew)
	prep, err := Claude{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(prep.Files) != 1 || prep.Files[0].Path != filepath.Join(req.StateDir, ClaudeSettingsFile) ||
		!bytes.Equal(prep.Files[0].Data, CrewClaudeSettings()) || prep.Files[0].Exclude {
		t.Fatalf("files = %+v, want only the crew settings in the state directory", prep.Files)
	}
	if prep.ContextPath != req.ContextPath || prep.SessionID != harnesstest.SessionID {
		t.Fatalf("context path %q, session %q", prep.ContextPath, prep.SessionID)
	}
	harnesstest.WritePrepared(t, prep)
	spec, err := Claude{}.Build(context.Background(), harness.AgentSpec{
		Role: harness.RoleCrew, Cwd: req.Cwd, ContextPath: prep.ContextPath, Launch: prep.Launch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Delivery() != harness.DeliveryAppendSystemPromptFile || spec.ContextPath() != req.ContextPath {
		t.Fatalf("delivery %q, context path %q", spec.Delivery(), spec.ContextPath())
	}
}
