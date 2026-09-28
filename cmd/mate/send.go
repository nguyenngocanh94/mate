package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdSend implements `mate send <project> <crew> "<one line>"`
// (docs/mvp.md task 13, section 4 "Mate -> Crew"): type one line into a
// crew's pane and verify it landed, the way internal/send is built to.
func cmdSend(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate send <project> <crew> "<one line>" [--workspace <dir>] [--marker] [--queue] [--from user|mate]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	markerFlag := fs.Bool("marker", false, "prefix the app-sent sentinel "+strconv.Quote(send.Marker))
	queueFlag := fs.Bool("queue", false, "type into a busy pane instead of refusing; the harness queues it")
	fromFlag := fs.String("from", "", "who is sending: user or mate (default: mate when MATE_AGENT_ROLE=mate, else user)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 3 {
		fs.Usage()
		return newUsageError("mate send: want exactly 3 arguments: <project> <crew> \"<one line>\"")
	}
	source, err := resolveSendSource(*fromFlag)
	if err != nil {
		return &usageError{err}
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	opts := send.Options{Marker: *markerFlag, QueueWhileBusy: *queueFlag}
	report, err := sendToCrew(context.Background(), w, spawn.LiveDeps(), fs.Arg(0), fs.Arg(1), fs.Arg(2), source, opts)
	if err != nil {
		printSendRefusalDetails(stderr, err)
		return err
	}
	writeSendReport(stdout, w, fs.Arg(0), fs.Arg(1), source, report)
	return nil
}

// writeSendReport prints the send trace and, when the Mate sent it, the
// correction nudge (nudge.go) and the line telling it to end
// its turn (auto_turn.go), which stays last. A line the captain sends from a
// shell is not the Mate answering a Crew, so it gets the trace alone.
func writeSendReport(stdout io.Writer, w *store.Workspace, project, crew, source string, report send.Report) {
	fmt.Fprintln(stdout, sendSummaryLine(report))
	printSendCorrectionNudge(stdout, w, project, crew, source)
	if source == store.SourceMate {
		printTurnEnd(stdout, turnSendLine(crew))
	}
}

// resolveSendSource applies docs/mvp.md task 13's default: Source is mate
// when the pane's own environment carries MATE_AGENT_ROLE=mate (the Mate
// pane), user otherwise. An explicit --from (the console's own call) always
// wins over the environment.
func resolveSendSource(fromFlag string) (string, error) {
	switch fromFlag {
	case "":
		if os.Getenv("MATE_AGENT_ROLE") == "mate" {
			return store.SourceMate, nil
		}
		return store.SourceUser, nil
	case store.SourceUser, store.SourceMate:
		return fromFlag, nil
	default:
		return "", fmt.Errorf("mate send: --from must be %q or %q, got %q", store.SourceUser, store.SourceMate, fromFlag)
	}
}

// sendToCrew is send's core, kept separate from flag parsing so a test can
// drive it with a fake runtime (spawn.Deps{Runtime: runtime.NewFake()})
// instead of spawn.LiveDeps(). It resolves the crew's live agent handle from
// crews/<id>.meta, sends the line through internal/send, and on success
// appends the sent.log entry the box view (task 14) later reads. A refusal
// or any resolution failure appends nothing.
func sendToCrew(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, crew, text, source string, opts send.Options) (send.Report, error) {
	var report send.Report
	err := w.WithCrewSend(project, crew, func(prior *store.CrewSendAttempt, save func(*store.CrewSendAttempt) error) error {
		var err error
		report, err = sendToCrewLocked(ctx, w, deps, project, crew, text, source, opts, prior, save)
		return err
	})
	return report, err
}

func sendToCrewLocked(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, crew, text, source string, opts send.Options, prior *store.CrewSendAttempt, save func(*store.CrewSendAttempt) error) (send.Report, error) {
	resolved, err := resolveCrewHandle(ctx, w, deps, project, crew)
	if err != nil {
		return send.Report{}, err
	}
	if !resolved.AgentRecorded {
		return send.Report{}, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("crew %s/%s is stopped: crews/%s.meta names no agent", project, crew, crew))
	}
	if !resolved.SessionRunning {
		return send.Report{}, observability.NewError(observability.CodeRuntimeUnavailable,
			fmt.Sprintf("crew %s/%s's herdr session is not running", project, crew))
	}
	obs, err := deps.Runtime.InspectAgent(ctx, resolved.Handle)
	if err != nil {
		return send.Report{}, err
	}
	if obs.Handle.Name != resolved.Handle.Name || obs.Handle.Tab.PaneID != resolved.Handle.Tab.PaneID {
		return send.Report{}, observability.NewError(observability.CodeStateConflict, "crew agent moved; refusing send recovery")
	}
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return send.Report{}, err
	}
	identity, err := json.Marshal([]string{resolved.Handle.Session.Name, resolved.Handle.Session.SocketPath, resolved.Handle.Name,
		resolved.Handle.Tab.PaneID, string(resolved.Kind), obs.SessionRef, meta[spawn.MetaSessionID], meta[spawn.MetaStartedAt], meta[spawn.MetaLaunchedAt]})
	if err != nil {
		return send.Report{}, err
	}
	payload := text
	if opts.Marker {
		payload = send.Marker + text
	}
	opts.ResumePending = prior != nil && prior.Identity == string(identity) && prior.Text == payload && prior.Source == source
	sendDeps := send.Deps{Runtime: deps.Runtime, Sleep: deps.Sleep, BeforeType: func() error {
		return save(&store.CrewSendAttempt{Identity: string(identity), Text: payload, Source: source})
	}}
	report, err := send.Send(ctx, sendDeps, resolved.Handle, resolved.Kind, text, opts)
	if err != nil {
		if errors.Is(err, send.ErrEnterSwallowed) {
			err = fmt.Errorf("%w; the attempt is saved: retry the same mate send command to verify the whole draft and retry Enter only", err)
		}
		return report, err
	}
	if err := w.AppendSent(project, store.SentEntry{
		Source: source,
		Target: store.CrewTarget(crew),
		Text:   text,
	}); err != nil {
		return report, err
	}
	return report, save(nil)
}

