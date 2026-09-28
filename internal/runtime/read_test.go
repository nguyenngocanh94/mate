package runtime_test

import (
	"context"
	"github.com/nguyenngocanh94/mate/internal/process"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/runtime"
)

func TestHerdrReadAgentUsesRecentUnwrappedTextAndNamedSession(t *testing.T) {
	t.Parallel()
	var got process.Spec
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		got = spec
		return process.Result{Stdout: []byte("pane output\n")}, nil
	}}
	rt := runtime.NewHerdr(runner)
	output, err := rt.ReadAgent(context.Background(), runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: "lab-session"}, Name: "crew-agent",
	}, 12)
	if err != nil {
		t.Fatal(err)
	}
	if output != "pane output\n" {
		t.Fatalf("output = %q", output)
	}
	if !runtime.SessionBeforeTerminator(got.Args) {
		t.Fatalf("session flag is not in the option region: %v", got.Args)
	}
	joined := " " + strings.Join(got.Args, " ") + " "
	for _, want := range []string{"agent read crew-agent", "--source recent-unwrapped", "--lines 12", "--format text"} {
		if !strings.Contains(joined, " "+want+" ") {
			t.Fatalf("argv %v missing %q", got.Args, want)
		}
	}
}

// TestHerdrReadAgentFallsBackToTheVisibleScreenWhileCodexWorks pins what
// Herdr 0.8.2 does to a harness drawn on the alternate screen, measured
// 2026-09-26 against codex-cli 0.157.1: `--source recent-unwrapped` of a
// working Codex is refused with agent_not_idle (testdata
// error-agent-not-idle.json), and the refusal names `--source visible` as
// the read that works. Without the fallback every send to a working Codex
// crew failed as an unknown Herdr error, before the composer classifier
// could say the crew was mid-turn.
func TestHerdrReadAgentFallsBackToTheVisibleScreenWhileCodexWorks(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"text", "ansi"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			var calls [][]string
			runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
				calls = append(calls, spec.Args)
				if strings.Contains(strings.Join(spec.Args, " "), "--source recent-unwrapped") {
					return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-idle.json")}, nil
				}
				return process.Result{Stdout: []byte("• Working (5s • esc to interrupt)\n")}, nil
			}}
			rt := runtime.NewHerdr(runner)
			handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab-session"}, Name: "crew-agent"}
			read := rt.ReadAgent
			if format == "ansi" {
				read = rt.ReadAgentStyled
			}
			output, err := read(context.Background(), handle, 40)
			if err != nil {
				t.Fatalf("read of a working agent: %v", err)
			}
			if output != "• Working (5s • esc to interrupt)\n" {
				t.Fatalf("output = %q, want the visible screen", output)
			}
			if len(calls) != 2 {
				t.Fatalf("herdr ran %d times, want the refused read then the visible one: %v", len(calls), calls)
			}
			joined := " " + strings.Join(calls[1], " ") + " "
			for _, want := range []string{"agent read crew-agent", "--source visible", "--lines 40", "--format " + format} {
				if !strings.Contains(joined, " "+want+" ") {
					t.Fatalf("fallback argv %v missing %q", calls[1], want)
				}
			}
		})
	}
}

// TestHerdrReadAgentDoesNotRetryOtherFailures keeps the fallback to the one
// refusal it answers: a missing agent is still a missing agent.
func TestHerdrReadAgentDoesNotRetryOtherFailures(t *testing.T) {
	t.Parallel()
	calls := 0
	runner := &process.FakeRunner{Handler: func(_ context.Context, _ process.Spec) (process.Result, error) {
		calls++
		return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-found-get.json")}, nil
	}}
	rt := runtime.NewHerdr(runner)
	_, err := rt.ReadAgent(context.Background(), runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: "lab-session"}, Name: "crew-agent",
	}, 40)
	if !runtime.IsAgentNotFound(err) {
		t.Fatalf("err = %v, want agent_not_found", err)
	}
	if calls != 1 {
		t.Fatalf("herdr ran %d times, want 1", calls)
	}
}
