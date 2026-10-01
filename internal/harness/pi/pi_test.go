package pi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

const testSessionID = "3f0c5a8e-6b1d-4c2e-9a7f-2d4b8e1c0a11"

// crewRequest is a Crew launch in a fresh temp tree, with its brief on disk.
func crewRequest(t *testing.T) harness.PrepareRequest {
	t.Helper()
	dir := t.TempDir()
	cwd := filepath.Join(dir, "work")
	state := filepath.Join(dir, "state")
	for _, d := range []string{cwd, state} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	brief := filepath.Join(state, "brief.md")
	if err := os.WriteFile(brief, []byte("# Brief\n\nDo the task.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return harness.PrepareRequest{
		Role: harness.RoleCrew, Cwd: cwd, StateDir: state, ContextPath: brief,
		NewSessionID: func() string { return testSessionID },
	}
}

func build(t *testing.T, p Pi, req harness.PrepareRequest, model string, effort harness.Effort) harness.LaunchSpec {
	t.Helper()
	prep, err := p.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prep.SessionID != testSessionID {
		t.Fatalf("Prepare session = %q, want the minted %q", prep.SessionID, testSessionID)
	}
	spec, err := p.Build(context.Background(), harness.AgentSpec{
		Role: harness.RoleCrew, Kind: KindPi, Cwd: req.Cwd, ContextPath: prep.ContextPath, Launch: prep.Launch,
		Model: model, Effort: effort, TaskPrompt: "go",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return spec
}

// A Crew launch keeps the operator's pi out of the pane, names its session
// where pi itself would keep it, and hands pi the brief by path.
func TestPiCrewLaunch(t *testing.T) {
	root := t.TempDir()
	req := crewRequest(t)
	spec := build(t, Pi{SessionsDir: root}, req, "deepseek/deepseek-flash", harness.EffortHigh)
	want := []string{
		"--no-extensions", "--no-skills", "--no-approve", "--offline",
		"--session-dir", SessionDir(root, req.Cwd), "--session-id", testSessionID,
		"--model", "deepseek/deepseek-flash",
		"--thinking", "high",
		"--append-system-prompt", req.ContextPath,
	}
	if got := spec.Args(); !slices.Equal(got, want) {
		t.Fatalf("args =\n%q\nwant\n%q", got, want)
	}
	if spec.Kind() != "pi" || spec.Delivery() != harness.DeliveryAppendSystemPromptFile || !spec.ContextRequired() {
		t.Fatalf("spec kind %q delivery %q required %v", spec.Kind(), spec.Delivery(), spec.ContextRequired())
	}
	if spec.TaskPrompt() != "go" {
		t.Fatalf("task prompt = %q", spec.TaskPrompt())
	}
	// Nothing is written into the worktree.
	entries, err := os.ReadDir(req.Cwd)
	if err != nil || len(entries) != 0 {
		t.Fatalf("the worktree holds %v (%v), want nothing", entries, err)
	}
}

// Every Effort mate has is a level pi's --thinking takes, so each is passed
// as it is; pi's own clamping is read back from the session.
func TestPiPassesEveryEffortThrough(t *testing.T) {
	for _, e := range harness.Efforts {
		spec := build(t, Pi{SessionsDir: t.TempDir()}, crewRequest(t), "", e)
		args := spec.Args()
		i := slices.Index(args, "--thinking")
		if i < 0 || args[i+1] != string(e) || spec.EffortOmitted() {
			t.Errorf("effort %s: args %q, omitted %v", e, args, spec.EffortOmitted())
		}
		if slices.Contains(args, "--model") {
			t.Errorf("effort %s: no model asked, but args %q name one", e, args)
		}
	}
}

func TestPiResumeNamesTheSameSession(t *testing.T) {
	root := t.TempDir()
	req := crewRequest(t)
	req.ResumeSessionID = testSessionID
	p := Pi{SessionsDir: root}
	prep, err := p.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := p.Build(context.Background(), harness.AgentSpec{
		Role: harness.RoleCrew, Kind: KindPi, Cwd: req.Cwd, ContextPath: prep.ContextPath, Launch: prep.Launch,
		ResumeSessionID: testSessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	args := spec.Args()
	if i := slices.Index(args, "--session-id"); i < 0 || args[i+1] != testSessionID {
		t.Fatalf("args %q do not resume %s", args, testSessionID)
	}
}

// A Mate on pi is refused at both ends, and the refusal names the
// capability pi lacks.
func TestPiRefusesAMateNamingHooks(t *testing.T) {
	req := crewRequest(t)
	req.Role = harness.RoleMate
	_, err := Pi{}.Prepare(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "Hooks") {
		t.Fatalf("Prepare of a Mate = %v, want a refusal naming Hooks", err)
	}
	_, err = Pi{}.Build(context.Background(), harness.AgentSpec{Role: harness.RoleMate, Kind: KindPi, Cwd: req.Cwd, ContextPath: req.ContextPath})
	if err == nil || !strings.Contains(err.Error(), "Hooks") {
		t.Fatalf("Build of a Mate = %v, want a refusal naming Hooks", err)
	}
}

// pi appends a missing path to the system prompt as its text, with no
// warning, so a brief that is not there must stop the launch.
func TestPiRefusesAMissingBrief(t *testing.T) {
	req := crewRequest(t)
	missing := filepath.Join(req.StateDir, "gone.md")
	_, err := Pi{SessionsDir: t.TempDir()}.Build(context.Background(), harness.AgentSpec{
		Role: harness.RoleCrew, Kind: KindPi, Cwd: req.Cwd, ContextPath: missing,
	})
	if !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("Build with a missing brief = %v, want ErrContextRequired", err)
	}
}

// SessionDir is pi's own directory name for a cwd (session-manager.js
// getDefaultSessionDirPath), so `pi --resume` there finds a mate session.
func TestPiSessionDirIsPisOwnName(t *testing.T) {
	got := SessionDir("/r", "/Volumes/Work/x/")
	if want := "/r/--Volumes-Work-x--"; got != want {
		t.Fatalf("SessionDir = %q, want %q", got, want)
	}
}

func TestPiSessionsRootFollowsTheAgentDir(t *testing.T) {
	t.Setenv(AgentDirEnv, "/agent")
	if got, err := sessionsRoot(""); err != nil || got != "/agent/sessions" {
		t.Fatalf("sessionsRoot = %q, %v", got, err)
	}
	if got, err := sessionsRoot("/mine"); err != nil || got != "/mine" {
		t.Fatalf("sessionsRoot(configured) = %q, %v", got, err)
	}
	if _, err := sessionsRoot("relative"); err == nil {
		t.Fatal("a relative sessions directory was taken")
	}
}

// pi starts a fresh session under an id it has no file for, so a resume is
// checked against the files before anything is launched.
func TestPiResumableLooksForTheFile(t *testing.T) {
	root := t.TempDir()
	s := piSessions{root: root}
	var none *harness.NoSessionError
	if err := s.Resumable(testSessionID); !errors.As(err, &none) {
		t.Fatalf("Resumable with no file = %v, want NoSessionError", err)
	}
	dir := SessionDir(root, "/w")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-10-01T03-54-06-652Z_"+testSessionID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Resumable(testSessionID); err != nil {
		t.Fatalf("Resumable with the file = %v", err)
	}
}

// A graceful stop empties the composer, then quits.
func TestPiStopsByClearingThenQuitting(t *testing.T) {
	stop := Pi{}.Capabilities().GracefulStop.Impl
	if !slices.Equal(stop.ClearKeys(), []string{"ctrl+u"}) || stop.ExitPrompt() != "/quit" {
		t.Fatalf("stop = %q then %q", stop.ClearKeys(), stop.ExitPrompt())
	}
}