// resolvedCrew is what resolveCrewHandle found for one crew. AgentRecorded
// is false exactly when crews/<id>.meta names no agent (a stopped crew).
// SessionRunning is only meaningful when AgentRecorded is true, and being
// false there means Herdr's own session for the workspace is gone - the
// agent may still be "recorded" but there is nothing live to reach.
type resolvedCrew struct {
	AgentRecorded  bool
	SessionRunning bool
	Handle         runtime.AgentHandle
	Kind           harness.Kind
}

// resolveCrewHandle reads crews/<id>.meta and, when it names an agent,
// looks up the workspace's Herdr session so send/peek/state can each decide
// what an absent agent or a dead session means for them. It returns a real
// error only for a genuine resolution failure (a malformed meta file, an
// unparseable harness, Herdr refusing the session lookup itself) - never
// for the ordinary "this crew is stopped" or "this crew's agent is gone"
// cases, which are exactly what the three callers exist to report.
func resolveCrewHandle(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, crew string) (resolvedCrew, error) {
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return resolvedCrew{}, err
	}
	agent := meta[spawn.MetaAgent]
	if agent == "" {
		return resolvedCrew{}, nil
	}
	kind, err := harness.ParseKind(meta[spawn.MetaHarness])
	if err != nil {
		return resolvedCrew{}, err
	}
	spec, err := spawn.SessionSpec(deps, w)
	if err != nil {
		return resolvedCrew{}, err
	}
	session, running, err := deps.Runtime.LookupSession(ctx, spec)
	if err != nil {
		return resolvedCrew{}, err
	}
	if !running {
		return resolvedCrew{AgentRecorded: true}, nil
	}
	tab := runtime.TabHandle{
		Session:     session,
		WorkspaceID: meta[spawn.MetaWorkspace],
		TabID:       meta[spawn.MetaTab],
		PaneID:      meta[spawn.MetaPane],
		Label:       spawn.CrewTabLabelPrefix + crew,
	}
	handle := runtime.AgentHandle{Session: session, Name: agent, RawID: crew, Kind: kind, Tab: tab}
	return resolvedCrew{AgentRecorded: true, SessionRunning: true, Handle: handle, Kind: kind}, nil
}

// printSendRefusalDetails prints whatever pending text or screen tail a
// send's error carries. mainRun already prints the error's own message
// ("mate: <reason>"), so this only adds the extra evidence
// internal/send's coded errors put in Details rather than in the message.
func printSendRefusalDetails(stderr io.Writer, err error) {
	var coded *observability.Error
	if !errors.As(err, &coded) {
		return
	}
	if tail, ok := coded.Details["screen_tail"].(string); ok && tail != "" {
		fmt.Fprintf(stderr, "screen tail:\n%s\n", tail)
	}
	if pending, ok := coded.Details["pending"].(string); ok && pending != "" {
		fmt.Fprintf(stderr, "pending text: %q\n", pending)
	}
}

// sendSummaryLine renders the one line docs/mvp.md task 13 asks for:
// "sent to k3 (empty -> typed -> enter x1 -> working)". Busy is rendered as
// "working" because that is what it means to a human reading this line: the
// harness picked the line up and is now mid-turn.
func sendSummaryLine(r send.Report) string {
	action := "typed"
	if r.Resumed {
		action = "resumed saved draft"
	}
	return fmt.Sprintf("sent to %s (%s → %s → enter ×%d → %s)",
		r.Agent, composerLabel(r.Before.State), action, r.Presses, composerLabel(r.After.State))
}

func composerLabel(s send.ComposerState) string {
	if s == send.StateBusy {
		return "working"
	}
	return string(s)
}
