package outbox

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Stow before a restart (docs/mvp.md task 37,
// docs/research/firstmate-memory-2026-09-24.md B7). A restart loses the
// Mate's conversation, and whatever it did not file is gone; mate owns the
// one place a Mate is restarted, so it asks first, through the same queue
// and the same verified send as every other line the app types into a Mate:
// `⟦mate⟧ stow: ...` (memory.StowLine), then a wait for that turn to end,
// bounded, then the restart.

// DefaultStowCeiling bounds the whole stow: the line reaching the composer
// and the Mate's turn on it.
const DefaultStowCeiling = 3 * time.Minute

// stowQuiet is how long a Mate that was never seen busy after the stow
// line must have been in its turn before an empty composer counts as the
// turn's end: the line is typed, the composer clears, and a harness can take
// a moment to draw its spinner.
const stowQuiet = 10 * time.Second

// StowOptions tune one stow.
type StowOptions struct {
	// Ceiling bounds the wait; zero means DefaultStowCeiling.
	Ceiling time.Duration
	// Poll is the pause between looks; zero means the sender's Interval.
	Poll time.Duration
	// RequireEmpty stows only into a composer that is empty right now: a
	// Mate mid-turn is reported and not stowed (`mate mate stop`, where
	// the captain may want the Mate gone now). Without it a busy Mate is
	// stowed once its turn ends, as any queued line is.
	RequireEmpty bool
	// RequireCompletion forbids composer-only inference for automatic refresh.
	RequireCompletion bool
	// Text optionally adds a checkpoint receipt command to the stow request.
	Text string
}

// StowResult is what a stow did. Exactly one of Stowed, Held and a Reason
// for not stowing describes it.
type StowResult struct {
	// Stowed is true when the stow line reached the Mate and its turn on
	// it ended.
	Stowed bool
	// Held is true when nothing was typed because the composer holds
	// someone's unsent text: the caller asks before restarting over it.
	// Pending is that text.
	Held    bool
	Pending string
	// Reason says why the Mate was not stowed, in the words the outcome
	// line uses after "not stowed: ".
	Reason string
	// Detail is the observation behind Reason, for a log rather than the
	// outcome line.
	Detail string
	// Waited is how long the stow took.
	Waited time.Duration
	// Agent is the Mate's agent name, when one was found.
	Agent string
}

// Outcome is the outcome line's first words: "stowed" or "not stowed:
// <reason>".
func (r StowResult) Outcome() string {
	if r.Stowed {
		return "stowed"
	}
	return "not stowed: " + r.Reason
}

// Stow asks the project's Mate to record what exists only in its
// conversation, and waits for it to finish. It never types over someone's
// unsent text (Held), never leaves the line queued for the next Mate (a
// stow that runs out of time withdraws its line), and returns an error only
// for a failure around it - the outbox unreadable, sent.log unwritable -
// never for a Mate that could not be stowed, which is a Reason.
func (s *Sender) Stow(ctx context.Context, project string, opts StowOptions) (StowResult, error) {
	start := s.deps.now()
	ceiling := opts.Ceiling
	if ceiling <= 0 {
		ceiling = DefaultStowCeiling
	}
	poll := opts.Poll
	if poll <= 0 {
		poll = s.deps.interval()
	}
	var out StowResult
	done := func(r StowResult) (StowResult, error) {
		r.Waited = s.deps.now().Sub(start)
		r.Agent = out.Agent
		return r, nil
	}
	if s.deps.Runtime == nil || s.deps.Handle == nil {
		return done(StowResult{Reason: "no runtime is wired to reach the Mate"})
	}
	handle, kind, err := s.deps.Handle(ctx, project)
	if err != nil {
		return done(StowResult{Reason: "the Mate was not running", Detail: oneLine(err)})
	}
	out.Agent = handle.Name
	// One stow reads the pane every poll for up to the ceiling; a pane
	// that has not changed keeps its last reading.
	look := &stowLook{}
	before, err := s.composer(ctx, handle, kind, look)
	if err != nil {
		return done(StowResult{Reason: "its pane could not be read (" + oneLine(err) + ")"})
	}
	switch before.State {
	case send.StatePending:
		return done(StowResult{Held: true, Pending: before.Pending, Reason: "the composer holds unsent text"})
	case send.StateUnknown:
		return done(StowResult{Reason: "its pane shows no composer to type into"})
	case send.StateBusy:
		if opts.RequireEmpty {
			return done(StowResult{Reason: "the Mate is mid-turn"})
		}
	}

	text := opts.Text
	if text == "" {
		text = memory.StowLine
	}
	queued, err := s.Enqueue(project, Request{
		Source: store.OutboxSourceStow,
		Key:    "stow@" + start.UTC().Format(time.RFC3339Nano),
		Text:   text,
	})
	if err != nil {
		return out, err
	}
	id := queued.Item.ID
	// Cancellation and errors must not leave a stale stow for a later session.
	defer func() { _ = s.Withdraw(project, id, "stow request ended") }()
	// The ceiling is kept twice: on the clock, and as a count of polls, so
	// the wait is bounded even under a clock that does not move (tests) or
	// polls that take longer than their interval (a slow Herdr).
	deadline := start.Add(ceiling)
	maxPolls := int(ceiling/poll) + 1
	quietPolls := int((stowQuiet + poll - 1) / poll)
	polls := 0
	expired := func() bool { return polls >= maxPolls || !s.deps.now().Before(deadline) }

	// 1. The line into the composer. The console's own loop may deliver it
	// first; this attempt and that one agree under the outbox's lock.
	var item store.OutboxItem
	for {
		if _, err := s.Attempt(ctx, project); err != nil {
			return out, err
		}
		if item, err = s.item(project, id); err != nil {
			return out, err
		}
		if item.State == store.OutboxSent {
			break
		}
		if item.State == store.OutboxDropped {
			return done(StowResult{Reason: "the stow line was withdrawn before it was typed"})
		}
		if expired() {
			reason := fmt.Sprintf("the stow line did not reach the Mate within %s", Span(ceiling))
			if err := s.Withdraw(project, id, "the restart went ahead without it"); err != nil {
				return out, err
			}
			return done(StowResult{Reason: reason})
		}
		if err := s.deps.sleep(ctx, poll); err != nil {
			return out, err
		}
		polls++
	}

	// 2. The turn on it, by the evidence the harness declares
	// (harness.TurnEndEvidence). Claude's Stop hook writes the Mate's answer
	// to sent.log (docs/mvp.md task 08), which says the turn ended; Codex
	// has no such hook, but its rollout records `task_complete` when a turn
	// ends, and mate.meta names the rollout once the Mate's SessionStart
	// hook has run. A Codex composer reads empty between tool calls
	// (task 38: a stow judged over by the composer was cut off mid-turn),
	// so while the transcript is known it is the only rule. Claude's hook
	// does not fire every turn (section 7), a transcript can be unknown,
	// and a harness can declare no evidence at all (plan section 3.7), so
	// the composer remains the fallback: busy after the line, then empty on
	// two looks in a row.
	evidence := s.turnEnd(kind)
	sawBusy, empties, since := false, 0, 0
	for ; ; since++ {
		if evidence != nil && evidence.EndsInTranscript() {
			if transcript := s.mateTranscript(project); transcript != "" {
				if data, err := os.ReadFile(transcript); err == nil && evidence.TranscriptTurnEnded(data, item.SentAt) {
					if c, err := s.composer(ctx, handle, kind, look); err == nil && c.State == send.StateEmpty {
						return done(StowResult{Stowed: true})
					}
				}
				if expired() {
					return done(StowResult{Reason: fmt.Sprintf("the Mate had not finished its stow turn after %s", Span(ceiling))})
				}
				if err := s.deps.sleep(ctx, poll); err != nil {
					return out, err
				}
				polls++
				continue
			}
		}
		if evidence != nil && evidence.LogsAnswers() {
			ended, err := s.answeredAfter(project, item)
			if err != nil {
				return out, err
			}
			if ended {
				if c, err := s.composer(ctx, handle, kind, look); err == nil && c.State == send.StateEmpty {
					return done(StowResult{Stowed: true})
				}
			}
		}
		c, err := s.composer(ctx, handle, kind, look)
		switch {
		case err != nil:
			empties = 0
		case c.State == send.StateBusy:
			sawBusy, empties = true, 0
		case !opts.RequireCompletion && c.State == send.StateEmpty && (sawBusy || since >= quietPolls || s.deps.now().Sub(item.SentAt) >= stowQuiet):
			empties++
			if empties >= 2 {
				return done(StowResult{Stowed: true})
			}
		default:
			empties = 0
		}
		if expired() {
			return done(StowResult{Reason: fmt.Sprintf("the Mate had not finished its stow turn after %s", Span(ceiling))})
		}
		if err := s.deps.sleep(ctx, poll); err != nil {
			return out, err
		}
		polls++
	}
}

// Span writes a wait the way an outcome line says it: "3 minutes", "90s".
func Span(d time.Duration) string {
	switch {
	case d == time.Minute:
		return "1 minute"
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	}
	return d.Round(time.Second).String()
}

// stowLook is one stow's last reading of the Mate's composer and the hash
// of the snapshot it was read from.
type stowLook struct {
	read bool
	hash [sha256.Size]byte
	c    send.Classification
}

// composer reads the Mate's composer from one styled read, through
// Deps.Observer as a delivery does. The observer is asked only about a
// snapshot that differs from the last one look holds, as watch asks: the
// stow's wait polls every couple of seconds for minutes, and a Mate whose
// pane redraws nothing must not cost an observer call each time
// (docs/plans/jev-observer-2026-10-08.md section 4.3). A snapshot the
// observer could not read leaves look as it was, so it is asked about
// again.
func (s *Sender) composer(ctx context.Context, handle runtime.AgentHandle, kind harness.Kind, look *stowLook) (send.Classification, error) {
	profile, err := s.deps.Harnesses.Lookup(kind)
	if err != nil {
		return send.Classification{State: send.StateUnknown}, err
	}
	screens := profile.Screen()
	screen, err := s.deps.Runtime.ReadAgentStyled(ctx, handle, screens.ReadSource(), send.DefaultLines)
	if err != nil {
		return send.Classification{}, err
	}
	hash := sha256.Sum256([]byte(screen))
	if look.read && look.hash == hash {
		return look.c, nil
	}
	observed, err := s.deps.observer().Observe(ctx, screens, screen)
	if err != nil {
		return send.Classification{}, err
	}
	c := send.Classification{State: observed.Composer, Evidence: observed.Evidence, Pending: observed.Draft}
	look.read, look.hash, look.c = true, hash, c
	return c, nil
}

// answeredAfter reports whether sent.log has a line from the Mate after
// the stow line itself: the stow turn's Stop-hook answer. The stow line is
// in sent.log twice (the outbox's record and the UserPromptSubmit hook's
// echo, in either order, section 7), so the answer is looked for after the
// first of them.
func (s *Sender) answeredAfter(project string, item store.OutboxItem) (bool, error) {
	entries, _, err := s.ws.ReadSent(project, item.SentLogFrom)
	if err != nil {
		return false, err
	}
	want := flatten(item.Text)
	seen := false
	for _, e := range entries {
		switch {
		case e.Source == store.SourceApp && e.Target == store.TargetMate && e.Text == want:
			seen = true
		case seen && e.Source == store.SourceMate && e.Target == store.SourceUser:
			return true, nil
		}
	}
	return false, nil
}

// MateMetaTranscript is mate.meta's transcript key, spawn.MetaTranscript,
// named here because importing spawn from this package is a cycle;
// internal/spawn's tests hold the two equal.
const MateMetaTranscript = "transcript"

// turnEnd is the turn-end evidence the Mate's harness declares, or nil when
// it declares none verified and the composer is all there is.
func (s *Sender) turnEnd(kind harness.Kind) harness.TurnEndEvidence {
	profile, err := s.deps.Harnesses.Lookup(kind)
	if err != nil {
		return nil
	}
	if c := profile.Capabilities().TurnEnd; c.Verified() {
		return c.Impl
	}
	return nil
}

// mateTranscript is the transcript mate.meta records for the Mate, or "".
func (s *Sender) mateTranscript(project string) string {
	meta, err := s.ws.ReadMateMeta(project)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(meta[MateMetaTranscript])
}

// item reads one item back by id.
func (s *Sender) item(project string, id int64) (store.OutboxItem, error) {
	items, err := s.ws.ReadOutbox(project)
	if err != nil {
		return store.OutboxItem{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return store.OutboxItem{}, fmt.Errorf("outbox: item %d is gone from %s's outbox", id, project)
}

// Withdraw drops a still-queued item so no sender ever types it: a stow
// whose restart went ahead without it must not reach the next Mate. An item
// already sent or dropped is left as it is.
func (s *Sender) Withdraw(project string, id int64, reason string) error {
	return s.ws.UpdateOutbox(project, s.deps.now(), func(items []store.OutboxItem) ([]store.OutboxItem, bool, error) {
		for i := range items {
			if items[i].ID == id && items[i].Queued() {
				items[i].State = store.OutboxDropped
				items[i].LastRefusal = reason
				return items, true, nil
			}
		}
		return items, false, nil
	})
}

func oneLine(err error) string {
	msg := err.Error()
	for i, r := range msg {
		if r == '\n' {
			return msg[:i]
		}
	}
	return msg
}
