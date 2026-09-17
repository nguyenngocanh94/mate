package query

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/nguyenngocanh94/matev2/internal/application"
	"github.com/nguyenngocanh94/matev2/internal/config"
	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/persistence"
)

// LoadSnapshot reads the whole Console navigation tree - Workspace ->
// Project -> {Mate, Task -> Crew} - in one pass, and stamps it with the
// moment the read finished.
//
// A row whose own fields fail to read partially (worktree, binding, last
// event, repo list) degrades that field to Unknown with the store's reason
// and adds it to Snapshot.Warnings, rather than failing the whole tree; a
// failure to list the rows themselves (ListProjects/ListTasks/ListCrews)
// still fails the call, since there is no partial list to render.
func LoadSnapshot(ctx context.Context, deps application.Deps) (Snapshot, error) {
	ws, err := deps.Store.GetWorkspace(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	projects, err := application.ListProjects(ctx, deps)
	if err != nil {
		return Snapshot{}, err
	}
	// ListProjects has now run deps.check(), so Clock and Store are wired.
	w := &warnings{}
	snap := Snapshot{
		WorkspaceID: ws.WorkspaceID,
		Workspace:   loadWorkspace(w, deps, ws.WorkspaceID),
		Projects:    make([]ProjectNode, 0, len(projects)),
	}
	merges := loadOpenMerges(ctx, deps)
	for _, p := range projects {
		node, err := loadProjectNode(ctx, deps, w, p, merges)
		if err != nil {
			return Snapshot{}, err
		}
		snap.Projects = append(snap.Projects, node)
	}
	deriveActions(&snap)
	snap.Warnings = w.list
	snap.AsOf = deps.Clock.Now()
	return snap, nil
}

// loadWorkspace reports where the open workspace lives. The root comes from
// the store's own resolved location (never a caller-supplied path, ADR
// 0015) and the database path is derived from it by the same constructor
// every other command uses, so the Console cannot show a database file that
// is not the one it read.
func loadWorkspace(w *warnings, deps application.Deps, workspaceID string) Field[WorkspaceValue] {
	row := RowRef{Kind: RowWorkspace, ID: workspaceID, Label: "workspace"}
	root, err := deps.Store.WorkspaceRoot()
	if err != nil {
		return note(w, UnknownField[WorkspaceValue](readFailureReason(err)), "workspace root", row)
	}
	paths, err := config.NewPaths(root.String())
	if err != nil {
		return note(w, UnknownField[WorkspaceValue](readFailureReason(err)), "workspace root", row)
	}
	return KnownField(WorkspaceValue{
		Name:         filepath.Base(paths.Root),
		Root:         paths.Root,
		DatabasePath: paths.DBFile,
	})
}

func loadProjectNode(ctx context.Context, deps application.Deps, w *warnings, p application.ProjectSummary, merges openMergeIndex) (ProjectNode, error) {
	node := ProjectNode{ProjectID: p.ProjectID, Name: p.Name}
	row := RowRef{Kind: RowProject, ID: p.ProjectID, Label: p.Name}
	node.Mate = loadMateNode(ctx, deps, w, p, row)
	node.Repos = loadRepos(ctx, deps, w, p.ProjectID, row)
	tasks, err := application.ListTasks(ctx, deps, p.ProjectID)
	if err != nil {
		return ProjectNode{}, err
	}
	node.Tasks = make([]TaskNode, 0, len(tasks))
	for _, t := range tasks {
		tn, err := loadTaskNode(ctx, deps, w, node, t, merges)
		if err != nil {
			return ProjectNode{}, err
		}
		node.Tasks = append(node.Tasks, tn)
	}
	node.Attention = projectAttention(node)
	return node, nil
}

// openMergeIndex is the one ListMergeRequests result for the whole
// snapshot. A failed list makes every Crew's OpenMerge Unknown rather than
// inventing "no open request" from a read that did not succeed.
type openMergeIndex struct {
	byCrew map[string]OpenMergeValue
	err    error
}

func loadOpenMerges(ctx context.Context, deps application.Deps) openMergeIndex {
	requests, err := deps.Store.ListMergeRequests(ctx, "")
	if err != nil {
		return openMergeIndex{err: err}
	}
	out := make(map[string]OpenMergeValue)
	for _, r := range requests {
		if r.Status.IsOpen() {
			out[r.CrewID] = OpenMergeValue{RequestID: r.RequestID, Status: r.Status}
		}
	}
	return openMergeIndex{byCrew: out}
}

func loadRepos(ctx context.Context, deps application.Deps, w *warnings, projectID string, row RowRef) Field[[]RepoValue] {
	repos, err := application.ListRepos(ctx, deps, projectID)
	if err != nil {
		return note(w, UnknownField[[]RepoValue](readFailureReason(err)), "repos", row)
	}
	if len(repos) == 0 {
		return AbsentField[[]RepoValue]("no repo is registered in this project")
	}
	out := make([]RepoValue, 0, len(repos))
	for _, r := range repos {
		out = append(out, RepoValue{
			RepoID: r.RepoID, DisplayName: r.DisplayName,
			Path: r.Path, DefaultBranch: r.DefaultBranch,
		})
	}
	return KnownField(out)
}

// resolveRepo picks one row's repo out of its Project's already-read repo
// list. A repo id that is not in the list is Absent, not Unknown: the list
// read succeeded, so "this id is not registered here" is a fact the read
// established - a recorded inconsistency, not a failure to look.
func resolveRepo(repos Field[[]RepoValue], repoID, ownerKind string) Field[RepoValue] {
	if repos.State == Unknown {
		return UnknownField[RepoValue](repos.Reason)
	}
	if repoID == "" {
		return AbsentField[RepoValue]("no repo is recorded for this " + ownerKind)
	}
	if repos.State == Known {
		for _, r := range repos.Value {
			if r.RepoID == repoID {
				return KnownField(r)
			}
		}
	}
	return AbsentField[RepoValue](fmt.Sprintf("repo %s is not registered in this project", repoID))
}

func loadMateNode(ctx context.Context, deps application.Deps, w *warnings, p application.ProjectSummary, projectRow RowRef) MateNode {
	mate, err := application.DesignatedMate(ctx, deps.Store, p.ProjectID)
	switch {
	case errors.Is(err, persistence.ErrNotFound):
		return mateWithout(AbsentField[MateIdentity]("this project has no designated Mate"))
	case err != nil:
		// Without a Mate id there is no Mate row to hang a warning on, so
		// these are reported against the Project and their field names say
		// which of the Project's fields each one is.
		reason := readFailureReason(err)
		node := mateWithout(UnknownField[MateIdentity](reason))
		node.Designated = note(w, node.Designated, "mate", projectRow)
		node.AgentName = note(w, node.AgentName, "mate agent name", projectRow)
		node.Binding = note(w, node.Binding, "mate binding", projectRow)
		node.LastEvent = note(w, node.LastEvent, "mate last event", projectRow)
		node.Error = note(w, node.Error, "mate error reason", projectRow)
		return node
	}
	node := MateNode{Designated: KnownField(MateIdentity{
		MateID: mate.MateID, HarnessKind: mate.HarnessKind,
		Status: mate.Status, IsDefault: mate.IsDefault,
	})}
	row := RowRef{Kind: RowMate, ID: mate.MateID, Label: "mate of " + p.Name}
	name, binding := loadAgent(ctx, deps, mate.MateID)
	node.AgentName = note(w, name, "agent name", row)
	node.Binding = note(w, binding, "binding", row)
	// A Mate's own events are not addressable: the events table has a
	// project_id but no mate_id, so what a Mate row can honestly show is its
	// Project's last recorded event. The design's own sample agrees - it
	// renders crew.started under a Mate's "Last event".
	_, evField := lastEvent(ctx, deps, persistence.EventFilter{ProjectID: p.ProjectID}, "project")
	node.LastEvent = note(w, evField, "last event", row)
	node.Error = note(w, mateErrorRef(ctx, deps, mate), "error reason", row)
	return node
}

// mateWithout carries an Absent or Unknown designation forward into every
// dependent field with the same reason, so a caller never meets a field
// left at the unset zero FieldState. Unknown must not decay into Absent on
// the way down: "the project has no Mate" and "the project's Mate could not
// be read" are different sentences all the way to the leaves.
func mateWithout(designated Field[MateIdentity]) MateNode {
	carried := func(what string) string {
		if designated.State == Unknown {
			return "the project's Mate could not be read, so its " + what + " could not be either: " + designated.Reason
		}
		return "the project has no designated Mate, so it has no " + what
	}
	derive := func(what string) Field[string] {
		if designated.State == Unknown {
			return UnknownField[string](carried(what))
		}
		return AbsentField[string](carried(what))
	}
	node := MateNode{Designated: designated}
	node.AgentName = derive("agent name")
	if designated.State == Unknown {
		node.Binding = UnknownField[BindingValue](carried("runtime binding"))
		node.LastEvent = UnknownField[EventValue](carried("last event"))
		node.Error = UnknownField[ErrorReason](carried("error reason"))
		return node
	}
	node.Binding = AbsentField[BindingValue](carried("runtime binding"))
	node.LastEvent = AbsentField[EventValue](carried("last event"))
	node.Error = AbsentField[ErrorReason](carried("error reason"))
	return node
}

func loadTaskNode(ctx context.Context, deps application.Deps, w *warnings, p ProjectNode, t application.TaskSummary, merges openMergeIndex) (TaskNode, error) {
	node := TaskNode{
		TaskID: t.TaskID, ProjectID: t.ProjectID, RepoID: t.RepoID,
		Title: t.Title, Brief: t.Brief, Status: t.Status, CreatedAt: t.CreatedAt,
	}
	row := RowRef{Kind: RowTask, ID: t.TaskID, Label: t.Title}
	node.Repo = note(w, resolveRepo(p.Repos, t.RepoID, "task"), "repo", row)
	ev, evField := lastEvent(ctx, deps, persistence.EventFilter{TaskID: t.TaskID}, "task")
	node.LastEvent = note(w, evField, "last event", row)
	node.Error = note(w, taskErrorRef(t.Status, ev, evField), "error reason", row)
	crews, err := application.ListCrews(ctx, deps, t.TaskID)
	if err != nil {
		return TaskNode{}, err
	}
	node.Crews = make([]CrewNode, 0, len(crews))
	for _, c := range crews {
		node.Crews = append(node.Crews, loadCrewNode(ctx, deps, w, p, crews, c, merges))
	}
	node.Attention = taskAttention(node, p.Mate)
	return node, nil
}

func loadCrewNode(ctx context.Context, deps application.Deps, w *warnings, p ProjectNode, siblings []application.CrewSummary, c application.CrewSummary, merges openMergeIndex) CrewNode {
	node := CrewNode{
		CrewID: c.CrewID, TaskID: c.TaskID, ProjectID: c.ProjectID, RepoID: c.RepoID,
		Attempt: c.Attempt, HarnessKind: c.HarnessKind, Status: c.Status, CreatedAt: c.CreatedAt,
		RetryOf: retryOf(c, siblings),
	}
	row := RowRef{Kind: RowCrew, ID: c.CrewID, Label: fmt.Sprintf("attempt %d", c.Attempt)}
	node.Repo = note(w, resolveRepo(p.Repos, c.RepoID, "attempt"), "repo", row)
	node.Worktree = note(w, loadWorktree(ctx, deps, c.CrewID), "worktree", row)
	name, binding := loadAgent(ctx, deps, c.CrewID)
	node.AgentName = note(w, name, "agent name", row)
	node.Binding = note(w, binding, "binding", row)
	ev, evField := lastEvent(ctx, deps, persistence.EventFilter{CrewID: c.CrewID}, "attempt")
	node.LastEvent = note(w, evField, "last event", row)
	node.Error = note(w, crewErrorRef(c.Status, ev, evField), "error reason", row)
	node.OpenMerge = note(w, openMergeField(c.CrewID, merges), "open merge", row)
	node.Attention = crewAttention(node)
	return node
}

func openMergeField(crewID string, merges openMergeIndex) Field[OpenMergeValue] {
	if merges.err != nil {
		return UnknownField[OpenMergeValue](readFailureReason(merges.err))
	}
	if v, ok := merges.byCrew[crewID]; ok {
		return KnownField(v)
	}
	return AbsentField[OpenMergeValue]("no open merge request")
}

// retryOf reports which attempt this one retries. crew.retry_of_crew_id is
// set automatically by application.ReserveCrewAttempt (G5-09: re-spawning a
// Task whose only prior attempt is terminal is a retry by construction), so
// the linkage is a recorded fact; the previous attempt's *number* is
// resolved from the sibling attempts already read, costing no extra read.
func retryOf(c application.CrewSummary, siblings []application.CrewSummary) Field[RetryValue] {
	if c.RetryOfCrewID == "" {
		return AbsentField[RetryValue]("this is a first attempt, not a retry")
	}
	for _, s := range siblings {
		if s.CrewID == c.RetryOfCrewID {
			return KnownField(RetryValue{CrewID: s.CrewID, Attempt: s.Attempt})
		}
	}
	return AbsentField[RetryValue](fmt.Sprintf(
		"attempt %s is recorded as the retried attempt but is not among this task's attempts, so its number is not known",
		c.RetryOfCrewID))
}

func loadWorktree(ctx context.Context, deps application.Deps, crewID string) Field[WorktreeValue] {
	wt, err := deps.Store.GetWorktree(ctx, crewID)
	switch {
	case err == nil:
		return KnownField(WorktreeValue{Path: wt.Path, Branch: wt.Branch, Status: WorktreeStatus(wt.Status)})
	case errors.Is(err, persistence.ErrNotFound):
		return AbsentField[WorktreeValue]("no worktree is recorded for this attempt")
	default:
		return UnknownField[WorktreeValue](readFailureReason(err))
	}
}

// loadAgent reads one agent's runtime_binding rows once and answers two
// different questions from them: which Herdr agent name is recorded for
// this agent at all (the name survives release, since the released row
// stays for audit), and which binding it currently holds. Both come from
// the same read, so a failed read makes both Unknown.
func loadAgent(ctx context.Context, deps application.Deps, agentID string) (Field[string], Field[BindingValue]) {
	rows, err := application.BindingsForAgent(ctx, deps.Store, agentID)
	if err != nil {
		reason := readFailureReason(err)
		return UnknownField[string](reason), UnknownField[BindingValue](reason)
	}
	if len(rows) == 0 {
		return AbsentField[string]("no runtime binding has ever been recorded for this agent"),
			AbsentField[BindingValue]("no runtime binding has ever been recorded for this agent")
	}
	name := AbsentField[string]("the runtime bindings recorded for this agent name no Herdr agent")
	binding := AbsentField[BindingValue]("every runtime binding recorded for this agent has been released")
	// ListBindings orders by (created_at, rowid), so the last row is the most
	// recent. The one-held-per-agent index makes at most one of them held.
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if name.State != Known && r.HerdrAgent != "" {
			name = KnownField(r.HerdrAgent)
		}
		if binding.State != Known && r.Status != persistence.BindingReleased {
			binding = bindingField(r)
		}
	}
	return name, binding
}

