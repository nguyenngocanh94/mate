// Package send types one line into an agent pane and proves it landed.
//
// It is the one place in mate that puts bytes into a harness composer, for
// `mate send` (task 13), the console's reply key (task 15) and the Mate's
// outbox (internal/outbox, task 30), which delivers `[assign]` and the auto
// daemon's digest.
//
// It never uses `herdr agent prompt` (runtime.PromptAgent) to deliver. v1
// measured that call reporting success twice while the model received
// nothing or received garbage (docs/phase1/inbox-protocol.md question 1):
// against a Codex modal it returned `agent_prompted` and exit 0 with the
// pane state unchanged and no turn ever starting, and against a Claude
// composer holding a half-typed human line it concatenated the two into one
// mangled prompt. Both failures are invisible to the caller, and both are
// screen states this package reads before it types anything.
//
// The sequence is firstmate's, which has run this way in production for
// months (bin/fm-tmux-lib.sh fm_tmux_submit_core): classify, type the text
// exactly once, settle, then press enter and re-read until the composer
// clears. Enter is retried; the text never is, because a swallowed enter
// leaves the line in the composer and retyping would double it.
//
// Every Enter after the first, and every Enter of a resumed send, goes only
// while a fresh read shows the composer holding exactly the typed line
// (pendingMatches). The line is typed only into a composer the observer
// reads as empty, and no observer mate configures reads one empty that the
// fixture's deterministic classifier does not (Observation.Deterministic:
// on the composer, Jev may veto and never enable, internal/screen/chain), so
// the first Enter follows the settle as it always has.
package send

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	screenpkg "github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/fixture"
)

// Marker is the sentinel every line mate sends on its own initiative
// carries, so a Mate can tell an app-generated digest from something its
// human typed (docs/mvp.md sections 4 and 5).
//
// This used to be the single control byte 0x1f. A task 15 live run found
// that byte never reaching Claude's own UserPromptSubmit payload: it typed
// into the pane fine (herdr's own `pane send-text` preserves it, proved
// against a bare shell), but something between the terminal and Claude
// Code's composer ate it before the model ever saw a prompt, so the hook
// recorded the line as the user's own typing. Measured 2026-09-17 against
// Herdr 0.8.2 and Claude Code 2.1.274 (internal/send's
// TestLiveMarkerSurvivesToTheHook): the 0x1f byte was stripped every time;
// this bracket sentinel and a plain "[mate] " ASCII fallback both arrived
// byte for byte. The bracket form was kept because U+27E6/U+27E7 are
// mathematical bracket glyphs nobody types by hand, while still being
// ordinary printable UTF-8 that survives typing, tmux and Claude's own
// input handling.
const Marker = "⟦mate⟧ "

// Sentinel reasons a send did not deliver. Each is wrapped in a coded
// observability.Error, so callers may match with errors.Is and the CLI still
// gets its exit code.
var (
	// ErrAgentBusy is the harness mid-turn. The line was not typed.
	ErrAgentBusy = errors.New("agent is mid-turn")
	// ErrComposerPending is someone else's unsubmitted text in the
	// composer. The line was not typed, and the pending text is untouched.
	ErrComposerPending = errors.New("composer holds unsubmitted text")
	// ErrComposerUnknown is a screen with no composer mate can name: a
	// dialog, a scrolled transcript, a harness still starting.
	ErrComposerUnknown = errors.New("composer not recognised on screen")
	// ErrEnterSwallowed is the line typed and still sitting in the composer
	// after every enter. The text reached the pane but not the model, which
	// is precisely the failure `herdr agent prompt` hides.
	ErrEnterSwallowed = errors.New("enter did not submit the line")
	// ErrSubmissionUnconfirmed means input may have arrived, but neither a
	// cleared composer nor an idle-to-busy transition proves submission.
	ErrSubmissionUnconfirmed = errors.New("submission unconfirmed; do not retype")
	// ErrDialogOpen is a dialog the observer names over the pane. Nothing
	// was typed.
	ErrDialogOpen = errors.New("a dialog is open over the pane")
	// ErrPaneChanged is a pane that showed something else by the time the
	// observer had read it. Nothing was typed, and the send may be retried.
	ErrPaneChanged = errors.New("pane changed while it was being read")
)

