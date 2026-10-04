package grok

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

const testSessionID = "3b03c470-291f-4210-abd3-7dc8e18b0ff7"

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

func build(t *testing.T, req harness.PrepareRequest, model string, effort harness.Effort) harness.LaunchSpec {
	t.Helper()
	prep, err := Grok{}.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	spec, err := Grok{}.Build(context.Background(), harness.AgentSpec{
		Role: harness.RoleCrew, Kind: KindGrok, Cwd: req.Cwd, ContextPath: prep.ContextPath, Launch: prep.Launch,
		Model: model, Effort: effort, TaskPrompt: "go", ResumeSessionID: req.ResumeSessionID,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return spec
}

func TestGrokCrewLaunchInlinesTheBrief(t *testing.T) {
	req := crewRequest(t)
	brief, err := os.ReadFile(req.ContextPath)
	if err != nil {
		t.Fatal(err)
	}
	spec := build(t, req, "grok-4.7", harness.EffortHigh)
	want := []string{
		"--fullscreen", "--always-approve", "--no-plan", "--trust",
		"--session-id", testSessionID,
		"--model", "grok-4.7",
		"--effort", "high",
		"--append-system-prompt", string(brief),
	}
	if got := spec.Args(); !slices.Equal(got, want) {
		t.Fatalf("args =\n%q\nwant\n%q", got, want)
	}
	if spec.Kind() != "grok" || spec.Delivery() != harness.DeliveryAppendSystemPrompt || !spec.ContextRequired() || spec.EffortOmitted() {
		t.Fatalf("spec kind %q delivery %q required %v omitted %v", spec.Kind(), spec.Delivery(), spec.ContextRequired(), spec.EffortOmitted())
	}
	if err := spec.ValidateRequiredContext(); err != nil {
		t.Fatal(err)
	}
}

func TestGrokPassesTheMenuAndOmitsMax(t *testing.T) {
	for _, e := range []harness.Effort{harness.EffortLow, harness.EffortMedium, harness.EffortHigh, harness.EffortXHigh} {
		spec := build(t, crewRequest(t), "", e)
		args := spec.Args()
		i := slices.Index(args, "--effort")
		if i < 0 || args[i+1] != string(e) || spec.EffortOmitted() {
			t.Errorf("effort %s: args %q, omitted %v", e, args, spec.EffortOmitted())
		}
	}
	spec := build(t, crewRequest(t), "", harness.EffortMax)
	if slices.Contains(spec.Args(), "--effort") || !spec.EffortOmitted() {
		t.Fatalf("max: args %q, omitted %v", spec.Args(), spec.EffortOmitted())
	}
}

func TestGrokResumeUsesTheResumeFlag(t *testing.T) {
	req := crewRequest(t)
	req.ResumeSessionID = testSessionID
	spec := build(t, req, "", "")
	args := spec.Args()
	if slices.Contains(args, "--session-id") {
		t.Fatalf("args %q start a new session", args)
	}
	i := slices.Index(args, "--resume")
	if i < 0 || args[i+1] != testSessionID {
		t.Fatalf("args %q do not resume %s", args, testSessionID)
	}
}

func TestGrokRefusesAMateNamingHooks(t *testing.T) {
	req := crewRequest(t)
	req.Role = harness.RoleMate
	_, err := Grok{}.Prepare(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "Hooks") {
		t.Fatalf("Prepare of a Mate = %v, want a refusal naming Hooks", err)
	}
	_, err = Grok{}.Build(context.Background(), harness.AgentSpec{Role: harness.RoleMate, Kind: KindGrok, Cwd: req.Cwd, ContextPath: req.ContextPath})
	if err == nil || !strings.Contains(err.Error(), "Hooks") {
		t.Fatalf("Build of a Mate = %v, want a refusal naming Hooks", err)
	}
}

func TestGrokRefusesForeignLaunchData(t *testing.T) {
	req := crewRequest(t)
	_, err := Grok{}.Build(context.Background(), harness.AgentSpec{
		Role: harness.RoleCrew, Kind: KindGrok, Cwd: req.Cwd, ContextPath: req.ContextPath, Launch: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "launch data string") {
		t.Fatalf("Build = %v, want a refusal of the foreign launch data", err)
	}
}

func TestGrokRefusesAMissingBrief(t *testing.T) {
	req := crewRequest(t)
	missing := filepath.Join(req.StateDir, "gone.md")
	_, err := Grok{}.Build(context.Background(), harness.AgentSpec{
		Role: harness.RoleCrew, Kind: KindGrok, Cwd: req.Cwd, ContextPath: missing, Launch: grokLaunch{sessionID: testSessionID},
	})
	if !errors.Is(err, harness.ErrContextRequired) {
		t.Fatalf("Build with a missing brief = %v, want ErrContextRequired", err)
	}
}

func TestGrokSessionDirEscapesTheCwd(t *testing.T) {
	const cwd = "/private/tmp/grok-harness-probe.o9lc"
	got := SessionDir("/r", cwd, testSessionID)
	want := "/r/" + url.PathEscape(cwd) + "/" + testSessionID
	if got != want {
		t.Fatalf("SessionDir = %q, want %q", got, want)
	}
}

func TestGrokSessionsRootFollowsTheHome(t *testing.T) {
	t.Setenv(homeEnv, "/g")
	if got, err := sessionsRoot(""); err != nil || got != "/g/sessions" {
		t.Fatalf("sessionsRoot = %q, %v", got, err)
	}
	if got, err := sessionsRoot("/mine"); err != nil || got != "/mine/sessions" {
		t.Fatalf("sessionsRoot(configured) = %q, %v", got, err)
	}
	if _, err := sessionsRoot("relative"); err == nil {
		t.Fatal("a relative grok home was taken")
	}
}

func TestGrokResumableLooksForTheDirectory(t *testing.T) {
	root := t.TempDir()
	s := grokSessions{home: root}
	var none *harness.NoSessionError
	if err := s.Resumable(testSessionID); !errors.As(err, &none) {
		t.Fatalf("Resumable with no directory = %v, want NoSessionError", err)
	}
	dir := SessionDir(filepath.Join(root, "sessions"), "/private/tmp/probe", testSessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Resumable(testSessionID); err != nil {
		t.Fatalf("Resumable with the directory = %v", err)
	}
	// The ledger is not required until a turn has been recorded.
	src := grokTranscripts{home: root}
	if _, reason := src.Locate(harness.TranscriptLocateRequest{SessionID: testSessionID}); reason != harness.LocateNotFound {
		t.Fatalf("Locate before updates.jsonl = %q, want %s", reason, harness.LocateNotFound)
	}
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loc, reason := src.Locate(harness.TranscriptLocateRequest{SessionID: testSessionID})
	if reason != "" || loc.Path != filepath.Join(dir, "updates.jsonl") || loc.Rule != grokRuleSessionDir {
		t.Fatalf("Locate = %+v, %q", loc, reason)
	}
	if _, reason := src.Locate(harness.TranscriptLocateRequest{}); reason != harness.LocateNoSession {
		t.Fatalf("Locate with no id = %q, want %s", reason, harness.LocateNoSession)
	}
}

func TestGrokLocateRefusesTwoDirectories(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	for _, cwd := range []string{"/a", "/b"} {
		dir := SessionDir(sessions, cwd, testSessionID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := (grokSessions{home: root}).Resumable(testSessionID); err == nil {
		t.Fatal("two directories for one id were resumable")
	}
}

func TestGrokStopsByClearingOnceThenQuitting(t *testing.T) {
	stop := Grok{}.Capabilities().GracefulStop.Impl
	if !slices.Equal(stop.ClearKeys(), []string{"ctrl+c"}) || stop.ExitPrompt() != "/quit" {
		t.Fatalf("stop = %q then %q", stop.ClearKeys(), stop.ExitPrompt())
	}
	if (Grok{}).Capabilities().Session.Impl.AtStop("/w", time.Time{}, "runtime") != "" {
		t.Fatal("AtStop named a session a launch already named")
	}
}

func TestGrokStartupInventsNoDialog(t *testing.T) {
	s := Grok{}.Screen()
	for _, dialog := range []harness.StartupScreen{harness.StartupScreenTrustDialog, harness.StartupScreenUpdateDialog, harness.StartupScreenHooksReview} {
		if _, err := s.StartupAnswer(dialog); err == nil || !strings.Contains(err.Error(), string(dialog)) {
			t.Errorf("StartupAnswer(%s) = %v, want a refusal naming it", dialog, err)
		}
	}
}
