package timeline

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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

// The locator rules, in the order they are tried. They are written out in
// docs/timeline.md; these are the strings that land in a payload.
const (
	// LocatorMeta: `.meta` already names the file. Claude's Stop hook writes
	// `transcript=` (docs/mvp.md decision 9), which is exact.
	LocatorMeta = "meta.transcript"
	// LocatorClaudeProjects: `session_id=` plus Claude's own naming rule,
	// `~/.claude/projects/<slug>/<session-id>.jsonl`.
	LocatorClaudeProjects = "claude.projects"
	// LocatorHerdrSession: Herdr's `agent_session.value`, which for Codex is
	// the rollout's session uuid. Measured 2026-09-20 on Herdr 0.8.2: it is
	// present and correct for every Codex agent and null for every Claude
	// one, so it is a Codex rule and only a Codex rule.
	LocatorHerdrSession = "herdr.agent_session"
	// LocatorAdopt: harness.AdoptCodexRollout over the rollout directory, by
	// canonical cwd and launch time. The fallback when Herdr has no session
	// for the agent - a crew whose pane is gone, or a rebuild after the
	// session was deleted.
	LocatorAdopt = "codex.adopt"
	// LocatorRecorded: a path an earlier pass of this same database already
	// resolved for the actor. It is a cache of one of the rules above, not a
	// rule of its own, and a rebuild starts without it.
	LocatorRecorded = "session.recorded"
)

// The reasons a locate can fail, carried in an `ingest.unresolved` payload.
const (
	unresolvedNoSession    = "no_session_id"
	unresolvedNoFile       = "transcript_not_found"
	unresolvedAdoptPending = "rollout_not_adopted"
	unresolvedNoHarness    = "unknown_harness"
	unresolvedNoWorktree   = "no_worktree_recorded"
)

// locateMate finds the Mate's transcript. The Mate is Claude by default and
// its Stop hook records the path, so the first rule is almost always the one
// that fires; the second exists for a Mate whose Stop hook has not run yet,
// which is every Mate before its first turn ends.
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
	switch kind {
	case harness.KindClaude:
		if loc.SessionID == "" {
			return loc, unresolvedNoSession
		}
		path, ok := p.ing.claudeTranscript(p.ing.ws.MateDir(p.project), loc.SessionID)
		if !ok {
			return loc, unresolvedNoFile
		}
		loc.Path, loc.Source = path, LocatorClaudeProjects
		return loc, ""
	case harness.KindCodex:
		path, ok := p.ing.codexRolloutForSession(loc.SessionID)
		if ok {
			loc.Path, loc.Source = path, LocatorHerdrSession
			return loc, ""
		}
		return p.adoptCodex(loc, p.ing.ws.MateDir(p.project), launchTime(meta))
	default:
		return loc, unresolvedNoHarness
	}
}

// locateCrew finds one crew's transcript.
//
// A Codex crew is the interesting case and the one docs/mvp.md M5 names: its
// rollout id is not in any file mate writes, so it comes from the runtime -
// Herdr's `agent_session.value` - with harness.AdoptCodexRollout over cwd and
// launch time as the fallback for a crew whose agent is gone.
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
	cwd := filepath.Join(p.ing.ws.Root(), filepath.FromSlash(worktree))

	switch kind {
	case harness.KindClaude:
		if loc.SessionID == "" {
			return loc, unresolvedNoSession
		}
		path, ok := p.ing.claudeTranscript(cwd, loc.SessionID)
		if !ok {
			return loc, unresolvedNoFile
		}
		loc.Path, loc.Source = path, LocatorClaudeProjects
		return loc, ""
	case harness.KindCodex:
		if ref := p.sessionRef(ctx, crew.ID); ref != "" {
			if path, ok := p.ing.codexRolloutForSession(ref); ok {
				loc.SessionID = ref
				loc.Path, loc.Source = path, LocatorHerdrSession
				return loc, ""
			}
		}
		if path, ok := p.ing.codexRolloutForSession(loc.SessionID); ok {
			loc.Path, loc.Source = path, LocatorHerdrSession
			return loc, ""
		}
		return p.adoptCodex(loc, cwd, launchTime(crew.Meta))
	default:
		return loc, unresolvedNoHarness
	}
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

// adoptCodex is harness.AdoptCodexRollout over every rollout whose header
// parses, with the agent's canonical cwd and its recorded launch time. The
// rule is the v1 one and is deliberately conservative: an ambiguous match is
// no match, because two crews launched in the same worktree really are
// indistinguishable from the rollout alone.
func (p *pass) adoptCodex(loc Located, cwd string, launchedAt time.Time) (Located, string) {
	candidates, err := p.ing.codexCandidates()
	if err != nil || len(candidates) == 0 {
		return loc, unresolvedAdoptPending
	}
	canonical := cwd
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		canonical = resolved
	}
	adoption := harness.AdoptCodexRollout(candidates, canonical, launchedAt, loc.SessionID)
	if adoption.Status != harness.CodexAdoptionKnown {
		return loc, unresolvedAdoptPending
	}
	loc.Path, loc.Source = adoption.Candidate.Path, LocatorAdopt
	loc.SessionID = adoption.Candidate.Meta.SessionID
	return loc, ""
}

// claudeTranscript is Claude's own naming rule:
// `<projects root>/<slug of cwd>/<session-id>.jsonl`.
func (i *Ingester) claudeTranscript(cwd, sessionID string) (string, bool) {
	root := i.deps.ClaudeProjectsDir
	if root == "" {
		var err error
		root, err = harness.ClaudeProjectsDir()
		if err != nil {
			return "", false
		}
	}
	path := harness.ClaudeTranscriptPath(root, cwd, sessionID)
	if path == "" || !fileExists(path) {
		return "", false
	}
	return path, true
}

// codexRolloutForSession finds the rollout whose file name carries a session
// id. Codex names a rollout `rollout-<timestamp>-<session-id>.jsonl`, so the
// id is in the name and no file has to be opened to find the right one.
func (i *Ingester) codexRolloutForSession(sessionID string) (string, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", false
	}
	root, err := i.codexSessionsRoot()
	if err != nil {
		return "", false
	}
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil //nolint:nilerr // an unreadable branch is not an answer
		}
		name := d.Name()
		if strings.HasSuffix(name, ".jsonl") && strings.Contains(name, sessionID) {
			found = path
		}
		return nil
	})
	if found == "" {
		return "", false
	}
	return found, true
}

func (i *Ingester) codexSessionsRoot() (string, error) {
	if i.deps.CodexSessionsDir != "" {
		return i.deps.CodexSessionsDir, nil
	}
	return harness.CodexSessionsDir("")
}

// codexCandidates is every rollout whose first record is a session_meta this
// build understands. It is read fresh each time it is needed rather than
// cached: a crew spawned during this console's run writes its rollout after
// the console started.
func (i *Ingester) codexCandidates() ([]harness.CodexRolloutCandidate, error) {
	root, err := i.codexSessionsRoot()
	if err != nil {
		return nil, err
	}
	var out []harness.CodexRolloutCandidate
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil //nolint:nilerr // an unreadable branch is skipped, not fatal
		}
		head, err := readHead(path, 64*1024)
		if err != nil {
			return nil //nolint:nilerr
		}
		meta, err := harness.ParseCodexSessionMeta(head)
		if err != nil {
			return nil //nolint:nilerr
		}
		out = append(out, harness.CodexRolloutCandidate{Path: path, Meta: meta})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out, nil
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