// Runtime is the slice of runtime.Adapter a send needs. It is narrow on
// purpose: this package can read a pane, type, press keys and wait, and it
// structurally cannot start, stop, attach or prompt anything.
type Runtime interface {
	ReadAgent(ctx context.Context, handle runtime.AgentHandle, source harness.ReadSource, lines int) (string, error)
	// ReadAgentStyled is the same snapshot with SGR attributes intact. The
	// composer classifier needs them: a harness's own faint suggestion and
	// a person's unsubmitted line are the same characters
	// (internal/screen/fixture/classify.go's faintPlaceholder), and typing
	// over the second is the mistake this package exists to prevent.
	ReadAgentStyled(ctx context.Context, handle runtime.AgentHandle, source harness.ReadSource, lines int) (string, error)
	SendText(ctx context.Context, handle runtime.AgentHandle, text string) error
	SendKeys(ctx context.Context, handle runtime.AgentHandle, keys []string) error
	WaitAgent(ctx context.Context, handle runtime.AgentHandle, until runtime.WaitCondition) (runtime.ObservedAgent, error)
}

var _ Runtime = runtime.Adapter(nil)

// Deps are the collaborators a send needs. Sleep is injectable so tests run
// at memory speed; a nil Sleep waits for real.
type Deps struct {
	Runtime Runtime
	// Harnesses are the harnesses a target may run. A send to a kind that
	// is not registered is refused before the pane is read.
	Harnesses harness.Registry
	Sleep     func(ctx context.Context, d time.Duration) error
	// BeforeType persists an attempt after readiness checks and before any
	// bytes are sent. A failure prevents typing. Recovery never calls it.
	BeforeType func() error
	// Observer reads the pane once, before anything is typed, and the
	// send's policy decides from that Observation. Nil means the fixture
	// observer (internal/screen/fixture). The re-reads after typing never
	// ask it: they compare the composer with the typed text directly.
	Observer screenpkg.Observer
}

func (d Deps) observer() screenpkg.Observer {
	if d.Observer != nil {
		return d.Observer
	}
	return fixture.New()
}

// Defaults measured by firstmate and carried over unchanged (bin/fm-send.sh,
// FM_SEND_RETRIES / FM_SEND_SLEEP, and the slash-command settle).
const (
	// DefaultSettle is the pause between typing and the first enter.
	DefaultSettle = 300 * time.Millisecond
	// SlashSettle is that pause for a line starting with `/`: a slash
	// command opens a completion popup, and an enter that arrives before
	// the popup has settled selects nothing and is eaten.
	SlashSettle = 1200 * time.Millisecond
	// DefaultRetries is how many enters are pressed in total.
	DefaultRetries = 3
	// DefaultRetrySleep is the pause between an enter and the re-read that
	// judges it.
	DefaultRetrySleep = 400 * time.Millisecond
	// DefaultLines is the pane snapshot size every classification uses.
	DefaultLines = 40
	// DefaultWaitTimeout bounds the optional post-send confirmation that
	// the harness picked the line up.
	DefaultWaitTimeout = 5 * time.Second
	// tailLines is how much screen an error quotes.
	tailLines = 12
)

// Options tune one send. The zero value is the measured default: type once,
// settle 300ms (1200ms for a slash command), up to three enters 400ms apart,
// no marker, refuse a busy pane, no post-send wait.
type Options struct {
	// ResumePending is only for a caller holding a recorded attempt for this
	// exact agent incarnation and payload. It never types, even if empty.
	ResumePending bool
	// QueueWhileBusy types into a pane that is mid-turn instead of
	// refusing. The harness queues the line behind the running turn. It is
	// off by default because a queued line is not an answered line, and a
	// caller that wants that has to say so.
	QueueWhileBusy bool
	// Marker prefixes the from-app sentinel.
	Marker bool
	// Settle overrides the pause between typing and the first enter.
	Settle time.Duration
	// Retries is the total number of enters. Values below one mean one.
	Retries int
	// RetrySleep is the pause between an enter and its verifying re-read.
	RetrySleep time.Duration
	// Lines is the pane snapshot size. Zero means DefaultLines.
	Lines int
	// WaitForWorking asks Herdr, after a confirmed submit, to observe the
	// agent leaving idle. It is a confirmation, never a gate: Herdr's
	// status is screen scraping (docs/mvp.md decision 8), so a failure is
	// a warning in the Report and the send still succeeded.
	WaitForWorking bool
	// WaitTimeout bounds that confirmation.
	WaitTimeout time.Duration
}

