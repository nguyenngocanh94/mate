package send_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/harness/harnesstest"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/fixture"
	"github.com/nguyenngocanh94/mate/internal/send"
)

// scripted is a send.Runtime whose pane shows a different screen at each
// step. Screens are consumed one per ReadAgent, which is exactly how a send
// observes a pane: once to decide, then once after every enter.
type scripted struct {
	screens []string
	reads   int
	typed   []string
	keys    [][]string
	waits   int
	waitErr error
	status  runtime.AgentStatus
	textErr error
	keysErr error
	// onType, if set, replaces the remaining screens when text is typed,
	// modelling a harness redrawing the composer with the new line in it.
	onType func(text string) []string
	// onEnter, if set, replaces the remaining screens on the nth enter.
	onEnter func(press int) []string
	lines   []int
	sources []harness.ReadSource
}

func (s *scripted) ReadAgentStyled(ctx context.Context, handle runtime.AgentHandle, source harness.ReadSource, lines int) (string, error) {
	// The scripted screens carry no attributes, which is what a screen with
	// nothing drawn faint looks like; send.Send reads this one.
	return s.ReadAgent(ctx, handle, source, lines)
}

func (s *scripted) ReadAgent(_ context.Context, _ runtime.AgentHandle, source harness.ReadSource, lines int) (string, error) {
	s.lines = append(s.lines, lines)
	s.sources = append(s.sources, source)
	if s.reads >= len(s.screens) {
		return s.screens[len(s.screens)-1], nil
	}
	out := s.screens[s.reads]
	s.reads++
	return out, nil
}

func (s *scripted) SendText(_ context.Context, _ runtime.AgentHandle, text string) error {
	if s.textErr != nil {
		return s.textErr
	}
	s.typed = append(s.typed, text)
	if s.onType != nil {
		s.screens = append(s.screens[:s.reads], s.onType(text)...)
	}
	return nil
}

func (s *scripted) SendKeys(_ context.Context, _ runtime.AgentHandle, keys []string) error {
	if s.keysErr != nil {
		return s.keysErr
	}
	s.keys = append(s.keys, keys)
	if s.onEnter != nil {
		s.screens = append(s.screens[:s.reads], s.onEnter(len(s.keys))...)
	}
	return nil
}

func (s *scripted) WaitAgent(_ context.Context, _ runtime.AgentHandle, _ runtime.WaitCondition) (runtime.ObservedAgent, error) {
	s.waits++
	if s.waitErr != nil {
		return runtime.ObservedAgent{}, s.waitErr
	}
	return runtime.ObservedAgent{Status: s.status}, nil
}

var _ send.Runtime = (*scripted)(nil)

const rule = "─────────────────────────────────────────"

// claudeScreen renders Claude's composer box holding content, the way the
// captures under testdata/screens draw it (U+00A0 after the glyph).
func claudeScreen(content string) string {
	return "some transcript\n" + rule + "\n❯ " + content + "\n" + rule + "\n  Sonnet 5 · medium\n"
}

func claudeBusyScreen() string {
	return "some transcript\n✶ Pollinating…\n" + rule + "\n❯ \n" + rule + "\n"
}

func testDeps(rt send.Runtime) (send.Deps, *[]time.Duration) {
	var slept []time.Duration
	return send.Deps{
		Harnesses: catalog.Default(),
		Runtime:   rt,
		Sleep: func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		},
	}, &slept
}

func target() runtime.AgentHandle {
	return runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: "fm-lab-test"},
		Name:    "crew-k3",
		Kind:    claude.KindClaude,
		Tab:     runtime.TabHandle{PaneID: "w1:p1"},
	}
}

