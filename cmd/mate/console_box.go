package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// The message box's three actions (mvp.md task 15, section 5), bridged from
// internal/ui/console to internal/send, internal/spawn and internal/store.
// This is the only place those packages are reached for a box key: the
// Console itself imports none of them (its doc.go boundary, enforced by
// internal/ui/console/boundary_test.go).
//
// TODO(task 13): `mate send`, `mate peek` and `mate state` are another
// agent's change, landing as cmd/mate/{send,peek,state}.go. When they do,
// boxReplyAction and boxPeekAction should call that command's own function
// rather than send.Send/ReadAgent directly, so the console's `r` and the
// CLI's `mate send` cannot drift on retries, settle or what they write to
// sent.log. The shape here is deliberately the same one those commands need
// - resolve the handle, send, record - so the merge is a call swap.

// peekLines is how much of a crew's pane `p` reads, matching mvp.md section
// 4's "mate peek <crew> đọc 40 dòng cuối pane" and send.DefaultLines, which
// is the window the composer classifier already judges a screen from.
const peekLines = 40

// boxResolveAction is Enter and the `[resolve]` button: hand one inbox item
// to the Mate and ask it to answer the crew.
//
// The line is query.BoxResolveLine's, built next to the merge that produced
// the item: the crew's own question, the absolute path of the status file to
// read, and the `mate send` that answers the crew. It replaces the old
// bare `signal: <path>`, which said only "here is a file" and left the Mate
// to infer that answering was its job. The path is absolute because the
// Mate's cwd is its own workspace directory, not the project's, so a path
// relative to the project resolves to nothing there. The line carries the
// from-app marker sentinel, which is what lets the Mate tell an
// app-generated line from something its human typed, and what the Mate's
// UserPromptSubmit hook checks before clearing `.auto`.
//
// Since task 30 the line is queued rather than refused. The Mate's composer
// is busy most of the time (mvp.md section 7: `[assign]` met `agent is
// mid-turn` four or five times in a row in task 24), so the action appends
// the line to the Mate's outbox, makes one immediate attempt so an idle Mate
// gets it with no delay, and returns either way; the console's sender loop
// (consoleDelivery) retries every two seconds until the composer is empty.
// sent.log is still written only once the composer cleared - by the outbox,
// in the same words as before - so the box never shows the Mate a message no
// agent received. The same inbox entry assigned twice is queued once
// (req.Key, query.BoxEntry.AssignKey), and the answer says when it was.
func boxResolveAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req console.ActionRequest) (string, error) {
	resolve := strings.TrimSpace(req.Input)
	if resolve == "" {
		return "", observability.NewError(observability.CodeUsage, "no resolve line was built for this entry")
	}
	if err := store.ValidateProjectName(req.Target); err != nil {
		return "", err
	}
	if _, ok := ws.Project(req.Target); !ok {
		return "", fmt.Errorf("%w: %s", store.ErrNoProject, req.Target)
	}
	key := req.Key
	if key == "" {
		// A caller that did not name the entry (a script, an older test)
		// still gets dedup, on the words themselves.
		key = "line:" + resolve
	}
	sender := consoleOutbox(ws, deps)
	queued, err := sender.Enqueue(req.Target, outbox.Request{
		Source: store.OutboxSourceAssign, Key: key, Text: resolve,
	})
	if err != nil {
		return "", err
	}
	item := queued.Item
	if item.Queued() {
		attempt, err := sender.Attempt(ctx, req.Target)
		if err != nil {
			return "", err
		}
		if attempt.Item.ID == item.ID {
			item = attempt.Item
			if attempt.Delivered && !queued.Duplicate {
				return fmt.Sprintf("sent to %s; the Mate answers the crew with mate send", attempt.Agent), nil
			}
			if attempt.Refusal != nil && !queued.Duplicate {
				return fmt.Sprintf("queued for the Mate (%s); it goes in when the composer is empty",
					refusalReason(attempt.Refusal)), nil
			}
		} else if !queued.Duplicate {
			return "queued for the Mate, behind a line it has not taken yet", nil
		}
	}
	switch item.State {
	case store.OutboxSent:
		return fmt.Sprintf("already assigned %s, sent %s; not queued again",
			item.At.UTC().Format("15:04"), item.SentAt.UTC().Format("15:04")), nil
	default:
		return fmt.Sprintf("already assigned %s, still queued; not queued again",
			item.At.UTC().Format("15:04")), nil
	}
}

