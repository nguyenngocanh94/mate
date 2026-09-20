package timeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/crewstate"
	"github.com/nguyenngocanh94/matev2/internal/db"
	"github.com/nguyenngocanh94/matev2/internal/gitx"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// HealthReading is one observation of one agent's pane. It is the only input
// to this package that is not a file: nothing on disk records that a composer
// was busy at 10:32:41, and a busy stretch nobody explains is exactly the
// hole M5 exists to close.
//
// The composer word is crewstate.Composer, the spelling the console and
// `matev2 state` already share, so this package needs neither internal/send
// nor the Herdr adapter it pulls in.
type HealthReading struct {
	Project      string
	Crew         string // empty for the Mate
	AgentPresent bool
	Composer     crewstate.Composer
	At           time.Time
}

// SessionRefFunc reports the harness session id the runtime records for one
// crew's agent - Herdr's `agent_session.value`, which for Codex is the
// rollout's session uuid. It is a seam for the same reason watch.HandleFunc
// is one: this package must not be able to reach Herdr, let alone start
// anything. An error or an empty string means "the runtime did not say", and
// the locator falls through to its next rule.
type SessionRefFunc func(ctx context.Context, project, crew string) (string, error)

// Deps are the ingest's collaborators. The zero value works: it reads the
// real clock, the real git, the real harness directories, and resolves no
// session refs.
type Deps struct {
	Now        func() time.Time
	SessionRef SessionRefFunc
	// Health returns the observer's latest readings. The ingest turns a
	// change in a reading into `health.changed`, and a busy stretch with no
	// tool call inside it into a `thinking` action.
	Health func() []HealthReading
	// ClaudeProjectsDir and CodexSessionsDir override the harness transcript
	// roots; tests point them at a fixture directory.
	ClaudeProjectsDir string
	CodexSessionsDir  string
	Git               gitx.Git
	// ThinkingGap is the shortest uncovered stretch inside a turn that
	// becomes a `thinking` action. Zero means DefaultThinkingGap.
	ThinkingGap time.Duration
}

// DefaultThinkingGap is the shortest silence inside a turn the ingest is
// willing to leave unexplained. It is short because the question M5 asks is
// "what was this agent doing", and five seconds of a model reasoning between
// two tool calls is an answer, not noise.
const DefaultThinkingGap = 5 * time.Second

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) git() gitx.Git {
	if d.Git.Runner != nil {
		return d.Git
	}
	return gitx.New()
}

func (d Deps) thinkingGap() time.Duration {
	if d.ThinkingGap > 0 {
		return d.ThinkingGap
	}
	return DefaultThinkingGap
}

func (d Deps) health() []HealthReading {
	if d.Health == nil {
		return nil
	}
	return d.Health()
}

// Ingester reads a workspace's files into a timeline database. One Ingester
// belongs to one writer: Ingest is called from a single goroutine (the
// observer's poll loop), and internal/db has already refused a second
// process.
type Ingester struct {
	ws   *store.Workspace
	d    *db.DB
	deps Deps
}

// New builds an ingest over an opened workspace and a writable database.
func New(ws *store.Workspace, d *db.DB, deps Deps) *Ingester {
	return &Ingester{ws: ws, d: d, deps: deps}
}

// Ingest reads everything new since the last pass and records it. It is what
// the observer calls at the end of every poll.
//
// A project that fails is reported and the others still run: a half-written
// `.meta` or a transcript that vanished under the parser must not stop the
// rest of the workspace being recorded. The error is the join of what failed.
func (i *Ingester) Ingest(ctx context.Context) error {
	return i.ingestAll(ctx, nil)
}

// ingestAll runs one pass over every project. With a transaction it runs
// inside that one, which is how a rebuild is atomic; without one each project
// commits on its own, which is what a poll wants - a project whose `.meta` is
// half-written must not hold back the rest of the workspace.
func (i *Ingester) ingestAll(ctx context.Context, shared *sql.Tx) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !i.d.Writable() {
		return errors.New("timeline: this database handle is read-only")
	}
	if err := i.ws.LoadConfig(); err != nil {
		return err
	}
	var errs []error
	for _, ref := range i.ws.Projects() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := i.ingestProject(ctx, ref.Name, shared); err != nil {
			if shared != nil {
				return fmt.Errorf("timeline: %s: %w", ref.Name, err)
			}
			errs = append(errs, fmt.Errorf("timeline: %s: %w", ref.Name, err))
		}
	}
	return errors.Join(errs...)
}

