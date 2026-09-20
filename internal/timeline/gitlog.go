package timeline

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/crewstate"
	"github.com/nguyenngocanh94/matev2/internal/db"
	"github.com/nguyenngocanh94/matev2/internal/gitx"
)

// Commit is one commit of a crew's branch that the default branch does not
// have. The record separator and field separator are control characters no
// commit subject can contain, so a subject with a newline in it is still one
// record.
type Commit struct {
	SHA       string
	Committed time.Time
	Subject   string
	Files     []string
}

const (
	commitRecordSep = "\x1e"
	commitFieldSep  = "\x1f"
	commitFormat    = commitRecordSep + "%H" + commitFieldSep + "%cI" + commitFieldSep + "%s" + commitFieldSep
)

// ingestGit records what a crew actually produced.
//
// `git log <default>..<branch>` - two dots, the list of commits that belong
// to this branch alone, which is the same spelling `matev2 diff` uses for its
// commit list and for the same reason (docs/mvp.md section 7: three dots
// there is the diff, two dots here is the list).
//
// `merge.done` is the other half: a crew is merged when it has been closed
// `finished` and its branch tip is now an ancestor of the default branch.
// There is no file that records a merge - `matev2 merge` types into no pane,
// so `sent.log` is silent about it (docs/mvp.md section 7) - so this is
// derived from git itself, which is also what makes it survive a rebuild.
func (p *pass) ingestGit(ctx context.Context) error {
	cfg, err := p.ing.ws.LoadProject(p.project)
	if err != nil {
		// A project whose yaml is unreadable is not a project with no
		// commits; nothing is concluded and the next pass asks again.
		return nil //nolint:nilerr
	}
	repo := p.ing.ws.RepoDir(cfg.Repo)
	base := cfg.DefaultBranch
	if base == "" {
		base = "main"
	}
	git := p.ing.deps.git()

	for _, crew := range p.crews {
		branch := strings.TrimSpace(crew.Meta[MetaBranch])
		if branch == "" {
			continue
		}
		exists, err := git.BranchExists(ctx, repo, branch)
		if err != nil {
			continue
		}
		if exists {
			commits, err := commitsBetween(ctx, git, repo, base, branch)
			if err == nil {
				for _, commit := range commits {
					p.commitEvent(crew, commit, branch, repo)
				}
			}
		}
		// The commits the crew's own transcript shows it making, whether or
		// not any poll ever caught the branch ahead. They are keyed by the
		// full sha, so a commit both rules find is one event.
		for _, sighting := range p.commitSightings[crew.ActorID] {
			commit, ok := commitByRef(ctx, git, repo, sighting.sha)
			if !ok {
				continue
			}
			p.commitEvent(crew, commit, branch, repo)
		}
		if err := p.mergeEvent(ctx, git, crew, repo, base, branch, exists); err != nil {
			return err
		}
	}
	return nil
}

func (p *pass) commitEvent(crew crewRecord, commit Commit, branch, repo string) {
	p.b.event(pendingEvent{
		Dedup:   dedup(KindGitCommitted, crew.ActorID, commit.SHA),
		Project: p.project, At: commit.Committed, ActorID: crew.ActorID, Kind: KindGitCommitted,
		TaskActor: crew.ActorID,
		Payload: map[string]any{
			"sha": commit.SHA, "short": shortSHA(commit.SHA), "subject": commit.Subject,
			"branch": branch, "files": commit.Files,
		},
		RefPath: repo,
	})
}