// refusalReason is a refusal in the few words an outcome line has room for.
// The full observation is in the outbox item's last_refusal, and the inbox
// row keeps saying the line is queued until it is not.
func refusalReason(err error) string {
	switch {
	case errors.Is(err, send.ErrAgentBusy):
		return "the Mate is mid-turn"
	case errors.Is(err, send.ErrComposerPending):
		return "its composer holds unsubmitted text"
	case errors.Is(err, send.ErrComposerUnknown):
		return "its pane shows no composer"
	case errors.Is(err, send.ErrEnterSwallowed):
		return "enter did not submit it"
	}
	return err.Error()
}

// boxReplyAction is `r`: one line into the crew's own composer.
//
// No marker byte: this line is the user's, not the app's (mvp.md section 4 -
// "Ai đó gõ một dòng trả lời vào pane crew"), and to the crew it is simply a
// new prompt. It is recorded with Source: user for the same reason.
func boxReplyAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req console.ActionRequest) (string, error) {
	text := strings.TrimSpace(req.Input)
	if text == "" {
		return "", observability.NewError(observability.CodeUsage, "a reply is one non-empty line")
	}
	if req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage, "no crew was named for the reply")
	}
	handle, kind, err := spawn.CrewHandle(ctx, ws, deps, req.Target, req.Crew)
	if err != nil {
		return "", err
	}
	report, err := send.Send(ctx, send.Deps{Runtime: deps.Runtime, Harnesses: deps.Harnesses}, handle, kind, text, send.Options{})
	if err != nil {
		return "", err
	}
	if err := ws.AppendSent(req.Target, store.SentEntry{
		Source: store.SourceUser,
		Target: store.CrewTarget(req.Crew),
		Text:   text,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("replied to %s in %d enter(s)", report.Agent, report.Presses), nil
}

// boxPeekAction is `p`: the crew's last peekLines of pane, returned as text
// for the overlay. It writes nothing - not to the pane, not to sent.log -
// because looking at a screen is not communication, and a peek in the box
// would be a record of the reader rather than of the crew.
func boxPeekAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req console.ActionRequest) (string, error) {
	if req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage, "no crew was named for the peek")
	}
	handle, _, err := spawn.CrewHandle(ctx, ws, deps, req.Target, req.Crew)
	if err != nil {
		return "", err
	}
	screen, err := deps.Runtime.ReadAgent(ctx, handle, peekLines)
	if err != nil {
		return "", err
	}
	return screen, nil
}

// restartMateAction is the Actions menu's restart_mate entry on the Mate row:
// stow, stop the Mate, then start it again, through the same spawn seams the
// action menu's own Stop and Start use. It is one action rather than two
// keystrokes because the state it exists for - a Mate that no longer answers
// - is one a reader wants out of in one gesture, and a stop that is not
// followed by a start leaves the project with no Mate at all.
//
// Since task 37 (B7) the restart first asks the Mate to file what exists
// only in its conversation: the `⟦mate⟧ stow:` line goes through the
// outbox like every other line the app types into a Mate, and the restart
// waits for that turn to end, up to outbox.DefaultStowCeiling, then goes
// ahead either way. The captain's own unsent text in the composer is never
// typed over: the first press is held with a question on the outcome line,
// and a second press within restartConfirmWindow restarts without stowing.
// The outcome line opens with "stowed" or "not stowed: <reason>".
//
// A Mate that was already gone is not an error: StopMate reports it and the
// start proceeds, which is exactly the case a reader reaching for a restart
// is most often in. The returned line names every step, so the outcome line
// says what actually happened rather than only that something did.
func restartMateAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req console.ActionRequest, holds *restartHolds) (string, error) {
	if req.Target == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for the restart")
	}
	if err := store.ValidateProjectName(req.Target); err != nil {
		return "", err
	}
	confirmed := holds.take(req.Target, time.Now())
	stow, err := consoleOutbox(ws, deps).Stow(ctx, req.Target, outbox.StowOptions{})
	if err != nil {
		return "", err
	}
	if stow.Held {
		if !confirmed {
			holds.put(req.Target, time.Now())
			return fmt.Sprintf("held: the Mate's composer holds unsent text (%s), so mate typed nothing and restarted nothing; choose Restart again within %s to restart without stowing, which discards that text",
				strconv.Quote(recallClip(stow.Pending, 80)), outbox.Span(restartConfirmWindow)), nil
		}
		stow.Reason = "the composer held unsent text; restarted on your confirmation"
	}
	// A restart keeps the harness the Mate was running: StartMate otherwise
	// launches the workspace default, and a Codex Mate restarted from the
	// console came back as a fresh Claude Mate (found 2026-09-24, task 38).
	kind, err := restartHarness(ws, req.Target)
	if err != nil {
		return "", err
	}
	stopped, err := spawn.StopMate(ctx, ws, deps, req.Target)
	gone := mateNotRecorded(err)
	if err != nil && !gone {
		return "", err
	}
	res, err := spawn.StartMate(ctx, ws, deps, spawn.StartRequest{Project: req.Target, Resume: true, Harness: kind})
	if err != nil {
		return "", err
	}
	was := "stopped " + stopped.Agent
	if gone || stopped.AlreadyGone {
		was = "the previous Mate was already gone"
	}
	return fmt.Sprintf("%s; %s; Mate %s is running on %s in pane %s", stow.Outcome(), was, res.Agent, res.Harness, res.Pane), nil
}

