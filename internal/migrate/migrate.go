// Package migrate moves a workspace written before layout 2, its repos
// beside `.mate/`, to layout 2, where every repo lives under its project's
// directory (docs/mvp.md section 3; docs/plans/workspace-layout-and-tools-
// 2026-10-08.md section 4.2).
//
// It is one way. Every precondition of every project is checked before the
// first rename, and one that fails moves nothing. A repo is renamed, never
// copied, never deleted and never renamed back. Each rename is recorded in
// `.mate/migrate.log` as it happens, so a run that stopped half way - the
// machine went down between two steps - is finished by the next run, which
// reads where every repo is from the disk and from that log.
package migrate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Deps is what a migrate needs from outside the workspace.
type Deps struct {
	// Runtime is asked whether each project's Mate is running
	// (query.ReadLiveness). Nil, or a Herdr that cannot be asked, leaves
	// the question open, and a Mate whose meta records a pane is then
	// taken as running.
	Runtime runtime.ReadAdapter
	// Session is the workspace's Herdr session (spawn.SessionSpec).
	Session runtime.SessionSpec
	// Git runs the worktree repair and the dirty check. The zero value is
	// the real git.
	Git gitx.Git
	// Rename moves one directory. Nil is os.Rename; a test replaces it to
	// stop a run half way.
	Rename func(from, to string) error
	// Now stamps migrate.log. Nil is time.Now.
	Now func() time.Time
}

func (d Deps) rename(from, to string) error {
	if d.Rename != nil {
		return d.Rename(from, to)
	}
	return os.Rename(from, to)
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Stage is how far a move had got when Plan found it.
type Stage int

const (
	// StageNew is a repo still at its old path.
	StageNew Stage = iota
	// StageStaged is a repo named like its project, moved aside to
	// Move.Staging; the project's directory and the rename into it are
	// still to come.
	StageStaged
	// StageRenamed is a repo already at its new path while project.yaml
	// still names the old one.
	StageRenamed
	// StageRecorded is a repo project.yaml already names at its new path,
	// whose worktrees, briefs and finished line in migrate.log may still be
	// to come: only migrate.log says so.
	StageRecorded
)

// Move is one repo to move under its project's directory.
type Move struct {
	Project string
	// Repo is the repo's name in project.yaml.
	Repo string
	// Old and New are the absolute paths before and after.
	Old, New string
	// SameName is a repo at the project's own directory, `<root>/<p>`:
	// it is moved aside first so the directory can be made in its place.
	SameName bool
	// Staging is where a SameName repo was moved aside, from StageStaged on.
	Staging string
	Stage   Stage
	// Dirty is how many uncommitted changes the repo has; they move with
	// it untouched.
	Dirty int
	// Warning is set when the dirty check itself failed: the repo is not
	// known to be clean.
	Warning string
}

// note is what the move's line says after the paths.
func (m Move) note() string {
	switch {
	case m.Stage != StageNew:
		return " (finishes a move that stopped half way)"
	case m.Dirty > 0:
		return fmt.Sprintf(" (%d uncommitted change(s) kept)", m.Dirty)
	}
	return ""
}

func oneLine(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return strings.TrimSpace(msg)
}

// Refusal is one reason a migrate will not start.
type Refusal struct {
	// Project is "" for a reason about the whole workspace.
	Project string
	Reason  string
}

// RefusedError is a migrate that moved nothing: its first line says so,
// and every reason follows on a line of its own.
type RefusedError struct {
	Refusals []Refusal
}

func (e *RefusedError) Error() string {
	lines := []string{"nothing moved"}
	for _, r := range e.Refusals {
		lines = append(lines, r.Reason)
	}
	return strings.Join(lines, "\n")
}

// Summary is what one run did, or for DryRun would do.
type Summary struct {
	Moves []Move
	// Repaired is how many closed crews' worktrees were re-attached.
	Repaired int
	// Rewrote is how many briefs had a repo path rewritten.
	Rewrote int
	// Warnings are worktrees that could not be re-attached. They do not
	// stop a migrate: the worktree is a closed crew's, and `git worktree
	// repair` can be run on it later.
	Warnings []string
	// Layout is true once workspace.yaml says layout 2.
	Layout bool
}

const (
	lockHeld = "another mate migrate is running on this workspace"
	// livenessBound caps the question to Herdr, so a wedged server makes
	// a migrate refuse rather than hang.
	livenessBound = 5 * time.Second
	// stagingInfix is in the name a repo named like its project is moved
	// aside to: `<root>/<p>.migrating-<nonce>`.
	stagingInfix = ".migrating-"
)

// Plan finds every repo to move and every reason not to move any. It
// changes nothing.
func Plan(ctx context.Context, ws *store.Workspace, deps Deps) ([]Move, []Refusal, error) {
	if err := ws.LoadConfig(); err != nil {
		return nil, nil, err
	}
	journal, err := ws.ReadMigrateLog()
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", ws.MigrateLog(), err)
	}
	lctx, cancel := context.WithTimeout(ctx, livenessBound)
	live := query.ReadLiveness(lctx, deps.Runtime, deps.Session)
	cancel()
	snap, err := query.LoadLive(ctx, ws, query.Harnesses{}, nil, live)
	if err != nil {
		return nil, nil, err
	}
	nodes := map[string]query.ProjectNode{}
	for _, p := range snap.Projects {
		nodes[p.ProjectID] = p
	}

	var refusals []Refusal
	configs := map[string]store.ProjectConfig{}
	owners := map[string]string{} // absolute repo path -> project
	var registered []repoRef
	for _, ref := range ws.Projects() {
		cfg, err := ws.LoadProject(ref.Name)
		if err != nil {
			refusals = append(refusals, Refusal{Project: ref.Name, Reason: fmt.Sprintf("project %s: project.yaml cannot be read: %v", ref.Name, err)})
			continue
		}
		configs[ref.Name] = cfg
		for _, r := range cfg.Repos {
			owners[ws.RepoDir(r.Path)] = ref.Name
			registered = append(registered, repoRef{project: ref.Name, name: r.Name, path: ws.RepoDir(r.Path)})
		}
	}
	var moves []Move
	for _, ref := range ws.Projects() {
		cfg, ok := configs[ref.Name]
		if !ok {
			continue
		}
		pl := planner{ctx: ctx, ws: ws, deps: deps, project: ref.Name, journal: journal, owners: owners, live: live}
		pm := pl.plan(cfg, nodes[ref.Name])
		moves = append(moves, pm...)
		refusals = append(refusals, pl.refusals...)
	}
	refusals = append(refusals, nested(moves, registered)...)
	if len(moves) > 0 && !ws.LayoutOld() {
		refusals = append(refusals, Refusal{Reason: "workspace.yaml already says layout 2 but a project.yaml names a repo outside its project's directory; fix that project.yaml by hand"})
	}
	return moves, refusals, nil
}