// mergeEvent decides whether a crew's branch has landed.
//
// The evidence is git's, not a log's: the crew is closed `finished` and its
// last commit is reachable from the default branch. A branch `matev2 merge`
// deleted on its way out is handled by the same rule from the other side -
// the branch is gone and the commits it carried are in `default`, so the last
// commit this ingest recorded for the crew is the one to test.
//
// Which is why a crew's commits are read from its transcript as well as from
// `git log`: measured 2026-09-20, a crew committed six seconds before the
// captain merged, no five-second poll fell inside that window, and a merge
// that could only be proved from a branch nobody had read was not proved at
// all. The transcript keeps the sha for ever.
func (p *pass) mergeEvent(ctx context.Context, git gitx.Git, crew crewRecord,
	repo, base, branch string, branchExists bool) error {

	if crew.state() != crewstate.StateFinished {
		return nil
	}
	tip := ""
	if branchExists {
		sha, err := git.HeadCommit(ctx, repo, branch)
		if err != nil {
			return nil //nolint:nilerr
		}
		tip = sha
	} else {
		sha, err := p.lastRecordedCommit(ctx, crew.ActorID)
		if err != nil {
			return err
		}
		tip = sha
	}
	if tip == "" {
		return nil
	}
	landed, err := git.IsAncestor(ctx, repo, tip, base)
	if err != nil || !landed {
		return nil //nolint:nilerr
	}
	at := metaTime(crew.Meta, MetaStoppedAt)
	if at.IsZero() {
		at = p.now
	}
	// The merge closes the crew, so the person who ran it is the person who
	// closed it: `matev2 merge` runs `crew stop` itself on success
	// (docs/mvp.md M4). linkCauses names which turn or gesture it was.
	mergeDedup := dedup(KindMergeDone, crew.ActorID)
	p.b.event(pendingEvent{
		Dedup:   mergeDedup,
		Project: p.project, At: at, ActorID: p.mate.ActorID, Subject: crew.ActorID,
		Kind: KindMergeDone, TaskActor: crew.ActorID,
		Payload: map[string]any{
			"crew": crew.ID, "branch": branch, "into": base,
			"sha": tip, "short": shortSHA(tip),
		},
		RefPath: repo,
	})
	for i := range p.b.tasks {
		if p.b.tasks[i].CrewActorID == crew.ActorID {
			p.b.tasks[i].MergedDedup = mergeDedup
		}
	}
	return nil
}

// lastRecordedCommit is the newest commit this timeline has for a crew,
// looking at what this pass has just found as well as at the table: a crew
// that commits and is merged inside one poll would otherwise have its commit
// and its merge decided in the same pass, with the merge unable to see it.
func (p *pass) lastRecordedCommit(ctx context.Context, crewActorID string) (string, error) {
	newest := time.Time{}
	sha := ""
	for _, e := range p.b.events {
		if e.Kind != KindGitCommitted || e.ActorID != crewActorID {
			continue
		}
		if value, ok := e.Payload["sha"].(string); ok && !e.At.Before(newest) {
			newest, sha = e.At, value
		}
	}
	var stored sql.NullString
	var storedAt sql.NullString
	err := p.tx.QueryRowContext(ctx,
		`SELECT json_extract(payload, '$.sha'), at FROM event
		  WHERE actor_id = ? AND kind = ? ORDER BY at DESC, id DESC LIMIT 1`,
		crewActorID, KindGitCommitted).Scan(&stored, &storedAt)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if stored.Valid && !db.ParseTime(storedAt.String).Before(newest) {
		return stored.String, nil
	}
	return sha, nil
}

// commitsBetween runs one `git log` and parses it. gitx.LogOneline is the
// review list and carries only a short sha and a subject; the timeline needs
// the commit's own time and the files it touched, so this asks for them
// directly rather than pretending a one-line summary has them.
func commitsBetween(ctx context.Context, git gitx.Git, repo, base, branch string) ([]Commit, error) {
	out, err := git.Log(ctx, repo, base+".."+branch,
		"--no-color", "--reverse", "--name-only", "--format="+commitFormat)
	if err != nil {
		return nil, err
	}
	return parseCommits(out), nil
}

func parseCommits(out string) []Commit {
	var commits []Commit
	for _, record := range strings.Split(out, commitRecordSep) {
		if strings.TrimSpace(record) == "" {
			continue
		}
		fields := strings.SplitN(record, commitFieldSep, 4)
		if len(fields) < 4 {
			continue
		}
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))
		if err != nil {
			continue
		}
		var files []string
		for _, line := range strings.Split(fields[3], "\n") {
			if name := strings.TrimSpace(line); name != "" {
				files = append(files, name)
			}
		}
		commits = append(commits, Commit{
			SHA:       strings.TrimSpace(fields[0]),
			Committed: at,
			Subject:   strings.TrimSpace(fields[2]),
			Files:     files,
		})
	}
	return commits
}

// commitByRef reads one commit by whatever names it - a short sha echoed by
// `git commit` is enough. A ref git cannot resolve is not an error here: a
// crew may have committed in a worktree whose objects are no longer reachable,
// and a commit nobody can look up is one this timeline cannot describe.
func commitByRef(ctx context.Context, git gitx.Git, repo, ref string) (Commit, bool) {
	out, err := git.Log(ctx, repo, ref, "-1", "--no-color", "--name-only", "--format="+commitFormat)
	if err != nil {
		return Commit{}, false
	}
	commits := parseCommits(out)
	if len(commits) != 1 {
		return Commit{}, false
	}
	return commits[0], true
}

func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}
