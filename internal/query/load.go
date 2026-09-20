package query

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/box"
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
	// The mode is the presence of `mate/.auto` and nothing else: there is no
	// separate record of it, so a project whose flag file cannot be read at
	// all reads as manual, which is the mode that sends nothing.
	p := ProjectNode{ProjectID: ref.Name, Name: ref.Name, Mode: ModeFor(ws.Auto(ref.Name))}

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
	// The box is read before the Crews because a Crew's state depends on
	// it: `blocked` is an open incident in the merged view and nothing
	// else (mvp.md section 4b).
	view, viewOK, boxField := loadBox(ws, ref.Name)
	p.Box = boxField
	if viewOK {
		var mateLines []box.Entry
		for _, e := range view.Entries {
			// The Mate's own activity is every line typed into its pane
			// (target `mate`, by the captain or the app) and every line it
			// sent to a crew.
			if e.Kind == box.KindMessage && (e.Crew == "" || e.Source == box.SourceMate) {
				mateLines = append(mateLines, e)
			}
		}
		p.Mate.LastEvent = lastActivity(mateLines, "nothing has been typed into the Mate's pane yet")
	}
	p.Crews, p.ClosedCrews = loadCrews(ws, ref.Name, p.Repos, view, viewOK, w)
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
		Tokens:    AbsentField[TokenValue](noTimeline),
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
		Tokens:     AbsentField[TokenValue](why),
	}
}

func unknownMate(reason string, w *warnings, row RowRef) MateNode {
	return MateNode{
		Designated: note(w, UnknownField[MateIdentity](reason), "mate", row),
		AgentName:  UnknownField[string](reason),
		Binding:    UnknownField[BindingValue](reason),
		LastEvent:  UnknownField[EventValue](reason),
		Error:      UnknownField[ErrorReason](reason),
		Tokens:     AbsentField[TokenValue](noTimeline),
	}
}

const notAnErrorState = "the recorded status is not an error state"

// noObserver is why a Crew row carries no health: nothing has observed it.
// The observer lives in the console process (mvp.md section 4b), so a
// one-shot CLI read never has one, and a console that has just opened has
// not polled yet.
const noObserver = "no observer has looked at this crew yet"

// noTimeline is why a Crew or Mate row carries no token usage: this
// package reads only `.matev2/`'s flat files, and the ledger lives in the
// derived `.matev2/matev2.db` (mvp.md M5 task 27). A one-shot CLI read that
// never opens that database, or a console that has not read it yet, keeps
// this reason rather than a zero total that would render as "no tokens
// spent".
const noTimeline = "no timeline database has been read for this row yet"

// loadCrews lists `crews/*.meta` and reads each one, resolving the Crew's
// state through CrewStateOf - the one ordering of mvp.md section 4b.
//
// Closed Crews - `state=finished|failed` in the meta, which only
// `matev2 crew stop` and a failed spawn write - are counted and dropped:
// the tree is the list of work in flight, and closing is the decision that
// ends a task (ProjectNode.Crews). A Crew whose meta could not be read is
// kept, as a row whose fields say they could not be read, because an
// unreadable record is not evidence of a closed one.
func loadCrews(ws *store.Workspace, project string, repos Field[[]RepoValue], view box.View, viewOK bool, w *warnings) ([]CrewNode, int) {
	ids, err := crewIDs(ws.CrewsDir(project))
	if err != nil {
		// A project whose crews directory cannot be listed gets no Crew
		// rows and one warning naming the failure - never an empty list
		// presented as "this project has no crews".
		note(w, UnknownField[[]CrewNode](readFailureReason(err)), "crews",
			RowRef{Kind: RowProject, ID: project, Label: project})
		return nil, 0
	}
	out := make([]CrewNode, 0, len(ids))
	closed := 0
	for _, id := range ids {
		c := loadCrew(ws, project, id, repos, view, viewOK, w)
		if c.Closed {
			closed++
			continue
		}
		out = append(out, c)
	}
	return out, closed
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

func loadCrew(ws *store.Workspace, project, id string, repos Field[[]RepoValue], view box.View, viewOK bool, w *warnings) CrewNode {
	row := RowRef{Kind: RowCrew, ID: id, Label: "crew " + id}
	c := CrewNode{
		CrewID:    id,
		ProjectID: project,
		RepoID:    project,
		Repo:      repoFor(repos, project),
		LastEvent: AbsentField[EventValue]("matev2 records no event log yet"),
		Error:     AbsentField[ErrorReason](notAnErrorState),
		// This package reads files; an observation comes from Herdr. A
		// snapshot loaded without an observer running therefore carries no
		// health, and says so - the Console's wiring fills this in from
		// internal/watch when one is running (CrewNode.Health).
		Health: AbsentField[CrewHealth](noObserver),
		Tokens: AbsentField[TokenValue](noTimeline),
	}
	if viewOK {
		c.LastEvent = lastActivity(view.ByCrew[id], "no status line or message for this crew yet")
	}

	meta, err := ws.ReadCrewMeta(project, id)
	if err != nil {
		// An unreadable meta says nothing about the crew's state. It stays
		// at `spawned` - the state of a crew nothing is recorded about -
		// and the warning below is what tells the reader the record itself
		// could not be read.
		reason := readFailureReason(err)
		c.Status = CrewSpawned
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

	openIncident := viewOK && len(box.BlockingIncidents(view, id)) > 0
	c.Status = CrewStateOf(meta, openIncident, lastStatusVerb(ws, project, id, w, row))
	c.Closed = c.Status.Closed()
	return c
}

// lastStatusVerb is the verb of the crew's most recent recognised status
// line, "" when it has written none. It is one of the three inputs
// CrewStateOf resolves a state from; box owns the parsing, including the
// legacy verbs a status file written before 2026-09-18 may still carry.
func lastStatusVerb(ws *store.Workspace, project, id string, w *warnings, row RowRef) string {
	entries, _, err := ws.ReadStatus(project, id, 0)
	if err != nil {
		note(w, UnknownField[CrewStatus](readFailureReason(err)), "status", row)
		return ""
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, e.Line)
	}
	verb := box.LastVerb(lines)
	if verb == box.StateUnknown {
		return ""
	}
	return string(verb)
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

// lastActivity is the UPDATED column's value: the time of the newest box
// entry among the given ones - a status line (its file's mtime), a message
// in sent.log, or an incident - with the entry's kind as the event type.
// No entries is a legitimate Absent with the caller's reason, never a
// zero time rendered as midnight.
func lastActivity(entries []box.Entry, whyAbsent string) Field[EventValue] {
	if len(entries) == 0 {
		return AbsentField[EventValue](whyAbsent)
	}
	last := entries[0]
	for _, e := range entries[1:] {
		if !e.At.Before(last.At) {
			last = e
		}
	}
	return KnownField(EventValue{EventType: string(last.Kind), OccurredAt: last.At})
}
