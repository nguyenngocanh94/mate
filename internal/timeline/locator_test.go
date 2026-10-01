package timeline_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// The locator rules of docs/timeline.md, each proved on its own. They are not
// interchangeable: `agent_session.value` is a Codex fact that Herdr reports
// for a live agent, and the adoption fallback is what is left once the agent
// is gone.

// Herdr's `agent_session.value` is the rollout's session uuid, and Codex puts
// that uuid in the rollout's file name, so the value alone finds the file.
func TestCrewCodexIsLocatedFromTheRuntimesAgentSession(t *testing.T) {
	f := newFixture(t)
	sessions := filepath.Join(f.root, "codex-sessions", "2026", "09", "19")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const sessionID = "01a0b944-33bb-7503-9db7-cd51ff61855a"
	rollout := filepath.Join(sessions, "rollout-2026-09-19T17-44-09-"+sessionID+".jsonl")
	copyFile(t, abs(t, codexFixture), rollout)

	// The `.meta` names no transcript and no session, so only the runtime can
	// answer - which is the case docs/mvp.md M5 names for a Codex crew.
	clearCrewTranscript(t, f)
	var asked []string
	f.ing = timeline.New(f.ws, f.db, timeline.Deps{
		Now: func() time.Time { return fixtureNow },
		SessionRef: func(_ context.Context, project, crew string) (string, error) {
			asked = append(asked, project+"/"+crew)
			return sessionID, nil
		},
		Harnesses:       catalog.Default(),
		TranscriptRoots: transcriptRoots(filepath.Join(f.root, "no-claude-projects"), filepath.Join(f.root, "codex-sessions")),
	})
	f.ingest(t)

	if len(asked) == 0 {
		t.Fatal("the locator never asked the runtime for the crew's session")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM turn WHERE actor_id = ?`, f.crewActor()); n != codexTurns {
		t.Fatalf("%d turn(s) for the crew, want %d: the rollout was not found from agent_session.value", n, codexTurns)
	}
	var path string
	if err := f.db.SQL().QueryRow(`SELECT transcript_path FROM session WHERE actor_id = ? AND transcript_path <> ''`,
		f.crewActor()).Scan(&path); err != nil {
		t.Fatalf("read the session: %v", err)
	}
	if path != rollout {
		t.Fatalf("the session names %q, want the rollout %q", path, rollout)
	}
}

// With no runtime to ask - a crew whose pane is gone, or a rebuild long after
// the session was deleted - harness.AdoptCodexRollout matches on the canonical
// cwd and the launch time, which is v1's rule and deliberately conservative.
func TestCrewCodexFallsBackToAdoptingARolloutByCwdAndLaunchTime(t *testing.T) {
	f := newFixture(t)
	worktree := filepath.Join(f.root, ".worktrees", "shop-buybtn")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	sessions := filepath.Join(f.root, "codex-sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	// A rollout whose header names this worktree and a launch time after the
	// crew's. Only the header matters to the adoption rule.
	rollout := filepath.Join(sessions, "rollout-2026-09-19T17-44-09-adopted.jsonl")
	rewriteCodexCwd(t, abs(t, codexFixture), rollout, worktree)

	clearCrewTranscript(t, f)
	f.ing = timeline.New(f.ws, f.db, timeline.Deps{
		Now:             func() time.Time { return fixtureNow },
		Harnesses:       catalog.Default(),
		TranscriptRoots: transcriptRoots(filepath.Join(f.root, "no-claude-projects"), sessions),
	})
	f.ingest(t)

	if n := f.count(t, `SELECT COUNT(*) FROM turn WHERE actor_id = ?`, f.crewActor()); n != codexTurns {
		t.Fatalf("%d turn(s) for the crew, want %d: the rollout was not adopted", n, codexTurns)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ? AND actor_id = ?`,
		timeline.KindIngestUnresolved, f.crewActor()); n != 0 {
		t.Fatalf("the crew's transcript was adopted and %d unresolved event(s) were still written", n)
	}
}