// Step is one observation made during a send, in order, for the caller's log.
type Step struct {
	// What names the step: "classify", "type", "settle", "enter", "wait".
	What string
	// State is the composer state observed after the step, where one was.
	State ComposerState
	// Evidence is the screen line that state was read from.
	Evidence string
	// Detail carries anything else worth logging (a duration, a status).
	Detail string
}

// Report is everything a send observed. A caller logs it whether the send
// succeeded or failed; on failure it is attached to the error's details.
type Report struct {
	// Agent is the live agent name the line was addressed to.
	Agent string
	// Text is exactly what was typed, marker included.
	Text string
	// Before is the classification that decided whether to type at all.
	Before Classification
	// Typed is whether the text reached the composer.
	Typed bool
	// Resumed means Enter was retried for a recorded, fully matched draft.
	Resumed bool
	// Settled is the pause taken between typing and the first enter.
	Settled time.Duration
	// Presses is how many enters were sent.
	Presses int
	// After is the classification the send finished on.
	After Classification
	// Steps is the ordered trace.
	Steps []Step
	// Warnings are non-fatal observations, notably a WaitForWorking that
	// did not confirm.
	Warnings []string
}

// Delivered requires a cleared composer or an observed transition to busy.
// Unknown and a pane that was already busy do not prove submission.
func (r Report) Delivered() bool {
	return (r.Typed || r.Resumed) && r.Presses > 0 &&
		(r.After.State == StateEmpty || (r.After.State == StateBusy && r.Before.State != StateBusy))
}

