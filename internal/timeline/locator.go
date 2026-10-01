package timeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// Located is one agent's transcript: where it is, which harness wrote it, and
// which rule found it. Source is kept because the rules are not equally
// strong and a reader deciding whether to trust a token total should be able
// to see which one fired.
type Located struct {
	// Finalized is an immutable snapshot taken after a confirmed runtime stop.
	Finalized bool
	ActorID   string
	Kind      harness.Kind
	Path      string
	SessionID string
	Source    string
}

// The locator rules the timeline applies itself, before it asks the
// harness. They are written out in docs/timeline.md with the harness's own
// rules; these are the strings that land in a payload.
const (
	// LocatorMeta: `.meta` already names the file. Claude's Stop hook writes
	// `transcript=` (docs/mvp.md decision 9), which is exact.
	LocatorMeta = "meta.transcript"
	// LocatorRecorded: a path an earlier pass of this same database already
	// resolved for the actor. It is a cache of one of the rules, not a rule
	// of its own, and a rebuild starts without it.
	LocatorRecorded = "session.recorded"
)

// The reasons a locate can fail, carried in an `ingest.unresolved` payload.
// A harness may give a reason of its own (a rollout not adopted yet).
const (
	unresolvedNoSession  = harness.LocateNoSession
	unresolvedNoFile     = harness.LocateNotFound
	unresolvedNoHarness  = "unknown_harness"
	unresolvedNoWorktree = "no_worktree_recorded"
	// unresolvedUnobservable: the harness is registered but declares no
	// transcript it can read (plan section 3.7). Nothing is guessed in its
	// place: the agent has no turns and no tokens.
	unresolvedUnobservable = "transcript_unobservable"
)

// locateMate finds the Mate's transcript. Its Stop hook records the path,
// so the first rule is almost always the one that fires; the harness's own
// rules exist for a Mate whose Stop hook has not run yet, which is every
// Mate before its first turn ends.
func (p *pass) locateMate() (Located, string) {
	meta := p.mate.Meta
	if len(meta) == 0 {
		return Located{}, unresolvedNoSession
	}
	kind := harness.Kind(strings.TrimSpace(meta[MetaHarness]))
	loc := Located{ActorID: p.mate.ActorID, Kind: kind, SessionID: strings.TrimSpace(meta[MetaSessionID])}

	if path := strings.TrimSpace(meta[MetaTranscript]); path != "" {
		if fileExists(path) {
			loc.Path, loc.Source = path, LocatorMeta
			return loc, ""
		}
	}
	src, reason := p.ing.transcriptSource(kind)
	if reason != "" {
		return loc, reason
	}
	return p.ing.locate(loc, src, harness.TranscriptLocateRequest{
		Cwd: p.ing.ws.MateDir(p.project), SessionID: loc.SessionID, LaunchedAt: launchTime(meta),
	})
}

// locateCrew finds one crew's transcript.
//
// A Codex crew is the interesting case and the one docs/mvp.md M5 names: its
// rollout id is not in any file mate writes, so its harness asks the runtime -
// Herdr's `agent_session.value` - through the request's RuntimeSession, with
// adoption over cwd and launch time as the fallback for a crew whose agent is
// gone.
func (p *pass) locateCrew(ctx context.Context, crew crewRecord) (Located, string) {
	kind := harness.Kind(strings.TrimSpace(crew.harness()))
	loc := Located{ActorID: crew.ActorID, Kind: kind, SessionID: strings.TrimSpace(crew.Meta[MetaSessionID])}

	if path := strings.TrimSpace(crew.Meta[MetaTranscript]); path != "" && fileExists(path) {
		loc.Path, loc.Source = path, LocatorMeta
		return loc, ""
	}
	// A crew's transcript binding does not move while one agent runs: a
	// crew is never resumed (docs/mvp.md section 4b), so a path this
	// workspace has already resolved for the current launch is still the
	// right file. Re-using it keeps the ingest from asking the runtime about
	// every crew on every poll, which is two more `herdr` calls per crew per
	// five seconds on top of the observer's three. `mate crew relaunch`
	// starts a new session under the same actor, so a recorded path is only
	// re-used when it belongs to the launch the meta names now.
	if path, session, ok := p.recordedTranscript(ctx, crew.ActorID, loc.SessionID, launchTime(crew.Meta)); ok {
		loc.Path, loc.Source = path, LocatorRecorded
		if loc.SessionID == "" {
			loc.SessionID = session
		}
		return loc, ""
	}
	worktree := strings.TrimSpace(crew.Meta[MetaWorktree])
	if worktree == "" {
		return loc, unresolvedNoWorktree
	}
	src, reason := p.ing.transcriptSource(kind)
	if reason != "" {
		return loc, reason
	}
	return p.ing.locate(loc, src, harness.TranscriptLocateRequest{
		Cwd:        filepath.Join(p.ing.ws.Root(), filepath.FromSlash(worktree)),
		SessionID:  loc.SessionID,
		LaunchedAt: launchTime(crew.Meta),
		RuntimeSession: func() string {
			return p.sessionRef(ctx, crew.ID)
		},
	})
}