// `started_at` is written once the crew is ready and has its brief, and Codex
// opens its rollout before that: measured 2026-09-24 (task 34), a rollout's
// first record 0.2s before the crew's `started_at`, so a rebuild after the
// crew was gone found no transcript for it. The anchor is `launched_at`,
// taken before `agent start`; an older record without it gets
// `started_at` less a bounded slack. A rollout opened before the launch is
// still refused.
func TestCrewCodexAdoptionAnchorsOnTheLaunchNotTheReadyTime(t *testing.T) {
	// The fixture rollout's first record is 2026-09-19T10:44:11.839Z.
	for _, tc := range []struct {
		name       string
		launchedAt string
		adopted    bool
	}{
		{name: "record from before launched_at existed", launchedAt: "", adopted: true},
		{name: "launched before the rollout opened", launchedAt: "2026-09-19T10:44:08Z", adopted: true},
		{name: "rollout older than the launch", launchedAt: "2026-09-19T10:44:12Z", adopted: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			worktree := filepath.Join(f.root, ".worktrees", "shop-buybtn")
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				t.Fatalf("mkdir worktree: %v", err)
			}
			sessions := filepath.Join(f.root, "codex-sessions")
			if err := os.MkdirAll(sessions, 0o755); err != nil {
				t.Fatalf("mkdir sessions: %v", err)
			}
			rewriteCodexCwd(t, abs(t, codexFixture), filepath.Join(sessions, "rollout-2026-09-19T17-44-09-adopted.jsonl"), worktree)
			clearCrewTranscript(t, f)
			meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
			if err != nil {
				t.Fatalf("ReadCrewMeta: %v", err)
			}
			// Ready, and so `started_at`, just after the rollout opened.
			meta[timeline.MetaStartedAt] = "2026-09-19T10:44:12Z"
			delete(meta, timeline.MetaLaunchedAt)
			if tc.launchedAt != "" {
				meta[timeline.MetaLaunchedAt] = tc.launchedAt
			}
			if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
				t.Fatalf("WriteCrewMeta: %v", err)
			}
			f.ing = timeline.New(f.ws, f.db, timeline.Deps{
				Now:             func() time.Time { return fixtureNow },
				Harnesses:       catalog.Default(),
				TranscriptRoots: transcriptRoots(filepath.Join(f.root, "no-claude-projects"), sessions),
			})
			f.ingest(t)

			n := f.count(t, `SELECT COUNT(*) FROM turn WHERE actor_id = ?`, f.crewActor())
			if tc.adopted && n != codexTurns {
				t.Fatalf("%d turn(s) for the crew, want %d: the rollout was not adopted", n, codexTurns)
			}
			if !tc.adopted && n != 0 {
				t.Fatalf("%d turn(s) for the crew: a rollout older than the launch was adopted", n)
			}
		})
	}
}

