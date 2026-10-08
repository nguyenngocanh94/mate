package watch

import (
	"context"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/fixture"
	"github.com/nguyenngocanh94/mate/internal/send"
)

// asking is one observer call in flight for one crew's pane snapshot. The
// call's goroutine writes obs and err and then closes done; the polling
// goroutine reads them only after done is closed.
type asking struct {
	hash string
	done chan struct{}
	obs  screen.Observation
	err  error
}

// reading is the composer an observer read off one snapshot, and which
// observer read it ("fixture", "jev", ...).
type reading struct {
	composer send.ComposerState
	source   string
}

// fixtureSource is the Source the fixture observer stamps.
const fixtureSource = "fixture"

// composer is the crew's composer for this round, read off the snapshot
// whose hash is hash.
//
// The fixture observer (Deps.Observer nil) is local and immediate, and is
// asked in the round. A configured observer can take seconds to answer
// (Jev's deadline is notice.Timeout), so it is asked in the background and
// the poll loop never waits for it (docs/plans/jev-observer-2026-10-08.md
// section 7). A still pane keeps the observer's reading of it. A pane the
// observer has not read yet is read by the fixture in the round, and that
// reading stands until the observer's answer for the same snapshot arrives:
// a busy pane redraws its spinner every poll, so an answer is often for a
// snapshot already gone, and keeping the reading from before the change
// would freeze the column for the whole turn. An answer for a snapshot the
// pane has since left is dropped (section 4.3), and the pane's current
// snapshot is asked about next. At most one call per crew is in flight.
//
// A snapshot the observer could not read is no reading: the round that
// learns it ends without a verdict, and the next asks again.
func (w *Watcher) composer(ctx context.Context, obs *observation, screens harness.ScreenProfile, pane, hash string) (reading, error) {
	if w.deps.Observer == nil {
		if obs.observed == hash {
			return obs.reading, nil
		}
		observed, err := fixture.New().Observe(ctx, screens, pane)
		if err != nil {
			return reading{}, err
		}
		obs.observed, obs.reading = hash, reading{observed.Composer, fixtureSource}
		return obs.reading, nil
	}
	if a := obs.asking; a != nil {
		select {
		case <-a.done:
			obs.asking = nil
			if a.hash == hash {
				if a.err != nil {
					return reading{}, a.err
				}
				obs.observed, obs.reading = hash, reading{a.obs.Composer, a.obs.Source}
			}
		default:
		}
	}
	if obs.observed == hash {
		return obs.reading, nil
	}
	if obs.asking == nil {
		w.ask(ctx, obs, screens, pane, hash)
	}
	interim, err := fixture.New().Observe(ctx, screens, pane)
	if err != nil {
		return reading{}, err
	}
	return reading{interim.Composer, fixtureSource}, nil
}

// ask starts the observer call for one snapshot, bounded by notice.Timeout
// and by ctx: Stop cancels the run's context, and waits for the call.
func (w *Watcher) ask(ctx context.Context, obs *observation, screens harness.ScreenProfile, pane, hash string) {
	a := &asking{hash: hash, done: make(chan struct{})}
	obs.asking = a
	observer := w.deps.Observer
	w.observing.Add(1)
	go func() {
		defer w.observing.Done()
		defer close(a.done)
		ctx, cancel := context.WithTimeout(ctx, notice.Timeout)
		defer cancel()
		a.obs, a.err = observer.Observe(ctx, screens, pane)
	}()
}

// AwaitObserver blocks until every observer call in flight has answered,
// so the next Poll uses what it said. Stop waits the same way. A caller
// driving Poll itself (a test) calls it between rounds, never while Start
// is polling.
func (w *Watcher) AwaitObserver() {
	w.observing.Wait()
}