func bindingField(r persistence.BindingRecord) Field[BindingValue] {
	v := BindingValue{
		Status: BindingStatus(r.Status), AgentName: r.HerdrAgent,
		Runtime: "herdr", Session: r.HerdrSession, Workspace: r.HerdrWorkspace,
		Tab: r.HerdrTab, Pane: r.HerdrPane,
		BoundSince: r.ReservedAt, BoundSinceKind: BoundSinceReserved,
	}
	if r.ActivatedAt != nil {
		v.BoundSince, v.BoundSinceKind = *r.ActivatedAt, BoundSinceActivated
	}
	switch v.Status {
	case BindingActive:
		// ADR 0019's health observer does not exist: nothing in the database
		// knows whether the process is still there.
		return KnownNote(v, "recorded active; this does not prove the agent is alive")
	case BindingStale:
		return KnownNote(v, "recorded stale: mate could not confirm the agent stopped, and attach is refused while stale")
	case BindingReserved:
		return KnownNote(v, "recorded reserved: the name and slot are held but no agent has started")
	}
	return KnownField(v)
}

// lastEvent returns the most recently recorded event matching f. ListEvents
// orders ascending by (recorded_at, rowid), so the latest match is the last
// element, not the first f.Limit rows; Phase 1 event volume per row is
// small enough that reading the full match set is not a real cost. owner
// names the row kind for the Absent reason ("no event is recorded for this
// attempt").
func lastEvent(ctx context.Context, deps application.Deps, f persistence.EventFilter, owner string) (observability.Event, Field[EventValue]) {
	events, err := deps.Store.ListEvents(ctx, f)
	if err != nil {
		return observability.Event{}, UnknownField[EventValue](readFailureReason(err))
	}
	if len(events) == 0 {
		return observability.Event{}, AbsentField[EventValue]("no event is recorded for this " + owner)
	}
	ev := events[len(events)-1]
	return ev, KnownField(EventValue{
		EventID: ev.EventID, EventType: ev.EventType,
		OccurredAt: ev.OccurredAt, CorrelationID: ev.CorrelationID,
	})
}

