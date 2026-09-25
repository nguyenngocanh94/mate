package host

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
)

func TestWezTermStageSplitsThenReplacesOwnPane(t *testing.T) {
	t.Parallel()
	script := &weztermScript{self: "10", next: 20}
	fake := &process.FakeRunner{Handler: script.handle}
	h := Open(WezTerm, Options{
		Runner: fake,
		Pane:   "10",
		Herdr:  "herdr",
	})
	if h == nil {
		t.Fatal("Open(WezTerm) is nil")
	}
	ctx := context.Background()
	target := StageTarget{Session: "mate-acme", AgentName: "mate-shop"}

	first, err := h.Stage(ctx, target)
	if err != nil {
		t.Fatalf("first Stage: %v", err)
	}
	if first.PaneID != "20" {
		t.Fatalf("first pane = %q, want 20", first.PaneID)
	}
	assertWeztermSeq(t, fake.Calls, [][]string{
		{"cli", "get-pane-direction", "--pane-id", "10", "Right"},
		{"cli", "split-pane", "--pane-id", "10", "--right", "--percent", "70", "--", "herdr", "--session", "mate-acme", "agent", "attach", "mate-shop"},
		{"cli", "activate-pane", "--pane-id", "10"},
	})
	assertNoSendText(t, fake.Calls)

	target.AgentName = "crew-k3"
	second, err := h.Stage(ctx, target)
	if err != nil {
		t.Fatalf("second Stage: %v", err)
	}
	if second.PaneID != "21" {
		t.Fatalf("second pane = %q, want 21", second.PaneID)
	}
	assertWeztermSeq(t, fake.Calls[3:], [][]string{
		{"cli", "get-pane-direction", "--pane-id", "10", "Right"},
		{"cli", "kill-pane", "--pane-id", "20"},
		{"cli", "split-pane", "--pane-id", "10", "--right", "--percent", "70", "--", "herdr", "--session", "mate-acme", "agent", "attach", "crew-k3"},
		{"cli", "activate-pane", "--pane-id", "10"},
	})
	assertNoSendText(t, fake.Calls)
}

func TestWezTermStageRefusesAForeignRightPane(t *testing.T) {
	t.Parallel()
	fake := &process.FakeRunner{Handler: (&weztermScript{
		self:    "10",
		right:   "99",
		next:    20,
		foreign: true,
	}).handle}
	h := Open(WezTerm, Options{Runner: fake, Pane: "10"})
	_, err := h.Stage(context.Background(), StageTarget{Session: "mate-acme", AgentName: "mate-shop"})
	if err == nil {
		t.Fatal("Stage accepted a foreign pane")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("error = %v, want state_conflict", err)
	}
	for _, call := range fake.Calls {
		if containsArg(call.Args, "kill-pane") || containsArg(call.Args, "split-pane") {
			t.Fatalf("foreign pane must not kill or split: %+v", fake.Calls)
		}
	}
}

func TestWezTermEnsureSplitCreatesAnEmptyRightPane(t *testing.T) {
	t.Parallel()
	script := &weztermScript{self: "10", next: 20}
	fake := &process.FakeRunner{Handler: script.handle}
	h := Open(WezTerm, Options{Runner: fake, Pane: "10"})
	got, err := h.EnsureSplit(context.Background())
	if err != nil {
		t.Fatalf("EnsureSplit: %v", err)
	}
	if got.PaneID != "20" {
		t.Fatalf("pane = %q, want 20", got.PaneID)
	}
	assertWeztermSeq(t, fake.Calls, [][]string{
		{"cli", "get-pane-direction", "--pane-id", "10", "Right"},
		{"cli", "split-pane", "--pane-id", "10", "--right", "--percent", "70"},
		{"cli", "activate-pane", "--pane-id", "10"},
	})
	for _, c := range fake.Calls {
		if containsArg(c.Args, "herdr") {
			t.Fatalf("EnsureSplit must not attach an agent: %+v", c.Args)
		}
	}
	again, err := h.EnsureSplit(context.Background())
	if err != nil {
		t.Fatalf("second EnsureSplit: %v", err)
	}
	if again.PaneID != "20" {
		t.Fatalf("second pane = %q, want the same 20", again.PaneID)
	}
	splits := 0
	for _, c := range fake.Calls {
		if containsArg(c.Args, "split-pane") {
			splits++
		}
	}
	if splits != 1 {
		t.Fatalf("split-pane calls = %d, want 1", splits)
	}
}

