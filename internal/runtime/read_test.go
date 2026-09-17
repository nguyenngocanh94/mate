package runtime_test

import (
	"context"
	"github.com/nguyenngocanh94/matev2/internal/process"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/runtime"
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
