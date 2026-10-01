package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testSessionID = "11111111-2222-3333-4444-555555555555"

// prepareFixture is one agent's directories: its cwd, its state directory
// and its instructions, already on disk where the role keeps them.
func prepareFixture(t *testing.T, role AgentRole) PrepareRequest {
	t.Helper()
	cwd, state := t.TempDir(), t.TempDir()
	instructions := filepath.Join(state, "brief.md")
	if role == RoleMate {
		instructions = filepath.Join(cwd, "AGENTS.md")
	}
	if err := os.WriteFile(instructions, []byte("# instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return PrepareRequest{
		Role: role, Cwd: cwd, StateDir: state, ContextPath: instructions,
		Binary: "/usr/local/bin/mate", NewSessionID: func() string { return testSessionID },
	}
}

// writePrepared lays the files down the way spawn does.
func writePrepared(t *testing.T, prep Prepared) {
	t.Helper()
	for _, f := range prep.Files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.Path, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func fileData(t *testing.T, prep Prepared, path string) []byte {
	t.Helper()
	for _, f := range prep.Files {
		if f.Path == path {
			return f.Data
		}
	}
	var names []string
	for _, f := range prep.Files {
		names = append(names, f.Path)
	}
	t.Fatalf("Prepare names no %s; it names %v", path, names)
	return nil
}

// A Claude Mate loads its manual from CLAUDE.md in its cwd, so the launch
// carries no context flag, and its session and settings go on the argv.
func TestClaudePrepareMate(t *testing.T) {
	req := prepareFixture(t, RoleMate)
	prep, err := Claude{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if prep.SessionID != testSessionID || prep.ContextPath != "" {
		t.Fatalf("session %q, context path %q; want the minted id and no context path", prep.SessionID, prep.ContextPath)
	}
	if got := fileData(t, prep, filepath.Join(req.Cwd, "CLAUDE.md")); string(got) != "@AGENTS.md\n" {
		t.Fatalf("CLAUDE.md = %q, want \"@AGENTS.md\\n\"", got)
	}
	settings := ClaudeSettingsPath(req.Cwd)
	want, err := ClaudeSettings(req.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileData(t, prep, settings); !bytes.Equal(got, want) {
		t.Fatalf("settings = %s, want ClaudeSettings", got)
	}
	writePrepared(t, prep)
	spec, err := Claude{}.Build(context.Background(), AgentSpec{
		Role: RoleMate, Cwd: req.Cwd, ContextPath: prep.ContextPath, Launch: prep.Launch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Delivery() != DeliveryCwdManual {
		t.Fatalf("delivery = %q, want %q", spec.Delivery(), DeliveryCwdManual)
	}
	args := spec.Args()
	if i := slices.Index(args, "--session-id"); i < 0 || args[i+1] != testSessionID {
		t.Fatalf("args %q carry no --session-id %s", args, testSessionID)
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
	spec, err = Claude{}.Build(context.Background(), AgentSpec{
		Role: RoleMate, Cwd: req.Cwd, Launch: prep.Launch, ResumeSessionID: req.ResumeSessionID,
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
	req := prepareFixture(t, RoleMate)
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
	if got := fileData(t, prep, path); string(got) != done {
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
	req := prepareFixture(t, RoleCrew)
	prep, err := Claude{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(prep.Files) != 1 || prep.Files[0].Path != filepath.Join(req.StateDir, ClaudeSettingsFile) ||
		!bytes.Equal(prep.Files[0].Data, CrewClaudeSettings()) || prep.Files[0].Exclude {
		t.Fatalf("files = %+v, want only the crew settings in the state directory", prep.Files)
	}
	if prep.ContextPath != req.ContextPath || prep.SessionID != testSessionID {
		t.Fatalf("context path %q, session %q", prep.ContextPath, prep.SessionID)
	}
	writePrepared(t, prep)
	spec, err := Claude{}.Build(context.Background(), AgentSpec{
		Role: RoleCrew, Cwd: req.Cwd, ContextPath: prep.ContextPath, Launch: prep.Launch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Delivery() != DeliveryAppendSystemPromptFile || spec.ContextPath() != req.ContextPath {
		t.Fatalf("delivery %q, context path %q", spec.Delivery(), spec.ContextPath())
	}
}

// Codex reads AGENTS.override.md at its cwd and nothing else; a Crew's copy
// of its brief is kept out of git, and no session id exists before the
// first prompt.
func TestCodexPrepareCrewCopiesTheBriefIntoTheWorktree(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	req := prepareFixture(t, RoleCrew)
	prep, err := Codex{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	override := CodexInstructionPath(req.Cwd)
	if len(prep.Files) != 1 || prep.Files[0].Path != override || !prep.Files[0].Exclude ||
		string(prep.Files[0].Data) != "# instructions\n" {
		t.Fatalf("files = %+v, want the brief copied to %s and excluded", prep.Files, override)
	}
	if prep.ContextPath != override || prep.SessionID != "" || prep.Launch != nil {
		t.Fatalf("context path %q, session %q, launch %v", prep.ContextPath, prep.SessionID, prep.Launch)
	}
	writePrepared(t, prep)
	if _, err := (Codex{}).Build(context.Background(), AgentSpec{Role: RoleCrew, Cwd: req.Cwd, ContextPath: prep.ContextPath}); err != nil {
		t.Fatal(err)
	}
}

func TestCodexPrepareMateNamesItsHooks(t *testing.T) {
	req := prepareFixture(t, RoleMate)
	req.ResumeSessionID = "01a0d260-cd47-77d2-bee7-46d98aa0461a"
	prep, err := Codex{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if prep.SessionID != req.ResumeSessionID {
		t.Fatalf("session = %q, want the resumed one", prep.SessionID)
	}
	if got := fileData(t, prep, CodexHooksPath(req.Cwd)); !bytes.Equal(got, CodexHooks(req.Binary)) {
		t.Fatalf("hooks = %s, want CodexHooks", got)
	}
	if got := fileData(t, prep, CodexInstructionPath(req.Cwd)); string(got) != "# instructions\n" {
		t.Fatalf("override = %q, want the manual", got)
	}
}

func TestPrepareRefusesRelativePathsAndOtherRoles(t *testing.T) {
	for _, l := range []Launcher{Claude{}, Codex{}} {
		for name, mutate := range map[string]func(*PrepareRequest){
			"relative cwd":          func(r *PrepareRequest) { r.Cwd = "." },
			"relative state":        func(r *PrepareRequest) { r.StateDir = "state" },
			"relative instructions": func(r *PrepareRequest) { r.ContextPath = "brief.md" },
			"user role":             func(r *PrepareRequest) { r.Role = RoleUser },
		} {
			req := prepareFixture(t, RoleCrew)
			mutate(&req)
			if _, err := l.Prepare(context.Background(), req); err == nil {
				t.Errorf("%T %s: Prepare succeeded, want a refusal", l, name)
			}
		}
	}
}

// One harness's launch data is never another's.
func TestBuildRefusesAnotherHarnessesLaunchData(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	req := prepareFixture(t, RoleCrew)
	claude, err := Claude{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	codex, err := Codex{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	writePrepared(t, codex)
	// The cwd Codex is given would launch, so only the launch-data guard
	// can refuse it.
	if _, err := (Codex{}).Build(context.Background(), AgentSpec{Cwd: req.Cwd}); err != nil {
		t.Fatalf("Codex refused its own prepared cwd: %v", err)
	}
	foreign := fmt.Sprintf("launch data %T", claude.Launch)
	if _, err := (Codex{}).Build(context.Background(), AgentSpec{Cwd: req.Cwd, Launch: claude.Launch}); err == nil || !strings.Contains(err.Error(), foreign) {
		t.Fatalf("Codex given Claude's launch data: err = %v, want a refusal naming %s", err, foreign)
	}
	if _, err := (Claude{}).Build(context.Background(), AgentSpec{Cwd: req.Cwd, ContextPath: req.ContextPath, Launch: "x"}); err == nil || !strings.Contains(err.Error(), "launch data string") {
		t.Fatalf("Claude given foreign launch data: err = %v, want a refusal naming string", err)
	}
}

// NewLaunchSpec runs the harness's own context check after the generic
// ones, and ValidateRequiredContext runs it again, so the runtime boundary
// refuses a spec whose context stopped being deliverable after it was built.
func TestNewLaunchSpecRunsTheHarnessCheckEachTime(t *testing.T) {
	cwd := t.TempDir()
	path := writeAbs(t, cwd, "context.md", "you are a crew")
	refuse := false
	plan := LaunchPlan{
		RuntimeKind: "fake", Screen: claudeScreen{}, Cwd: cwd, Delivery: DeliveryAppendSystemPromptFile,
		Args: []string{"--append-system-prompt-file", path}, ContextPath: path, ContextRequired: true,
		CheckContext: func(LaunchSpec) error {
			if refuse {
				return ErrContextRequired
			}
			return nil
		},
	}
	spec, err := NewLaunchSpec(plan)
	if err != nil {
		t.Fatal(err)
	}
	refuse = true
	if err := spec.ValidateRequiredContext(); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("revalidation err = %v, want the harness check's refusal", err)
	}
	if _, err := NewLaunchSpec(plan); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("NewLaunchSpec err = %v, want the harness check's refusal", err)
	}
	refuse = false
	plan.Env = []EnvVar{{Key: "SECRET", Value: "x"}}
	if _, err := NewLaunchSpec(plan); err == nil {
		t.Fatal("NewLaunchSpec accepted an env key off the allowlist")
	}
}

// The cwd is absolute on every launch, not only one whose context is
// required: a relative one would resolve against the Mate process cwd.
func TestNewLaunchSpecRefusesARelativeCwdWithoutRequiredContext(t *testing.T) {
	plan := LaunchPlan{RuntimeKind: "claude", Screen: claudeScreen{}, Cwd: "rel/dir", Delivery: DeliveryCwdManual, Args: []string{"--x"}}
	if spec, err := NewLaunchSpec(plan); err == nil {
		t.Fatalf("NewLaunchSpec accepted cwd %q: %+v", plan.Cwd, spec)
	}
	plan.Cwd = t.TempDir()
	if _, err := NewLaunchSpec(plan); err != nil {
		t.Fatalf("NewLaunchSpec refused an absolute cwd: %v", err)
	}
}

// A launch names the screen profile its pane is read with; a plan without
// one is refused rather than started into a pane nobody can classify.
func TestNewLaunchSpecRefusesAPlanWithoutAScreen(t *testing.T) {
	plan := LaunchPlan{RuntimeKind: "claude", Cwd: t.TempDir(), Delivery: DeliveryCwdManual, Args: []string{"--x"}}
	if spec, err := NewLaunchSpec(plan); err == nil {
		t.Fatalf("NewLaunchSpec accepted a plan with no screen profile: %+v", spec)
	}
	plan.Screen = claudeScreen{}
	spec, err := NewLaunchSpec(plan)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Screen() != (claudeScreen{}) {
		t.Fatalf("spec screen = %#v, want the plan's", spec.Screen())
	}
}

// The Codex chain check is one of those: a spec is refused once a parent
// directory's AGENTS.md pushes the chain past the cap after Build, which no
// generic check of the override alone would see.
func TestCodexSpecRefusedOnceItsChainOutgrowsTheCap(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	root := t.TempDir()
	initFakeGit(t, root)
	cwd := filepath.Join(root, "sub")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAbs(t, cwd, CodexOverrideName, "you are a crew")
	spec, err := Codex{MaxChainBytes: 1000}.Build(context.Background(), AgentSpec{Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	writeAbs(t, root, CodexBaseName, string(bytes.Repeat([]byte("r"), 1000)))
	if err := spec.ValidateRequiredContext(); !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("err = %v, want ErrContextTooLarge from the chain check", err)
	}
}