func TestSendTypesOnceAndConfirmsTheComposerCleared(t *testing.T) {
	t.Parallel()
	rt := &scripted{screens: []string{claudeScreen(""), claudeScreen("")}}
	deps, slept := testDeps(rt)
	// Claude's own screens, read through the source Claude does not use, so
	// a read that spells recent-unwrapped instead of asking the profile
	// fails below.
	deps.Harnesses = harnesstest.ReadingVisible(deps.Harnesses)

	report, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "say PONG", send.Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !report.Delivered() {
		t.Fatalf("report does not read as delivered: %+v", report)
	}
	if len(rt.typed) != 1 || rt.typed[0] != "say PONG" {
		t.Fatalf("typed = %#v, want the line exactly once", rt.typed)
	}
	if len(rt.keys) != 1 || rt.keys[0][0] != "enter" {
		t.Fatalf("keys = %#v, want one enter", rt.keys)
	}
	if report.Before.State != send.StateEmpty || report.After.State != send.StateEmpty {
		t.Fatalf("states before/after = %s/%s", report.Before.State, report.After.State)
	}
	if report.Presses != 1 {
		t.Fatalf("presses = %d, want 1", report.Presses)
	}
	if got := (*slept)[0]; got != send.DefaultSettle {
		t.Fatalf("settle = %s, want %s", got, send.DefaultSettle)
	}
	for _, n := range rt.lines {
		if n != send.DefaultLines {
			t.Fatalf("pane read %d lines, want %d", n, send.DefaultLines)
		}
	}
	// Every read goes through the source the harness's screens were measured
	// through.
	if len(rt.sources) == 0 {
		t.Fatal("the send never read the pane")
	}
	for _, src := range rt.sources {
		if src != harness.ReadVisible {
			t.Fatalf("pane read through %q, want the profile's %q", src, harness.ReadVisible)
		}
	}
	want := []string{"classify", "type", "settle", "enter"}
	var got []string
	for _, s := range report.Steps {
		got = append(got, s.What)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("steps = %v, want %v", got, want)
	}
}

func TestSendRefusesAComposerHoldingSomeoneElsesText(t *testing.T) {
	t.Parallel()
	rt := &scripted{screens: []string{claudeScreen("half typed")}}
	deps, _ := testDeps(rt)

	report, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "say PONG", send.Options{})
	if !errors.Is(err, send.ErrComposerPending) {
		t.Fatalf("err = %v, want ErrComposerPending", err)
	}
	if len(rt.typed) != 0 || len(rt.keys) != 0 {
		t.Fatalf("a refused send touched the pane: typed=%#v keys=%#v", rt.typed, rt.keys)
	}
	if report.Before.Pending != "half typed" {
		t.Fatalf("report pending = %q", report.Before.Pending)
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("err code = %v, want state_conflict", err)
	}
	if coded.Details["pending"] != "half typed" {
		t.Fatalf("error details omit the pending text: %v", coded.Details)
	}
}

func TestSendRefusesABusyPaneUnlessTheCallerQueues(t *testing.T) {
	t.Parallel()
	rt := &scripted{screens: []string{claudeBusyScreen()}}
	deps, _ := testDeps(rt)
	_, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "say PONG", send.Options{})
	if !errors.Is(err, send.ErrAgentBusy) {
		t.Fatalf("err = %v, want ErrAgentBusy", err)
	}
	if len(rt.typed) != 0 {
		t.Fatalf("a busy refusal typed anyway: %#v", rt.typed)
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeTargetBlocked {
		t.Fatalf("err code = %v, want target_blocked", err)
	}

	queued := &scripted{screens: []string{claudeBusyScreen(), claudeScreen("")}}
	qdeps, _ := testDeps(queued)
	report, err := send.Send(context.Background(), qdeps, target(), claude.KindClaude, "say PONG", send.Options{QueueWhileBusy: true})
	if err != nil {
		t.Fatalf("queued send: %v", err)
	}
	if report.Before.State != send.StateBusy || len(queued.typed) != 1 {
		t.Fatalf("queued send did not type into the busy pane: %+v %#v", report.Before, queued.typed)
	}
}

