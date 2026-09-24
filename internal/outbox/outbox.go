// Package outbox is the one sender into a Mate's composer (docs/mvp.md task
// 30): every line matev2 types into a Mate on its own initiative - an
// `[assign]` the captain pressed, a digest the auto daemon built - is queued
// in `mate/.outbox` (internal/store/outbox.go) and delivered from here.
//
// # Why a queue
//
// The Mate's composer is shared with the captain and is busy most of the
// time. Task 24 measured `[assign]` on a supervising Mate refused four or
// five times in a row with `agent is mid-turn`, because section 9's poll
// loop keeps the Mate inside a tool call for most of every cycle; the
// captain was left pressing the button again until it went through. The
// auto daemon had the same problem and its own private retry and `wedged`
// stopwatch for it. Both now enqueue here and return, and this package
// retries every DefaultInterval until the composer is empty.
//
// # What it never does
//
// It never hands a line to the harness's own queue (`herdr agent prompt`,
// send.Options.QueueWhileBusy): a line queued behind a running turn is one
// nobody can prove was read, which is the failure mvp.md section 7 records.
// It never types into a composer that is not empty: send.Send classifies
// first and refuses Busy, Pending and Unknown. And it never types one item
// twice: an item is attempted only under the outbox's writers' lock, marked
// sent in the same critical section that delivered it, and - for the one
// window a crash can leave open, between sent.log and the outbox rewrite -
// looked for in sent.log before it is typed again.
//
// # Wedged
//
// A queued item older than WedgedAfter at a failed attempt opens a `wedged`
// incident whose crew is `mate` (mvp.md section 4b); the next verified send
// resolves it. The clock is the oldest queued item's own `at`, on disk, so a
// console restart neither resets it nor opens a second incident: the file is
// the state, and the open line is written only when none is open. This is
// the one implementation of that rule; internal/autopilot no longer has its
// own.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// MateCrew is the crew field of the `wedged` incident this package opens.
// The Mate is not a crew, but `incidents.log` has one identity per line - a
// (crew, kind) pair - and a wedged composer is a finding about the Mate's
// pane, so this is the name it is filed under.
const MateCrew = "mate"

// WedgedKind is the incident kind (box.IncidentWedged's spelling; this
// package does not import internal/box).
const WedgedKind = "wedged"

// Defaults of mvp.md task 30.
const (
	// DefaultInterval is how often the loop retries the oldest queued item
	// of each project.
	DefaultInterval = 2 * time.Second
	// DefaultWedgedAfter is how long an item may wait before `wedged`.
	DefaultWedgedAfter = 5 * time.Minute
)

// Runtime is send.Runtime: read a pane, type, press keys, wait. It
// structurally cannot start, stop or prompt anything.
type Runtime interface {
	send.Runtime
}

var _ Runtime = runtime.Adapter(nil)

// HandleFunc resolves the Herdr handle and harness of one project's Mate.
// cmd/matev2 wires spawn.MateHandle into it, so this package imports nothing
// that can start or stop an agent. An error means the line has nowhere to
// go yet: the Mate is not running, or Herdr is not answering.
type HandleFunc func(ctx context.Context, project string) (runtime.AgentHandle, harness.Kind, error)

// Clock is where the sender reads the time; tests pass a fake one.
type Clock interface {
	Now() time.Time
}

// Sleeper is the pause between passes and inside a send.
type Sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

