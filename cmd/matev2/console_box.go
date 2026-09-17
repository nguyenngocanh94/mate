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

// boxForwardAction is Enter: hand one box entry to the Mate.
//
// Only the signal line goes in - `signal: <absolute status file path>`,
// never the status text itself - so the Mate reads the file rather than
// trusting a copy the console made (mvp.md section 5). The path is
// absolute because the Mate's cwd is its own workspace directory, not the
// project's, so a path relative to the project resolves to nothing there.
// The line carries the from-app marker sentinel, which is what lets the
// Mate tell an app-generated line from something its human typed, and what
// the Mate's UserPromptSubmit hook checks before clearing `.auto`.
//
// A refused send is returned as it came back. internal/send already names
// which composer state it observed and quotes the screen it read that from,
// and the console prints that verbatim on its outcome line: a reader
// deciding whether to retry or to go look at the pane needs the observation,
// not a reworded summary of it.
func boxForwardAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req console.ActionRequest) (string, error) {
	signal := strings.TrimSpace(req.Input)
	if signal == "" {
		return "", observability.NewError(observability.CodeUsage, "no signal line was built for this entry")
	}
	handle, kind, err := spawn.MateHandle(ctx, ws, deps, req.Target)
	if err != nil {
		return "", err
	}
	report, err := send.Send(ctx, send.Deps{Runtime: deps.Runtime}, handle, kind, signal, send.Options{Marker: true})
	if err != nil {
		return "", err
	}
	// sent.log is written only after the composer cleared. Recording a line
	// the Mate never received would put a message in the box that no agent
	// ever saw, which is the one thing the box must never contain.
	if err := ws.AppendSent(req.Target, store.SentEntry{
		Source: store.SourceApp,
		Target: store.TargetMate,
		Text:   signal,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s delivered to %s in %d enter(s)", signal, report.Agent, report.Presses), nil
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

// boxSendRefusal reports whether an error is one of internal/send's three
// "nothing was typed" refusals. The console shows all three the same way -
// the reason on the outcome line and nothing else done - so this exists for
// the tests that have to tell a refusal from a transport failure.
func boxSendRefusal(err error) bool {
	return errors.Is(err, send.ErrComposerPending) ||
		errors.Is(err, send.ErrAgentBusy) ||
		errors.Is(err, send.ErrComposerUnknown)
}