func TestWezTermEnsureSplitThenStageReplacesTheEmptyPane(t *testing.T) {
	t.Parallel()
	script := &weztermScript{self: "10", next: 20}
	fake := &process.FakeRunner{Handler: script.handle}
	h := Open(WezTerm, Options{Runner: fake, Pane: "10", Herdr: "herdr"})
	if _, err := h.EnsureSplit(context.Background()); err != nil {
		t.Fatalf("EnsureSplit: %v", err)
	}
	got, err := h.Stage(context.Background(), StageTarget{Session: "mate-acme", AgentName: "mate-shop"})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if got.PaneID != "21" {
		t.Fatalf("pane = %q, want 21", got.PaneID)
	}
	assertWeztermSeq(t, fake.Calls[3:], [][]string{
		{"cli", "get-pane-direction", "--pane-id", "10", "Right"},
		{"cli", "kill-pane", "--pane-id", "20"},
		{"cli", "split-pane", "--pane-id", "10", "--right", "--percent", "70", "--", "herdr", "--session", "mate-acme", "agent", "attach", "mate-shop"},
		{"cli", "activate-pane", "--pane-id", "10"},
	})
}

func TestWezTermEnsureSplitRefusesAForeignPane(t *testing.T) {
	t.Parallel()
	fake := &process.FakeRunner{Handler: (&weztermScript{
		self: "10", right: "99", foreign: true,
	}).handle}
	h := Open(WezTerm, Options{Runner: fake, Pane: "10"})
	_, err := h.EnsureSplit(context.Background())
	if err == nil {
		t.Fatal("EnsureSplit accepted a foreign pane")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("error = %v, want state_conflict", err)
	}
}

func TestWezTermStageRejectsEmptyTarget(t *testing.T) {
	t.Parallel()
	h := Open(WezTerm, Options{Runner: &process.FakeRunner{}, Pane: "1"})
	_, err := h.Stage(context.Background(), StageTarget{})
	if err == nil {
		t.Fatal("empty target succeeded")
	}
}

type weztermScript struct {
	self    string
	right   string
	next    int
	foreign bool
	spawned string
}

func (s *weztermScript) handle(_ context.Context, spec process.Spec) (process.Result, error) {
	args := spec.Args
	switch {
	case containsArg(args, "get-pane-direction"):
		if s.foreign {
			return process.Result{Stdout: []byte(s.right)}, nil
		}
		return process.Result{Stdout: []byte(s.spawned)}, nil
	case containsArg(args, "kill-pane"):
		s.spawned = ""
		return process.Result{}, nil
	case containsArg(args, "activate-pane"):
		return process.Result{}, nil
	case containsArg(args, "split-pane"):
		id := s.next
		s.next++
		s.spawned = strconv.Itoa(id)
		return process.Result{Stdout: []byte(s.spawned + "\n")}, nil
	default:
		return process.Result{ExitCode: 1, Stderr: []byte("unexpected " + strings.Join(args, " "))}, nil
	}
}

func assertWeztermSeq(t *testing.T, calls []process.Spec, want [][]string) {
	t.Helper()
	if len(calls) < len(want) {
		t.Fatalf("calls = %d, want at least %d: %+v", len(calls), len(want), calls)
	}
	for i, args := range want {
		if calls[i].Name != "wezterm" {
			t.Fatalf("call %d binary = %q, want wezterm", i, calls[i].Name)
		}
		if !equalArgs(calls[i].Args, args) {
			t.Fatalf("call %d args = %#v, want %#v", i, calls[i].Args, args)
		}
	}
}

func assertNoSendText(t *testing.T, calls []process.Spec) {
	t.Helper()
	for _, c := range calls {
		if containsArg(c.Args, "send-text") {
			t.Fatalf("send-text is forbidden: %+v", c.Args)
		}
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
