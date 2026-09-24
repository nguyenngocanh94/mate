package main

import (
	"context"
	"fmt"
	"time"

	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// consoleSessionStream is the runtime/UI boundary for the embedded session
// view (ADR 0026). The Console receives only its own SessionChannel
// closure; runtime.SessionStream and runtime.TerminalSize never cross into
// internal/ui/console.
//
// A nil stream yields a nil factory, which the Console reads as "no stream
// transport" rather than as a failure at open time. The transport is passed
// in rather than type-asserted out of spawn.Deps so a test can supply
// runtime.FakeSessionStream beside the fake Adapter, which is a separate
// double.
func consoleSessionStream(ws *store.Workspace, stream runtime.SessionStream) console.SessionStreamFactory {
	if stream == nil {
		return nil
	}
	return func(ctx context.Context, target console.SessionTarget, size console.TerminalSize) (console.SessionChannel, error) {
		ref, err := sessionRef(ws, target)
		if err != nil {
			return nil, err
		}
		channel, err := stream.Open(ctx, ref, runtime.TerminalSize{Cols: size.Cols, Rows: size.Rows})
		if err != nil {
			return nil, err
		}
		return consoleSessionChannel{channel: channel}, nil
	}
}

// sessionRef resolves the Herdr identity of the agent a target names out
// of its `.meta` - `mate.meta` for a Mate, `crews/<id>.meta` for a crew -
// which is the only record there is (internal/spawn/doc.go). The meta is
// re-read on every open rather than captured from the Console's snapshot:
// the snapshot can be a refresh old, and opening a PTY against a pane
// nobody owns is exactly the failure that record is a hint about, not
// proof of.
func sessionRef(ws *store.Workspace, target console.SessionTarget) (runtime.AgentSessionRef, error) {
	if target.ProjectID == "" {
		return runtime.AgentSessionRef{}, observability.NewError(observability.CodeUsage,
			"the session target names no Project")
	}
	var (
		meta    map[string]string
		err     error
		stopped error
	)
	switch target.Kind {
	case console.SessionTargetMate:
		meta, err = ws.ReadMateMeta(target.ProjectID)
		stopped = errMateStopped(target.ProjectID)
	case console.SessionTargetCrew:
		if target.ID == "" {
			return runtime.AgentSessionRef{}, observability.NewError(observability.CodeUsage,
				"the session target names no crew")
		}
		meta, err = ws.ReadCrewMeta(target.ProjectID, target.ID)
		stopped = errCrewStopped(target.ProjectID, target.ID)
	default:
		return runtime.AgentSessionRef{}, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("the live session view has no target of kind %q", target.Kind))
	}
	if err != nil {
		return runtime.AgentSessionRef{}, err
	}
	if meta[spawn.MetaAgent] == "" || meta[spawn.MetaPane] == "" {
		// An agent with no pane is the same stopped record seen from the
		// other side: StopMate and StopCrew drop both keys together, so one
		// without the other is a half-written meta, and neither is
		// something to open a PTY against.
		return runtime.AgentSessionRef{}, stopped
	}
	session := meta[spawn.MetaSession]
	if session == "" {
		session = ws.Session()
	}
	return runtime.AgentSessionRef{HerdrSession: session, AgentName: meta[spawn.MetaAgent]}, nil
}

// errMateStopped is the stopped state, not a failure: a Project whose Mate
// has never been started (or has been stopped) has nothing to stream, and
// the reader's next move is the 's' key. It is coded CodeStateConflict
// rather than CodeUnknown so nothing downstream treats it as a transport
// fault.
func errMateStopped(project string) error {
	return observability.NewError(observability.CodeStateConflict,
		fmt.Sprintf("the Mate of %s is stopped; press s to start it", project))
}

// errCrewStopped is the crew counterpart. A stopped crew is not restarted
// from the Console - the Mate spawns a new one - so there is no key to
// offer, only the record that remains.
func errCrewStopped(project, crew string) error {
	return observability.NewError(observability.CodeStateConflict,
		fmt.Sprintf("crew %s of %s is stopped; its record stays in crews/%s", crew, project, crew))
}

// consoleSessionChannel adapts the runtime transport without exposing any
// runtime type to internal/ui/console.
type consoleSessionChannel struct{ channel runtime.SessionChannel }

