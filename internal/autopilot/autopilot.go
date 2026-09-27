package autopilot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// DefaultInterval is the digest window of docs/mvp.md section 5: the daemon
// looks once every 90 seconds and says at most one thing.
const DefaultInterval = 90 * time.Second

// DefaultWedgedAfter is how long a digest may wait before `wedged`. It is
// the outbox's rule (outbox.DefaultWedgedAfter), which is the one
// implementation of it since task 30; the name stays here because the
// section 5 contract is the daemon's.
const DefaultWedgedAfter = outbox.DefaultWedgedAfter

// Clock is where the daemon reads the time; tests pass a fake one.
type Clock interface {
	Now() time.Time
}

// Sleeper is the pause between ticks, interruptible by the context. Tests
// pass one that returns immediately.
type Sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

// Deps are the daemon's collaborators. Outbox is required.
type Deps struct {
	// Outbox is where a digest is queued, and the sender that makes the
	// one immediate attempt right after (task 30). The daemon types
	// nothing itself.
	Outbox  *outbox.Sender
	Clock   Clock
	Sleeper Sleeper
	// Interval is the digest window; zero means DefaultInterval.
	Interval time.Duration
	// QuietAfter is how long the captain leaves a finished Mate alone
	// before auto mode comes back on; zero means DefaultQuietAfter.
	QuietAfter time.Duration
}

func (d Deps) quietAfter() time.Duration {
	if d.QuietAfter > 0 {
		return d.QuietAfter
	}
	return DefaultQuietAfter
}

func (d Deps) now() time.Time {
	if d.Clock != nil {
		return d.Clock.Now()
	}
	return time.Now()
}

func (d Deps) sleep(ctx context.Context, dur time.Duration) error {
	if d.Sleeper != nil {
		return d.Sleeper.Sleep(ctx, dur)
	}
	timer := time.NewTimer(dur)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (d Deps) interval() time.Duration {
	if d.Interval > 0 {
		return d.Interval
	}
	return DefaultInterval
}

// Status is what the daemon's digests have done for one project, for the
// console header and footer. It is read out of the project's outbox, so it
// counts the digests the outbox still remembers (store.OutboxRetention),
// not only the ones this console delivered.
type Status struct {
	// Sends is how many digests the outbox holds as delivered.
	Sends int
	// LastSentAt is when the last one was delivered, zero before the first.
	LastSentAt time.Time
	// Notice is why the queued digest has not been delivered yet, empty
	// when none is waiting or when it has not been tried. It is one line
	// per project, not one per item: a project has at most one digest
	// queued, so it has one outcome.
	Notice   string
	NoticeAt time.Time
}

// Sent reports whether a digest has been delivered for the project.
func (s Status) Sent() bool { return s.Sends > 0 }

// Pilot is the auto-mode daemon over one workspace.
//
// One Pilot ticks from one goroutine: Start owns it, and a caller driving
// Tick itself (a test) is that goroutine. Snapshot may be called from any
// other goroutine at any time.
//
// The workspace handle is this package's own for the reason the observer's
// is (store.Workspace caches workspace.yaml and re-reads it on LoadConfig
// while the console reloads on its own goroutine); cmd/mate does the
// opening, and gives the Outbox sender the same handle.
type Pilot struct {
	ws   *store.Workspace
	deps Deps

	// talk is each project's sent.log as far as rearm has read it. Only
	// the ticking goroutine touches it.
	talk map[string]*talk

	mu     sync.Mutex
	seen   map[string]bool
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a daemon over a workspace. It queues nothing until Start or
// Tick is called.
func New(ws *store.Workspace, deps Deps) *Pilot {
	return &Pilot{ws: ws, deps: deps, seen: make(map[string]bool), talk: make(map[string]*talk)}
}

// Start begins ticking in its own goroutine until Stop or a cancelled
// context. Calling it twice is a no-op.
func (p *Pilot) Start(ctx context.Context) {
	p.mu.Lock()
	if p.cancel != nil {
		p.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.done = make(chan struct{})
	p.mu.Unlock()
	go p.run(runCtx)
}

// Stop ends the ticking goroutine and waits for the tick in flight.
func (p *Pilot) Stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.cancel, p.done = nil, nil
	p.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (p *Pilot) run(ctx context.Context) {
	defer close(p.done)
	for {
		// A failed tick is not fatal: Herdr may be restarting, a project
		// directory may be half-written. Nothing was sent, no cursor moved,
		// and the next tick asks again.
		_ = p.Tick(ctx)
		if err := p.deps.sleep(ctx, p.deps.interval()); err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// Snapshot is the daemon's per-project state, read out of the outbox of each
// project the daemon has ticked, for a reader on another goroutine.
func (p *Pilot) Snapshot() map[string]Status {
	p.mu.Lock()
	projects := make([]string, 0, len(p.seen))
	for project := range p.seen {
		projects = append(projects, project)
	}
	p.mu.Unlock()

	out := make(map[string]Status, len(projects))
	for _, project := range projects {
		items, err := p.ws.ReadOutbox(project)
		if err != nil {
			continue
		}
		var status Status
		for _, item := range items {
			if item.Source != store.OutboxSourceDigest {
				continue
			}
			switch {
			case item.State == store.OutboxSent:
				status.Sends++
				if item.SentAt.After(status.LastSentAt) {
					status.LastSentAt = item.SentAt
				}
			case item.Queued() && item.LastRefusal != "" && p.ws.Auto(project):
				status.Notice = fmt.Sprintf("auto digest for %s not delivered: %s", project, item.LastRefusal)
				status.NoticeAt = item.TriedAt
			}
		}
		out[project] = status
	}
	return out
}

// Tick runs one digest window over every project of the workspace.
//
// The error it returns is the joined per-project failures of the tick; the
// tick itself always finishes. Nothing is ever queued for a project whose
// `.auto` flag is absent, which is the whole of manual mode.
func (p *Pilot) Tick(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.deps.Outbox == nil {
		return errors.New("autopilot: no outbox is wired")
	}
	// Re-read workspace.yaml every tick, for the reason the observer does: a
	// project registered beside this console must come under the daemon
	// without a restart.
	if err := p.ws.LoadConfig(); err != nil {
		return err
	}

	var errs []error
	for _, ref := range p.ws.Projects() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.tickProject(ctx, ref.Name); err != nil {
			errs = append(errs, fmt.Errorf("autopilot: %s: %w", ref.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (p *Pilot) tickProject(ctx context.Context, project string) error {
	p.mu.Lock()
	p.seen[project] = true
	p.mu.Unlock()
	now := p.deps.now()
	if !p.ws.Auto(project) {
		rearmed, err := p.rearm(project, now)
		if err != nil {
			return err
		}
		if !rearmed {
			// Manual. Nothing is queued. A digest still waiting from
			// before the flag went off is the outbox's to withdraw: it
			// re-reads the flag before it types, so the captain who took
			// over is not answered by a machine. The cursor stays on
			// disk, which is what makes turning auto back on resume
			// rather than replay.
			return nil
		}
	}
	return p.digest(ctx, project, now)
}

// digest is a tick of a project in auto mode.
func (p *Pilot) digest(ctx context.Context, project string, now time.Time) error {
	// The gather runs under the outbox's lock (Offer), because the cursor
	// it reads is moved by the sender when a digest is marked sent, under
	// that same lock. Reading it outside could see a digest's items as new
	// a moment after the sender delivered them.
	crewsDir := p.ws.CrewsDir(project)
	_, queued, err := p.deps.Outbox.Offer(project, store.OutboxSourceDigest, func() (outbox.Request, bool, error) {
		view, err := box.Load(p.ws, project)
		if err != nil {
			return outbox.Request{}, false, err
		}
		cursor, err := p.ws.ReadAutoCursor(project)
		if err != nil {
			return outbox.Request{}, false, err
		}
		items := Gather(view, cursor, now)
		if len(items) == 0 {
			// Nothing new. The daemon is not a heartbeat.
			return outbox.Request{}, false, nil
		}
		return outbox.Request{
			Key:    Key(items),
			Text:   Line(items, crewsDir),
			Cursor: Advance(cursor, items),
		}, true, nil
	})
	if err != nil || !queued {
		return err
	}
	// One immediate attempt, so an idle Mate gets the digest now rather than
	// at the sender loop's next pass. A refusal is not an error here: the
	// digest stays queued and the loop retries it.
	_, err = p.deps.Outbox.Attempt(ctx, project)
	return err
}
