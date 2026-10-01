package runtime

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

func TestMapHerdrErrorKeepsNotFoundCasesDistinct(t *testing.T) {
	t.Parallel()
	if got := MapHerdrError(HerdrAgentNotFound); got != observability.CodeNotFound {
		t.Fatalf("agent_not_found = %q, want not_found", got)
	}
	if got := MapHerdrError(HerdrAgentPaneNotFound); got != observability.CodeUsage {
		t.Fatalf("agent_pane_not_found = %q, want usage (must not collapse into not_found)", got)
	}
	if MapHerdrError(HerdrAgentNotFound) == MapHerdrError(HerdrAgentPaneNotFound) {
		t.Fatal("the two not-found Herdr codes must not share a taxonomy bucket")
	}
}

func TestMapHerdrErrorTaxonomy(t *testing.T) {
	t.Parallel()
	cases := map[string]observability.Code{
		HerdrAgentBlocked:        observability.CodeTargetBlocked,
		HerdrAgentNotReady:       observability.CodeTargetBlocked,
		HerdrTimeout:             observability.CodeTimeout,
		HerdrAgentNameTaken:      observability.CodeAlreadyExists,
		HerdrInvalidAgentName:    observability.CodeUsage,
		HerdrWorkspaceNotFound:   observability.CodeNotFound,
		HerdrTabNotFound:         observability.CodeNotFound,
		HerdrInvalidAgentTimeout: observability.CodeUsage,
		HerdrAgentPaneBusy:       observability.CodeStateConflict,
		"nope":                   observability.CodeUnknown,
	}
	for code, want := range cases {
		if got := MapHerdrError(code); got != want {
			t.Errorf("MapHerdrError(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestIsAgentNotFoundDoesNotTreatAMissingPaneAsGone(t *testing.T) {
	t.Parallel()
	if !IsAgentNotFound(NewHerdrError(HerdrAgentNotFound, "agent missing")) {
		t.Fatal("agent_not_found is the confirmation that the name is gone")
	}
	if IsAgentNotFound(NewHerdrError(HerdrAgentPaneNotFound, "pane missing")) {
		t.Fatal("a missing pane is not proof the agent is gone")
	}
	if IsAgentNotFound(nil) || IsAgentNotFound(fmt.Errorf("nope")) {
		t.Fatal("unrelated errors are not a confirmed stop")
	}
}

// IsServerNotRunning separates positive proof the session is down from the
// other runtime-unavailable failures - the executable refusing to run, a
// transport fault - which say nothing about the session. The stage refusal
// and the observer's notice word the first as a stopped session and must
// leave the rest as their own sentence.
func TestIsServerNotRunningNamesOnlyTheDownServer(t *testing.T) {
	t.Parallel()
	if !IsServerNotRunning(NewHerdrError(HerdrServerNotRunning, "no herdr server is running")) {
		t.Fatal("server_not_running is the proof the session is down")
	}
	// An executable that would not run is runtime-unavailable too, but the
	// session may well be fine; it must not read as "the session is down".
	execFailed := observability.WrapError(observability.CodeRuntimeUnavailable,
		"herdr executable failed to run", fmt.Errorf("exec: herdr: executable file not found"))
	if IsServerNotRunning(execFailed) {
		t.Fatal("a herdr that would not run is not proof the session is down")
	}
	if IsServerNotRunning(nil) || IsServerNotRunning(fmt.Errorf("nope")) {
		t.Fatal("unrelated errors are not a stopped server")
	}
}

func TestNewHerdrErrorPreservesOriginalCode(t *testing.T) {
	t.Parallel()
	err := NewHerdrError(HerdrAgentPaneNotFound, "pane missing")
	if err.Code != observability.CodeUsage {
		t.Fatalf("code = %q", err.Code)
	}
	if err.Details["herdr_code"] != HerdrAgentPaneNotFound {
		t.Fatalf("details = %#v", err.Details)
	}
	notFound := NewHerdrError(HerdrAgentNotFound, "agent missing")
	if notFound.Code != observability.CodeNotFound {
		t.Fatalf("agent_not_found mapped to %q", notFound.Code)
	}
}

func TestAgentStartArgvPutsSessionBeforeTerminator(t *testing.T) {
	t.Parallel()
	args, err := AgentStartArgv("mate-ws", "m-mate_001", "claude", "w1:p1", []string{"--append-system-prompt-file", "/abs/ctx.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !SessionBeforeTerminator(args) {
		t.Fatalf("session not before --: %#v", args)
	}
	labHelper := []string{"agent", "start", "--kind", "claude", "--pane", "w1:p1", "--", "--append-system-prompt-file", "/abs/ctx.md", "--session", "mate-ws"}
	if SessionBeforeTerminator(labHelper) {
		t.Fatalf("trailing --session after -- must not count as delivered: %#v", labHelper)
	}
	// The production builder itself must not place --session after --.
	seenSessionAfter := false
	inOptions := true
	for _, a := range args {
		if a == "--" {
			inOptions = false
			continue
		}
		if !inOptions && a == "--session" {
			seenSessionAfter = true
		}
	}
	if seenSessionAfter {
		t.Fatalf("production argv leaked --session after --: %#v", args)
	}
}

func TestSocketPaths(t *testing.T) {
	t.Parallel()
	if got := DefaultSocketPath("/Users/x/.config"); got != "/Users/x/.config/herdr/herdr.sock" {
		t.Fatalf("default = %q", got)
	}
	if got := NamedSocketPath("/Users/x/.config", "fm-lab-x"); got != "/Users/x/.config/herdr/sessions/fm-lab-x/herdr.sock" {
		t.Fatalf("named = %q", got)
	}
}

func TestCheckUnixSocketPathRefusesPathLongerThanSunPath(t *testing.T) {
	t.Parallel()
	p := "/" + strings.Repeat("x", 200) + "/herdr/sessions/mate-0123456789abcdef0123456789abcdef/herdr.sock"
	err := checkUnixSocketPath(p)
	if err == nil {
		t.Fatal("expected sun_path overflow to be refused")
	}
	if !strings.Contains(err.Error(), "sun_path") {
		t.Fatalf("err = %v", err)
	}
	short := NamedSocketPath("/Users/x/.config", "fm-lab-x")
	if err := checkUnixSocketPath(short); err != nil {
		t.Fatalf("a normal named socket must fit: %v", err)
	}
}

// Herdr's CLI contract (ADR 0003) bounds agent start --timeout to > 3000 and
// <= 300000 ms; an out-of-range value must be unconstructible, and zero must
// mean the flag is not emitted at all.
func TestAgentStartCommandEnforcesTimeoutBounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		timeout time.Duration
		ok      bool
	}{
		{"zero emits no flag", 0, true},
		{"negative", -time.Second, false},
		{"below minimum", 2 * time.Second, false},
		{"minimum is exclusive", 3000 * time.Millisecond, false},
		{"smallest valid", 3001 * time.Millisecond, true},
		{"maximum is inclusive", 300000 * time.Millisecond, true},
		{"above maximum", 10 * time.Minute, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args, err := AgentStartArgvTimeout("mate-ws", "m-mate_001", "claude", "w1:p1", tc.timeout, nil)
			if !tc.ok {
				if err == nil {
					t.Fatalf("out-of-range timeout emitted argv: %#v", args)
				}
				if observability.ExitCode(err) != observability.ExitUsage {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			hasFlag := false
			for i, a := range args {
				if a == "--timeout" {
					hasFlag = true
					if i+1 >= len(args) || args[i+1] != fmt.Sprintf("%d", tc.timeout.Milliseconds()) {
						t.Fatalf("argv = %#v", args)
					}
				}
			}
			if hasFlag != (tc.timeout != 0) {
				t.Fatalf("timeout %v: --timeout presence = %v, argv %#v", tc.timeout, hasFlag, args)
			}
		})
	}
}

// An argv with an empty --session or --pane reproduces the G1
// agent_pane_not_found shape, so such a command must be unconstructible.
func TestAgentStartCommandRefusesEmptyRequiredValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                       string
		session, agent, kind, pane string
	}{
		{name: "no session", session: "", agent: "m-mate_001", kind: "claude", pane: "w1:p1"},
		{name: "blank session", session: "  ", agent: "m-mate_001", kind: "claude", pane: "w1:p1"},
		{name: "no pane", session: "mate-ws", agent: "m-mate_001", kind: "claude", pane: ""},
		{name: "no name", session: "mate-ws", agent: "", kind: "claude", pane: "w1:p1"},
		{name: "no kind", session: "mate-ws", agent: "m-mate_001", kind: "", pane: "w1:p1"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewAgentStartCommand(tc.session, tc.agent, tc.kind, tc.pane, 0, nil); err == nil {
				t.Fatal("expected a fail-closed constructor error")
			}
			args, err := AgentStartArgv(tc.session, tc.agent, tc.kind, tc.pane, nil)
			if err == nil {
				t.Fatalf("emitted argv for an incomplete start: %#v", args)
			}
			if args != nil {
				t.Fatalf("argv must be nil on refusal: %#v", args)
			}
		})
	}
}