func (c consoleSessionChannel) Read(ctx context.Context) ([]byte, error) {
	return c.channel.Read(ctx)
}

func (c consoleSessionChannel) Write(ctx context.Context, input []byte) error {
	return c.channel.Write(ctx, input)
}

func (c consoleSessionChannel) Resize(ctx context.Context, size console.TerminalSize) error {
	return c.channel.Resize(ctx, runtime.TerminalSize{Cols: size.Cols, Rows: size.Rows})
}

func (c consoleSessionChannel) Close(ctx context.Context) error {
	return c.channel.Close(ctx)
}

// consoleSessionMetadata is the stream's slow side channel: recorded
// lifecycle and one runtime observation per tick. It never reads the pane's
// contents - the PTY bytes are the sole source of the live frame - and it
// never rewrites a `.meta`, so an agent that disappears from Herdr while
// the view is open is reported as an absent runtime, not promoted into a
// lifecycle change (ADR 0025).
//
// A Mate's recorded status is what `mate status` establishes. A crew's is
// its declared state, resolved in the order of mvp.md section 4b; whether
// Herdr still has the agent is asked the same way for both, and is reported
// on Runtime rather than folded into either status.
func consoleSessionMetadata(ws *store.Workspace, deps spawn.Deps) console.SessionMetadataReader {
	return func(ctx context.Context, target console.SessionTarget) (console.SessionSnapshot, error) {
		snap := console.SessionSnapshot{Target: target, AsOf: time.Now().UTC()}
		if target.ProjectID == "" {
			return snap, observability.NewError(observability.CodeUsage, "the session target names no Project")
		}
		var (
			status spawn.Status
			err    error
		)
		switch target.Kind {
		case console.SessionTargetMate:
			status, err = spawn.MateStatus(ctx, ws, deps, target.ProjectID)
		case console.SessionTargetCrew:
			status, err = spawn.CrewStatus(ctx, ws, deps, target.ProjectID, target.ID)
		default:
			return snap, observability.NewError(observability.CodeUsage,
				fmt.Sprintf("session metadata has no target of kind %q", target.Kind))
		}
		if err != nil {
			return snap, err
		}
		// The box on every metadata tick (mvp.md task 15). It is a set of
		// small file reads under `.mate/`, on the poll that already runs for
		// this session, so the rail follows a crew appending to its status
		// file without the reader pressing 'r' - which is the whole point of
		// a message box rather than a snapshot field.
		snap.Box = query.LoadBox(ws, target.ProjectID)
		snap.RecordedStatus = query.KnownField(string(status.State))
		if target.Kind == console.SessionTargetCrew {
			snap.RecordedStatus = query.KnownField(crewRecordedStatus(ws, target.ProjectID, target.ID))
		}
		switch status.State {
		case spawn.StateRunning:
			snap.Runtime = console.SessionRuntime{Status: query.Known, ObservedAt: time.Now().UTC()}
		default:
			// Stale and stopped are both "Herdr does not have this agent".
			// Absent, never Unknown: the poll itself succeeded.
			snap.Runtime = console.SessionRuntime{Status: query.Absent, Reason: status.Detail}
		}
		return snap, nil
	}
}

// crewRecordedStatus is the word the crew row shows in the tree: the crew's
// declared state, resolved in the fixed order of mvp.md section 4b through
// the same query.CrewStateOf the workspace tree uses. Herdr's own answer
// about the agent is deliberately not an input: that is an observation, and
// it is reported on Runtime and never here (decision 8).
func crewRecordedStatus(ws *store.Workspace, project, crew string) string {
	meta, err := ws.ReadCrewMeta(project, crew)
	if err != nil {
		meta = nil
	}
	openIncident := false
	if view, err := box.Load(ws, project); err == nil {
		openIncident = len(box.BlockingIncidents(view, crew)) > 0
	}
	verb := ""
	if entries, _, err := ws.ReadStatus(project, crew, 0); err == nil {
		lines := make([]string, 0, len(entries))
		for _, e := range entries {
			lines = append(lines, e.Line)
		}
		if v := box.LastVerb(lines); v != box.StateUnknown {
			verb = string(v)
		}
	}
	return string(query.CrewStateOf(meta, openIncident, verb))
}
