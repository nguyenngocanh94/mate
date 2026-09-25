package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		Runner:  fake,
		Pane:    "10",
		Herdr:   "herdr",
		WezTerm: "wezterm",
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
		{"cli", "list", "--format", "json"},
		{"cli", "split-pane", "--pane-id", "10", "--right", "--percent", "70", "--", "herdr", "--session", "mate-acme", "agent", "attach", "mate-shop", "--takeover"},
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
	assertWeztermSeq(t, fake.Calls[4:], [][]string{
		{"cli", "get-pane-direction", "--pane-id", "10", "Right"},
		{"cli", "kill-pane", "--pane-id", "20"},
		{"cli", "list", "--format", "json"},
		{"cli", "split-pane", "--pane-id", "10", "--right", "--percent", "70", "--", "herdr", "--session", "mate-acme", "agent", "attach", "crew-k3", "--takeover"},
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
	h := Open(WezTerm, Options{Runner: fake, Pane: "10", WezTerm: "wezterm"})
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
	h := Open(WezTerm, Options{Runner: fake, Pane: "10", WezTerm: "wezterm"})
	got, err := h.EnsureSplit(context.Background())
	if err != nil {
		t.Fatalf("EnsureSplit: %v", err)
	}
	if got.PaneID != "20" {
		t.Fatalf("pane = %q, want 20", got.PaneID)
	}
	assertWeztermSeq(t, fake.Calls, [][]string{
		{"cli", "get-pane-direction", "--pane-id", "10", "Right"},
		{"cli", "list", "--format", "json"},
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
	h := Open(WezTerm, Options{Runner: fake, Pane: "10", Herdr: "herdr", WezTerm: "wezterm"})
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
	assertWeztermSeq(t, fake.Calls[4:], [][]string{
		{"cli", "get-pane-direction", "--pane-id", "10", "Right"},
		{"cli", "kill-pane", "--pane-id", "20"},
		{"cli", "list", "--format", "json"},
		{"cli", "split-pane", "--pane-id", "10", "--right", "--percent", "70", "--", "herdr", "--session", "mate-acme", "agent", "attach", "mate-shop", "--takeover"},
		{"cli", "activate-pane", "--pane-id", "10"},
	})
}

func TestWezTermEnsureSplitRefusesAForeignPane(t *testing.T) {
	t.Parallel()
	fake := &process.FakeRunner{Handler: (&weztermScript{
		self: "10", right: "99", foreign: true,
	}).handle}
	h := Open(WezTerm, Options{Runner: fake, Pane: "10", WezTerm: "wezterm"})
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
	h := Open(WezTerm, Options{Runner: &process.FakeRunner{}, Pane: "1", WezTerm: "wezterm"})
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
	// cols is the console pane's width `cli list` reports; 0 answers the
	// list with an error, as a mux that cannot be listed does.
	cols int
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
	case containsArg(args, "list"):
		if s.cols == 0 {
			return process.Result{ExitCode: 1, Stderr: []byte("no mux")}, nil
		}
		return process.Result{Stdout: []byte(`[{"pane_id":` + s.self + `,"size":{"cols":` + strconv.Itoa(s.cols) + `,"rows":36}},{"pane_id":99,"size":{"cols":7,"rows":3}}]`)}, nil
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

// TestWezTermRunsTheCLIWezTermNamesForItsPanes: WezTerm installed as an
// .app puts no `wezterm` on PATH, but sets WEZTERM_EXECUTABLE_DIR for every
// pane it spawns, and the CLI sits there beside the GUI. WEZTERM_EXECUTABLE
// is the GUI itself and has no `cli`. Calling the bare name failed the
// split (found live on 2026-09-25: `mate console` in WezTerm.app drew no
// pane; env measured in a WezTerm 20240203 pane).
func TestWezTermRunsTheCLIWezTermNamesForItsPanes(t *testing.T) {
	t.Parallel()
	script := &weztermScript{self: "10", next: 20}
	fake := &process.FakeRunner{Handler: script.handle}
	dir := t.TempDir()
	exe := filepath.Join(dir, "wezterm")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := Open(WezTerm, Options{Runner: fake, Herdr: "herdr", Env: func(k string) string {
		switch k {
		case "WEZTERM_PANE":
			return "10"
		case "WEZTERM_EXECUTABLE_DIR":
			return dir
		case "WEZTERM_EXECUTABLE":
			return filepath.Join(dir, "wezterm-gui")
		}
		return ""
	}})
	if _, err := h.EnsureSplit(context.Background()); err != nil {
		t.Fatalf("EnsureSplit: %v", err)
	}
	if len(fake.Calls) == 0 {
		t.Fatal("EnsureSplit ran nothing")
	}
	for _, c := range fake.Calls {
		if c.Name != exe {
			t.Fatalf("ran %q, want the CLI in WEZTERM_EXECUTABLE_DIR %q", c.Name, exe)
		}
	}
}

// TestWezTermSplitLeavesTheConsoleItsColumns: mate is the left ~20% pane,
// 40 to 48 columns (the console design). The stage is split off in cells so
// the console keeps them, whatever the window's width: 70% of an 80-column
// window left mate 23 columns and the too-small screen (found live on
// 2026-09-25).
func TestWezTermSplitLeavesTheConsoleItsColumns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cols int
		want string // --cells of the stage
	}{
		{80, "39"},   // mate keeps 40
		{200, "159"}, // 20% is 40
		{300, "251"}, // 20% is 60, capped at 48
	} {
		script := &weztermScript{self: "10", next: 20, cols: tc.cols}
		fake := &process.FakeRunner{Handler: script.handle}
		h := Open(WezTerm, Options{Runner: fake, Pane: "10", Herdr: "herdr", WezTerm: "wezterm"})
		if _, err := h.EnsureSplit(context.Background()); err != nil {
			t.Fatalf("%d cols: EnsureSplit: %v", tc.cols, err)
		}
		var split []string
		for _, c := range fake.Calls {
			if containsArg(c.Args, "split-pane") {
				split = c.Args
			}
		}
		if got := argAfter(split, "--cells"); got != tc.want || containsArg(split, "--percent") {
			t.Fatalf("%d cols: split %q, want --cells %s", tc.cols, split, tc.want)
		}
	}
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// TestWezTermFindsTheAppBundleCLIWithoutItsEnv: a pane whose environment
// lost WEZTERM_EXECUTABLE_DIR (tmux or ssh inside WezTerm) and has no
// `wezterm` on PATH still reaches the CLI in the app bundle rather than
// failing with `exec: "wezterm": executable file not found` (reported
// 2026-09-25).
func TestWezTermFindsTheAppBundleCLIWithoutItsEnv(t *testing.T) {
	t.Parallel()
	bundle := filepath.Join(t.TempDir(), "wezterm")
	if err := os.WriteFile(bundle, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := weztermCLI(Options{Env: func(string) string { return "" }}, func(string) (string, error) {
		return "", errors.New("not found")
	}, []string{filepath.Join(t.TempDir(), "missing"), bundle})
	if got != bundle {
		t.Fatalf("CLI = %q, want the bundle's %q", got, bundle)
	}
	// PATH wins over the bundle, and the pane's own dir wins over both.
	got = weztermCLI(Options{Env: func(string) string { return "" }}, func(string) (string, error) {
		return "/usr/local/bin/wezterm", nil
	}, []string{bundle})
	if got != "/usr/local/bin/wezterm" {
		t.Fatalf("CLI = %q, want the one on PATH", got)
	}
	dir := filepath.Dir(bundle)
	got = weztermCLI(Options{Env: func(k string) string {
		if k == "WEZTERM_EXECUTABLE_DIR" {
			return dir
		}
		return ""
	}}, func(string) (string, error) { return "/usr/local/bin/wezterm", nil }, nil)
	if got != bundle {
		t.Fatalf("CLI = %q, want the pane's own %q", got, bundle)
	}
}
