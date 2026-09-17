package runtime_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/process"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
)

// attachProbe is a Herdr adapter whose attach handoff records the argv it
// was handed instead of exec'ing an interactive client.
func attachProbe(t *testing.T, handler func(spec process.Spec) (process.Result, error)) (*runtime.Herdr, *[]string) {
	t.Helper()
	var argv []string
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if !hasSessionBeforeTerminator(spec.Args) {
			t.Fatalf("argv missing --session in option region: %#v", spec.Args)
		}
		return handler(spec)
	}}
	rt := runtime.NewHerdr(runner)
	rt.Attach = func(_ context.Context, args []string) error {
		argv = append([]string(nil), args...)
		return nil
	}
	return rt, &argv
}

func attachHandle(session, name, pane, terminal string) runtime.AgentHandle {
	return runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session},
		Name:    name,
		RawID:   "mate_g407",
		Kind:    harness.KindClaude,
		Tab: runtime.TabHandle{
			Session:     runtime.SessionHandle{Name: session},
			WorkspaceID: "w1",
			TabID:       "w1:t2",
			PaneID:      pane,
			TerminalID:  terminal,
			Cwd:         "/tmp/g4-07-lab/proj",
		},
	}
}

// A pane that has an agent attaches through `herdr agent attach <name>`, and
// the adapter learns that by asking Herdr, not by assuming it.
//
// The name attached is the one Herdr *resolved*, not the string that was
// passed in: `agent get`/`agent attach` take a TARGET, and live 0.8.2 accepts
// a pane id there as well as a name (`agent get w1:p2` returns the agent).
// The fixture's agent is named mate-g4-01, so a test that passed the request
// through unchanged would attach to a name Herdr never confirmed.
func TestHerdrAttachDetectsLiveAgent(t *testing.T) {
	t.Parallel()
	var probed bool
	rt, argv := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		switch {
		case argvHas(spec.Args, "agent", "get", "mate-g407"):
			probed = true
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-get.json")}, nil
		default:
			t.Fatalf("unexpected argv %#v", spec.Args)
			return process.Result{}, nil
		}
	})
	handle := attachHandle("fm-lab-x", "mate-g407", "w1:p2", "term_stale")
	if _, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{}); err != nil {
		t.Fatalf("AttachAgent: %v", err)
	}
	if !probed {
		t.Fatal("adapter did not probe herdr for the agent; the mode was assumed")
	}
	if !argvHas(*argv, "agent", "attach", "mate-g4-01") {
		t.Fatalf("argv = %#v, want agent attach on the name Herdr resolved", *argv)
	}
	if !runtime.SessionBeforeTerminator(*argv) {
		t.Fatalf("attach argv missing --session: %#v", *argv)
	}
}

// A pane with no agent attaches through `herdr terminal attach
// <terminal_id>`, and the terminal id is read live from the pane rather than
// trusted from the persisted handle (closed ids are unproven-not-reused).
func TestHerdrAttachFallsBackToTerminalWhenPaneHasNoAgent(t *testing.T) {
	t.Parallel()
	rt, argv := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		switch {
		case argvHas(spec.Args, "agent", "get", "mate-g407"):
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-found-get.json")}, nil
		case argvHas(spec.Args, "pane", "get", "w1:p2"):
			return process.Result{Stdout: readRuntimeTestdata(t, "pane-get-agentless.json")}, nil
		default:
			t.Fatalf("unexpected argv %#v", spec.Args)
			return process.Result{}, nil
		}
	})
	handle := attachHandle("fm-lab-x", "mate-g407", "w1:p2", "term_stale")
	if _, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{}); err != nil {
		t.Fatalf("AttachAgent: %v", err)
	}
	if !argvHas(*argv, "terminal", "attach", "term_65ac171f1de702") {
		t.Fatalf("argv = %#v, want the live terminal id, not the persisted one", *argv)
	}
	for _, a := range *argv {
		if a == "term_stale" {
			t.Fatalf("argv used the persisted terminal id: %#v", *argv)
		}
	}
}

