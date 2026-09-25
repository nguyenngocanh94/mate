package host

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
)

func TestGhosttyStageSplitsThenClosesOwnTerminal(t *testing.T) {
	t.Parallel()
	script := &ghosttyScript{left: "left", next: []string{"stage-1", "stage-2"}}
	fake := &process.FakeRunner{Handler: script.handle}
	h := Open(Ghostty, Options{Runner: fake, Herdr: "/opt/herdr"})
	if h == nil {
		t.Fatal("Open(Ghostty) is nil")
	}
	ctx := context.Background()
	first, err := h.Stage(ctx, StageTarget{Session: "mate-acme", AgentName: "mate-shop"})
	if err != nil {
		t.Fatalf("first Stage: %v", err)
	}
	if first.PaneID != "stage-1" {
		t.Fatalf("first pane = %q, want stage-1", first.PaneID)
	}
	assertGhosttyHas(t, fake.Calls, ghosttyListScript)
	assertGhosttyHas(t, fake.Calls, `set command of cfg to "/opt/herdr --session mate-acme agent attach mate-shop"`)
	assertGhosttyHas(t, fake.Calls, "split leftPane direction right")
	assertGhosttyHas(t, fake.Calls, "focus leftPane")
	assertNoInputText(t, fake.Calls)

	second, err := h.Stage(ctx, StageTarget{Session: "mate-acme", AgentName: "crew-k3"})
	if err != nil {
		t.Fatalf("second Stage: %v", err)
	}
	if second.PaneID != "stage-2" {
		t.Fatalf("second pane = %q, want stage-2", second.PaneID)
	}
	assertGhosttyHas(t, fake.Calls, `whose id is "stage-1"`)
	assertGhosttyHas(t, fake.Calls, `set command of cfg to "/opt/herdr --session mate-acme agent attach crew-k3"`)
	assertNoInputText(t, fake.Calls)
}

func TestGhosttyStageResolvesHerdrOnPATH(t *testing.T) {
	t.Parallel()
	script := &ghosttyScript{left: "left", next: []string{"stage-1"}}
	fake := &process.FakeRunner{Handler: script.handle}
	h := Open(Ghostty, Options{Runner: fake, Herdr: "sh"})
	_, err := h.Stage(context.Background(), StageTarget{Session: "mate-acme", AgentName: "mate-shop"})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	for _, c := range fake.Calls {
		src := string(c.Stdin)
		if !strings.Contains(src, "set command of cfg to") {
			continue
		}
		if strings.Contains(src, `set command of cfg to "sh --session`) ||
			strings.Contains(src, `set command of cfg to "direct:`) {
			t.Fatalf("command = %s, want absolute path without direct:", src)
		}
		if !strings.Contains(src, "/sh --session mate-acme agent attach mate-shop\"") {
			t.Fatalf("command = %s, want absolute sh", src)
		}
		return
	}
	t.Fatal("no split command")
}

func TestGhosttyEnsureSplitCreatesAnEmptyRightPane(t *testing.T) {
	t.Parallel()
	script := &ghosttyScript{left: "left", next: []string{"stage-1", "stage-2"}}
	fake := &process.FakeRunner{Handler: script.handle}
	h := Open(Ghostty, Options{Runner: fake})
	got, err := h.EnsureSplit(context.Background())
	if err != nil {
		t.Fatalf("EnsureSplit: %v", err)
	}
	if got.PaneID != "stage-1" {
		t.Fatalf("pane = %q, want stage-1", got.PaneID)
	}
	assertGhosttyHas(t, fake.Calls, ghosttySplitEmptyScript)
	for _, c := range fake.Calls {
		if strings.Contains(string(c.Stdin), "set command of cfg") {
			t.Fatalf("EnsureSplit must not attach an agent: %s", c.Stdin)
		}
	}
	again, err := h.EnsureSplit(context.Background())
	if err != nil {
		t.Fatalf("second EnsureSplit: %v", err)
	}
	if again.PaneID != "stage-1" {
		t.Fatalf("second pane = %q, want the same stage-1", again.PaneID)
	}
	splits := 0
	for _, c := range fake.Calls {
		if strings.Contains(string(c.Stdin), "split leftPane") {
			splits++
		}
	}
	if splits != 1 {
		t.Fatalf("split calls = %d, want 1", splits)
	}
}

func TestGhosttyStageRefusesAForeignTerminal(t *testing.T) {
	t.Parallel()
	script := &ghosttyScript{left: "left", extra: "nvim", next: []string{"stage-1"}}
	fake := &process.FakeRunner{Handler: script.handle}
	h := Open(Ghostty, Options{Runner: fake})
	_, err := h.Stage(context.Background(), StageTarget{Session: "mate-acme", AgentName: "mate-shop"})
	if err == nil {
		t.Fatal("Stage accepted a foreign pane")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("error = %v, want state_conflict", err)
	}
	for _, call := range fake.Calls {
		src := string(call.Stdin)
		if strings.Contains(src, "split leftPane") || strings.Contains(src, "close (") {
			t.Fatalf("foreign pane must not close or split: %s", src)
		}
	}
}

type ghosttyScript struct {
	left  string
	extra string
	next  []string
	live  []string
}

func (s *ghosttyScript) handle(_ context.Context, spec process.Spec) (process.Result, error) {
	src := string(spec.Stdin)
	switch {
	case strings.Contains(src, "repeat with t in terminals"):
		ids := []string{s.left}
		ids = append(ids, s.live...)
		if s.extra != "" {
			ids = append(ids, s.extra)
		}
		return process.Result{Stdout: []byte(strings.Join(ids, "\n") + "\n")}, nil
	case strings.Contains(src, "close (first terminal"):
		return process.Result{}, nil
	case strings.Contains(src, "split leftPane direction right"):
		if len(s.next) == 0 {
			return process.Result{ExitCode: 1, Stderr: []byte("no more panes")}, nil
		}
		id := s.next[0]
		s.next = s.next[1:]
		s.live = []string{id}
		return process.Result{Stdout: []byte(id + "\n")}, nil
	default:
		return process.Result{ExitCode: 1, Stderr: []byte("unexpected script")}, nil
	}
}

func assertGhosttyHas(t *testing.T, calls []process.Spec, snippet string) {
	t.Helper()
	for _, c := range calls {
		if c.Name == "osascript" && bytes.Contains(c.Stdin, []byte(snippet)) {
			return
		}
	}
	t.Fatalf("no osascript stdin contained %q in %d calls", snippet, len(calls))
}

func assertNoInputText(t *testing.T, calls []process.Spec) {
	t.Helper()
	for _, c := range calls {
		if bytes.Contains(c.Stdin, []byte("input text")) {
			t.Fatalf("input text is forbidden: %s", c.Stdin)
		}
	}
}
