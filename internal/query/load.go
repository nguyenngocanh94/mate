package query

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

// Load reads one Snapshot out of `.matev2/`. It is the only file in this
// package that touches the filesystem, and it is the one implementation of
// the Console's LoadFunc.
//
// Everything it reports is recorded state. It never calls Herdr: a Mate
// whose `mate.meta` records a pane is reported as running because that is
// what the file says, not because anything checked the pane is alive - the
// caveat travels on the field's own Reason, the way every other
// unverifiable fact in this package does (mvp.md section 2, decision 8).
//
// Reads that fail degrade to an Unknown field rather than failing the whole
// snapshot: one unreadable project.yaml must not blank the workspace. Only
// a failure to list the projects at all is returned as an error, because
// after that there is no tree to show.
func Load(ctx context.Context, ws *store.Workspace) (Snapshot, error) {
	return load(ctx, ws, time.Now)
}

func load(ctx context.Context, ws *store.Workspace, now func() time.Time) (Snapshot, error) {
	if ws == nil {
		return Snapshot{}, fmt.Errorf("query: no workspace is open")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	// Re-read workspace.yaml on every load: the Console's 'r' is meant to
	// pick up a project another process registered, and a cached config
	// would quietly answer with the tree as it was when the binary started.
	if err := ws.LoadConfig(); err != nil {
		return Snapshot{}, err
	}

	root := ws.Root()
	var w warnings
	snap := Snapshot{
		// The workspace has no id of its own (mvp.md section 3): the
		// directory is the only name there is, so the breadcrumb shows
		// that rather than the derived Herdr session name, which names a
		// runtime handle and not this workspace.
		WorkspaceID: filepath.Base(root),
		Workspace: KnownField(WorkspaceValue{
			Name: filepath.Base(root),
			Root: root,
		}),
	}
	for _, ref := range ws.Projects() {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		snap.Projects = append(snap.Projects, loadProject(ws, ref, &w))
	}
	snap.AsOf = now().UTC()
	snap.Warnings = w.list
	deriveActions(&snap)
	return snap, nil
}

func loadProject(ws *store.Workspace, ref store.ProjectRef, w *warnings) ProjectNode {
	row := RowRef{Kind: RowProject, ID: ref.Name, Label: ref.Name}
	p := ProjectNode{ProjectID: ref.Name, Name: ref.Name}

	cfg, err := ws.LoadProject(ref.Name)
	switch {
	case err != nil:
		p.Repos = note(w, UnknownField[[]RepoValue](readFailureReason(err)), "repos", row)
	default:
		p.Repos = KnownField([]RepoValue{{
			RepoID:        ref.Name,
			DisplayName:   filepath.Base(cfg.Repo),
			Path:          filepath.Join(ws.Root(), cfg.Repo),
			DefaultBranch: cfg.DefaultBranch,
		}})
	}

	p.Mate = loadMate(ws, ref.Name, w)
	p.Crews = loadCrews(ws, ref.Name, p.Repos, w)
	for i := range p.Crews {
		p.Crews[i].Attention = crewAttention(p.Crews[i])
	}
	p.Attention = projectAttention(p)
	return p
}

// loadMate reads `mate/mate.meta`. A Mate row exists exactly when that file
// does: mvp.md has no separate designation record, so the file's presence is
// the designation.
//
// The status is derived from the keys the file carries and nothing else. A
// recorded pane means the Mate was started; the caveat that this does not
// prove the agent is alive rides on the Binding field, which is where the
// Console already renders it.
func loadMate(ws *store.Workspace, project string, w *warnings) MateNode {
	row := RowRef{Kind: RowMate, ID: project, Label: project}
	path := ws.MateMeta(project)

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return absentMate("this project has no Mate yet; press s to create one")
		}
		return unknownMate(readFailureReason(err), w, row)
	}
	meta, err := ws.ReadMateMeta(project)
	if err != nil {
		return unknownMate(readFailureReason(err), w, row)
	}

	kind, kindErr := ParseHarnessKind(meta["harness"])
	if kindErr != nil {
		kind = HarnessKind(meta["harness"])
	}
	pane := meta["pane"]
	status := MateCreated
	if pane != "" {
		status = MateRunning
	}

	out := MateNode{
		Designated: KnownField(MateIdentity{
			MateID:      "mate:" + project,
			HarnessKind: kind,
			Status:      status,
			IsDefault:   true,
		}),
		LastEvent: AbsentField[EventValue]("matev2 records no event log yet"),
		Error:     AbsentField[ErrorReason](notAnErrorState),
	}
	if agent := meta["agent"]; agent != "" {
		out.AgentName = KnownField(agent)
	} else {
		out.AgentName = AbsentField[string]("mate.meta records no agent name")
	}
	if pane == "" {
		out.Binding = AbsentField[BindingValue]("mate.meta records no pane; the Mate has not been started")
		return out
	}
	out.Binding = KnownNote(BindingValue{
		Status:    BindingActive,
		AgentName: out.AgentName.Value,
		Runtime:   "herdr",
		Session:   meta["session"],
		Workspace: meta["workspace"],
		Tab:       meta["tab"],
		Pane:      pane,
	}, "recorded in mate.meta; this does not prove the agent is alive")
	return out
}