// B1 (PR #9 counter-review). The recorded agent name being dead does not mean
// the recorded pane is empty: anything may have taken it since. Attaching
// there would stream a different, live agent's terminal under the dead name.
// The pane must be verified empty at attach time, and a reoccupied pane is
// refused with the occupant named.
func TestHerdrAttachRefusesWhenTheRecordedPaneHostsAnotherAgent(t *testing.T) {
	t.Parallel()
	rt, argv := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		switch {
		case argvHas(spec.Args, "agent", "get", "mate-g407"):
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-found-get.json")}, nil
		case argvHas(spec.Args, "pane", "get", "w1:p2"):
			return process.Result{Stdout: readRuntimeTestdata(t, "pane-get-with-agent.json")}, nil
		case argvHas(spec.Args, "agent", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-list-pane-occupant.json")}, nil
		default:
			t.Fatalf("unexpected argv %#v", spec.Args)
			return process.Result{}, nil
		}
	})
	handle := attachHandle("fm-lab-x", "mate-g407", "w1:p2", "")
	_, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{})
	if err == nil {
		t.Fatal("attached into a pane hosting a different live agent")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("err = %v, want state_conflict", err)
	}
	if !strings.Contains(coded.Message, "occupant-agent") {
		t.Fatalf("refusal must name the agent that actually holds the pane: %q", coded.Message)
	}
	if got, _ := coded.Details["pane_agent"].(string); got != "occupant-agent" {
		t.Fatalf("details = %+v", coded.Details)
	}
	if len(*argv) != 0 {
		t.Fatalf("terminal was handed over to the wrong agent: %#v", *argv)
	}
}

// The same refusal must hold when the occupant cannot be named: an
// unidentified agent in the pane is still not the one that was asked for.
func TestHerdrAttachRefusesAnOccupiedPaneEvenWhenTheOccupantCannotBeNamed(t *testing.T) {
	t.Parallel()
	rt, argv := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		switch {
		case argvHas(spec.Args, "agent", "get", "mate-g407"):
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-found-get.json")}, nil
		case argvHas(spec.Args, "pane", "get", "w1:p2"):
			return process.Result{Stdout: readRuntimeTestdata(t, "pane-get-with-agent.json")}, nil
		case argvHas(spec.Args, "agent", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-list-empty.json")}, nil
		default:
			t.Fatalf("unexpected argv %#v", spec.Args)
			return process.Result{}, nil
		}
	})
	handle := attachHandle("fm-lab-x", "mate-g407", "w1:p2", "")
	_, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{})
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("err = %v, want state_conflict", err)
	}
	if len(*argv) != 0 {
		t.Fatalf("terminal was handed over: %#v", *argv)
	}
}

// An attach must report what it actually connected to, not what the caller
// recorded: the live agent name and the live pane, read from Herdr.
func TestHerdrAttachReportsTheLiveAgentAndPane(t *testing.T) {
	t.Parallel()
	rt, _ := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "get", "mate-g407") {
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-get.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	// The recorded pane and name are deliberately stale; agent-get.json
	// resolves to mate-g4-01 in its own pane.
	handle := attachHandle("fm-lab-x", "mate-g407", "w1:p999", "term_stale")
	got, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{})
	if err != nil {
		t.Fatalf("AttachAgent: %v", err)
	}
	if got.Mode != runtime.AttachAgent {
		t.Fatalf("mode = %q", got.Mode)
	}
	if got.AgentName != "mate-g4-01" {
		t.Fatalf("agent name = %q; the outcome must carry the name Herdr resolved", got.AgentName)
	}
	if got.PaneID == "w1:p999" || got.PaneID == "" {
		t.Fatalf("pane = %q; the outcome must carry the live pane, not the recorded one", got.PaneID)
	}
}

// A terminal attach connected to no agent, and must not claim one.
func TestHerdrAttachTerminalReportsNoAgentName(t *testing.T) {
	t.Parallel()
	rt, _ := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		switch {
		case argvHas(spec.Args, "agent", "get", "mate-g407"):
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-found-get.json")}, nil
		case argvHas(spec.Args, "pane", "get", "w1:p2"):
			return process.Result{Stdout: readRuntimeTestdata(t, "pane-get-agentless.json")}, nil
		default:
			t.Fatalf("unexpected argv %#v", spec.Args)
			return process.Result{}, nil
		}
	})
	handle := attachHandle("fm-lab-x", "mate-g407", "w1:p2", "term_stale")
	got, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{})
	if err != nil {
		t.Fatalf("AttachAgent: %v", err)
	}
	if got.Mode != runtime.AttachTerminal {
		t.Fatalf("mode = %q", got.Mode)
	}
	if got.AgentName != "" {
		t.Fatalf("terminal attach claimed agent %q", got.AgentName)
	}
	if got.TerminalID != "term_65ac171f1de702" {
		t.Fatalf("terminal = %q", got.TerminalID)
	}
}