// transcriptSource is the transcript a harness declares, or the reason
// there is none to read: the kind is not registered, or the harness cannot
// be observed (plan section 3.7).
func (i *Ingester) transcriptSource(kind harness.Kind) (harness.TranscriptSource, string) {
	profile, err := i.deps.Harnesses.Lookup(kind)
	if err != nil {
		return nil, unresolvedNoHarness
	}
	transcript := profile.Capabilities().Transcript
	if !transcript.Verified() {
		return nil, unresolvedUnobservable
	}
	return transcript.Impl, ""
}

// locate asks a harness to find an agent's transcript by its own rules.
func (i *Ingester) locate(loc Located, src harness.TranscriptSource, req harness.TranscriptLocateRequest) (Located, string) {
	req.Root = i.deps.TranscriptRoots[loc.Kind]
	found, reason := src.Locate(req)
	if reason != "" {
		return loc, reason
	}
	loc.Path, loc.Source = found.Path, found.Rule
	if found.SessionID != "" {
		loc.SessionID = found.SessionID
	}
	return loc, ""
}

// recordedTranscript is a transcript an earlier pass of this database already
// resolved for an actor, if the file is still there and belongs to the
// current launch. A file that has gone is not a locate: the rules are tried
// again from the top.
//
// A crew relaunched in place (`mate crew relaunch`) keeps its actor and gets
// a fresh harness session, so the actor's newest row may be the dead
// session's. Two facts tell them apart: a recorded harness session id that is
// not the one the meta names now (Claude records it at launch), and a file
// last written before the current agent was launched (Codex records none,
// and a dead agent's rollout stops growing when it dies).
func (p *pass) recordedTranscript(ctx context.Context, actorID, currentSession string, launchedAt time.Time) (path, sessionID string, ok bool) {
	rows, err := p.tx.QueryContext(ctx,
		`SELECT transcript_path, harness_session_id FROM session
		  WHERE actor_id = ? AND transcript_path <> '' ORDER BY started_at DESC, rowid DESC`,
		actorID)
	if err != nil {
		return "", "", false
	}
	defer rows.Close()
	for rows.Next() {
		var candidate, session string
		if err := rows.Scan(&candidate, &session); err != nil {
			return "", "", false
		}
		if currentSession != "" && session != "" && session != currentSession {
			continue
		}
		fi, err := os.Stat(candidate)
		if err != nil || fi.IsDir() {
			continue
		}
		if !launchedAt.IsZero() && fi.ModTime().Before(launchedAt) {
			continue
		}
		return candidate, session, true
	}
	return "", "", false
}

func (p *pass) sessionRef(ctx context.Context, crew string) string {
	if p.ing.deps.SessionRef == nil {
		return ""
	}
	ref, err := p.ing.deps.SessionRef(ctx, p.project, crew)
	if err != nil {
		// "The runtime did not answer" is not a finding, exactly as it is
		// not one for the observer (docs/mvp.md section 7): the next rule is
		// tried and the next pass asks again.
		return ""
	}
	return strings.TrimSpace(ref)
}

// readHead reads at most n bytes from the front of a file: enough for the
// first record, and not the whole of a rollout that may be megabytes.
func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := f.Read(buf)
	if read == 0 && err != nil {
		return nil, err
	}
	return buf[:read], nil
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// unresolved records that an agent's transcript could not be found. It is an
// event and not a log line because the consequence is invisible otherwise:
// every turn, tool call and token of that agent is simply missing, and a
// timeline with a hole that says nothing about itself is worse than one with
// a hole that does.
//
// One event per (actor, reason): a locate that keeps failing for the same
// reason is the same fact every five seconds, and the dedup key says so.
func (p *pass) unresolved(actorID, harnessKind, reason string) {
	p.b.event(pendingEvent{
		Dedup:   dedup(KindIngestUnresolved, actorID, reason),
		Project: p.project, At: p.now, ActorID: actorID, Kind: KindIngestUnresolved,
		Payload: map[string]any{"reason": reason, "harness": harnessKind},
	})
}
