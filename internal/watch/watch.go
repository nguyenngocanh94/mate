package watch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/crewstate"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// Defaults of docs/mvp.md task 18. The poll is short because the console
// draws the health column from it; the stale threshold is long because the
// cost of the two mistakes is not symmetric - a crew wrongly called stale
// puts a false item in the human's inbox, while a crew called stale a minute
// late costs a minute.
const (
	// DefaultPollInterval is the pause between rounds.
	DefaultPollInterval = 5 * time.Second
	// DefaultStaleAfter is how long the pane and the status file must both
	// be unchanged before `stale` opens.
	DefaultStaleAfter = 3 * time.Minute
)

// Runtime is the slice of runtime.Adapter the observer needs. It is narrow
// on purpose: this package can ask Herdr what it has and read a pane, and it
// structurally cannot start, stop, type into or prompt anything.
type Runtime interface {
	InspectAgent(ctx context.Context, handle runtime.AgentHandle) (runtime.ObservedAgent, error)
	// ReadAgentStyled, not ReadAgent: the health column reports the
	// composer state, and telling a harness's own faint suggestion from a
	// person's unsubmitted line needs the attributes
	// (internal/send/classify.go's faintPlaceholder).
	ReadAgentStyled(ctx context.Context, handle runtime.AgentHandle, lines int) (string, error)
}

var _ Runtime = runtime.Adapter(nil)

// HandleFunc resolves the Herdr handle and harness kind recorded for one
// crew. It is a seam rather than a direct call to internal/spawn so the
// observer imports no code that can start or stop an agent; cmd/matev2 wires
// spawn.CrewHandle into it.
//
// An error means "mate could not resolve a handle to look at" - the crew has
// no agent recorded, or the Herdr session is not running. That is not a
// finding: the observer skips the crew for this round and concludes nothing.
type HandleFunc func(ctx context.Context, project, crew string) (runtime.AgentHandle, harness.Kind, error)

// Clock is where the observer reads the time. Tests pass a fake one so every
// threshold in this package can be crossed without waiting.
type Clock interface {
	Now() time.Time
}

// Sleeper is the pause between polls, interruptible by the context. Tests
// pass one that returns immediately, or one that ends the run.
type Sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