// With no agent and no pane to fall back to there is nothing to attach to,
// and the honest answer is Herdr's agent_not_found, mapped to not_found.
func TestHerdrAttachWithoutAgentOrPaneIsNotFound(t *testing.T) {
	t.Parallel()
	rt, argv := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "get", "mate-g407") {
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-found-get.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	handle := attachHandle("fm-lab-x", "mate-g407", "", "")
	_, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{})
	if err == nil {
		t.Fatal("attach with no agent and no pane must fail")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeNotFound {
		t.Fatalf("err = %v, want not_found", err)
	}
	if got, _ := coded.Details["herdr_code"].(string); got != runtime.HerdrAgentNotFound {
		t.Fatalf("herdr_code = %q, want %q", got, runtime.HerdrAgentNotFound)
	}
	if len(*argv) != 0 {
		t.Fatalf("terminal was handed over on a failed resolution: %#v", *argv)
	}
}

// pane_not_found (from `pane get`) is a missing target; agent_pane_not_found
// (from `agent start --pane`) stays the usage bucket. They must not merge.
func TestHerdrAttachPaneNotFoundStaysDistinctFromAgentPaneNotFound(t *testing.T) {
	t.Parallel()
	rt, _ := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		switch {
		case argvHas(spec.Args, "agent", "get", "mate-g407"):
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-found-get.json")}, nil
		case argvHas(spec.Args, "pane", "get", "w1:p999"):
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-pane-not-found-get.json")}, nil
		default:
			t.Fatalf("unexpected argv %#v", spec.Args)
			return process.Result{}, nil
		}
	})
	handle := attachHandle("fm-lab-x", "mate-g407", "w1:p999", "")
	_, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{})
	var coded *observability.Error
	if !errors.As(err, &coded) {
		t.Fatalf("err = %v, want a coded error", err)
	}
	if got, _ := coded.Details["herdr_code"].(string); got != runtime.HerdrPaneNotFound {
		t.Fatalf("herdr_code = %q, want %q", got, runtime.HerdrPaneNotFound)
	}
	if coded.Code != observability.CodeNotFound {
		t.Fatalf("pane_not_found mapped to %q, want not_found", coded.Code)
	}
	if runtime.MapHerdrError(runtime.HerdrAgentPaneNotFound) != observability.CodeUsage {
		t.Fatal("agent_pane_not_found must stay in the usage bucket")
	}
	if runtime.MapHerdrError(runtime.HerdrAgentNotFound) == runtime.MapHerdrError(runtime.HerdrAgentPaneNotFound) {
		t.Fatal("agent_not_found and agent_pane_not_found must stay distinct")
	}
}

// Attaching into a workspace whose Herdr session is down is the runtime
// being unavailable, not the target being missing. It must not be mistaken
// for agent_not_found and silently fall back to a terminal attach.
func TestHerdrAttachServerNotRunningIsRuntimeUnavailable(t *testing.T) {
	t.Parallel()
	rt, argv := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-server-not-running.json")}, nil
	})
	handle := attachHandle("fm-lab-x", "mate-g407", "w1:p2", "term_x")
	_, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{})
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeRuntimeUnavailable {
		t.Fatalf("err = %v, want runtime_unavailable", err)
	}
	if len(*argv) != 0 {
		t.Fatalf("terminal was handed over with no server: %#v", *argv)
	}
}

