package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// recoverySummaryFor is how long the one-line summary stays on the status
// line after recovery finishes. A row that failed keeps its own error.
const recoverySummaryFor = 30 * time.Second

// consoleRecovery runs internal/recover in the background while the console is
// open (docs/mvp.md M17) and says where it is: a status line while it works,
// a summary after, and an error on the row of each agent that did not come
// back. The console is usable throughout; it reads this the way it reads the
// observer's, through apply on every load.
type consoleRecovery struct {
	now func() time.Time

	mu     sync.Mutex
	status query.RecoveryStatus
	doneAt time.Time
	// rowErrs is the error of each agent that did not come back, by
	// project and crew ("" for the Mate).
	rowErrs map[[2]string]string

	done chan struct{}
}

// startConsoleRecovery begins recovering the workspace and returns at once.
// stop cancels what is left and waits briefly for it to undo a half-started
// agent.
func startConsoleRecovery(ctx context.Context, ws *store.Workspace, deps spawn.Deps) (r *consoleRecovery, stop func()) {
	return startRecovery(ctx, func(ctx context.Context, progress func(recovery.Progress)) recovery.Result {
		return recovery.Run(ctx, ws, deps, progress)
	})
}

func startRecovery(ctx context.Context, run func(context.Context, func(recovery.Progress)) recovery.Result) (*consoleRecovery, func()) {
	r := &consoleRecovery{now: time.Now, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(r.done)
		res := run(ctx, r.progress)
		r.finish(res)
	}()
	return r, func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(abandonedWait):
		}
	}
}

func (r *consoleRecovery) progress(p recovery.Progress) {
	line := "recovering…"
	if p.Total > 0 {
		line = fmt.Sprintf("recovering %d of %d…", p.Done, p.Total)
	}
	r.mu.Lock()
	r.status = query.RecoveryStatus{Line: line, Active: true}
	r.mu.Unlock()
}

func (r *consoleRecovery) finish(res recovery.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.doneAt = r.now()
	r.status = recoverySummary(res)
	r.rowErrs = map[[2]string]string{}
	for _, it := range res.Items {
		if it.Err != nil {
			r.rowErrs[[2]string{it.Project, it.Crew}] = "recovery failed: " + oneLineText(it.Err.Error())
		}
	}
}

// recoverySummary is the line recovery leaves behind: what came back, what
// was repaired, and the first thing that did not. A pass that found nothing
// says nothing.
func recoverySummary(res recovery.Result) query.RecoveryStatus {
	if res.Idle() {
		return query.RecoveryStatus{}
	}
	var parts []string
	if len(res.Items) > 0 || (res.Err == nil && len(res.Watchers) == 0) {
		parts = append(parts, fmt.Sprintf("recovered %d", res.Recovered()))
	}
	links := 0
	for _, f := range res.Fixes {
		if f.Err == nil {
			links++
		}
	}
	if links > 0 {
		parts = append(parts, fmt.Sprintf("repaired %d %s", links, plural(links, "link", "links")))
	}
	if n := len(res.Watchers) - failedWatchers(res); n > 0 {
		parts = append(parts, fmt.Sprintf("restarted %d pull request %s", n, plural(n, "watcher", "watchers")))
	}
	failures := res.Failures()
	if len(failures) > 0 {
		failed := fmt.Sprintf("%d failed: %s", len(failures), failures[0])
		parts = append(parts, failed)
	}
	return query.RecoveryStatus{Line: strings.Join(parts, "; "), Failed: len(failures) > 0}
}

func failedWatchers(res recovery.Result) int {
	n := 0
	for _, w := range res.Watchers {
		if w.Err != nil {
			n++
		}
	}
	return n
}

func oneLineText(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(first)
}

// apply puts recovery's state into a snapshot query.Load built out of files
// alone: the status line, and the error of each row that did not come back.
func (r *consoleRecovery) apply(snap query.Snapshot) query.Snapshot {
	if r == nil {
		return snap
	}
	r.mu.Lock()
	status, doneAt := r.status, r.doneAt
	errs := make(map[[2]string]string, len(r.rowErrs))
	for k, v := range r.rowErrs {
		errs[k] = v
	}
	r.mu.Unlock()

	if !status.Active && !doneAt.IsZero() && r.now().Sub(doneAt) > recoverySummaryFor {
		status = query.RecoveryStatus{}
	}
	snap.Recovery = status
	for p := range snap.Projects {
		project := &snap.Projects[p]
		if msg, ok := errs[[2]string{project.ProjectID, ""}]; ok {
			project.Mate.Error = query.KnownField(query.ErrorReason(msg))
		}
		for c := range project.Crews {
			crew := &project.Crews[c]
			if msg, ok := errs[[2]string{project.ProjectID, crew.CrewID}]; ok {
				crew.Error = query.KnownField(query.ErrorReason(msg))
			}
		}
	}
	return snap
}