// Reindex drops every derived table and rebuilds the whole timeline from the
// files and the transcripts. It is `matev2 reindex`, and it is the executable
// statement of decision 6: nothing here is a record, so throwing all of it
// away and reading the files again has to produce the same story.
func (i *Ingester) Reindex(ctx context.Context) error {
	if !i.d.Writable() {
		return errors.New("timeline: this database handle is read-only")
	}
	tx, err := i.d.SQL().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := db.ResetDerivedTx(ctx, tx); err != nil {
		return err
	}
	if err := i.ingestAll(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ingestProject is one project's pass, in one transaction. The order of the
// phases is the order their facts depend on each other: actors and tasks name
// everybody, the transcripts date the crews' own shell commands (which is how
// a status line - a line with no timestamp of its own - gets one), the logs
// read from their cursors, and the causality pass runs last because it needs
// every event of the pass to already have an id.
func (i *Ingester) ingestProject(ctx context.Context, project string, shared *sql.Tx) (err error) {
	tx := shared
	if tx == nil {
		tx, err = i.d.SQL().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() {
			if err != nil {
				_ = tx.Rollback()
			}
		}()
	}

	now := i.deps.now()
	w := newWriter(tx, now)
	b := newBatch()
	p := &pass{ing: i, project: project, now: now, b: b, w: w, tx: tx}

	if err = p.ingestMeta(ctx); err != nil {
		return err
	}
	if err = p.ingestTranscripts(ctx); err != nil {
		return err
	}
	if err = p.ingestStatus(ctx); err != nil {
		return err
	}
	if err = p.ingestSent(ctx); err != nil {
		return err
	}
	if err = p.ingestIncidents(ctx); err != nil {
		return err
	}
	if err = p.ingestGit(ctx); err != nil {
		return err
	}
	if err = p.ingestHealth(ctx); err != nil {
		return err
	}
	if err = w.flush(ctx, b); err != nil {
		return err
	}
	if err = p.fillThinking(ctx); err != nil {
		return err
	}
	if err = p.linkCauses(ctx); err != nil {
		return err
	}
	if err = p.updateCounters(ctx); err != nil {
		return err
	}
	if err = p.projectScene(ctx); err != nil {
		return err
	}
	if shared != nil {
		// The caller owns the transaction and commits every project at once.
		return nil
	}
	return tx.Commit()
}

// pass is the mutable state of one project's ingest.
type pass struct {
	ing     *Ingester
	project string
	now     time.Time
	b       *batch
	w       *writer
	tx      *sql.Tx

	// crews is every crew of the project, from `crews/<id>.meta`.
	crews []crewRecord
	// mate is the project's Mate, from `mate/mate.meta`.
	mate mateRecord
	// statusClock dates the status lines: a crew's own shell commands,
	// keyed by actor, with the command text as written in the transcript.
	statusClock map[string][]datedCommand
	// commitSightings are the commits an actor's transcript shows it making,
	// keyed by actor. They matter because a branch is short-lived: `matev2
	// merge` deletes it, and a crew that commits seconds before the merge can
	// leave no window in which any poll could have read `git log`.
	commitSightings map[string][]commitSighting
}

type datedCommand struct {
	command string
	at      time.Time
}

// commitSighting is a commit an agent's transcript shows it making: the sha
// git printed back at it, and when.
type commitSighting struct {
	sha string
	at  time.Time
}

// readCursor is where the last pass stopped in one append-only file.
func (p *pass) readCursor(ctx context.Context, path string) (int64, error) {
	var offset int64
	err := p.tx.QueryRowContext(ctx, `SELECT byte_offset FROM cursor WHERE source_path = ?`, path).Scan(&offset)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return offset, nil
}
