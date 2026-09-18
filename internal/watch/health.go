package watch

import (
	"time"

	"github.com/nguyenngocanh94/matev2/internal/send"
)

// Health is one observation of a crew: what Herdr answered about the agent,
// what its pane was showing, and how long nothing about it has moved. It is
// the health column of docs/mvp.md section 4b - an observation beside the
// state, never a state - and the console draws it in the crew row's NOTE
// cell.
//
// It exists only in memory. Nothing writes a health file: an observation is
// true at the instant it was made and stale immediately afterwards, which is
// what ObservedAt is for.
type Health struct {
	// AgentPresent is Herdr's own answer about the recorded agent. False
	// means Herdr said it does not have it; a lookup that failed produces
	// no Health at all rather than a false here.
	AgentPresent bool
	// Composer is what send.ClassifyComposer read off the pane. It is
	// send.StateUnknown when the agent is gone or the screen could not be
	// classified.
	Composer send.ComposerState
	// QuietFor is how long the pane's contents and the crew's status file
	// have both been unchanged, measured from the first poll that saw this
	// crew: a console that has just opened knows nothing about the hour
	// before it started, and says so by counting from zero.
	QuietFor time.Duration
	// ObservedAt is when this observation was made.
	ObservedAt time.Time
}

// CrewRef names one crew of one project. It is the key of a health snapshot,
// because the console asks about a crew it found in a snapshot of the whole
// workspace.
type CrewRef struct {
	Project string
	Crew    string
}

// Snapshot is every crew's latest health, copied. It never blocks on a poll
// in flight: the poller builds its results off the lock and swaps them in
// under it, so a reader waits only for the swap.
func (w *Watcher) Snapshot() map[CrewRef]Health {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[CrewRef]Health, len(w.health))
	for ref, h := range w.health {
		out[ref] = h
	}
	return out
}

// Health is one crew's latest observation, and whether there is one.
func (w *Watcher) Health(project, crew string) (Health, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	h, ok := w.health[CrewRef{Project: project, Crew: crew}]
	return h, ok
}
