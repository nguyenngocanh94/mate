package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/autopilot"
	"github.com/nguyenngocanh94/matev2/internal/outbox"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
)

// The console's auto-daemon wiring (mvp.md task 19): internal/autopilot over
// the real spawn.MateHandle, and its state put into the snapshot the Console
// draws. Nothing here stubs internal/autopilot - the point is that a Mate
// started through the fake Herdr really is digested to, and that the row the
// Console gets says so.

func TestConsolePilotDigestsToAStartedMate(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	rt := deps.Runtime.(*runtime.Fake)
	handle, _, err := spawn.MateHandle(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateHandle: %v", err)
	}
	rt.SetReadOutput(handle, claudeEmptyScreen)

	if err := w.SetAuto("shop", true); err != nil {
		t.Fatalf("SetAuto: %v", err)
	}
	if err := w.AppendStatus("shop", "k3", "needs-decision: pick A or B"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}

	pilot := autopilot.New(w, autopilot.Deps{
		Outbox: outbox.New(w, outbox.Deps{
			Runtime: deps.Runtime,
			Handle:  consoleMateHandle(w, deps),
			Sleeper: noSleep{},
		}),
		Sleeper: noSleep{},
	})
	if err := pilot.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(rt.SentText) != 1 || !strings.HasPrefix(rt.SentText[0].Text, send.Marker+"digest: 1 item(s)") {
		t.Fatalf("typed %+v into %s, want one marked digest", rt.SentText, res.Agent)
	}

	// And the snapshot the Console draws carries the daemon's own state.
	snap, err := query.Load(context.Background(), w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if got := snap.Projects[0].Daemon; got.Sends != 0 {
		t.Fatalf("query.Load filled Daemon = %+v; it reads files and cannot know this", got)
	}
	merged := withAutoStatus(snap, pilot.Snapshot())
	daemon := merged.Projects[0].Daemon
	if daemon.Sends != 1 || daemon.LastSentAt.IsZero() {
		t.Fatalf("Daemon = %+v, want one send with a time", daemon)
	}
	if daemon.Notice != "" {
		t.Fatalf("Daemon.Notice = %q after a delivered digest", daemon.Notice)
	}
}

// A project the daemon has not ticked keeps the zero value: "this console has
// sent nothing for it" is what the zero value already says, and borrowing a
// neighbour's would put another project's clock on this row.
func TestWithAutoStatusLeavesUntickedProjectsAlone(t *testing.T) {
	snap := query.Snapshot{Projects: []query.ProjectNode{
		{ProjectID: "shop"}, {ProjectID: "blog"},
	}}
	merged := withAutoStatus(snap, map[string]autopilot.Status{
		"shop": {Sends: 3, Notice: "not delivered"},
	})
	if merged.Projects[0].Daemon.Sends != 3 {
		t.Fatalf("shop = %+v, want the daemon's state", merged.Projects[0].Daemon)
	}
	if merged.Projects[1].Daemon != (query.AutoDaemon{}) {
		t.Fatalf("blog = %+v, want the zero value", merged.Projects[1].Daemon)
	}
}

// noSleep runs the daemon's real settle and retry durations at memory speed.
type noSleep struct{}

func (noSleep) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }
