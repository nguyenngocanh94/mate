package autopilot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// Defaults of docs/mvp.md section 5.
const (
	// DefaultInterval is the digest window: the daemon looks once every 90
	// seconds and says at most one thing.
	DefaultInterval = 90 * time.Second
	// DefaultWedgedAfter is how long a digest may fail to reach the Mate
	// before a `wedged` incident opens.
	DefaultWedgedAfter = 5 * time.Minute
)

// Runtime is the slice of runtime.Adapter the daemon needs, which is exactly
// send.Runtime's: read a pane, type, press keys, wait. It structurally cannot
// start, stop or `herdr agent prompt` anything.
type Runtime interface {
	send.Runtime
}

var _ Runtime = runtime.Adapter(nil)

// HandleFunc resolves the Herdr handle and harness kind of one project's
// Mate. It is a seam rather than a direct call to internal/spawn so this
// package imports nothing that can start or stop an agent; cmd/matev2 wires
// spawn.MateHandle into it.
//
// An error means the digest has nowhere to go - the Mate is not running, or
// the Herdr session is not up.
type HandleFunc func(ctx context.Context, project string) (runtime.AgentHandle, harness.Kind, error)

// Clock is where the daemon reads the time; tests pass a fake one.
type Clock interface {
	Now() time.Time
}

// Sleeper is the pause between ticks and inside a send, interruptible by the
// context. Tests pass one that returns immediately.
type Sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

// Deps are the daemon's collaborators. Runtime and Handle are required;
// everything else has a default.
type Deps struct {
	Runtime Runtime
	Handle  HandleFunc
	Clock   Clock
	Sleeper Sleeper
	// Interval is the digest window; zero means DefaultInterval.
	Interval time.Duration
	// WedgedAfter is how long undelivered digests open a `wedged` incident
	// after; zero means DefaultWedgedAfter.
	WedgedAfter time.Duration
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

func (d Deps) wedgedAfter() time.Duration {
	if d.WedgedAfter > 0 {
		return d.WedgedAfter
	}
	return DefaultWedgedAfter
}

// Status is what the daemon has done for one project, for the console header
// and footer. It is an in-process observation, not a record: nothing on disk
// says "the daemon has sent 14 digests", and a console that restarts starts
// this count again.
type Status struct {
	// Sends is how many digests this console has delivered.
	Sends int
	// LastSentAt is when the last one was delivered, zero before the first.
	LastSentAt time.Time
	// Notice is why the last tick did not deliver, empty when it did or
	// when there was nothing to deliver. It is one line per tick, not one
	// per item: a tick sends one digest, so it has one outcome.
	Notice   string
	NoticeAt time.Time
}

// Sent reports whether this console has delivered anything for the project.
func (s Status) Sent() bool { return s.Sends > 0 }

// Pilot is the auto-mode daemon over one workspace.
//
// One Pilot ticks from one goroutine: Start owns it, and a caller driving
// Tick itself (a test) is that goroutine. Snapshot may be called from any
// other goroutine at any time.
//
// The workspace handle is this package's own for the reason the observer's
// is (store.Workspace caches workspace.yaml and re-reads it on LoadConfig
// while the console reloads on its own goroutine); cmd/matev2 does the
// opening.
type Pilot struct {
	ws   *store.Workspace
	deps Deps

	// failing is the first tick at which each project had something to say
	// and could not say it. Only the ticking goroutine touches it. It is
	// in-process on purpose: it is a stopwatch, not a record, and the record
	// it produces - the `wedged` incident - is the durable half.
	failing map[string]time.Time

	mu     sync.Mutex
	status map[string]Status
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a daemon over a workspace. It sends nothing until Start or Tick
// is called.
func New(ws *store.Workspace, deps Deps) *Pilot {
	return &Pilot{
		ws:      ws,
		deps:    deps,
		failing: make(map[string]time.Time),
		status:  make(map[string]Status),
	}
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

// Snapshot is the daemon's per-project state, copied for a reader on another
// goroutine.
func (p *Pilot) Snapshot() map[string]Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]Status, len(p.status))
	for project, status := range p.status {
		out[project] = status
	}
	return out
}