// absentMate and unknownMate carry the designation's own state forward onto
// every dependent field, so no field is ever left at the unset zero
// FieldState.
func absentMate(why string) MateNode {
	return MateNode{
		Designated: AbsentField[MateIdentity](why),
		AgentName:  AbsentField[string](why),
		Binding:    AbsentField[BindingValue](why),
		LastEvent:  AbsentField[EventValue](why),
		Error:      AbsentField[ErrorReason](notAnErrorState),
	}
}

func unknownMate(reason string, w *warnings, row RowRef) MateNode {
	return MateNode{
		Designated: note(w, UnknownField[MateIdentity](reason), "mate", row),
		AgentName:  UnknownField[string](reason),
		Binding:    UnknownField[BindingValue](reason),
		LastEvent:  UnknownField[EventValue](reason),
		Error:      UnknownField[ErrorReason](reason),
	}
}

const notAnErrorState = "the recorded status is not an error state"

// loadCrews lists `crews/*.meta` and reads each one, with the last line of
// `crews/<id>.status` as the Crew's status text. The five states of mvp.md
// section 4 are what a crew actually writes; CrewStatus is a string type
// precisely so an unrecognised word renders as itself rather than as a
// blank cell.
func loadCrews(ws *store.Workspace, project string, repos Field[[]RepoValue], w *warnings) []CrewNode {
	ids, err := crewIDs(ws.CrewsDir(project))
	if err != nil {
		// A project whose crews directory cannot be listed gets no Crew
		// rows and one warning naming the failure - never an empty list
		// presented as "this project has no crews".
		note(w, UnknownField[[]CrewNode](readFailureReason(err)), "crews",
			RowRef{Kind: RowProject, ID: project, Label: project})
		return nil
	}
	out := make([]CrewNode, 0, len(ids))
	for _, id := range ids {
		out = append(out, loadCrew(ws, project, id, repos, w))
	}
	return out
}

func crewIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".meta")
		if store.ValidateCrewID(id) != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func loadCrew(ws *store.Workspace, project, id string, repos Field[[]RepoValue], w *warnings) CrewNode {
	row := RowRef{Kind: RowCrew, ID: id, Label: "crew " + id}
	c := CrewNode{
		CrewID:    id,
		ProjectID: project,
		RepoID:    project,
		Repo:      repoFor(repos, project),
		LastEvent: AbsentField[EventValue]("matev2 records no event log yet"),
		Error:     AbsentField[ErrorReason](notAnErrorState),
	}

	meta, err := ws.ReadCrewMeta(project, id)
	if err != nil {
		reason := readFailureReason(err)
		c.Status = CrewStatus("unknown")
		c.AgentName = UnknownField[string](reason)
		c.Binding = note(w, UnknownField[BindingValue](reason), "binding", row)
		c.Worktree = note(w, UnknownField[WorktreeValue](reason), "worktree", row)
		return c
	}

	c.Task = meta["task"]
	if kind, err := ParseHarnessKind(meta["harness"]); err == nil {
		c.HarnessKind = kind
	} else {
		c.HarnessKind = HarnessKind(meta["harness"])
	}

	if worktree := meta["worktree"]; worktree != "" {
		c.Worktree = KnownField(WorktreeValue{
			Path:   worktree,
			Branch: meta["branch"],
			Status: WorktreeRecordedCreated,
		})
	} else {
		c.Worktree = AbsentField[WorktreeValue]("crews/" + id + ".meta records no worktree")
	}

	pane := meta["pane"]
	if agent := meta["agent"]; agent != "" {
		c.AgentName = KnownField(agent)
	} else {
		c.AgentName = AbsentField[string]("crews/" + id + ".meta records no agent name")
	}
	if pane == "" {
		c.Binding = AbsentField[BindingValue]("crews/" + id + ".meta records no pane")
	} else {
		c.Binding = KnownNote(BindingValue{
			Status:    BindingActive,
			AgentName: c.AgentName.Value,
			Runtime:   "herdr",
			Session:   meta["session"],
			Workspace: meta["workspace"],
			Tab:       meta["tab"],
			Pane:      pane,
		}, "recorded in crews/"+id+".meta; this does not prove the agent is alive")
	}

	c.Status = crewStatus(ws, project, id, w, row)
	return c
}

// crewStatus is the last line of `crews/<id>.status`. A crew that has
// written nothing yet is `reserved`: the meta exists, so the crew was
// recorded, and nothing it wrote says otherwise.
func crewStatus(ws *store.Workspace, project, id string, w *warnings, row RowRef) CrewStatus {
	entries, _, err := ws.ReadStatus(project, id, 0)
	if err != nil {
		note(w, UnknownField[CrewStatus](readFailureReason(err)), "status", row)
		return CrewStatus("unknown")
	}
	for i := len(entries) - 1; i >= 0; i-- {
		line := strings.TrimSpace(entries[i].Line)
		if line == "" {
			continue
		}
		// A status line is `state: one line` (mvp.md section 4); the state
		// is the column word and the rest is the pointer.
		if state, _, ok := strings.Cut(line, ":"); ok {
			line = strings.TrimSpace(state)
		}
		if line != "" {
			return CrewStatus(line)
		}
	}
	return CrewReserved
}

func repoFor(repos Field[[]RepoValue], repoID string) Field[RepoValue] {
	switch repos.State {
	case Known:
		for _, r := range repos.Value {
			if r.RepoID == repoID {
				return KnownField(r)
			}
		}
		return AbsentField[RepoValue]("no repo named " + repoID + " is registered in this project")
	case Absent:
		return AbsentField[RepoValue](repos.Reason)
	default:
		return UnknownField[RepoValue](repos.Reason)
	}
}
