package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/process"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
)

func claudeLaunch(t *testing.T) harness.LaunchSpec {
	t.Helper()
	cwd := t.TempDir()
	path := filepath.Join(cwd, "context.md")
	if err := os.WriteFile(path, []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch, err := harness.Claude{}.BuildLaunchSpec(context.Background(), harness.AgentSpec{Kind: harness.KindClaude, Cwd: cwd, ContextPath: path})
	if err != nil {
		t.Fatal(err)
	}
	return launch
}

// SendKeys is `agent send-keys <name>`: Herdr resolves the name to its pane
// inside the same request, so mate never looks a pane id up and presses into
// it as two separate calls (PR 96 counter-review B2). No `agent get`, no
// `pane` command, and never the pane the handle recorded.
func TestHerdrSendKeysIsAgentAddressedOneCallPerPress(t *testing.T) {
	t.Parallel()
	var argvs [][]string
	rt := runtime.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if !hasSessionBeforeTerminator(spec.Args) {
			t.Fatalf("send-keys missing --session in option region: %#v", spec.Args)
		}
		argvs = append(argvs, append([]string(nil), spec.Args...))
		if argvHas(spec.Args, "agent", "send-keys") {
			return process.Result{Stdout: []byte(`{"id":"cli:agent:send-keys","result":{"type":"ok"}}`)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-g4-01", Kind: harness.KindClaude, Tab: runtime.TabHandle{PaneID: "w9:p9"}}
	if err := rt.SendKeys(context.Background(), h, []string{"down"}); err != nil {
		t.Fatal(err)
	}
	if len(argvs) != 1 {
		t.Fatalf("herdr calls = %d, want exactly one (the press itself): %#v", len(argvs), argvs)
	}
	got := strings.Join(argvs[0], " ")
	if !strings.Contains(got, "agent send-keys mate-g4-01 down") {
		t.Fatalf("argv = %q, want the agent-addressed press", got)
	}
	if strings.Contains(got, "w9:p9") || strings.Contains(got, " pane ") {
		t.Fatalf("send-keys addressed a pane: %q", got)
	}
}

func TestHerdrSendKeysRefusesEmptyOrMalformedKeys(t *testing.T) {
	t.Parallel()
	rt := runtime.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		t.Fatalf("no Herdr call may be made for a refused key list: %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-g4-01"}
	for _, keys := range [][]string{nil, {}, {""}, {"down enter"}, {"--session"}, {"-x"}} {
		err := rt.SendKeys(context.Background(), h, keys)
		var coded *observability.Error
		if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
			t.Fatalf("keys %#v: err = %v, want usage refusal", keys, err)
		}
	}
}

func TestHerdrSendKeysReportsAMissingAgentAsNotFound(t *testing.T) {
	t.Parallel()
	rt := runtime.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "send-keys") {
			// Herdr 0.8.2's own answer for a gone agent, including one whose
			// pane was just closed (measured 2026-09-14).
			return process.Result{ExitCode: 1, Stderr: []byte(`{"error":{"code":"agent_not_found","message":"agent target mate-g4-01 not found"},"id":"cli:agent:send-keys"}`)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-g4-01"}
	err := rt.SendKeys(context.Background(), h, []string{"enter"})
	if !runtime.IsAgentNotFound(err) {
		t.Fatalf("err = %v, want agent_not_found", err)
	}
}

func TestFakeSendKeysRecordsPressesAndRunsTheScreenHook(t *testing.T) {
	t.Parallel()
	f := runtime.NewFake()
	session := runtime.SessionHandle{Name: "lab"}
	h := runtime.AgentHandle{Session: session, Name: "crew-1", Kind: harness.KindCodex, Tab: runtime.TabHandle{PaneID: "w1:p1"}}
	f.SeedAgent(h, runtime.AgentIdle)
	f.SetReadOutput(h, "dialog")
	f.OnSendKeys = func(handle runtime.AgentHandle, keys []string) {
		if handle.Name == "crew-1" && len(keys) == 1 && keys[0] == "1" {
			f.SetReadOutput(handle, "selected")
		}
	}
	if err := f.SendKeys(context.Background(), h, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	screen, err := f.ReadAgent(context.Background(), h, 10)
	if err != nil || screen != "selected" {
		t.Fatalf("screen after hook = %q err=%v", screen, err)
	}
	if len(f.SentKeys) != 1 || f.SentKeys[0].Handle.Name != "crew-1" || strings.Join(f.SentKeys[0].Keys, ",") != "1" {
		t.Fatalf("sent keys = %+v", f.SentKeys)
	}
	missing := runtime.AgentHandle{Session: session, Name: "nobody"}
	if err := f.SendKeys(context.Background(), missing, []string{"enter"}); !runtime.IsAgentNotFound(err) {
		t.Fatalf("missing agent err = %v, want agent_not_found", err)
	}
	f.SendKeysErr = errors.New("forced")
	if err := f.SendKeys(context.Background(), h, []string{"enter"}); err == nil || err.Error() != "forced" {
		t.Fatalf("SendKeysErr not honoured: %v", err)
	}
}

// A started agent's pane shows its harness's empty composer unless a test
// scripts something else: the startup-prompt settle step reads every started
// agent, and a Fake that returned "" would make every launch fail as an
// unrecognised screen.
func TestFakeStartedAgentShowsAReadyScreenByDefault(t *testing.T) {
	t.Parallel()
	f := runtime.NewFake()
	launch := claudeLaunch(t)
	h := startFakeAgent(t, f, launch)
	screen, err := f.ReadAgent(context.Background(), h, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(screen, "❯") {
		t.Fatalf("default Claude screen = %q, want the empty composer", screen)
	}
	f2 := runtime.NewFake()
	f2.NextStartupScreen = "Something else entirely"
	h2 := startFakeAgent(t, f2, launch)
	if screen, _ := f2.ReadAgent(context.Background(), h2, 40); screen != "Something else entirely" {
		t.Fatalf("scripted screen = %q", screen)
	}
	if f2.NextStartupScreen != "" {
		t.Fatal("NextStartupScreen must be consumed by one start")
	}
}

func startFakeAgent(t *testing.T, f *runtime.Fake, launch harness.LaunchSpec) runtime.AgentHandle {
	t.Helper()
	id, err := runtime.ParseWorkspaceID("ws_sendkeys")
	if err != nil {
		t.Fatal(err)
	}
	session, err := f.EnsureSession(context.Background(), runtime.SessionSpec{WorkspaceID: id, ConfigHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := f.EnsureProjectWorkspace(context.Background(), runtime.WorkspaceSpec{Session: session, Label: "P", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	tab, err := f.CreateAgentTab(context.Background(), runtime.TabSpec{Workspace: ws, Label: "Crew", Cwd: launch.Cwd()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := runtime.AllocateAgentName(f.Names, session.Name, "m", "raw", runtime.FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := runtime.NewAgentStartSpec(tab, res, launch, 0)
	if err != nil {
		t.Fatal(err)
	}
	h, err := f.StartAgent(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// A removal that finds the tab already gone is the removal done. tab_not_found
// is what Herdr 0.8.2 answers for `tab close` after the tab's sole pane was
// force-closed (measured 2026-09-14); pane_not_found is the pane's own form.
func TestIsTabGoneNamesOnlyTheClosedTabAndPaneCodes(t *testing.T) {
	t.Parallel()
	if !runtime.IsTabGone(runtime.NewHerdrError(runtime.HerdrTabNotFound, "tab w1:t1 not found")) {
		t.Fatal("tab_not_found must read as gone")
	}
	if !runtime.IsTabGone(runtime.NewHerdrError(runtime.HerdrPaneNotFound, "pane w1:p1 not found")) {
		t.Fatal("pane_not_found must read as gone")
	}
	for _, code := range []string{runtime.HerdrAgentNotFound, runtime.HerdrWorkspaceNotFound, runtime.HerdrServerNotRunning, runtime.HerdrAgentPaneBusy} {
		if runtime.IsTabGone(runtime.NewHerdrError(code, code)) {
			t.Fatalf("%s must not read as a closed tab", code)
		}
	}
	if runtime.IsTabGone(nil) || runtime.IsTabGone(errors.New("plain")) || runtime.IsTabGone(observability.NewError(observability.CodeNotFound, "no herdr code")) {
		t.Fatal("only a Herdr-coded closed tab or pane counts")
	}
}