// Tick runs one digest window over every project of the workspace.
//
// The error it returns is the joined per-project failures of the tick; the
// tick itself always finishes. Nothing is ever sent for a project whose
// `.auto` flag is absent, which is the whole of supervised mode.
func (p *Pilot) Tick(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
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
	if !p.ws.Auto(project) {
		// Supervised. Nothing is sent, and the stopwatch is dropped: there
		// is no pending digest any more, so nothing is failing to arrive.
		// The cursor stays on disk, which is what makes turning auto back on
		// resume rather than replay.
		delete(p.failing, project)
		p.clearNotice(project)
		return nil
	}

	view, err := box.Load(p.ws, project)
	if err != nil {
		return err
	}
	cursor, err := p.ws.ReadAutoCursor(project)
	if err != nil {
		return err
	}
	now := p.deps.now()
	items := Gather(view, cursor, now)
	if len(items) == 0 {
		// Nothing new. The daemon is not a heartbeat, and a tick with
		// nothing to say cannot be failing to say it.
		delete(p.failing, project)
		p.clearNotice(project)
		return nil
	}

	line := Line(items, p.ws.CrewsDir(project))
	report, err := p.deliver(ctx, project, line)
	if err != nil {
		return p.undelivered(project, view, now, err)
	}

	// sent.log is written before the cursor moves. Recording a line the Mate
	// did receive and then failing to record how far we got costs one
	// repeated digest; moving the cursor first and then failing would lose
	// the items entirely, and the two are not the same mistake.
	if err := p.ws.AppendSent(project, store.SentEntry{
		Source: store.SourceApp,
		Target: store.TargetMate,
		Text:   line,
	}); err != nil {
		return err
	}
	if err := p.ws.WriteAutoCursor(project, Advance(cursor, items)); err != nil {
		return err
	}

	delete(p.failing, project)
	if err := p.resolveWedged(project, view, now, report.Agent); err != nil {
		return err
	}
	p.delivered(project, now)
	return nil
}

// deliver resolves the Mate's pane and types the digest into it, verified.
func (p *Pilot) deliver(ctx context.Context, project, line string) (send.Report, error) {
	if p.deps.Handle == nil {
		return send.Report{}, errors.New("autopilot: no handle resolver is wired")
	}
	handle, kind, err := p.deps.Handle(ctx, project)
	if err != nil {
		return send.Report{}, err
	}
	// The flag is read again here, as late as it can be read. box.Load and
	// the handle lookup both touch the filesystem and Herdr, and a captain
	// who typed into the Mate during that window has already had `.auto`
	// deleted by the Mate's own hook; answering them a moment later would be
	// exactly the takeover auto mode promises not to fight.
	if !p.ws.Auto(project) {
		return send.Report{}, errAutoOff
	}
	deps := send.Deps{Runtime: p.deps.Runtime}
	if p.deps.Sleeper != nil {
		deps.Sleep = p.deps.sleep
	}
	return send.Send(ctx, deps, handle, kind, line, send.Options{Marker: true})
}

// errAutoOff is the one "failure" that is not one: the captain took over
// between the tick starting and the line being typed. Nothing was sent,
// nothing is wedged, and the next tick will find the project supervised.
var errAutoOff = errors.New("auto mode was turned off during the tick")

// undelivered records a tick that had something to say and could not say it:
// one footer notice, and a `wedged` incident once the failure has lasted.
func (p *Pilot) undelivered(project string, view box.View, now time.Time, cause error) error {
	if errors.Is(cause, errAutoOff) {
		delete(p.failing, project)
		p.clearNotice(project)
		return nil
	}

	since, ok := p.failing[project]
	if !ok {
		since = now
		p.failing[project] = since
	}
	p.note(project, fmt.Sprintf("auto digest for %s not delivered: %v", project, cause), now)

	if now.Sub(since) < p.deps.wedgedAfter() {
		return nil
	}
	return p.openWedged(project, view, now, fmt.Sprintf(
		"no digest has reached the Mate for %s: %v", now.Sub(since).Round(time.Second), cause))
}

// openWedged appends the `open` line, unless one is already open. The file is
// the state (mvp.md section 4b), so a console that restarted opens nothing
// its predecessor already opened.
func (p *Pilot) openWedged(project string, view box.View, now time.Time, text string) error {
	if wedgedOpen(view) {
		return nil
	}
	return p.ws.AppendIncident(project, store.IncidentEntry{
		Time:  now,
		Crew:  MateCrew,
		Kind:  string(box.IncidentWedged),
		State: store.IncidentOpen,
		Text:  text,
	})
}

func (p *Pilot) resolveWedged(project string, view box.View, now time.Time, agent string) error {
	if !wedgedOpen(view) {
		return nil
	}
	return p.ws.AppendIncident(project, store.IncidentEntry{
		Time:  now,
		Crew:  MateCrew,
		Kind:  string(box.IncidentWedged),
		State: store.IncidentResolved,
		Text:  fmt.Sprintf("a digest was verified into %s's composer", agent),
	})
}

// wedgedOpen reads the daemon's own incident out of the merged view it
// already loaded, rather than re-reading incidents.log: the daemon is the
// only writer of a (mate, wedged) pair, so the view cannot be stale about it.
func wedgedOpen(view box.View) bool {
	for _, inc := range box.OpenIncidents(view, MateCrew) {
		if inc.Kind == box.IncidentWedged {
			return true
		}
	}
	return false
}

func (p *Pilot) delivered(project string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	status := p.status[project]
	status.Sends++
	status.LastSentAt = now
	status.Notice, status.NoticeAt = "", time.Time{}
	p.status[project] = status
}

func (p *Pilot) note(project, text string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	status := p.status[project]
	status.Notice, status.NoticeAt = text, now
	p.status[project] = status
}

func (p *Pilot) clearNotice(project string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	status, ok := p.status[project]
	if !ok || status.Notice == "" {
		return
	}
	status.Notice, status.NoticeAt = "", time.Time{}
	p.status[project] = status
}
