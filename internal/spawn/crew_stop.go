package spawn

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// StopCrew stops one crew's agent and closes its tab. It is deliberately
// the smaller half of docs/mvp.md task 16: the worktree, the branch and
// `crews/<id>/` are left exactly where they are, because they hold the work
// and the evidence, and only a teardown that has reviewed or merged the
// branch may remove them.
//
// Like StopMate, the proof of the stop is Herdr's own answer: the agent must
// be unknown to `agent get` and absent from the session inventory.
func StopCrew(ctx context.Context, w *store.Workspace, deps Deps, project, crew string) (StopResult, error) {
	if w == nil {
		return StopResult{}, errUsage("spawn: a workspace is required")
	}
	if deps.Runtime == nil {
		return StopResult{}, errUsage("spawn: a runtime adapter is required")
	}
	if deps.Names == nil {
		deps.Names = runtime.NewMemoryNameRegistry()
	}
	if err := store.ValidateProjectName(project); err != nil {
		return StopResult{}, err
	}
	if err := store.ValidateCrewID(crew); err != nil {
		return StopResult{}, err
	}
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return StopResult{}, err
	}
	out := StopResult{
		Project:   project,
		Agent:     meta[MetaAgent],
		Session:   meta[MetaSession],
		SessionID: meta[MetaSessionID],
	}
	if out.Agent == "" {
		return out, observability.NewError(observability.CodeNotFound,
			fmt.Sprintf("no crew %s is recorded for project %s; %s names no agent", crew, project, w.CrewMeta(project, crew)))
	}

	spec, err := deps.sessionSpec(w)
	if err != nil {
		return StopResult{}, err
	}
	session, running, err := deps.Runtime.LookupSession(ctx, spec)
	if err != nil {
		return StopResult{}, err
	}
	tab := runtime.TabHandle{
		Session:     session,
		WorkspaceID: meta[MetaWorkspace],
		TabID:       meta[MetaTab],
		PaneID:      meta[MetaPane],
		Label:       CrewTabLabelPrefix + crew,
	}
	if !running {
		out.AlreadyGone, out.TabClosed = true, true
		return out, clearCrewRunMeta(w, project, crew, meta, deps.now())
	}

	kind, _ := harness.ParseKind(meta[MetaHarness])
	handle := runtime.AgentHandle{Session: session, Name: out.Agent, RawID: crew, Kind: kind, Tab: tab}
	live, err := agentLive(ctx, deps, handle)
	if err != nil {
		return StopResult{}, err
	}
	out.AlreadyGone = !live
	if live {
		if err := stopLiveAgent(ctx, deps, handle); err != nil {
			return StopResult{}, err
		}
	}
	if err := confirmGone(ctx, deps, handle); err != nil {
		return StopResult{}, err
	}
	deps.Names.Release(session.Name, handle.Name)

	if tab.PaneID != "" || tab.TabID != "" {
		if err := deps.Runtime.RemoveTab(ctx, tab); err != nil && !runtime.IsTabGone(err) {
			return StopResult{}, err
		}
		out.TabClosed = true
	}
	return out, clearCrewRunMeta(w, project, crew, meta, deps.now())
}

// clearCrewRunMeta drops the keys that named a live pane and keeps
// everything a review still needs: the task, the branch, the worktree and
// the harness session identity.
func clearCrewRunMeta(w *store.Workspace, project, crew string, meta map[string]string, now time.Time) error {
	next := map[string]string{}
	for _, key := range []string{MetaTask, MetaHarness, MetaSession, MetaSessionID, MetaTranscript, MetaWorktree, MetaBranch, MetaStartedAt} {
		if v, ok := meta[key]; ok {
			next[key] = v
		}
	}
	next[MetaStoppedAt] = now.Format(time.RFC3339)
	return w.WriteCrewMeta(project, crew, next)
}

// CrewSummary is one row of `matev2 crew list`.
type CrewSummary struct {
	Crew    string
	Harness string
	Branch  string
	// Status is the last line of `crews/<id>.status`, or empty when the crew
	// has reported nothing yet.
	Status string
	Pane   string
	Task   string
}

// ListCrews reads every `crews/<id>.meta` of a project and the last line of
// each crew's status file. It asks Herdr nothing: a list is a view of what
// was recorded, and `matev2 state <crew>` (task 13) is where a live answer
// comes from.
func ListCrews(w *store.Workspace, project string) ([]CrewSummary, error) {
	if w == nil {
		return nil, errUsage("spawn: a workspace is required")
	}
	if err := store.ValidateProjectName(project); err != nil {
		return nil, err
	}
	if _, ok := w.Project(project); !ok {
		return nil, fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	ids, err := crewIDs(w, project)
	if err != nil {
		return nil, err
	}
	out := make([]CrewSummary, 0, len(ids))
	for _, id := range ids {
		meta, err := w.ReadCrewMeta(project, id)
		if err != nil {
			return nil, err
		}
		entries, _, err := w.ReadStatus(project, id, 0)
		if err != nil {
			return nil, err
		}
		last := ""
		if len(entries) > 0 {
			last = entries[len(entries)-1].Line
		}
		out = append(out, CrewSummary{
			Crew:    id,
			Harness: meta[MetaHarness],
			Branch:  meta[MetaBranch],
			Status:  last,
			Pane:    meta[MetaPane],
			Task:    meta[MetaTask],
		})
	}
	return out, nil
}

// readDirNames lists a directory's entries. A missing directory is not an
// error: a project with no crew has nothing under `crews/`.
func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

// crewIDs are the ids with a `.meta` under `crews/`, sorted. A file whose
// name is not a valid crew id is ignored rather than reported: `crews/` is a
// directory the user can also put things in.
func crewIDs(w *store.Workspace, project string) ([]string, error) {
	entries, err := readDirNames(w.CrewsDir(project))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, name := range entries {
		id, ok := strings.CutSuffix(name, ".meta")
		if !ok {
			continue
		}
		if store.ValidateCrewID(id) != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