// Herdr receives kind as --kind; a kind it could not read as one word must
// be unconstructible (counter-review of PR #3). Which words are harnesses is
// the registry's to say (harness.Registry), and a start's kind comes from a
// sealed LaunchSpec only a registered profile fills.
func TestNewAgentStartCommandRejectsMalformedKind(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{" ", "CLAUDE X", "Claude", "--pane", "claude ", "a/b"} {
		if _, err := NewAgentStartCommand("s", "n", kind, "p", 0, nil); err == nil {
			t.Fatalf("kind %q must not construct an agent start command", kind)
		}
	}
	cmd, err := NewAgentStartCommand("s", "n", "claude", "p", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	argv := cmd.Argv()
	for i, a := range argv {
		if a == "--kind" && argv[i+1] != "claude" {
			t.Fatalf("argv = %#v", argv)
		}
	}
	if _, err := NewAgentStartCommand("s", "n", "codex", "p", 0, nil); err != nil {
		t.Fatalf("codex is a supported kind: %v", err)
	}
}

// The zero AgentStartCommand is expressible by any caller (Go allows every
// exported struct's zero value); it must emit nil rather than argv carrying
// empty required values, which G1 observed surfacing as agent_pane_not_found.
func TestZeroAgentStartCommandEmitsNoArgv(t *testing.T) {
	t.Parallel()
	if argv := (AgentStartCommand{}).Argv(); argv != nil {
		t.Fatalf("zero command emitted argv %#v", argv)
	}
}