// repoRef is one registered repo of any project.
type repoRef struct{ project, name, path string }

// nested refuses every registered repo that lies inside a repo still at the
// old path it would move from: the rename would carry it along, leaving its
// project.yaml naming a path that no longer exists, or stranding its own
// move half way. The old layout never forbade nesting, so only a hand move
// can say where such a repo belongs.
func nested(moves []Move, registered []repoRef) []Refusal {
	var out []Refusal
	for _, m := range moves {
		if m.Stage != StageNew {
			continue
		}
		for _, r := range registered {
			if strings.HasPrefix(r.path, m.Old+string(filepath.Separator)) {
				out = append(out, Refusal{Project: r.project, Reason: fmt.Sprintf(
					"repo %s of project %s at %s is inside repo %s of project %s at %s, which would move; move or unregister the inner repo by hand first",
					r.name, r.project, r.path, m.Repo, m.Project, m.Old)})
			}
		}
	}
	return out
}

// planner plans one project.
type planner struct {
	ctx      context.Context
	ws       *store.Workspace
	deps     Deps
	project  string
	journal  []store.MigrateEntry
	owners   map[string]string
	live     query.Liveness
	refusals []Refusal
}

func (pl *planner) refuse(format string, args ...any) {
	pl.refusals = append(pl.refusals, Refusal{Project: pl.project, Reason: fmt.Sprintf(format, args...)})
}