// crewErrorReasonEvents names the Crew event types whose payload carries a
// human-readable "reason" for the error state that produced them.
// EventCrewReportedDone is here because CrewNeedsRebase and CrewNeedsRepair
// (via precheckFastForward, internal/application/crew_report.go) are both
// reached through `crew done`, which records crew.reported_done - there is
// no crew.needs_rebase event type - and that event's payload already
// carries "reason" when the precheck fails.
var crewErrorReasonEvents = map[string]bool{
	observability.EventCrewFailed:       true,
	observability.EventCrewNeedsRepair:  true,
	observability.EventCrewReportedDone: true,
}

func crewErrorRef(status domain.CrewStatus, ev observability.Event, last Field[EventValue]) Field[ErrorReason] {
	switch status {
	case domain.CrewFailed, domain.CrewNeedsRepair, domain.CrewNeedsRebase, domain.CrewBlocked:
	default:
		return AbsentField[ErrorReason]("the recorded status of this attempt is not an error state")
	}
	return errorReason(status.String(), ev, last, crewErrorReasonEvents)
}

func taskErrorRef(status domain.TaskStatus, ev observability.Event, last Field[EventValue]) Field[ErrorReason] {
	switch status {
	case domain.TaskFailed, domain.TaskBlocked:
	default:
		return AbsentField[ErrorReason]("the recorded status of this task is not an error state")
	}
	return errorReason(status.String(), ev, last, map[string]bool{observability.EventTaskStatusChanged: true})
}

