// Package send types one line into an agent pane and proves it landed.
//
// It is the one place in matev2 that puts bytes into a harness composer, for
// `matev2 send` (task 13), the console's reply key (task 15) and the auto
// daemon's digest (task 19).
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
package send

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
)

// Marker is the sentinel every line matev2 sends on its own initiative
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
// this bracket sentinel and a plain "[matev2] " ASCII fallback both arrived
// byte for byte. The bracket form was kept because U+27E6/U+27E7 are
// mathematical bracket glyphs nobody types by hand, while still being
// ordinary printable UTF-8 that survives typing, tmux and Claude's own
// input handling.
const Marker = "⟦matev2⟧ "

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
)

// Runtime is the slice of runtime.Adapter a send needs. It is narrow on
// purpose: this package can read a pane, type, press keys and wait, and it
// structurally cannot start, stop, attach or prompt anything.
type Runtime interface {
	ReadAgent(ctx context.Context, handle runtime.AgentHandle, lines int) (string, error)
	SendText(ctx context.Context, handle runtime.AgentHandle, text string) error
	SendKeys(ctx context.Context, handle runtime.AgentHandle, keys []string) error
	WaitAgent(ctx context.Context, handle runtime.AgentHandle, until runtime.WaitCondition) (runtime.ObservedAgent, error)
}

var _ Runtime = runtime.Adapter(nil)

// Deps are the collaborators a send needs. Sleep is injectable so tests run
// at memory speed; a nil Sleep waits for real.
type Deps struct {
	Runtime Runtime
	Sleep   func(ctx context.Context, d time.Duration) error
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

// Delivered reports whether the composer cleared, which is the only positive
// evidence this package accepts that a line was submitted.
func (r Report) Delivered() bool {
	return r.Typed && r.After.State != StatePending
}

// Send types one line into an agent's composer and verifies it was
// submitted. It returns the Report in every case, including every error, so
// a caller can log what was seen without unwrapping anything.
func Send(ctx context.Context, deps Deps, target runtime.AgentHandle, kind harness.Kind, text string, opts Options) (Report, error) {
	report := Report{Agent: target.Name}
	if deps.Runtime == nil {
		return report, observability.NewError(observability.CodeUsage, "send requires a runtime")
	}
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

	screen, err := deps.Runtime.ReadAgent(ctx, target, opts.Lines)
	if err != nil {
		return report, err
	}
	before, err := ClassifyComposer(kind, screen)
	if err != nil {
		return report, err
	}
	report.Before = before
	report.Steps = append(report.Steps, Step{What: "classify", State: before.State, Evidence: before.Evidence})

	switch before.State {
	case StateBusy:
		if !opts.QueueWhileBusy {
			return report, sendError(observability.CodeTargetBlocked, ErrAgentBusy,
				fmt.Sprintf("agent %s is mid-turn (%s); nothing was typed", target.Name, before.Evidence),
				map[string]any{"evidence": before.Evidence})
		}
	case StatePending:
		return report, sendError(observability.CodeStateConflict, ErrComposerPending,
			fmt.Sprintf("agent %s has unsubmitted text in its composer (%q); mate will not type over it", target.Name, before.Pending),
			map[string]any{"pending": before.Pending, "evidence": before.Evidence})
	case StateUnknown:
		return report, sendError(observability.CodeStateConflict, ErrComposerUnknown,
			fmt.Sprintf("agent %s is showing a screen mate cannot name; nothing was typed", target.Name),
			map[string]any{"screen_tail": ScreenTail(screen, tailLines)})
	}

	if err := deps.Runtime.SendText(ctx, target, payload); err != nil {
		return report, err
	}
	report.Typed = true
	report.Steps = append(report.Steps, Step{What: "type", Detail: payload})

	report.Settled = opts.Settle
	report.Steps = append(report.Steps, Step{What: "settle", Detail: opts.Settle.String()})
	if err := sleep(ctx, deps, opts.Settle); err != nil {
		return report, err
	}

	// Enter only, never the text again: a swallowed enter leaves the line in
	// the composer, so retyping would submit it twice.
	var after Classification
	for attempt := 1; attempt <= opts.Retries; attempt++ {
		if err := deps.Runtime.SendKeys(ctx, target, []string{"enter"}); err != nil {
			return report, err
		}
		report.Presses = attempt
		if err := sleep(ctx, deps, opts.RetrySleep); err != nil {
			return report, err
		}
		screen, err = deps.Runtime.ReadAgent(ctx, target, opts.Lines)
		if err != nil {
			return report, err
		}
		after, err = ClassifyComposer(kind, screen)
		if err != nil {
			return report, err
		}
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
			map[string]any{"pending": after.Pending, "screen_tail": ScreenTail(screen, tailLines)})
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