func TestSendRefusesAScreenItCannotName(t *testing.T) {
	t.Parallel()
	dialog := "a dialog\n  1. one\n  2. two\n  Press enter\n"
	rt := &scripted{screens: []string{dialog}}
	deps, _ := testDeps(rt)
	_, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "say PONG", send.Options{})
	if !errors.Is(err, send.ErrComposerUnknown) {
		t.Fatalf("err = %v, want ErrComposerUnknown", err)
	}
	if len(rt.typed) != 0 || len(rt.keys) != 0 {
		t.Fatalf("an unreadable screen was typed into: %#v %#v", rt.typed, rt.keys)
	}
	var coded *observability.Error
	if !errors.As(err, &coded) {
		t.Fatal("unknown refusal is not coded")
	}
	tail, _ := coded.Details["screen_tail"].(string)
	if !strings.Contains(tail, "Press enter") {
		t.Fatalf("screen tail = %q, want the screen mate refused", tail)
	}
}

// A swallowed enter is retried - and only the enter. The text is never typed
// again, because it is still in the composer and would otherwise double.
func TestSendRetriesEnterOnlyAndSucceedsWhenTheComposerClears(t *testing.T) {
	t.Parallel()
	rt := &scripted{screens: []string{claudeScreen("")}}
	rt.onType = func(text string) []string {
		return []string{claudeScreen(text), claudeScreen(text), claudeScreen("")}
	}
	deps, slept := testDeps(rt)

	report, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "say PONG", send.Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if report.Presses != 3 {
		t.Fatalf("presses = %d, want three (two swallowed, the third landed)", report.Presses)
	}
	if len(rt.typed) != 1 {
		t.Fatalf("text was typed %d times, want exactly once", len(rt.typed))
	}
	for _, k := range rt.keys {
		if len(k) != 1 || k[0] != "enter" {
			t.Fatalf("a retry pressed something other than enter: %#v", rt.keys)
		}
	}
	if !report.Delivered() {
		t.Fatalf("report does not read as delivered: %+v", report)
	}
	// One settle, then one retry sleep per press.
	if len(*slept) != 4 || (*slept)[1] != send.DefaultRetrySleep {
		t.Fatalf("sleeps = %v", *slept)
	}
}

func TestSendReportsAnEnterThatNeverSubmits(t *testing.T) {
	t.Parallel()
	rt := &scripted{screens: []string{claudeScreen("")}}
	rt.onType = func(text string) []string { return []string{claudeScreen(text)} }
	deps, _ := testDeps(rt)

	report, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "say PONG", send.Options{})
	if !errors.Is(err, send.ErrEnterSwallowed) {
		t.Fatalf("err = %v, want ErrEnterSwallowed", err)
	}
	if report.Presses != send.DefaultRetries {
		t.Fatalf("presses = %d, want %d", report.Presses, send.DefaultRetries)
	}
	if report.Delivered() {
		t.Fatal("a swallowed enter must not read as delivered")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Details["pending"] != "say PONG" {
		t.Fatalf("error does not name the stranded line: %v", err)
	}
	tail, _ := coded.Details["screen_tail"].(string)
	if !strings.Contains(tail, "say PONG") {
		t.Fatalf("screen tail = %q, want the stranded composer", tail)
	}
}

func TestSendRefusesAMultiLineMessageBeforeTouchingThePane(t *testing.T) {
	t.Parallel()
	rt := &scripted{screens: []string{claudeScreen("")}}
	deps, _ := testDeps(rt)
	for _, text := range []string{"two\nlines", "carriage\rreturn", "", "   "} {
		_, err := send.Send(context.Background(), deps, target(), claude.KindClaude, text, send.Options{})
		var coded *observability.Error
		if !errors.As(err, &coded) || coded.Code != observability.CodeUsage {
			t.Fatalf("text %q: err = %v, want a usage refusal", text, err)
		}
	}
	if rt.reads != 0 {
		t.Fatalf("a refused message still read the pane %d times", rt.reads)
	}
}