// A Claude agent has no `agent_session` at all (measured 2026-09-20, Herdr
// 0.8.2), so it is found from its own `session_id` and Claude's naming rule:
// `<projects root>/<slug of cwd>/<session-id>.jsonl`.
func TestMateClaudeIsLocatedFromItsSessionIDWhenTheHookHasNotWrittenThePath(t *testing.T) {
	f := newFixture(t)
	const sessionID = "8414030c-5d90-4925-94cc-c94e12aae4a9"
	projects := filepath.Join(f.root, "claude-projects")
	slug := claude.ClaudeProjectSlug(f.ws.MateDir(fixtureProject))
	dir := filepath.Join(projects, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	copyFile(t, abs(t, claudeFixture), filepath.Join(dir, sessionID+".jsonl"))

	meta, err := f.ws.ReadMateMeta(fixtureProject)
	if err != nil {
		t.Fatalf("ReadMateMeta: %v", err)
	}
	// The Stop hook has not run yet, which is every Mate before its first
	// turn ends.
	meta[timeline.MetaTranscript] = ""
	if err := f.ws.WriteMateMeta(fixtureProject, meta); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	f.ing = timeline.New(f.ws, f.db, timeline.Deps{
		Now:             func() time.Time { return fixtureNow },
		Harnesses:       catalog.Default(),
		TranscriptRoots: transcriptRoots(projects, filepath.Join(f.root, "no-codex-sessions")),
	})
	f.ingest(t)

	if n := f.count(t, `SELECT COUNT(*) FROM turn WHERE actor_id = ?`, f.mateActor()); n != claudeTurns {
		t.Fatalf("%d turn(s) for the Mate, want %d: the transcript was not found from session_id", n, claudeTurns)
	}
}

// `mate crew relaunch` gives a crew a fresh Claude session under the same
// actor and clears `transcript=`. The dead session's file is still on disk
// and was recorded by an earlier pass, so a locator that re-used the actor's
// newest recorded path would read it forever and never ingest the new one.
// The recorded session id no longer matching the meta's is what refuses it;
// both files are written after the relaunch here, so time alone cannot.
func TestRelaunchedClaudeCrewIsLocatedInItsNewSession(t *testing.T) {
	f := newFixture(t)
	const oldSession = "8414030c-5d90-4925-94cc-c94e12aae4a9"
	const newSession = "5a0c3b1e-2f44-4d1a-9a7e-0c1d2e3f4a5b"
	projects := filepath.Join(f.root, "claude-projects")
	f.ing = timeline.New(f.ws, f.db, timeline.Deps{
		Now:             func() time.Time { return fixtureNow },
		Harnesses:       catalog.Default(),
		TranscriptRoots: transcriptRoots(projects, filepath.Join(f.root, "no-codex-sessions")),
	})
	oldPath := filepath.Join(f.root, "old-session.jsonl")
	copyFile(t, abs(t, claudeFixture), oldPath)
	writeCrewBinding(t, f, map[string]string{
		"harness": "claude", timeline.MetaSessionID: oldSession, timeline.MetaTranscript: oldPath,
	})
	f.ingest(t)

	// The relaunch: a new session id, no transcript yet, a later launch.
	cwd := filepath.Join(f.ws.Root(), ".worktrees", "shop-buybtn")
	newPath := claude.ClaudeTranscriptPath(projects, cwd, newSession)
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	copyFile(t, abs(t, claudeFixture), newPath)
	writeCrewBinding(t, f, map[string]string{
		timeline.MetaSessionID: newSession, timeline.MetaTranscript: "",
		"launched_at": mustTime("2026-09-20T09:00:00Z").Format(time.RFC3339),
	})
	f.ingest(t)

	var path string
	if err := f.db.SQL().QueryRow(`SELECT transcript_path FROM session WHERE actor_id = ? AND harness_session_id = ?`,
		f.crewActor(), newSession).Scan(&path); err != nil {
		t.Fatalf("read the new session: %v", err)
	}
	if path != newPath {
		t.Fatalf("the relaunched session names %q, want its own transcript %q", path, newPath)
	}
}

// A Codex crew records no session id, so after a relaunch the dead agent's
// rollout is told apart by time: it stopped growing when the agent died,
// before the new one was launched. The locator then asks the runtime again
// and finds the new rollout.
func TestRelaunchedCodexCrewIsLocatedInItsNewRollout(t *testing.T) {
	f := newFixture(t)
	const oldSession = "01a0b944-33bb-7503-9db7-cd51ff61855a"
	const newSession = "7c2d9e10-4b5a-4c3d-8e9f-a0b1c2d3e4f5"
	sessions := filepath.Join(f.root, "codex-sessions", "2026", "09", "19")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oldRollout := filepath.Join(sessions, "rollout-2026-09-19T17-44-09-"+oldSession+".jsonl")
	copyFile(t, abs(t, codexFixture), oldRollout)
	clearCrewTranscript(t, f)
	ref := oldSession
	f.ing = timeline.New(f.ws, f.db, timeline.Deps{
		Now:             func() time.Time { return fixtureNow },
		SessionRef:      func(context.Context, string, string) (string, error) { return ref, nil },
		Harnesses:       catalog.Default(),
		TranscriptRoots: transcriptRoots(filepath.Join(f.root, "no-claude-projects"), filepath.Join(f.root, "codex-sessions")),
	})
	f.ingest(t)

	// The agent died at the end of the captured run; the relaunch came a day
	// later and Herdr now reports the new agent's rollout.
	if err := os.Chtimes(oldRollout, fixtureNow, fixtureNow); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	newRollout := filepath.Join(sessions, "rollout-2026-09-20T09-00-05-"+newSession+".jsonl")
	copyFile(t, abs(t, codexFixture), newRollout)
	writeCrewBinding(t, f, map[string]string{"launched_at": mustTime("2026-09-20T09:00:00Z").Format(time.RFC3339)})
	ref = newSession
	f.ingest(t)

	if n := f.count(t, `SELECT COUNT(*) FROM session WHERE actor_id = ? AND transcript_path = ?`,
		f.crewActor(), newRollout); n != 1 {
		t.Fatalf("%d session row(s) name the new rollout, want 1: the dead agent's rollout was re-used", n)
	}
}

// The observer's findings become incidents, opened and resolved as pairs.
func TestIncidentsBecomeOpenAndResolvedEvents(t *testing.T) {
	f := newFixture(t)
	open := mustTime("2026-09-19T10:45:10Z")
	resolved := mustTime("2026-09-19T10:45:40Z")
	for _, entry := range []store.IncidentEntry{
		{Time: open, Crew: fixtureCrew, Kind: "stale", State: store.IncidentOpen, Text: "no status line for 3m0s"},
		{Time: resolved, Crew: fixtureCrew, Kind: "stale", State: store.IncidentResolved, Text: "the pane changed"},
	} {
		if err := f.ws.AppendIncident(fixtureProject, entry); err != nil {
			t.Fatalf("AppendIncident: %v", err)
		}
	}
	f.ingest(t)

	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindIncidentOpen); n != 1 {
		t.Fatalf("%d incident.opened event(s), want 1", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindIncidentResol); n != 1 {
		t.Fatalf("%d incident.resolved event(s), want 1", n)
	}
	var kind, actor, resolvedAt string
	if err := f.db.SQL().QueryRow(
		`SELECT kind, actor_id, resolved_at FROM incident`).Scan(&kind, &actor, &resolvedAt); err != nil {
		t.Fatalf("read the incident: %v", err)
	}
	if kind != "stale" || actor != f.crewActor() {
		t.Fatalf("the incident is %q about %q", kind, actor)
	}
	if resolvedAt == "" {
		t.Fatal("the incident was never closed, although the observer wrote a resolved line")
	}
}

// A crew's commits and the merge that landed them come from git, because no
// file records either: `mate merge` types into no pane, so `sent.log` is
// silent about it (docs/mvp.md section 7).
func TestCommitsAndTheMergeComeFromGit(t *testing.T) {
	f := newFixture(t)
	repo := filepath.Join(f.root, fixtureProject)
	gitRun(t, repo, "checkout", "-b", fixtureBranch)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# shop\n\n[Buy](pages/checkout-express.html)\n"), 0o644); err != nil {
		t.Fatalf("edit README: %v", err)
	}
	gitRun(t, repo, "commit", "-am", "docs: add Buy link")
	gitRun(t, repo, "checkout", "main")
	f.ingest(t)

	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ? AND actor_id = ?`,
		timeline.KindGitCommitted, f.crewActor()); n != 1 {
		t.Fatalf("%d git.committed event(s), want the one commit the branch carries", n)
	}
	var subject, files string
	if err := f.db.SQL().QueryRow(
		`SELECT json_extract(payload, '$.subject'), json_extract(payload, '$.files')
		   FROM event WHERE kind = ?`, timeline.KindGitCommitted).Scan(&subject, &files); err != nil {
		t.Fatalf("read the commit: %v", err)
	}
	if subject != "docs: add Buy link" || files != `["README.md"]` {
		t.Fatalf("the commit event reads subject %q files %s", subject, files)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindMergeDone); n != 0 {
		t.Fatal("a merge was recorded for a crew that is not closed and a branch that has not landed")
	}

	// The captain merges from the Console and closes the crew, exactly as
	// `mate merge` does.
	gitRun(t, repo, "merge", "--ff-only", fixtureBranch)
	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	meta[timeline.MetaState] = "finished"
	meta[timeline.MetaStoppedAt] = mustTime("2026-09-19T10:47:00Z").Format(time.RFC3339)
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	f.ingest(t)

	events := f.story(t)
	var merge timeline.StoryEvent
	for _, e := range events {
		if e.Kind == timeline.KindMergeDone {
			merge = e
		}
	}
	if merge.ID == 0 {
		t.Fatal("no merge.done was recorded after the branch landed and the crew was closed")
	}
	if merge.Cause == 0 {
		t.Fatal("merge.done names no cause")
	}
	if merge.CauseKind != timeline.KindStatusAppend {
		t.Fatalf("merge.done is caused by %q; with no Mate turn running the merge, the rule is the crew's handback",
			merge.CauseKind)
	}
	if by := merge.Field("by"); by != "captain" {
		t.Fatalf("merge.done records by=%q, want captain", by)
	}
	var mergedEvent int64
	if err := f.db.SQL().QueryRow(`SELECT merged_event_id FROM task WHERE crew_actor_id = ?`,
		f.crewActor()).Scan(&mergedEvent); err != nil {
		t.Fatalf("read the task: %v", err)
	}
	if mergedEvent != merge.ID {
		t.Fatalf("the task's merged_event_id is %d, want %d", mergedEvent, merge.ID)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindCrewFinished); n != 1 {
		t.Fatalf("%d crew.finished event(s), want 1", n)
	}
}

// Auto mode is a flag with no history of its own, so it is an observation:
// turning it on is dated by the flag's mtime and recorded once.
func TestTurningAutoModeOnIsRecordedOnce(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindModeChanged); n != 0 {
		t.Fatalf("%d mode.changed event(s) for a project that has always been manual", n)
	}
	if err := f.ws.SetAuto(fixtureProject, true); err != nil {
		t.Fatalf("SetAuto: %v", err)
	}
	f.ingest(t)
	f.ingest(t)
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindModeChanged); n != 1 {
		t.Fatalf("%d mode.changed event(s) after auto was turned on once", n)
	}
	var to, dated string
	if err := f.db.SQL().QueryRow(
		`SELECT json_extract(payload, '$.to'), json_extract(payload, '$.dated_by')
		   FROM event WHERE kind = ?`, timeline.KindModeChanged).Scan(&to, &dated); err != nil {
		t.Fatalf("read the mode event: %v", err)
	}
	if to != "auto" || dated != "flag_mtime" {
		t.Fatalf("mode.changed reads to=%q dated_by=%q", to, dated)
	}
}

// The observer's composer readings become `health.changed` only when the
// classification changes, not once per poll.
func TestHealthIsRecordedOnlyWhenTheComposerChanges(t *testing.T) {
	f := newFixture(t)
	readings := []timeline.HealthReading{{
		Project: fixtureProject, Crew: fixtureCrew, AgentPresent: true,
		Composer: "busy", At: mustTime("2026-09-19T10:44:15Z"),
	}}
	f.ing = timeline.New(f.ws, f.db, timeline.Deps{
		Now:             func() time.Time { return fixtureNow },
		Health:          func() []timeline.HealthReading { return readings },
		Harnesses:       catalog.Default(),
		TranscriptRoots: transcriptRoots(filepath.Join(f.root, "no-claude-projects"), filepath.Join(f.root, "no-codex-sessions")),
	})
	f.ingest(t)
	f.ingest(t)
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindHealthChanged); n != 1 {
		t.Fatalf("%d health.changed event(s) for an unchanged composer, want 1", n)
	}
	readings = []timeline.HealthReading{{
		Project: fixtureProject, Crew: fixtureCrew, AgentPresent: true,
		Composer: "empty", At: mustTime("2026-09-19T10:44:45Z"),
	}}
	f.ingest(t)
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindHealthChanged); n != 2 {
		t.Fatalf("%d health.changed event(s) after the composer changed, want 2", n)
	}
}

// unobserved is a registered harness that declares no transcript it can
// read.
type unobserved struct{ codex.Codex }

func (unobserved) Kind() harness.Kind { return "unobserved" }

func (u unobserved) Capabilities() harness.Capabilities {
	c := u.Codex.Capabilities()
	c.Transcript = harness.Cap[harness.TranscriptSource]{Status: harness.CapUnknown, Reason: "never measured"}
	return c
}

// A crew on a harness with no transcript is recorded as unobservable, and
// nothing is guessed in its place (plan section 3.7): no turn, no token,
// even though its meta names a file the Codex harness could read.
func TestAHarnessWithNoTranscriptIsUnobservedNotGuessed(t *testing.T) {
	f := newFixture(t)
	reg, err := harness.NewRegistry(nil, claude.Claude{}, codex.Codex{}, unobserved{})
	if err != nil {
		t.Fatal(err)
	}
	f.ing = timeline.New(f.ws, f.db, timeline.Deps{
		Now:             func() time.Time { return fixtureNow },
		Harnesses:       reg,
		TranscriptRoots: transcriptRoots(filepath.Join(f.root, "no-claude-projects"), filepath.Join(f.root, "no-codex-sessions")),
	})
	writeCrewBinding(t, f, map[string]string{"harness": "unobserved"})
	f.ingest(t)

	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ? AND actor_id = ? AND json_extract(payload, '$.reason') = 'transcript_unobservable'`,
		timeline.KindIngestUnresolved, f.crewActor()); n != 1 {
		t.Fatalf("%d transcript_unobservable event(s) for the crew, want 1", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM turn WHERE actor_id = ?`, f.crewActor()); n != 0 {
		t.Fatalf("%d turn(s) recorded for an unobservable crew, want none", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM turn WHERE actor_id = ?`, f.mateActor()); n == 0 {
		t.Fatal("the Mate on Claude lost its turns too")
	}
}