// Send types one line into an agent's composer and verifies it was
// submitted. It returns the Report in every case, including every error, so
// a caller can log what was seen without unwrapping anything.
func Send(ctx context.Context, deps Deps, target runtime.AgentHandle, kind harness.Kind, text string, opts Options) (Report, error) {
	report := Report{Agent: target.Name}
	if deps.Runtime == nil {
		return report, observability.NewError(observability.CodeUsage, "send requires a runtime")
	}
	profile, err := deps.Harnesses.Lookup(kind)
	if err != nil {
		return report, err
	}
	screens := profile.Screen()
	source := screens.ReadSource()
	if strings.ContainsAny(text, "\r\n") {
		return report, observability.NewError(observability.CodeUsage,
			"send refuses a multi-line message: one line per send, long content goes in a file the agent is pointed at")
	}
	if strings.TrimSpace(text) == "" {
		return report, observability.NewError(observability.CodeUsage, "send requires a non-empty line")
	}
	opts = opts.withDefaults(text)
	payload := text
	if opts.Marker {
		payload = Marker + text
	}
	report.Text = payload
	// Reject terminal control bytes before a caller persists ownership of
	// an attempt; an invalid input must not strand a recovery receipt.
	if err := runtime.ValidateSendText(payload); err != nil {
		return report, err
	}

	// The styled read, not the plain one: see the Runtime interface above.
	screen, err := deps.Runtime.ReadAgentStyled(ctx, target, source, opts.Lines)
	if err != nil {
		return report, err
	}
	observed, err := deps.observer().Observe(screenpkg.WithCaller(ctx, screenpkg.CallerSend), screens, screen)
	if err != nil {
		return report, err
	}
	before := Classification{State: observed.Composer, Evidence: observed.Evidence, Pending: observed.Draft}
	report.Before = before
	report.Steps = append(report.Steps, Step{What: "classify", State: before.State, Evidence: before.Evidence})
	// Nothing is typed under a dialog the observer names. A screen it
	// cannot say has or lacks one, with no composer read off it either, is
	// refused below as an unknown screen, as it always was.
	if d := observed.Dialog; d != screenpkg.DialogNone && d != "" && (d != screenpkg.DialogUnknown || before.State != StateUnknown) {
		return report, sendError(observability.CodeStateConflict, ErrDialogOpen,
			fmt.Sprintf("a dialog is open: %s; nothing typed", d),
			map[string]any{"dialog": string(d), "screen_tail": ScreenTail(StripSGR(screen), tailLines)})
	}
	if opts.ResumePending && (before.State != StatePending || !pendingMatches(screens, screen, payload)) {
		return report, sendError(observability.CodeStateConflict, ErrSubmissionUnconfirmed,
			"recorded send no longer matches the whole pending composer; inspect the pane, do not retype",
			map[string]any{"screen_tail": ScreenTail(StripSGR(screen), tailLines)})
	}

	switch before.State {
	case StateBusy:
		if !opts.QueueWhileBusy {
			return report, sendError(observability.CodeTargetBlocked, ErrAgentBusy,
				fmt.Sprintf("agent %s is mid-turn (%s); nothing was typed", target.Name, before.Evidence),
				map[string]any{"evidence": before.Evidence})
		}
	case StatePending:
		if opts.ResumePending {
			break
		}
		return report, sendError(observability.CodeStateConflict, ErrComposerPending,
			fmt.Sprintf("agent %s has unsubmitted text in its composer (%q); mate will not type over it", target.Name, before.Pending),
			map[string]any{"pending": before.Pending, "evidence": before.Evidence})
	case StateUnknown:
		return report, sendError(observability.CodeStateConflict, ErrComposerUnknown,
			fmt.Sprintf("agent %s is showing a screen mate cannot name; nothing was typed", target.Name),
			map[string]any{"screen_tail": ScreenTail(StripSGR(screen), tailLines)})
	}

	if opts.ResumePending {
		report.Resumed = true
		report.Steps = append(report.Steps, Step{What: "resume", Detail: payload})
	} else {
		// An observer other than the fixture can take up to its deadline to
		// answer (Jev's is notice.Timeout), so its Observation may be of a
		// pane that has since moved on. The pane is read again and the
		// fixture classifies it: a composer that is no longer one this send
		// types into (empty, or busy when queueing) is refused before a byte
		// is typed, and the caller may retry. The fixture observer is
		// in-process, and its read-to-type gap is what it always was.
		if observed.Source != fixture.Source {
			if err := stillTypeable(ctx, deps, target, source, screens, opts); err != nil {
				return report, err
			}
		}
		if deps.BeforeType != nil {
			if err := deps.BeforeType(); err != nil {
				return report, err
			}
		}
		if err := deps.Runtime.SendText(ctx, target, payload); err != nil {
			return report, err
		}
		report.Typed = true
		report.Steps = append(report.Steps, Step{What: "type", Detail: payload})
	}

	report.Settled = opts.Settle
	report.Steps = append(report.Steps, Step{What: "settle", Detail: opts.Settle.String()})
	if err := sleep(ctx, deps, opts.Settle); err != nil {
		return report, err
	}
	if opts.ResumePending {
		// A human can edit during the settle interval. Do not submit the
		// earlier snapshot's text without reading the composer again.
		screen, err = deps.Runtime.ReadAgentStyled(ctx, target, source, opts.Lines)
		if err != nil {
			return report, err
		}
	}

	// Enter only, never the text again: a swallowed enter leaves the line in
	// the composer, so retyping would submit it twice.
	var after Classification
	for attempt := 1; attempt <= opts.Retries; attempt++ {
		if (attempt > 1 || opts.ResumePending) && !pendingMatches(screens, screen, payload) {
			return report, sendError(observability.CodeStateConflict, ErrSubmissionUnconfirmed,
				"pending composer changed or cannot be fully read; no further Enter sent",
				map[string]any{"screen_tail": ScreenTail(StripSGR(screen), tailLines)})
		}
		if err := deps.Runtime.SendKeys(ctx, target, []string{"enter"}); err != nil {
			return report, err
		}
		report.Presses = attempt
		if err := sleep(ctx, deps, opts.RetrySleep); err != nil {
			return report, err
		}
		screen, err = deps.Runtime.ReadAgentStyled(ctx, target, source, opts.Lines)
		if err != nil {
			return report, err
		}
		after = ClassifyComposer(screens, screen)
		report.After = after
		report.Steps = append(report.Steps, Step{
			What:     "enter",
			State:    after.State,
			Evidence: after.Evidence,
			Detail:   fmt.Sprintf("press %d of %d", attempt, opts.Retries),
		})
		if after.State != StatePending {
			break
		}
	}
	if after.State == StatePending {
		return report, sendError(observability.CodeStateConflict, ErrEnterSwallowed,
			fmt.Sprintf("typed into %s but %d enter(s) did not submit it; the line is still in the composer (%q)", target.Name, report.Presses, after.Pending),
			map[string]any{"pending": after.Pending, "screen_tail": ScreenTail(StripSGR(screen), tailLines)})
	}
	if !report.Delivered() {
		return report, sendError(observability.CodeStateConflict, ErrSubmissionUnconfirmed,
			"text was typed but submission is unconfirmed; inspect the pane, do not retype",
			map[string]any{"screen_tail": ScreenTail(StripSGR(screen), tailLines)})
	}

	if opts.WaitForWorking {
		obs, err := deps.Runtime.WaitAgent(ctx, target, runtime.WaitCondition{
			Until:   []runtime.AgentStatus{runtime.AgentWorking},
			Timeout: opts.WaitTimeout,
		})
		switch {
		case err != nil:
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("the composer cleared but Herdr did not report %s working within %s: %v", target.Name, opts.WaitTimeout, err))
			report.Steps = append(report.Steps, Step{What: "wait", Detail: "unconfirmed"})
		default:
			report.Steps = append(report.Steps, Step{What: "wait", Detail: string(obs.Status)})
		}
	}
	return report, nil
}