func TestSendPrefixesTheFromAppMarkerOnRequest(t *testing.T) {
	t.Parallel()
	rt := &scripted{screens: []string{claudeScreen(""), claudeScreen("")}}
	deps, _ := testDeps(rt)
	report, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "signal: crews/k3.status", send.Options{Marker: true})
	if err != nil {
		t.Fatal(err)
	}
	if rt.typed[0] != send.Marker+"signal: crews/k3.status" {
		t.Fatalf("typed = %q, want the send.Marker prefix", rt.typed[0])
	}
	if report.Text != rt.typed[0] {
		t.Fatalf("report text = %q, want what was typed", report.Text)
	}
}

// A slash command opens a completion popup; an enter before the popup has
// settled selects nothing (firstmate bin/fm-send.sh).
func TestSendWaitsLongerBeforeSubmittingASlashCommand(t *testing.T) {
	t.Parallel()
	rt := &scripted{screens: []string{claudeScreen(""), claudeScreen("")}}
	deps, slept := testDeps(rt)
	report, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "/help", send.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Settled != send.SlashSettle || (*slept)[0] != send.SlashSettle {
		t.Fatalf("settle = %s / %v, want %s", report.Settled, *slept, send.SlashSettle)
	}

	plain := &scripted{screens: []string{claudeScreen(""), claudeScreen("")}}
	pdeps, pslept := testDeps(plain)
	if _, err := send.Send(context.Background(), pdeps, target(), claude.KindClaude, "hello", send.Options{}); err != nil {
		t.Fatal(err)
	}
	if (*pslept)[0] != send.DefaultSettle {
		t.Fatalf("plain settle = %v, want %s", *pslept, send.DefaultSettle)
	}
}

// Herdr's agent status is screen scraping (docs/mvp.md decision 8), so the
// optional confirmation may only ever downgrade to a warning.
func TestSendTreatsAnUnconfirmedWaitAsAWarningNotAFailure(t *testing.T) {
	t.Parallel()
	rt := &scripted{screens: []string{claudeScreen(""), claudeScreen("")}, waitErr: errors.New("timeout")}
	deps, _ := testDeps(rt)
	report, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "say PONG", send.Options{WaitForWorking: true})
	if err != nil {
		t.Fatalf("an unconfirmed wait failed the send: %v", err)
	}
	if rt.waits != 1 {
		t.Fatalf("waits = %d, want one", rt.waits)
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "working") {
		t.Fatalf("warnings = %#v", report.Warnings)
	}

	ok := &scripted{screens: []string{claudeScreen(""), claudeScreen("")}, status: runtime.AgentWorking}
	odeps, _ := testDeps(ok)
	rep, err := send.Send(context.Background(), odeps, target(), claude.KindClaude, "say PONG", send.Options{WaitForWorking: true})
	if err != nil || len(rep.Warnings) != 0 {
		t.Fatalf("confirmed wait: err=%v warnings=%#v", err, rep.Warnings)
	}
	last := rep.Steps[len(rep.Steps)-1]
	if last.What != "wait" || last.Detail != string(runtime.AgentWorking) {
		t.Fatalf("last step = %+v, want the observed status", last)
	}
}

// The Codex composer has no box; the same send drives it from its
// placeholder (internal/screen/fixture/testdata/screens/codex_empty.txt).
func TestSendDrivesACodexComposer(t *testing.T) {
	t.Parallel()
	empty := "  Tip: something\n\n› " + codex.CodexComposerPlaceholder + "\n\n  gpt-5.6-terra high · /repo\n"
	rt := &scripted{screens: []string{empty, empty}}
	deps, _ := testDeps(rt)
	report, err := send.Send(context.Background(), deps, target(), codex.KindCodex, "read brief.md", send.Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !report.Delivered() || len(rt.typed) != 1 {
		t.Fatalf("codex send: %+v %#v", report, rt.typed)
	}
}

func TestSendSurfacesRuntimeFailures(t *testing.T) {
	t.Parallel()
	boom := errors.New("herdr is down")
	typeFail := &scripted{screens: []string{claudeScreen("")}, textErr: boom}
	deps, _ := testDeps(typeFail)
	if _, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "x", send.Options{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the runtime failure", err)
	}
	keyFail := &scripted{screens: []string{claudeScreen("")}, keysErr: boom}
	kdeps, _ := testDeps(keyFail)
	report, err := send.Send(context.Background(), kdeps, target(), claude.KindClaude, "x", send.Options{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the runtime failure", err)
	}
	if !report.Typed {
		t.Fatal("the report must still say the text was typed before the enter failed")
	}
	if _, err := send.Send(context.Background(), send.Deps{}, target(), claude.KindClaude, "x", send.Options{}); err == nil {
		t.Fatal("a send with no runtime must be refused")
	}
}

// A kind no harness is registered for is refused before the pane is read:
// there is nothing to classify its composer with.
func TestSendRefusesAnUnregisteredKind(t *testing.T) {
	rt := &scripted{screens: []string{claudeBusyScreen()}}
	deps, _ := testDeps(rt)
	reg, err := harness.NewRegistry(nil, codex.Codex{})
	if err != nil {
		t.Fatal(err)
	}
	deps.Harnesses = reg
	if _, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "x", send.Options{}); err == nil {
		t.Fatal("a send to a kind the registry does not hold was not refused")
	}
	if rt.reads != 0 || len(rt.typed) != 0 {
		t.Fatalf("reads %d, typed %v: the refusal must come before the pane is touched", rt.reads, rt.typed)
	}
}

// observing is a screen.Observer that counts its calls and, when says is
// set, answers with that observation instead of the fixture's.
type observing struct {
	calls int
	says  *screen.Observation
}

func (o *observing) Observe(ctx context.Context, profile harness.ScreenProfile, pane string) (screen.Observation, error) {
	o.calls++
	if o.says != nil {
		return *o.says, nil
	}
	return fixture.New().Observe(ctx, profile, pane)
}

// TestSendDecidesFromTheObserverOnce: the readiness decision is the
// Observer's reading, taken once before typing; the re-reads after each
// enter compare the composer with the typed text and never ask it again.
func TestSendDecidesFromTheObserverOnce(t *testing.T) {
	t.Parallel()
	rt := &scripted{
		screens: []string{claudeScreen("")},
		onType:  func(text string) []string { return []string{claudeScreen(text), claudeScreen("")} },
	}
	deps, _ := testDeps(rt)
	obs := &observing{}
	deps.Observer = obs
	report, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "say PONG", send.Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if report.Presses != 2 || !report.Delivered() {
		t.Fatalf("want delivery on the second enter: %+v", report)
	}
	if obs.calls != 1 {
		t.Fatalf("observer called %d times, want once, before typing", obs.calls)
	}

	busy := &scripted{screens: []string{claudeScreen("")}}
	bdeps, _ := testDeps(busy)
	bdeps.Observer = &observing{says: &screen.Observation{Composer: screen.ComposerBusy, Evidence: "✻ Thinking…"}}
	_, err = send.Send(context.Background(), bdeps, target(), claude.KindClaude, "say PONG", send.Options{})
	if !errors.Is(err, send.ErrAgentBusy) || !strings.Contains(err.Error(), "(✻ Thinking…)") {
		t.Fatalf("err = %v, want ErrAgentBusy quoting the observer's evidence", err)
	}
	if len(busy.typed) != 0 {
		t.Fatalf("a send the observer called busy typed anyway: %#v", busy.typed)
	}
}

// ComposerLabel keeps the word mate has always printed for the draft state.
func TestComposerLabelCallsADraftPending(t *testing.T) {
	for state, want := range map[send.ComposerState]string{
		send.StateEmpty: "empty", send.StatePending: "pending", send.StateBusy: "busy", send.StateUnknown: "unknown",
	} {
		if got := send.ComposerLabel(state); got != want {
			t.Errorf("ComposerLabel(%q) = %q, want %q", state, got, want)
		}
	}
}
