package main

import (
	"context"

	"github.com/nguyenngocanh94/matev2/internal/autopilot"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/outbox"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// The Mate's delivery wiring (mvp.md tasks 19 and 30), beside the observer's
// in console_watch.go and for the same reason: internal/query is file-only and
// internal/ui/console may reach neither Herdr, internal/outbox nor
// internal/autopilot (its boundary test), so this package is the one place
// that can hold both a live runtime and a snapshot.

// consoleDelivery is everything that types into a Mate's composer without a
// keystroke of the captain's, for as long as the console is open: the outbox
// sender loop (task 30), which delivers every queued `[assign]` and digest,
// and the auto daemon (task 19), which queues digests. They start and stop
// together because neither is any use without the other: a daemon with no
// sender queues digests nobody types, and a console without the sender
// leaves every `[assign]` the Mate was too busy to take waiting forever.
type consoleDelivery struct {
	sender *outbox.Sender
	pilot  *autopilot.Pilot
}

// Start runs the sender loop and the daemon, each on its own goroutine.
func (d *consoleDelivery) Start(ctx context.Context) {
	d.sender.Start(ctx)
	d.pilot.Start(ctx)
}

// Stop ends both and waits for what is in flight; the daemon first, so it
// cannot queue a digest after the sender has gone.
func (d *consoleDelivery) Stop() {
	d.pilot.Stop()
	d.sender.Stop()
}

// Snapshot is the daemon's per-project state, for the MODE cell and footer.
func (d *consoleDelivery) Snapshot() map[string]autopilot.Status { return d.pilot.Snapshot() }

// consolePilot builds the delivery for an open workspace.
//
// The sender loop and the daemon each open their own *store.Workspace over
// the same directory, exactly as consoleWatcher does: a Workspace caches
// workspace.yaml and rewrites that cache on LoadConfig, which both loops and
// query.Load (on the UI goroutine) call on their own schedules. The files
// themselves are coordinated by the outbox's own lock and by flock on the
// append-only logs.
func consolePilot(dir string, deps spawn.Deps) (*consoleDelivery, error) {
	senderWS, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	pilotWS, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	return &consoleDelivery{
		sender: consoleOutbox(senderWS, deps),
		pilot:  consoleAutoPilot(pilotWS, deps),
	}, nil
}

// consoleOutbox is an outbox sender over the live Herdr adapter. Its state is
// all on disk, so any number of them agree: the console's loop, the one an
// `[assign]` uses for its immediate attempt, and the daemon's.
func consoleOutbox(ws *store.Workspace, deps spawn.Deps) *outbox.Sender {
	return outbox.New(ws, outbox.Deps{
		Runtime: deps.Runtime,
		Handle:  consoleMateHandle(ws, deps),
	})
}

// consoleAutoPilot is the auto daemon over ws, queueing into ws's outbox and
// making its one immediate attempt through a sender over the same handle.
func consoleAutoPilot(ws *store.Workspace, deps spawn.Deps) *autopilot.Pilot {
	return autopilot.New(ws, autopilot.Deps{Outbox: consoleOutbox(ws, deps)})
}

// consoleMateHandle is outbox.HandleFunc over spawn.MateHandle: the recorded
// `mate/mate.meta` resolved against the session Herdr currently answers for.
// The sender takes it as a function so it imports nothing that could start
// or stop an agent.
func consoleMateHandle(ws *store.Workspace, deps spawn.Deps) outbox.HandleFunc {
	return func(ctx context.Context, project string) (runtime.AgentHandle, harness.Kind, error) {
		return spawn.MateHandle(ctx, ws, deps, project)
	}
}

// withAutoStatus puts the daemon's own state into a snapshot query.Load built
// out of files alone.
//
// A project the daemon has never ticked keeps the zero AutoDaemon, which
// renders as the bare mode word: "this console has sent nothing for it" is
// the truth, and it is what the zero value already says.
func withAutoStatus(snap query.Snapshot, status map[string]autopilot.Status) query.Snapshot {
	if len(status) == 0 {
		return snap
	}
	for p := range snap.Projects {
		project := &snap.Projects[p]
		s, ok := status[project.ProjectID]
		if !ok {
			continue
		}
		project.Daemon = query.AutoDaemon{
			Sends:      s.Sends,
			LastSentAt: s.LastSentAt,
			Notice:     s.Notice,
			NoticeAt:   s.NoticeAt,
		}
	}
	return snap
}