// restartHarness is the harness mate.meta records for the project's Mate,
// or "" (the workspace default) when there is no record of one.
func restartHarness(ws *store.Workspace, project string) (harness.Kind, error) {
	meta, err := ws.ReadMateMeta(project)
	if err != nil {
		return "", err
	}
	recorded := strings.TrimSpace(meta[spawn.MetaHarness])
	if recorded == "" {
		return "", nil
	}
	return harnesses.Parse(recorded)
}

// restartConfirmWindow is how long a held restart waits for the captain's
// second press.
const restartConfirmWindow = 2 * time.Minute

// restartHolds remembers, per project, a restart held because the Mate's
// composer had unsent text in it, so the next press within
// restartConfirmWindow is the captain's confirmation. It lives in the
// console process: a console restarted in between asks again, which is the
// safe side.
type restartHolds struct {
	mu sync.Mutex
	at map[string]time.Time
}

func newRestartHolds() *restartHolds { return &restartHolds{at: map[string]time.Time{}} }

func (h *restartHolds) put(project string, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.at[project] = now
}

// take reports whether a hold for project is still open, and clears it.
func (h *restartHolds) take(project string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, ok := h.at[project]
	delete(h.at, project)
	return ok && now.Sub(at) <= restartConfirmWindow
}

// clearComposerAction is the Actions menu's clear_composer entry: one Ctrl+U into
// the Mate's pane.
//
// It goes through runtime.SendKeys, not internal/send: send.Send types a
// line and verifies the composer cleared afterwards, which is the wrong
// shape entirely for a key whose whole purpose is that the composer is in a
// state nobody can classify. Nothing is typed, nothing is submitted and
// nothing is recorded in sent.log - a keypress that removes half-typed text
// is not a message the Mate was sent.
func clearComposerAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req console.ActionRequest) (string, error) {
	if req.Target == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for the composer clear")
	}
	handle, _, err := spawn.MateHandle(ctx, ws, deps, req.Target)
	if err != nil {
		return "", err
	}
	if err := deps.Runtime.SendKeys(ctx, handle, []string{clearComposerKey}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s pressed in %s's pane; nothing was sent", clearComposerKey, handle.Name), nil
}

// mateNotRecorded reports the one StopMate refusal a restart absorbs:
// `mate.meta` names no agent, so there was nothing to stop. Every other
// failure is propagated - a stop that could not be confirmed must not be
// followed by a start that would race the agent still running.
func mateNotRecorded(err error) bool {
	var coded *observability.Error
	return errors.As(err, &coded) && coded.Code == observability.CodeNotFound
}

// clearComposerKey is the one key [clear composer] presses. Ctrl+U is the
// readline kill-line every harness composer inherits (Claude Code and Codex
// both run their input through one), so it clears the line without
// submitting it.
const clearComposerKey = "ctrl+u"

// boxSendRefusal reports whether an error is one of internal/send's three
// "nothing was typed" refusals. The console shows all three the same way -
// the reason on the outcome line and nothing else done - so this exists for
// the tests that have to tell a refusal from a transport failure.
func boxSendRefusal(err error) bool {
	return errors.Is(err, send.ErrComposerPending) ||
		errors.Is(err, send.ErrAgentBusy) ||
		errors.Is(err, send.ErrComposerUnknown)
}