// Deps are the observer's collaborators. Runtime and Handle are required;
// everything else has a default.
type Deps struct {
	Runtime Runtime
	Handle  HandleFunc
	Clock   Clock
	Sleeper Sleeper
	// PollInterval is the pause between rounds; zero means
	// DefaultPollInterval.
	PollInterval time.Duration
	// StaleAfter is the quiet period that opens `stale`; zero means
	// DefaultStaleAfter.
	StaleAfter time.Duration
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

func (d Deps) pollInterval() time.Duration {
	if d.PollInterval > 0 {
		return d.PollInterval
	}
	return DefaultPollInterval
}

func (d Deps) staleAfter() time.Duration {
	if d.StaleAfter > 0 {
		return d.StaleAfter
	}
	return DefaultStaleAfter
}

// Watcher observes the open crews of a workspace.
//
// One Watcher polls from one goroutine: Start owns it, and a caller driving
// Poll itself (a test) is that goroutine. Snapshot and Health may be called
// from any other goroutine at any time.
//
// The workspace handle is this package's own: store.Workspace caches
// workspace.yaml and re-reads it on LoadConfig, so the observer opens a
// second handle rather than sharing the console's (cmd/matev2 does the
// opening).
type Watcher struct {
	ws   *store.Workspace
	deps Deps

	// crews is the per-crew memory of the previous rounds. Only the polling
	// goroutine touches it.
	crews map[CrewRef]*observation

	mu     sync.Mutex
	health map[CrewRef]Health
	cancel context.CancelFunc
	done   chan struct{}
}

// observation is what the previous polls established about one crew.
type observation struct {
	// screen is the hash of the last pane snapshot, empty before the first
	// readable one.
	screen string
	// statusAt is the resume offset of the crew's status file.
	statusAt int64
	// verb is the last status verb the crew wrote, empty if it has written
	// nothing.
	verb string
	// changedAt is the last time the pane or the status file moved. It is
	// set on the first observation too: the observer counts quiet from when
	// it started looking, never from a past it did not see.
	changedAt time.Time
	// composer is the last composer classification, and composerSince
	// when it last changed. A busy pane redraws its spinner every poll, so
	// its quiet clock is always near zero; "busy for how long" is this
	// clock instead.
	composer      send.ComposerState
	composerSince time.Time
}

// New builds an observer over a workspace. It does not poll until Start or
// Poll is called.
func New(ws *store.Workspace, deps Deps) *Watcher {
	return &Watcher{
		ws:     ws,
		deps:   deps,
		crews:  make(map[CrewRef]*observation),
		health: make(map[CrewRef]Health),
	}
}

// Start begins polling in its own goroutine until Stop or a cancelled
// context. Calling it twice is a no-op.
func (w *Watcher) Start(ctx context.Context) {
	w.mu.Lock()
	if w.cancel != nil {
		w.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.done = make(chan struct{})
	w.mu.Unlock()
	go w.run(runCtx)
}

// Stop ends the polling goroutine and waits for the round in flight. It is
// safe to call on a Watcher that was never started.
func (w *Watcher) Stop() {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.cancel, w.done = nil, nil
	w.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (w *Watcher) run(ctx context.Context) {
	defer close(w.done)
	for {
		// A failed round is not fatal: Herdr may be restarting, a project
		// directory may be half-written. The next round asks again, and
		// what the observer could not read it does not conclude from.
		_ = w.Poll(ctx)
		if err := w.deps.sleep(ctx, w.deps.pollInterval()); err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// Poll runs one round over every open crew of every project: it appends
// whatever incident transitions it found to `incidents.log` and replaces the
// health snapshot.
//
// The error it returns is the joined per-crew and per-project read failures
// of the round; the round itself always finishes. A caller that wants to
// know whether anything was observed reads Snapshot.
func (w *Watcher) Poll(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Re-read workspace.yaml every round: a project registered by another
	// process (or by the console beside this one) must come under watch
	// without a restart.
	if err := w.ws.LoadConfig(); err != nil {
		return err
	}

	now := w.deps.now()
	results := make(map[CrewRef]Health)
	seen := make(map[CrewRef]bool)
	listed := make(map[string]bool)
	var errs []error

	for _, ref := range w.ws.Projects() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.pollProject(ctx, ref.Name, now, results, seen, listed); err != nil {
			errs = append(errs, err)
		}
	}
	w.commit(results, seen, listed)
	return errors.Join(errs...)
}

func (w *Watcher) pollProject(ctx context.Context, project string, now time.Time,
	results map[CrewRef]Health, seen map[CrewRef]bool, listed map[string]bool) error {

	open, err := w.openIncidents(project)
	if err != nil {
		return fmt.Errorf("watch: read incidents of %s: %w", project, err)
	}
	crews, err := openCrews(w.ws, project)
	if err != nil {
		return fmt.Errorf("watch: list crews of %s: %w", project, err)
	}
	listed[project] = true

	var errs []error
	for _, crew := range crews {
		if err := ctx.Err(); err != nil {
			return err
		}
		ref := CrewRef{Project: project, Crew: crew}
		seen[ref] = true
		if err := w.pollCrew(ctx, ref, now, open, results); err != nil {
			errs = append(errs, fmt.Errorf("watch: observe crew %s of %s: %w", crew, project, err))
		}
	}
	return errors.Join(errs...)
}

// pollCrew is the whole contract of mvp.md section 4b for one crew.
func (w *Watcher) pollCrew(ctx context.Context, ref CrewRef, now time.Time,
	open map[incidentKey]bool, results map[CrewRef]Health) error {

	obs := w.observation(ref)

	first := obs.changedAt.IsZero()
	statusMoved, err := w.readStatus(ref, obs)
	if err != nil {
		// The crew's own file is unreadable. Nothing about the pane or the
		// agent has been established either, so nothing is concluded.
		return err
	}
	if first {
		// The first look at a crew reads whatever its status file already
		// holds. That backlog is not movement: a console opened beside a
		// crew that went quiet an hour ago must not report the crew as
		// having just spoken, and must not resolve an incident the previous
		// console left open. The quiet clock starts here instead.
		statusMoved = false
		obs.changedAt = now
	} else if statusMoved {
		obs.changedAt = now
	}

	handle, kind, err := w.deps.Handle(ctx, ref.Project, ref.Crew)
	if err != nil {
		// No handle to look at: the crew records no agent yet, or the Herdr
		// session is not running. Neither says the crew is in trouble.
		return nil
	}

	if _, err := w.deps.Runtime.InspectAgent(ctx, handle); err != nil {
		if !runtime.IsAgentNotFound(err) {
			// Herdr could not answer. "Mate could not look" is not "the
			// agent is gone" (mvp.md decision 8).
			return err
		}
		if err := w.openIncident(ref, box.IncidentRuntimeLost, now, open,
			fmt.Sprintf("herdr no longer lists agent %s", handle.Name)); err != nil {
			return err
		}
		results[ref] = Health{
			AgentPresent: false,
			Composer:     send.StateUnknown,
			QuietFor:     now.Sub(obs.changedAt),
			ObservedAt:   now,
		}
		return nil
	}
	if err := w.resolveIncident(ref, box.IncidentRuntimeLost, now, open,
		fmt.Sprintf("herdr lists agent %s again", handle.Name)); err != nil {
		return err
	}

	screen, err := w.deps.Runtime.ReadAgentStyled(ctx, handle, send.DefaultLines)
	if err != nil {
		// The agent is there and the pane is not readable: an unread screen
		// is not a quiet one, so the round ends without a verdict.
		return err
	}
	hash := screenHash(screen)
	paneMoved := obs.screen != "" && obs.screen != hash
	obs.screen = hash
	if paneMoved {
		obs.changedAt = now
	}

	composer := send.StateUnknown
	if class, err := send.ClassifyComposer(kind, screen); err == nil {
		composer = class.State
	}
	if obs.composerSince.IsZero() || composer != obs.composer {
		obs.composer = composer
		obs.composerSince = now
	}
	quiet := now.Sub(obs.changedAt)

	switch {
	case open[incidentKey{ref, box.IncidentStale}]:
		if reason, ok := staleCleared(paneMoved, statusMoved, composer); ok {
			if err := w.resolveIncident(ref, box.IncidentStale, now, open, reason); err != nil {
				return err
			}
		}
	case quiet >= w.deps.staleAfter() && composer != send.StateBusy && !waiting(obs.verb):
		text := fmt.Sprintf("no status line and no pane change for %s; composer %s",
			quiet.Round(time.Second), composer)
		if obs.verb != "" {
			text += fmt.Sprintf("; last status verb %s", obs.verb)
		}
		if err := w.openIncident(ref, box.IncidentStale, now, open, text); err != nil {
			return err
		}
	}

	results[ref] = Health{
		AgentPresent: true,
		Composer:     composer,
		QuietFor:     quiet,
		ComposerFor:  now.Sub(obs.composerSince),
		ObservedAt:   now,
	}
	return nil
}

// staleCleared is the resolve side of the stale rule: the pane moved, the
// crew spoke, or the harness is working again.
func staleCleared(paneMoved, statusMoved bool, composer send.ComposerState) (string, bool) {
	switch {
	case statusMoved:
		return "the crew wrote a status line", true
	case paneMoved:
		return "the pane changed", true
	case composer == send.StateBusy:
		return "the composer is busy again", true
	default:
		return "", false
	}
}

// waiting reports whether a status verb means the crew is waiting on a human
// rather than stuck. A crew that asked a question or handed back to the Mate
// is silent on purpose (mvp.md section 4b); calling that stale would put the
// same crew in the inbox twice.
//
// The vocabulary is box's, so there is one spelling of it in the codebase -
// including the legacy `done:`, which box already reads as `wait-mate`.
func waiting(verb string) bool {
	switch box.State(verb) {
	case box.StateNeedsDecision, box.StateWaitMate:
		return true
	default:
		return false
	}
}

// readStatus advances the crew's status cursor and reports whether the crew
// appended anything since the previous round.
func (w *Watcher) readStatus(ref CrewRef, obs *observation) (bool, error) {
	lines, next, err := w.ws.ReadStatus(ref.Project, ref.Crew, obs.statusAt)
	if err != nil {
		return false, err
	}
	obs.statusAt = next
	if len(lines) == 0 {
		return false, nil
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if verb := statusVerb(lines[i].Line); verb != "" {
			obs.verb = verb
			break
		}
	}
	return true, nil
}

// statusVerb is the crew verb of one `state: one line`, parsed by
// internal/box so the observer reads exactly what the console and the CLI
// read - the three verbs of mvp.md section 4b plus the pre-4b spellings box
// maps onto them. A line box does not recognise yields "", and readStatus
// then keeps the verb the crew last actually wrote: a malformed echo is not
// a state change.
func statusVerb(line string) string {
	if st := box.ParseStatus(line); st.State != box.StateUnknown {
		return string(st.State)
	}
	return ""
}

func (w *Watcher) observation(ref CrewRef) *observation {
	obs, ok := w.crews[ref]
	if !ok {
		obs = &observation{}
		w.crews[ref] = obs
	}
	return obs
}

// commit replaces the health snapshot and forgets the crews that are gone.
//
// A crew that is still listed but produced no observation this round keeps
// the one it had: the round failed to look, which is not the same as the
// crew having no health, and Health.ObservedAt already says how old the
// answer is. A crew of a project whose listing failed is kept for the same
// reason.
func (w *Watcher) commit(results map[CrewRef]Health, seen map[CrewRef]bool, listed map[string]bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for ref, h := range w.health {
		if _, fresh := results[ref]; fresh {
			continue
		}
		if seen[ref] || !listed[ref.Project] {
			results[ref] = h
		}
	}
	w.health = results
	for ref := range w.crews {
		if !seen[ref] && listed[ref.Project] {
			delete(w.crews, ref)
		}
	}
}

// incidentKey is one open incident: a crew and a kind, which is all the
// identity an incident has (mvp.md section 4b).
type incidentKey struct {
	ref  CrewRef
	kind box.IncidentKind
}

// openIncidents reads which incidents of a project are currently open: the
// file is the state, so a console that restarted sees exactly what the
// previous one left behind.
func (w *Watcher) openIncidents(project string) (map[incidentKey]bool, error) {
	entries, _, err := w.ws.ReadIncidents(project, 0)
	if err != nil {
		return nil, err
	}
	open := make(map[incidentKey]bool)
	for _, e := range entries {
		key := incidentKey{CrewRef{Project: project, Crew: e.Crew}, box.IncidentKind(e.Kind)}
		open[key] = e.Open()
	}
	return open, nil
}

func (w *Watcher) openIncident(ref CrewRef, kind box.IncidentKind, now time.Time,
	open map[incidentKey]bool, text string) error {

	key := incidentKey{ref, kind}
	if open[key] {
		return nil
	}
	if err := w.appendIncident(ref, kind, store.IncidentOpen, now, text); err != nil {
		return err
	}
	open[key] = true
	return nil
}

func (w *Watcher) resolveIncident(ref CrewRef, kind box.IncidentKind, now time.Time,
	open map[incidentKey]bool, text string) error {

	key := incidentKey{ref, kind}
	if !open[key] {
		return nil
	}
	if err := w.appendIncident(ref, kind, store.IncidentResolved, now, text); err != nil {
		return err
	}
	open[key] = false
	return nil
}

func (w *Watcher) appendIncident(ref CrewRef, kind box.IncidentKind, state string, now time.Time, text string) error {
	return w.ws.AppendIncident(ref.Project, store.IncidentEntry{
		Time:  now,
		Crew:  ref.Crew,
		Kind:  string(kind),
		State: state,
		Text:  text,
	})
}

// openCrews lists the crews of a project that are still in flight: a
// `crews/<id>.meta` whose declared state is not terminal. The decision is
// crewstate.Declare's, which is the one resolution order in the codebase
// (mvp.md section 4b) and a leaf package that can start nothing - so the
// observer still depends on nothing that can start or stop an agent. An
// incident is deliberately not an input here: `blocked` is a state the
// observer itself produces, and a crew it has flagged is exactly the crew it
// must keep watching so it can clear the flag again.
//
// A meta that cannot be read is skipped rather than watched: an unreadable
// record is not evidence of a running crew.
func openCrews(ws *store.Workspace, project string) ([]string, error) {
	entries, err := os.ReadDir(ws.CrewsDir(project))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, ok := strings.CutSuffix(e.Name(), ".meta")
		if !ok || store.ValidateCrewID(id) != nil {
			continue
		}
		meta, err := ws.ReadCrewMeta(project, id)
		if err != nil {
			continue
		}
		if crewstate.Declare(crewstate.Declaration{Meta: meta}).Closed() {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// screenHash is the pane snapshot reduced to something comparable between
// rounds. The screen itself is not kept: the observer answers "did this
// change", never "what did it say".
func screenHash(screen string) string {
	sum := sha256.Sum256([]byte(screen))
	return hex.EncodeToString(sum[:])
}