func (pl *planner) plan(cfg store.ProjectConfig, node query.ProjectNode) []Move {
	p, home := pl.project, pl.ws.ProjectHome(pl.project)
	var moves []Move
	for _, r := range cfg.Repos {
		at := pl.ws.RepoDir(r.Path)
		if strings.HasPrefix(r.Path, p+"/") {
			if old, ok := unfinished(pl.journal, p, at); ok {
				moves = append(moves, Move{Project: p, Repo: r.Name, Old: old, New: at, SameName: old == home, Stage: StageRecorded})
			}
			continue
		}
		m := Move{Project: p, Repo: r.Name, Old: at, New: filepath.Join(home, filepath.Base(at)), SameName: at == home}
		if reason := pl.locate(&m); reason != "" {
			pl.refuse("%s", reason)
			continue
		}
		if m.Stage == StageNew {
			if n, err := pl.deps.Git.IsDirty(pl.ctx, m.Old); err == nil {
				m.Dirty = n
			} else {
				m.Warning = fmt.Sprintf("could not tell whether %s has uncommitted changes (%v); it moves as it is", m.Old, oneLine(err))
			}
		}
		moves = append(moves, m)
	}
	if len(moves) == 0 {
		return nil
	}
	// The repo at the project's own directory goes first: until it has
	// moved aside, that directory is the repo, and every other repo would
	// be moved into it.
	sort.SliceStable(moves, func(i, j int) bool { return moves[i].SameName && !moves[j].SameName })
	vacates := moves[0].SameName && moves[0].Stage == StageNew

	if fi, err := os.Stat(home); err == nil {
		switch {
		case !fi.IsDir():
			pl.refuse("%s, the directory project %s's repos move under, is a file", home, p)
		case pl.owners[home] != "" && pl.owners[home] != p:
			pl.refuse("%s, the directory project %s's repos move under, is a repo of project %s; move that repo by hand first", home, p, pl.owners[home])
		case gitx.HasGitDir(home) && !vacates:
			pl.refuse("%s, the directory project %s's repos move under, is a git repository %s does not register", home, p, p)
		}
	} else if !os.IsNotExist(err) {
		pl.refuse("%s: %v", home, err)
	}

	seen := map[string]string{}
	for _, m := range moves {
		if other, ok := seen[m.New]; ok {
			pl.refuse("repos %s and %s of project %s would both move to %s", other, m.Repo, p, m.New)
			continue
		}
		seen[m.New] = m.Repo
		// While the repo named like the project is still at the project's
		// directory, every destination is inside that repo, which moves
		// away with all it holds first.
		if m.Stage != StageNew || vacates {
			continue
		}
		if _, err := os.Lstat(m.New); err == nil {
			pl.refuse("%s already exists; repo %s of project %s cannot move there", m.New, m.Repo, p)
		} else if !os.IsNotExist(err) {
			pl.refuse("%s: %v", m.New, err)
		}
	}

	moving := map[string]bool{}
	for _, m := range moves {
		moving[m.Repo] = true
	}
	// Open crews are the snapshot's rows: their state comes from meta,
	// status and incidents exactly as the console reads it.
	for _, c := range node.Crews {
		switch {
		case c.RepoID == "":
			pl.refuse("crew %s/%s is %s and which repo it works in cannot be told; close it with mate crew stop before migrating", p, c.CrewID, c.Status)
		case moving[c.RepoID]:
			pl.refuse("crew %s/%s is %s in repo %s, which would move; close it with mate crew stop before migrating", p, c.CrewID, c.Status, c.RepoID)
		}
	}
	switch mate := node.Mate.Designated; {
	case mate.State == query.Unknown:
		pl.refuse("the Mate of %s cannot be read (%s); stop it before migrating", p, mate.Reason)
	case mate.State == query.Known && mate.Value.Status.OccupiesActiveSlot():
		if pl.live.Asked {
			pl.refuse("the Mate of %s is running; stop it with mate mate stop %s before migrating", p, p)
		} else {
			pl.refuse("the Mate of %s may be running: mate.meta records a pane and Herdr could not be asked; stop it with mate mate stop %s before migrating", p, p)
		}
	}
	return moves
}

