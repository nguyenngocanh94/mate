package runtime_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
)

const agentGetJSON = `{"id":"cli:agent:get","result":{"agent":{"agent":"claude","agent_status":"idle","cwd":"/w","interactive_ready":true,"name":"mate-shop","pane_id":"w9:p9","tab_id":"w9:t9","terminal_id":"t9","workspace_id":"w9"},"type":"agent_info"}}`

// SendText is `pane send-text`, which Herdr addresses by pane and not by
// agent name. The pane therefore has to be resolved live from `agent get`
// inside the same call; the recorded handle is only checked against it.
func TestHerdrSendTextResolvesThePaneLiveBeforeTyping(t *testing.T) {
	t.Parallel()
	var argvs [][]string
	rt := runtime.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if !hasSessionBeforeTerminator(spec.Args) {
			t.Fatalf("send-text missing --session in option region: %#v", spec.Args)
		}
		argvs = append(argvs, append([]string(nil), spec.Args...))
		switch {
		case argvHas(spec.Args, "agent", "get"):
			return process.Result{Stdout: []byte(agentGetJSON)}, nil
		case argvHas(spec.Args, "pane", "send-text"):
			return process.Result{Stdout: []byte(`{"id":"cli:pane:send-text","result":{"type":"ok"}}`)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-shop", Kind: harness.KindClaude, Tab: runtime.TabHandle{PaneID: "w9:p9"}}
	if err := rt.SendText(context.Background(), h, "say PONG"); err != nil {
		t.Fatal(err)
	}
	if len(argvs) != 2 {
		t.Fatalf("herdr calls = %d, want the resolve and the send: %#v", len(argvs), argvs)
	}
	got := strings.Join(argvs[1], " ")
	if !strings.Contains(got, "pane send-text w9:p9 \x1b[200~say PONG\x1b[201~") {
		t.Fatalf("argv = %q, want bracketed paste sent to the live pane", got)
	}
	if strings.Contains(got, "enter") || strings.Contains(got, "send-keys") {
		t.Fatalf("send-text submitted the line: %q", got)
	}
}

// A handle whose recorded pane no longer holds the agent is refused, not
// typed into: docs/mvp.md section 7 ("do not trust an old handle").
func TestHerdrSendTextRefusesAPaneThatMoved(t *testing.T) {
	t.Parallel()
	rt := runtime.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "get") {
			return process.Result{Stdout: []byte(agentGetJSON)}, nil
		}
		t.Fatalf("nothing may be typed after a pane mismatch: %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-shop", Tab: runtime.TabHandle{PaneID: "w1:p1"}}
	err := rt.SendText(context.Background(), h, "hello")
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("err = %v, want a state_conflict refusal", err)
	}
}

func TestHerdrSendTextRefusesTextThatIsNotOneLine(t *testing.T) {
	t.Parallel()
	rt := runtime.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		t.Fatalf("no Herdr call may be made for refused text: %#v", spec.Args)
		return process.Result{}, nil
	}})
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-shop"}
	for _, text := range []string{"", "two\nlines", "carriage\rreturn", "bell\a", "escape\x1b[201~"} {
		err := rt.SendText(context.Background(), h, text)
		var coded *observability.Error
		if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
			t.Fatalf("text %q: err = %v, want usage refusal", text, err)
		}
	}
	// The 0x1f from-app marker is the one control byte mate types on purpose.
	if err := runtime.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "get") {
			return process.Result{Stdout: []byte(agentGetJSON)}, nil
		}
		return process.Result{Stdout: []byte(`{"id":"cli:pane:send-text","result":{"type":"ok"}}`)}, nil
	}}).SendText(context.Background(), runtime.AgentHandle{Session: runtime.SessionHandle{Name: "lab"}, Name: "mate-shop"}, "\x1f digest"); err != nil {
		t.Fatalf("the marker prefix must be typeable: %v", err)
	}
}

func TestFakeSendTextRecordsTypingAndRunsTheScreenHook(t *testing.T) {
	t.Parallel()
	f := runtime.NewFake()
	session := runtime.SessionHandle{Name: "lab"}
	h := runtime.AgentHandle{Session: session, Name: "crew-1", Kind: harness.KindCodex, Tab: runtime.TabHandle{PaneID: "w1:p1"}}
	f.SeedAgent(h, runtime.AgentIdle)
	f.SetReadOutput(h, "empty")
	f.OnSendText = func(handle runtime.AgentHandle, text string) {
		f.SetReadOutput(handle, "typed: "+text)
	}
	if err := f.SendText(context.Background(), h, "hello"); err != nil {
		t.Fatal(err)
	}
	screen, err := f.ReadAgent(context.Background(), h, harness.ReadRecentUnwrapped, 10)
	if err != nil || screen != "typed: hello" {
		t.Fatalf("screen after hook = %q err=%v", screen, err)
	}
	if len(f.SentText) != 1 || f.SentText[0].Text != "hello" {
		t.Fatalf("sent text = %+v", f.SentText)
	}
	if err := f.SendText(context.Background(), runtime.AgentHandle{Session: session, Name: "nobody"}, "x"); !runtime.IsAgentNotFound(err) {
		t.Fatalf("missing agent err = %v, want agent_not_found", err)
	}
	f.SendTextErr = errors.New("forced")
	if err := f.SendText(context.Background(), h, "x"); err == nil || err.Error() != "forced" {
		t.Fatalf("SendTextErr not honoured: %v", err)
	}
}