func clearCrewTranscript(t *testing.T, f *fixture) {
	t.Helper()
	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	meta[timeline.MetaTranscript] = ""
	meta[timeline.MetaSessionID] = ""
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
}

// writeCrewBinding overwrites keys of the fixture crew's `.meta`, the way a
// relaunch rewrites the keys that name its agent.
func writeCrewBinding(t *testing.T, f *fixture, keys map[string]string) {
	t.Helper()
	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	for k, v := range keys {
		meta[k] = v
	}
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read %s: %v", from, err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", to, err)
	}
}

// rewriteCodexCwd copies a rollout with its session_meta cwd replaced, so the
// adoption rule - which matches on the canonical cwd - has something to match.
func rewriteCodexCwd(t *testing.T, from, to, cwd string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read %s: %v", from, err)
	}
	canonical := cwd
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		canonical = resolved
	}
	replaced := replaceFirst(string(data),
		`"cwd":"/private/tmp/TestLiveAcceptanceTwoProjects1918684859/001/.worktrees/shop-buybtn"`,
		`"cwd":"`+canonical+`"`)
	if err := os.WriteFile(to, []byte(replaced), 0o644); err != nil {
		t.Fatalf("write %s: %v", to, err)
	}
}

func replaceFirst(s, old, new string) string {
	i := indexOfString(s, old)
	if i < 0 {
		panic("the fixture no longer carries " + old)
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOfString(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