// locate sets the move's stage from where the repo is on disk, or says why
// it cannot be moved.
func (pl *planner) locate(m *Move) string {
	if m.SameName {
		staged, err := stagings(pl.ws.Root(), filepath.Base(m.Old))
		if err != nil {
			return err.Error()
		}
		switch {
		case gitx.HasGitDir(m.Old):
			if len(staged) > 0 {
				return fmt.Sprintf("%s is still the repo but %s is beside it, left by a migrate that stopped; look at both before running mate migrate again", m.Old, strings.Join(staged, ", "))
			}
			m.Stage = StageNew
		case len(staged) > 1:
			return fmt.Sprintf("repo %s of project %s was moved aside more than once (%s); keep the right one and run mate migrate again", m.Repo, m.Project, strings.Join(staged, ", "))
		case len(staged) == 1:
			if !gitx.HasGitDir(staged[0]) {
				return fmt.Sprintf("%s, where a migrate moved repo %s of project %s aside, is not a git repository", staged[0], m.Repo, m.Project)
			}
			if _, err := os.Lstat(m.New); err == nil {
				return fmt.Sprintf("%s already exists and repo %s of project %s waits at %s to move there", m.New, m.Repo, m.Project, staged[0])
			}
			m.Stage, m.Staging = StageStaged, staged[0]
		case gitx.HasGitDir(m.New):
			m.Stage = StageRenamed
		default:
			return fmt.Sprintf("repo %s of project %s is not at %s or %s", m.Repo, m.Project, m.Old, m.New)
		}
		return ""
	}
	if _, err := os.Lstat(m.Old); err == nil {
		m.Stage = StageNew
		return ""
	} else if !os.IsNotExist(err) {
		return fmt.Sprintf("%s: %v", m.Old, err)
	}
	if gitx.HasGitDir(m.New) {
		m.Stage = StageRenamed
		return ""
	}
	return fmt.Sprintf("repo %s of project %s is not at %s or %s", m.Repo, m.Project, m.Old, m.New)
}

// stagings are the directories `<root>/<name>.migrating-*`.
func stagings(root, name string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), name+stagingInfix) {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out, nil
}

// unfinished is the old path of a repo migrate.log says was renamed to at,
// when no finished line for that move follows.
func unfinished(journal []store.MigrateEntry, project, at string) (string, bool) {
	old := ""
	for _, e := range journal {
		if e.Project != project || e.New != at {
			continue
		}
		switch {
		case e.Step == store.MigrateRenamed:
			old = e.Old
		case e.Done() && e.Old == old:
			old = ""
		}
	}
	return old, old != ""
}

// DryRun prints the moves a Run would make and every reason it would refuse,
// and changes nothing. It does not take the migrate lock; it says when
// another migrate holds it.
func DryRun(ctx context.Context, ws *store.Workspace, deps Deps, out io.Writer) (Summary, error) {
	moves, refusals, err := Plan(ctx, ws, deps)
	if err != nil {
		return Summary{}, err
	}
	if held, err := ws.MigrateLocked(); err != nil {
		return Summary{}, fmt.Errorf("migrate lock: %w", err)
	} else if held {
		refusals = append([]Refusal{{Reason: lockHeld}}, refusals...)
	}
	for _, m := range moves {
		fmt.Fprintf(out, "would move %s -> %s%s\n", m.Old, m.New, m.note())
	}
	for _, m := range moves {
		if m.Warning != "" {
			fmt.Fprintln(out, "warning: "+m.Warning)
		}
	}
	sum := Summary{Moves: moves}
	if len(refusals) > 0 {
		return sum, &RefusedError{Refusals: refusals}
	}
	if len(moves) == 0 && !ws.LayoutOld() {
		fmt.Fprintln(out, "nothing to do")
		return sum, nil
	}
	fmt.Fprintln(out, "would write workspace layout 2")
	return sum, nil
}