// stillTypeable reads the pane again and refuses with ErrPaneChanged unless
// the fixture reads a composer the send may type into: empty, or busy when
// the caller queues behind the turn.
func stillTypeable(ctx context.Context, deps Deps, target runtime.AgentHandle, source harness.ReadSource, screens harness.ScreenProfile, opts Options) error {
	again, err := deps.Runtime.ReadAgentStyled(ctx, target, source, opts.Lines)
	if err != nil {
		return err
	}
	// The fixture's own classifier: what fixture.Observe reads as
	// Deterministic.
	now := ClassifyComposer(screens, again).State
	if now == StateEmpty || (opts.QueueWhileBusy && now == StateBusy) {
		return nil
	}
	return sendError(observability.CodeStateConflict, ErrPaneChanged,
		"pane changed while it was being read; nothing typed",
		map[string]any{"composer": ComposerLabel(now), "screen_tail": ScreenTail(StripSGR(again), tailLines)})
}

// withDefaults fills the measured defaults, including the longer settle a
// slash command needs for its completion popup.
func (o Options) withDefaults(text string) Options {
	if o.Settle <= 0 {
		o.Settle = DefaultSettle
		if strings.HasPrefix(text, "/") {
			o.Settle = SlashSettle
		}
	}
	if o.Retries < 1 {
		o.Retries = DefaultRetries
	}
	if o.RetrySleep <= 0 {
		o.RetrySleep = DefaultRetrySleep
	}
	if o.Lines <= 0 {
		o.Lines = DefaultLines
	}
	if o.WaitTimeout <= 0 {
		o.WaitTimeout = DefaultWaitTimeout
	}
	return o
}

func sleep(ctx context.Context, deps Deps, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if deps.Sleep != nil {
		return deps.Sleep(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// sendError wraps a sentinel in the coded taxonomy, so errors.Is finds the
// reason and observability.ExitCode finds the exit.
func sendError(code observability.Code, sentinel error, message string, details map[string]any) error {
	return observability.WrapError(code, message, sentinel).WithDetails(details)
}
