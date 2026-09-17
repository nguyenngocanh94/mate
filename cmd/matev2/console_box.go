package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/ui/console"
)

// The message box's three actions (mvp.md task 15, section 5), bridged from
// internal/ui/console to internal/send, internal/spawn and internal/store.
// This is the only place those packages are reached for a box key: the
// Console itself imports none of them (its doc.go boundary, enforced by
// internal/ui/console/boundary_test.go).
//
// TODO(task 13): `matev2 send`, `matev2 peek` and `matev2 state` are another
// agent's change, landing as cmd/matev2/{send,peek,state}.go. When they do,
// boxReplyAction and boxPeekAction should call that command's own function
// rather than send.Send/ReadAgent directly, so the console's `r` and the
// CLI's `matev2 send` cannot drift on retries, settle or what they write to
// sent.log. The shape here is deliberately the same one those commands need
// - resolve the handle, send, record - so the merge is a call swap.

// peekLines is how much of a crew's pane `p` reads, matching mvp.md section
// 4's "matev2 peek <crew> đọc 40 dòng cuối pane" and send.DefaultLines, which
// is the window the composer classifier already judges a screen from.
const peekLines = 40

// boxResolveAction is Enter and the `[resolve]` button: hand one inbox item
// to the Mate and ask it to answer the crew.
//
// The line is query.BoxResolveLine's, built next to the merge that produced
// the item: the crew's own question, the absolute path of the status file to
// read, and the `matev2 send` that answers the crew. It replaces the old
// bare `signal: <path>`, which said only "here is a file" and left the Mate
// to infer that answering was its job. The path is absolute because the
// Mate's cwd is its own workspace directory, not the project's, so a path
// relative to the project resolves to nothing there. The line carries the
// from-app marker sentinel, which is what lets the Mate tell an
// app-generated line from something its human typed, and what the Mate's
// UserPromptSubmit hook checks before clearing `.auto`.
//
// A refused send is returned as it came back. internal/send already names
// which composer state it observed and quotes the screen it read that from,
// and the console prints that verbatim on its outcome line: a reader
// deciding whether to retry or to go look at the pane needs the observation,
// not a reworded summary of it.
func boxResolveAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req console.ActionRequest) (string, error) {
	resolve := strings.TrimSpace(req.Input)
	if resolve == "" {
		return "", observability.NewError(observability.CodeUsage, "no resolve line was built for this entry")
	}
	handle, kind, err := spawn.MateHandle(ctx, ws, deps, req.Target)
	if err != nil {
		return "", err
	}
	report, err := send.Send(ctx, send.Deps{Runtime: deps.Runtime}, handle, kind, resolve, send.Options{Marker: true})
	if err != nil {
		return "", err
	}
	// sent.log is written only after the composer cleared. Recording a line
	// the Mate never received would put a message in the box that no agent
	// ever saw, which is the one thing the box must never contain.
	if err := ws.AppendSent(req.Target, store.SentEntry{
		Source: store.SourceApp,
		Target: store.TargetMate,
		Text:   resolve,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s asked to decide in %d enter(s); the Mate answers the crew with matev2 send",
		report.Agent, report.Presses), nil
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
	report, err := send.Send(ctx, send.Deps{Runtime: deps.Runtime}, handle, kind, text, send.Options{})
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

// restartMateAction is the rail's [restart mate] label and its `R` key: stop
// the Mate, then start it again, through the same spawn seams the action
// menu's own Stop and Start use. It is one action rather than two keystrokes
// because the state it exists for - a Mate that no longer answers - is one a
// reader wants out of in one gesture, and a stop that is not followed by a
// start leaves the project with no Mate at all.
//
// A Mate that was already gone is not an error: StopMate reports it and the
// start proceeds, which is exactly the case a reader reaching for a restart
// is most often in. The returned line names both halves, so the outcome line
// says what actually happened rather than only that something did.
func restartMateAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req console.ActionRequest) (string, error) {
	if req.Target == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for the restart")
	}
	stopped, err := spawn.StopMate(ctx, ws, deps, req.Target)
	gone := mateNotRecorded(err)
	if err != nil && !gone {
		return "", err
	}
	res, err := spawn.StartMate(ctx, ws, deps, spawn.StartRequest{Project: req.Target, Resume: true})
	if err != nil {
		return "", err
	}
	was := "stopped " + stopped.Agent
	if gone || stopped.AlreadyGone {
		was = "the previous Mate was already gone"
	}
	return fmt.Sprintf("%s; Mate %s is running on %s in pane %s", was, res.Agent, res.Harness, res.Pane), nil
}

// clearComposerAction is [clear composer] and its `u` key: one Ctrl+U into
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