// Deps are the sender's collaborators. Runtime and Handle are needed to
// deliver; Enqueue and Offer need neither.
type Deps struct {
	Runtime     Runtime
	Handle      HandleFunc
	Clock       Clock
	Sleeper     Sleeper
	Interval    time.Duration
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

// Sender delivers a workspace's outboxes. All of its state is on disk, so
// any number of Senders over one workspace agree: the console's loop, the
// immediate attempt an `[assign]` makes, and the auto daemon's own attempt
// right after it enqueues.
//
// A Sender that runs its loop (Start) must own its *store.Workspace, for the
// reason the observer and the daemon own theirs: Drain re-reads
// workspace.yaml, which rewrites the handle's cache.
type Sender struct {
	ws   *store.Workspace
	deps Deps

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds a sender. It sends nothing until Attempt, Drain or Start.
func New(ws *store.Workspace, deps Deps) *Sender {
	return &Sender{ws: ws, deps: deps}
}

// Request is one line to queue.
type Request struct {
	// Source is store.OutboxSourceAssign or store.OutboxSourceDigest.
	Source string
	// Key is the dedup key; see store.OutboxItem.Key.
	Key string
	// Text is the line without the marker.
	Text string
	// Cursor is the digest's auto cursor to record once it is sent.
	Cursor map[string]int64
}

func (r Request) validate() error {
	if r.Source != store.OutboxSourceAssign && r.Source != store.OutboxSourceDigest {
		return observability.NewError(observability.CodeUsage, fmt.Sprintf("outbox: unknown source %q", r.Source))
	}
	if strings.TrimSpace(r.Key) == "" {
		return observability.NewError(observability.CodeUsage, "outbox: an item needs a dedup key")
	}
	if strings.TrimSpace(r.Text) == "" {
		return observability.NewError(observability.CodeUsage, "outbox: an item needs a non-empty line")
	}
	if strings.ContainsAny(r.Text, "\r\n") {
		return observability.NewError(observability.CodeUsage,
			"outbox: one line per item; long content goes in a file the Mate is pointed at")
	}
	return nil
}

// Enqueued is what Enqueue did.
type Enqueued struct {
	// Item is the item now in the outbox for the request's key: the new
	// one, or the earlier one that made this a duplicate.
	Item store.OutboxItem
	// Duplicate is true when an item with the same key was already queued
	// or sent, so nothing was added.
	Duplicate bool
}

// Enqueue appends one item unless an item with the same key is already
// queued or sent. A dropped item does not count: it was never delivered.
func (s *Sender) Enqueue(project string, req Request) (Enqueued, error) {
	if err := req.validate(); err != nil {
		return Enqueued{}, err
	}
	var out Enqueued
	now := s.deps.now()
	err := s.ws.UpdateOutbox(project, now, func(items []store.OutboxItem) ([]store.OutboxItem, bool, error) {
		for _, item := range items {
			if item.Key == req.Key && item.State != store.OutboxDropped {
				out = Enqueued{Item: item, Duplicate: true}
				return items, false, nil
			}
		}
		item, err := s.newItem(project, items, req, now)
		if err != nil {
			return items, false, err
		}
		out.Item = item
		return append(items, item), true, nil
	})
	return out, err
}

// Offer is the auto daemon's enqueue: at most one queued item of a source at
// a time, refreshed in place. build runs under the writers' lock, so what it
// reads (the auto cursor) cannot move under it: the cursor moves only when
// an item is marked sent, and that happens under the same lock.
//
// When build reports nothing to say, a queued item of that source is
// dropped - what it reported has been answered meanwhile. When it does have
// something, a queued item of that source with the same key is left alone,
// one with a different key is rewritten with the new line (keeping its id,
// its `at` - which is the wedged clock - and its attempts), and with none
// queued a new item is appended.
func (s *Sender) Offer(project, source string,
	build func() (req Request, ok bool, err error)) (store.OutboxItem, bool, error) {
	var (
		out  store.OutboxItem
		have bool
	)
	now := s.deps.now()
	err := s.ws.UpdateOutbox(project, now, func(items []store.OutboxItem) ([]store.OutboxItem, bool, error) {
		req, ok, err := build()
		if err != nil {
			return items, false, err
		}
		queued := -1
		for i, item := range items {
			if item.Source == source && item.Queued() {
				queued = i
				break
			}
		}
		if !ok {
			if queued < 0 {
				return items, false, nil
			}
			items[queued].State = store.OutboxDropped
			items[queued].LastRefusal = "nothing left to report"
			return items, true, nil
		}
		req.Source = source
		if err := req.validate(); err != nil {
			return items, false, err
		}
		have = true
		if queued >= 0 {
			item := &items[queued]
			if item.Key == req.Key {
				out = *item
				return items, false, nil
			}
			item.Key, item.Text, item.Cursor = req.Key, req.Text, req.Cursor
			out = *item
			return items, true, nil
		}
		item, err := s.newItem(project, items, req, now)
		if err != nil {
			return items, false, err
		}
		out = item
		return append(items, item), true, nil
	})
	return out, have, err
}

func (s *Sender) newItem(project string, items []store.OutboxItem, req Request, now time.Time) (store.OutboxItem, error) {
	from, err := s.ws.SentLogSize(project)
	if err != nil {
		return store.OutboxItem{}, err
	}
	return store.OutboxItem{
		ID:          store.NextOutboxID(items),
		At:          now,
		Source:      req.Source,
		Key:         req.Key,
		Text:        req.Text,
		State:       store.OutboxQueued,
		SentLogFrom: from,
		Cursor:      req.Cursor,
	}, nil
}

// Attempt is what one try at a project's oldest queued item did.
type Attempt struct {
	// Item is the item tried, as it stands after the try. It is the zero
	// value when nothing was queued.
	Item store.OutboxItem
	// Delivered is the item verified into the composer on this try (or
	// found already delivered in sent.log).
	Delivered bool
	// Dropped is a digest withdrawn because its project left auto mode.
	Dropped bool
	// Refusal is why the item is still queued.
	Refusal error
	// Agent is the Mate's agent name, when a send was made.
	Agent string
}

// Tried reports whether there was anything queued to try.
func (a Attempt) Tried() bool { return a.Item.ID != 0 }

// Attempt tries the oldest queued item of one project, once.
//
// The error is for what went wrong around a send - the outbox could not be
// read or written, sent.log could not be appended after a verified send -
// never for the send's own refusal, which is Attempt.Refusal: a Mate
// mid-turn is the normal case here, not a failure.
func (s *Sender) Attempt(ctx context.Context, project string) (Attempt, error) {
	var (
		out     Attempt
		postErr error
	)
	now := s.deps.now()
	err := s.ws.UpdateOutbox(project, now, func(items []store.OutboxItem) ([]store.OutboxItem, bool, error) {
		index := -1
		for i, item := range items {
			if item.Queued() {
				index = i
				break
			}
		}
		if index < 0 {
			return items, false, nil
		}
		item := &items[index]

		// A line sent.log already holds was delivered by a sender that
		// died before it could say so. Typing it again is the one mistake
		// this package exists to rule out.
		if at, ok, err := s.recorded(project, *item); err != nil {
			return items, false, err
		} else if ok {
			postErr = s.settle(project, item, at, "")
			out = Attempt{Item: *item, Delivered: true}
			return items, true, nil
		}

		if s.withdrawn(project, *item) {
			item.State = store.OutboxDropped
			item.LastRefusal = "auto mode was turned off before the digest could be typed"
			out = Attempt{Item: *item, Dropped: true}
			return items, true, nil
		}

		report, err := s.deliver(ctx, project, *item)
		if errors.Is(err, errWithdrawn) {
			item.State = store.OutboxDropped
			item.LastRefusal = "auto mode was turned off before the digest could be typed"
			out = Attempt{Item: *item, Dropped: true}
			return items, true, nil
		}
		item.Attempts++
		item.TriedAt = now
		if err != nil {
			item.LastRefusal = err.Error()
			out = Attempt{Item: *item, Refusal: err}
			if age := now.Sub(item.At); age >= s.deps.wedgedAfter() {
				postErr = s.openWedged(project, now, fmt.Sprintf(
					"nothing queued for the Mate has been delivered for %s: %v", age.Round(time.Second), err))
			}
			return items, true, nil
		}

		// sent.log first, then the digest cursor, then the item marked
		// sent: a crash after sent.log costs nothing (the next attempt
		// finds the line there), and the reverse order could lose a line
		// the Mate never got. The time is read again: a verified send
		// takes a second or more of settle and enter retries, and the
		// record is of when the composer cleared, not when the try began.
		delivered := s.deps.now()
		recordErr := s.ws.AppendSent(project, store.SentEntry{
			Time:   delivered,
			Source: store.SourceApp,
			Target: store.TargetMate,
			Text:   item.Text,
		})
		postErr = errors.Join(recordErr, s.settle(project, item, delivered, report.Agent))
		out = Attempt{Item: *item, Delivered: true, Agent: report.Agent}
		return items, true, nil
	})
	return out, errors.Join(err, postErr)
}

// settle marks an item delivered: the digest's cursor recorded, the item
// sent, and an open `wedged` resolved.
func (s *Sender) settle(project string, item *store.OutboxItem, at time.Time, agent string) error {
	var errs []error
	if item.Source == store.OutboxSourceDigest && item.Cursor != nil {
		errs = append(errs, s.ws.WriteAutoCursor(project, item.Cursor))
	}
	item.State = store.OutboxSent
	item.SentAt = at
	item.LastRefusal = ""
	who := "the Mate"
	if agent != "" {
		who = agent
	}
	errs = append(errs, s.resolveWedged(project, at, fmt.Sprintf("a queued line was verified into %s's composer", who)))
	return errors.Join(errs...)
}

// errWithdrawn is the one refusal that is not one: the captain took the
// composer over (the Mate's hook deleted `.auto`) while the digest was on its
// way. Nothing was typed, nothing is wedged.
var errWithdrawn = errors.New("auto mode was turned off")

// withdrawn reports a digest whose project is no longer in auto mode. An
// assign is the captain's own keystroke and is never withdrawn this way.
func (s *Sender) withdrawn(project string, item store.OutboxItem) bool {
	return item.Source == store.OutboxSourceDigest && !s.ws.Auto(project)
}

// deliver resolves the Mate's pane and types the line into it, verified.
func (s *Sender) deliver(ctx context.Context, project string, item store.OutboxItem) (send.Report, error) {
	if s.deps.Runtime == nil || s.deps.Handle == nil {
		return send.Report{}, errors.New("outbox: no runtime is wired to deliver with")
	}
	handle, kind, err := s.deps.Handle(ctx, project)
	if err != nil {
		return send.Report{}, err
	}
	// Read again as late as it can be read: the handle lookup touches Herdr,
	// and a captain who typed into the Mate meanwhile has already had
	// `.auto` deleted by the Mate's own hook. Answering them a moment later
	// is the takeover auto mode promises not to fight.
	if s.withdrawn(project, item) {
		return send.Report{}, errWithdrawn
	}
	deps := send.Deps{Runtime: s.deps.Runtime}
	if s.deps.Sleeper != nil {
		deps.Sleep = s.deps.sleep
	}
	return send.Send(ctx, deps, handle, kind, item.Text, send.Options{Marker: true})
}

// recorded looks for the item's own line in sent.log after it was queued.
func (s *Sender) recorded(project string, item store.OutboxItem) (time.Time, bool, error) {
	entries, _, err := s.ws.ReadSent(project, item.SentLogFrom)
	if err != nil {
		return time.Time{}, false, err
	}
	want := flatten(item.Text)
	for _, e := range entries {
		if e.Source == store.SourceApp && e.Target == store.TargetMate && e.Text == want {
			return e.Time, true, nil
		}
	}
	return time.Time{}, false, nil
}

// flatten is sent.log's own normalisation of a line (store.AppendSent).
func flatten(text string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(text)
}

// wedgedOpen reports whether the last (mate, wedged) line is `open`.
func (s *Sender) wedgedOpen(project string) (bool, error) {
	entries, _, err := s.ws.ReadIncidents(project, 0)
	if err != nil {
		return false, err
	}
	open := false
	for _, e := range entries {
		if e.Crew == MateCrew && e.Kind == WedgedKind {
			open = e.Open()
		}
	}
	return open, nil
}

func (s *Sender) openWedged(project string, now time.Time, text string) error {
	open, err := s.wedgedOpen(project)
	if err != nil || open {
		return err
	}
	return s.ws.AppendIncident(project, store.IncidentEntry{
		Time: now, Crew: MateCrew, Kind: WedgedKind, State: store.IncidentOpen, Text: text,
	})
}

func (s *Sender) resolveWedged(project string, now time.Time, text string) error {
	open, err := s.wedgedOpen(project)
	if err != nil || !open {
		return err
	}
	return s.ws.AppendIncident(project, store.IncidentEntry{
		Time: now, Crew: MateCrew, Kind: WedgedKind, State: store.IncidentResolved, Text: text,
	})
}

// Drain is one pass of the loop: the oldest queued item of every project
// that has one. The error joins the per-project failures; the pass always
// finishes.
func (s *Sender) Drain(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// workspace.yaml is re-read every pass, so a project registered beside
	// this console is delivered to without a restart.
	if err := s.ws.LoadConfig(); err != nil {
		return err
	}
	var errs []error
	for _, ref := range s.ws.Projects() {
		if err := ctx.Err(); err != nil {
			return err
		}
		// A lock-free look first: most passes over most projects find
		// nothing queued, and taking the writers' lock to learn that would
		// contend with a send in flight for no reason.
		items, err := s.ws.ReadOutbox(ref.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("outbox: %s: %w", ref.Name, err))
			continue
		}
		if !anyQueued(items) {
			continue
		}
		if _, err := s.Attempt(ctx, ref.Name); err != nil {
			errs = append(errs, fmt.Errorf("outbox: %s: %w", ref.Name, err))
		}
	}
	return errors.Join(errs...)
}

func anyQueued(items []store.OutboxItem) bool {
	for _, item := range items {
		if item.Queued() {
			return true
		}
	}
	return false
}

// Start runs Drain every Interval on its own goroutine until Stop or a
// cancelled context. Calling it twice is a no-op.
func (s *Sender) Start(ctx context.Context) {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	s.mu.Unlock()
	go s.run(runCtx)
}

// Stop ends the loop and waits for the pass in flight.
func (s *Sender) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (s *Sender) run(ctx context.Context) {
	defer close(s.done)
	for {
		// A failed pass is not fatal: Herdr may be restarting, a file may
		// be mid-write. Nothing queued was lost, and the next pass asks
		// again.
		_ = s.Drain(ctx)
		if err := s.deps.sleep(ctx, s.deps.interval()); err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}