// Run moves every repo of the plan under its project's directory, project
// by project, then writes layout 2 into workspace.yaml. It holds
// `.mate/migrate.lock` throughout. A refusal is a *RefusedError and nothing
// was moved; any other error stopped the run half way, and the next run
// finishes it.
func Run(ctx context.Context, ws *store.Workspace, deps Deps, out io.Writer) (Summary, error) {
	unlock, ok, err := ws.LockMigrate()
	if err != nil {
		return Summary{}, fmt.Errorf("migrate lock: %w", err)
	}
	if !ok {
		return Summary{}, &RefusedError{Refusals: []Refusal{{Reason: lockHeld}}}
	}
	defer unlock()

	moves, refusals, err := Plan(ctx, ws, deps)
	if err != nil {
		return Summary{}, err
	}
	if len(refusals) > 0 {
		return Summary{}, &RefusedError{Refusals: refusals}
	}
	var sum Summary
	if len(moves) == 0 && !ws.LayoutOld() {
		fmt.Fprintln(out, "nothing to do")
		return sum, nil
	}
	briefs := map[string]bool{}
	for _, m := range moves {
		if m.Warning != "" {
			sum.Warnings = append(sum.Warnings, m.Warning)
		}
	}
	for _, m := range moves {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		ex := executor{ctx: ctx, ws: ws, deps: deps, briefs: briefs}
		err := ex.move(m)
		sum.Repaired += ex.repaired
		sum.Warnings = append(sum.Warnings, ex.warnings...)
		sum.Rewrote = len(briefs)
		if err != nil {
			return sum, fmt.Errorf("moving %s to %s stopped half way: %w; run mate migrate again to finish", m.Old, m.New, err)
		}
		sum.Moves = append(sum.Moves, m)
		fmt.Fprintf(out, "moved %s -> %s%s\n", m.Old, m.New, m.note())
	}
	fmt.Fprintf(out, "repaired %d worktree(s)\n", sum.Repaired)
	fmt.Fprintf(out, "rewrote %d brief(s)\n", sum.Rewrote)
	for _, w := range sum.Warnings {
		fmt.Fprintln(out, "warning: "+w)
	}
	if err := ws.SetLayoutProjectDirs(); err != nil {
		return sum, err
	}
	sum.Layout = true
	fmt.Fprintln(out, "workspace layout 2")
	return sum, nil
}

// executor carries out one move.
type executor struct {
	ctx      context.Context
	ws       *store.Workspace
	deps     Deps
	briefs   map[string]bool
	repaired int
	warnings []string
}

func (ex *executor) log(m Move, step string) error {
	return ex.ws.AppendMigrateLog(store.MigrateEntry{Time: ex.deps.now(), Project: m.Project, Old: m.Old, New: m.New, Step: step})
}

// move is steps 1 to 6 of the plan for one repo, from the stage Plan found
// it at.
func (ex *executor) move(m Move) error {
	home := ex.ws.ProjectHome(m.Project)
	// 1 and 2: the project's directory and the rename. A repo at the
	// project's own directory is moved aside first, then into the
	// directory made in its place.
	if m.Stage == StageNew && m.SameName {
		staging, err := stagingPath(m.Old)
		if err != nil {
			return err
		}
		if err := ex.rename(m.Old, staging); err != nil {
			return err
		}
		if err := ex.log(m, store.MigrateStaged+" "+staging); err != nil {
			return err
		}
		m.Stage, m.Staging = StageStaged, staging
	}
	switch m.Stage {
	case StageNew, StageStaged:
		from := m.Old
		if m.Stage == StageStaged {
			from = m.Staging
		}
		if err := os.MkdirAll(home, 0o755); err != nil {
			return err
		}
		if err := ex.rename(from, m.New); err != nil {
			return err
		}
		if err := ex.log(m, store.MigrateRenamed); err != nil {
			return err
		}
	case StageRenamed:
		// The rename happened and its line may not have been written;
		// writing it now is what lets a crash after the next step be
		// finished too.
		if err := ex.log(m, store.MigrateRenamed); err != nil {
			return err
		}
	}
	closed, err := closedCrews(ex.ws, m.Project)
	if err != nil {
		return err
	}
	// 3. The closed crews' worktrees, whose links name the old path.
	ex.repairWorktrees(m, closed)
	// 4. project.yaml.
	if err := recordPath(ex.ws, m); err != nil {
		return err
	}
	// 5. The briefs of the closed crews.
	for _, c := range closed {
		if err := ex.rewriteBrief(m, c.id); err != nil {
			return err
		}
	}
	// 6. The line that says the move is finished.
	return ex.log(m, "")
}