// mateErrorRef answers why a Mate is in the one MateStatus that is an error
// state. `unknown` is reached through mate.unknown (application.
// MarkMateUnknown), whose payload carries the reason; a failed *start* is
// not an error state at all - RevertMateStart rolls the row back to
// created or stopped - so nothing else here is one.
//
// The reason is looked up by event type rather than taken from the Mate
// row's last event, because that last event is the Project's (see
// loadMateNode) and is very often a Crew event recorded afterwards.
//
// The filter is by Project, not by Mate (the events table has no mate_id
// column - see EventValue), so the payload's own mate_id is checked
// explicitly below: absence of a matching id is not proof of identity, so a
// missing, empty or non-string mate_id is treated as a non-match rather than
// trusted as this Mate's. That match is unreachable today, not vacuous: a
// Project can hold more than one Mate row over time (only the active one is
// unique - internal/persistence/tx_test.go:362-429 stops one Mate and starts
// a second in the same Project), but application.MarkMateUnknown always
// writes the payload's mate_id as the very row it is marking, so a
// mismatched or missing id would mean a bug elsewhere in the write path, not
// a relaxed index. The check makes this read correct on its own terms
// regardless of what the write path currently guarantees.
func mateErrorRef(ctx context.Context, deps application.Deps, mate persistence.MateRecord) Field[ErrorReason] {
	if mate.Status != domain.MateUnknown {
		return AbsentField[ErrorReason]("the recorded status of this Mate is not an error state")
	}
	ev, found := lastEvent(ctx, deps, persistence.EventFilter{
		ProjectID: mate.ProjectID, EventType: observability.EventMateUnknown,
	}, "Mate")
	if found.State == Known {
		if id, ok := ev.Payload["mate_id"].(string); !ok || id != mate.MateID {
			found = AbsentField[EventValue]("no mate.unknown event is recorded for this Mate")
		}
	}
	return errorReason(mate.Status.String(), ev, found, map[string]bool{observability.EventMateUnknown: true})
}

// errorReason folds a row's error status and its last event into the one
// answer a UI may render. The status was already read successfully to reach
// here, but the reason comes from a separate ListEvents read that can fail
// independently: that failure is Unknown, never "no reason recorded" -
// which would assert a negative the read never established. See ErrorReason.
func errorReason(status string, ev observability.Event, last Field[EventValue], reasonEvents map[string]bool) Field[ErrorReason] {
	switch last.State {
	case Unknown:
		return UnknownField[ErrorReason](last.Reason)
	case Absent:
		// The event read itself succeeded and established there is no event
		// at all for this row - a different fact than "an event exists but
		// carries no reason", so it must not share that sentence.
		return KnownNote(ErrorReason(""),
			"recorded "+status+", and no event is recorded that could carry a reason")
	}
	noReason := KnownNote(ErrorReason(""),
		"recorded "+status+", and no reason is recorded on the last matching event")
	if !reasonEvents[ev.EventType] {
		return noReason
	}
	reason, _ := ev.Payload["reason"].(string)
	if reason == "" {
		return noReason
	}
	return KnownField(ErrorReason(reason))
}
