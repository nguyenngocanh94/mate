package main

import (
	"context"

	"github.com/nguyenngocanh94/matev2/internal/autopilot"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// The auto daemon's wiring (mvp.md task 19), beside the observer's in
// console_watch.go and for the same reason: internal/query is file-only and
// internal/ui/console may reach neither Herdr nor internal/autopilot (its
// boundary test), so this package is the one place that can hold both a live
// runtime and a snapshot.

// consolePilot builds the auto-mode daemon for an open workspace.
//
// It opens its own *store.Workspace over the same directory, exactly as
// consoleWatcher does: a Workspace caches workspace.yaml and rewrites that
// cache on LoadConfig, which query.Load does on every refresh on the UI
// goroutine, while the daemon ticks on its own. The files themselves are read
// and appended under flock.
func consolePilot(dir string, deps spawn.Deps) (*autopilot.Pilot, error) {
	ws, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	return autopilot.New(ws, autopilot.Deps{
		Runtime: deps.Runtime,
		Handle:  consoleMateHandle(ws, deps),
	}), nil
}

// consoleMateHandle is autopilot.HandleFunc over spawn.MateHandle: the
// recorded `mate/mate.meta` resolved against the session Herdr currently
// answers for. The daemon takes it as a function so it imports nothing that
// could start or stop an agent.
func consoleMateHandle(ws *store.Workspace, deps spawn.Deps) autopilot.HandleFunc {
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