// rename moves from to to, refusing a destination that appeared since Plan
// looked: os.Rename would put a directory over an empty one.
func (ex *executor) rename(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s already exists", to)
	} else if !os.IsNotExist(err) {
		return err
	}
	return ex.deps.rename(from, to)
}

func stagingPath(old string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return old + stagingInfix + hex.EncodeToString(b[:]), nil
}

type crew struct {
	id   string
	meta map[string]string
}

// closedCrews are the project's finished and failed crews.
func closedCrews(ws *store.Workspace, project string) ([]crew, error) {
	ids, err := ws.CrewIDs(project)
	if err != nil {
		return nil, err
	}
	var out []crew
	for _, id := range ids {
		meta, err := ws.ReadCrewMeta(project, id)
		if err != nil || !query.CrewStateOf(meta, false, "").Closed() {
			continue
		}
		out = append(out, crew{id: id, meta: meta})
	}
	return out, nil
}

// repairWorktrees re-attaches the worktree of every closed crew of the
// moved repo that is still on disk, as recovery.RepairWorktrees does after a
// workspace moved.
func (ex *executor) repairWorktrees(m Move, closed []crew) {
	cfg, err := ex.ws.LoadProject(m.Project)
	if err != nil {
		ex.warnings = append(ex.warnings, fmt.Sprintf("the worktrees of repo %s of project %s were not checked: %v", m.Repo, m.Project, err))
		return
	}
	for _, c := range closed {
		r, err := cfg.CrewRepo(c.meta)
		if err != nil || r.Name != m.Repo {
			continue
		}
		wt := ex.ws.WorktreeDir(m.Project, c.id)
		if rel := c.meta["worktree"]; rel != "" {
			wt = filepath.Join(ex.ws.Root(), filepath.FromSlash(rel))
		}
		if _, err := ex.ws.Resolve(wt); err != nil {
			// A meta naming a worktree outside the workspace is not
			// followed there.
			ex.warnings = append(ex.warnings, fmt.Sprintf("worktree of crew %s/%s was not repaired: %v", m.Project, c.id, err))
			continue
		}
		if _, err := os.Stat(wt); err != nil {
			continue
		}
		if ok, err := ex.deps.Git.WorktreeAttached(ex.ctx, wt); err == nil && ok {
			continue
		}
		if err := ex.deps.Git.RepairWorktree(ex.ctx, m.New, wt); err != nil {
			ex.warnings = append(ex.warnings, fmt.Sprintf("worktree %s of crew %s/%s could not be re-attached: %v", wt, m.Project, c.id, err))
			continue
		}
		if ok, err := ex.deps.Git.WorktreeAttached(ex.ctx, wt); err != nil || !ok {
			ex.warnings = append(ex.warnings, fmt.Sprintf("worktree %s of crew %s/%s is still not attached after git worktree repair", wt, m.Project, c.id))
			continue
		}
		ex.repaired++
	}
}

// recordPath writes the repo's new path into project.yaml.
func recordPath(ws *store.Workspace, m Move) error {
	cfg, err := ws.LoadProject(m.Project)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(ws.Root(), m.New)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	found := false
	for i := range cfg.Repos {
		if cfg.Repos[i].Name != m.Repo {
			continue
		}
		found = true
		if cfg.Repos[i].Path == rel {
			return nil
		}
		cfg.Repos[i].Path = rel
	}
	if !found {
		return errors.New("project.yaml of " + m.Project + " no longer has repo " + m.Repo)
	}
	return ws.SaveProject(m.Project, cfg)
}

// rewriteBrief swaps the repo's old path for its new one in a closed crew's
// brief. A brief rewritten before a crash is left as it is: the swap
// leaves a path that already reads new alone.
func (ex *executor) rewriteBrief(m Move, id string) error {
	path := ex.ws.CrewBrief(m.Project, id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	next := recovery.ReplacePrefix(string(data), m.Old, m.New)
	if next == string(data) {
		return nil
	}
	if err := ex.ws.WriteCrewBrief(m.Project, id, []byte(next)); err != nil {
		return err
	}
	ex.briefs[path] = true
	return nil
}