// An explicit terminal attach is the caller's decision and must not consult
// the agent registry at all.
func TestHerdrAttachExplicitTerminalModeSkipsAgentProbe(t *testing.T) {
	t.Parallel()
	rt, argv := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "get") {
			t.Fatalf("explicit terminal attach probed the agent registry: %#v", spec.Args)
		}
		if argvHas(spec.Args, "pane", "get", "w1:p2") {
			return process.Result{Stdout: readRuntimeTestdata(t, "pane-get-agentless.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	handle := attachHandle("fm-lab-x", "mate-g407", "w1:p2", "")
	if _, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{Mode: runtime.AttachTerminal}); err != nil {
		t.Fatalf("AttachAgent: %v", err)
	}
	if !argvHas(*argv, "terminal", "attach", "term_65ac171f1de702") {
		t.Fatalf("argv = %#v", *argv)
	}
}

// An explicit agent attach is also the caller's decision: no probe, the
// agent name goes straight through (herdr resolves the target itself and
// reports agent_not_found before touching the terminal).
func TestHerdrAttachExplicitAgentModeSkipsProbe(t *testing.T) {
	t.Parallel()
	rt, argv := attachProbe(t, func(spec process.Spec) (process.Result, error) {
		t.Fatalf("explicit agent attach ran a herdr command: %#v", spec.Args)
		return process.Result{}, nil
	})
	handle := attachHandle("fm-lab-x", "mate-g407", "w1:p2", "term_x")
	got, err := rt.AttachAgent(context.Background(), handle, runtime.AttachTarget{Mode: runtime.AttachAgent})
	if err != nil {
		t.Fatalf("AttachAgent: %v", err)
	}
	if !argvHas(*argv, "agent", "attach", "mate-g407") {
		t.Fatalf("argv = %#v", *argv)
	}
	if got.PaneID != "" {
		t.Fatalf("pane = %q; an explicit agent attach observed no pane and must not report one", got.PaneID)
	}
}

// Without a handoff the adapter must refuse rather than pretend it attached.
func TestHerdrAttachWithoutHandoffRefuses(t *testing.T) {
	t.Parallel()
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		return process.Result{Stdout: readRuntimeTestdata(t, "agent-get.json")}, nil
	}}
	rt := runtime.NewHerdr(runner)
	_, err := rt.AttachAgent(context.Background(), attachHandle("fm-lab-x", "mate-g407", "w1:p2", ""), runtime.AttachTarget{})
	if err == nil {
		t.Fatal("attach without a TTY handoff must fail")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) {
		t.Fatalf("err = %v, want a coded error", err)
	}
	if _, ok := coded.Details["argv"]; !ok {
		t.Fatalf("refusal should carry the resolved argv: %+v", coded.Details)
	}
}

// The detach key is the shared Herdr binding and detaching does not stop the
// agent (ADR 0003, re-verified live for G4-07).
func TestDetachKeyIsTheSharedHerdrBinding(t *testing.T) {
	t.Parallel()
	if runtime.DetachKey != "ctrl+b q" {
		t.Fatalf("DetachKey = %q", runtime.DetachKey)
	}
}

func TestTTYHandoffRefusesWhenStdinIsNotATerminal(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	handoff := runtime.TTYHandoff{Binary: "herdr", Stdin: r, Stdout: os.Stdout, Stderr: os.Stderr}
	err = handoff.Attach(context.Background(), []string{"agent", "attach", "mate-g407"})
	if err == nil {
		t.Fatal("a handoff without a terminal must be refused, not attempted")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
		t.Fatalf("err = %v, want usage", err)
	}
}

// /dev/null is a character device but not a terminal. A mode-bit check
// would let it through, and `go test` hands a process exactly that.
func TestTTYHandoffRefusesDevNull(t *testing.T) {
	t.Parallel()
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	info, err := devNull.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		t.Skip("this platform does not report os.DevNull as a character device")
	}
	handoff := runtime.TTYHandoff{Binary: "herdr", Stdin: devNull, Stdout: devNull, Stderr: devNull}
	err = handoff.Attach(context.Background(), []string{"agent", "attach", "mate-g407"})
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
		t.Fatalf("err = %v, want usage; /dev/null is not a terminal", err)
	}
}

func TestTTYHandoffRefusesEmptyArgv(t *testing.T) {
	t.Parallel()
	handoff := runtime.TTYHandoff{Binary: "herdr"}
	if err := handoff.Attach(context.Background(), nil); err == nil {
		t.Fatal("empty argv must be refused")
	}
}

func TestAttachExitErrorMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		exit int
		want observability.Code
	}{
		{2, observability.CodeUsage},
		{1, observability.CodeUnknown},
		{101, observability.CodeUnknown},
	}
	for _, tc := range cases {
		err := runtime.AttachExitError(tc.exit)
		var coded *observability.Error
		if !errors.As(err, &coded) {
			t.Fatalf("exit %d: err = %v", tc.exit, err)
		}
		if coded.Code != tc.want {
			t.Fatalf("exit %d mapped to %q, want %q", tc.exit, coded.Code, tc.want)
		}
	}
	if runtime.AttachExitError(0) != nil {
		t.Fatal("exit 0 is a clean detach, not an error")
	}
}

// exec.ExitError carries the child's status; the handoff must report it as a
// coded error rather than leaking os/exec detail to the CLI envelope.
func TestAttachExitErrorFromExitError(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sh", "-c", "exit 2")
	runErr := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("setup: %v", runErr)
	}
	err := runtime.AttachRunError(runErr)
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
		t.Fatalf("err = %v, want usage", err)
	}
	if strings.Contains(coded.Message, "exec:") {
		t.Fatalf("message leaks os/exec detail: %q", coded.Message)
	}
}
