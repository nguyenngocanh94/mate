package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/db"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/timeline"
)

// routes is the whole API surface. Every pattern names GET explicitly: a
// POST to any of these is a 405 from the mux rather than something this
// package has to remember not to implement.
func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/workspace", s.cached(s.handleWorkspace))
	s.mux.HandleFunc("GET /api/projects/{project}", s.cached(s.handleProject))
	s.mux.HandleFunc("GET /api/projects/{project}/tasks/{crew}", s.cached(s.handleTask))
	s.mux.HandleFunc("GET /api/projects/{project}/tasks/{crew}/turns/{turn}", s.cached(s.handleTurn))
	s.mux.HandleFunc("GET /api/projects/{project}/tasks/{crew}/diff", s.cached(s.handleDiff))
	s.mux.HandleFunc("GET /api/events", s.handleEvents)
	s.mux.Handle("GET /", etagFileServer(uiFS()))
}

// builder computes one response's body at a known generation.
type builder func(ctx context.Context, r *http.Request, env envelope) (any, error)

// cached wraps a builder in the per-`event.id` generation cache. The
// response's own `last_event_id` is the generation it was built at, so the
// number a client polls `/api/events?since=` with is exactly the number the
// page it is looking at was computed from.
func (s *Server) cached(build builder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id, err := s.lastEventID(ctx)
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, "the timeline could not be read", err.Error())
			return
		}
		key := r.URL.Path + "?" + r.URL.RawQuery
		if body, ok := s.cache.get(key, id); ok {
			writeJSON(w, http.StatusOK, body)
			return
		}
		env := envelope{GeneratedAt: db.FormatTime(s.deps.now()), LastEventID: id}
		payload, err := build(ctx, r, env)
		if err != nil {
			var missing errNotFound
			if errors.As(err, &missing) {
				s.writeError(w, r, http.StatusNotFound, missing.reason, "")
				return
			}
			s.writeError(w, r, http.StatusInternalServerError, "the timeline could not be read", err.Error())
			return
		}
		body, err := json.Marshal(payload)
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, "the answer could not be encoded", err.Error())
			return
		}
		s.cache.put(key, id, body)
		writeJSON(w, http.StatusOK, body)
	}
}

func (s *Server) handleWorkspace(ctx context.Context, _ *http.Request, env envelope) (any, error) {
	out := WorkspaceResponse{envelope: env, Root: s.ws.Root(), Projects: []ProjectCard{}}
	for _, ref := range s.ws.Projects() {
		card := ProjectCard{Name: ref.Name, Mode: modeWord(s.ws, ref.Name), CrewsByState: map[string]int{}}
		scenes, err := s.sceneRows(ctx, ref.Name)
		if err != nil {
			return nil, err
		}
		facts, _, err := s.actorFacts(ctx, timeline.MateActorID(ref.Name))
		if err != nil {
			return nil, err
		}
		card.Mate = MateCard{Harness: facts.harness, Running: facts.running()}
		if sc, ok := scenes[timeline.MateActorID(ref.Name)]; ok {
			card.Mate.State, card.Mate.Since = sc.State, sc.Since
			card.Mate.TokensToday, card.Mate.ContextPct = sc.TokensToday, sc.ContextPct
		}
		for _, sc := range scenes {
			if sc.ActorKind != timeline.ActorCrew {
				continue
			}
			state := sc.State
			if state == "" {
				state = "unknown"
			}
			card.CrewsByState[state]++
		}
		items, reason := s.inbox(ref.Name)
		card.InboxWaiting, card.Error = len(items), reason
		out.Projects = append(out.Projects, card)
	}
	return out, nil
}

func (s *Server) handleProject(ctx context.Context, r *http.Request, env envelope) (any, error) {
	project, err := s.project(r)
	if err != nil {
		return nil, err
	}
	scenes, err := s.sceneRows(ctx, project)
	if err != nil {
		return nil, err
	}
	mate, err := s.mateBlock(ctx, project, scenes)
	if err != nil {
		return nil, err
	}
	tasks, err := s.tasks(ctx, project, s.deps.now())
	if err != nil {
		return nil, err
	}
	items, reason := s.inbox(project)
	return ProjectResponse{
		envelope: env, Project: project, Mode: modeWord(s.ws, project),
		Mate: mate, Tasks: tasks, Inbox: items, InboxError: reason,
	}, nil
}

func (s *Server) handleTask(ctx context.Context, r *http.Request, env envelope) (any, error) {
	project, err := s.project(r)
	if err != nil {
		return nil, err
	}
	crew := r.PathValue("crew")
	ledger, err := s.task(ctx, project, crew, s.deps.now())
	if err != nil {
		return nil, err
	}
	actorID := timeline.CrewActorID(project, crew)
	turns, err := s.turns(ctx, actorID)
	if err != nil {
		return nil, err
	}
	lines, err := s.statusLines(ctx, actorID)
	if err != nil {
		return nil, err
	}
	questions, err := s.questions(ctx, actorID)
	if err != nil {
		return nil, err
	}
	return TaskResponse{
		envelope: env, Project: project, Crew: crew, Ledger: ledger,
		Turns: turns, StatusLines: lines, Questions: questions,
		Branch: s.branch(ctx, project, ledger.Branch),
	}, nil
}

func (s *Server) handleTurn(ctx context.Context, r *http.Request, env envelope) (any, error) {
	project, err := s.project(r)
	if err != nil {
		return nil, err
	}
	crew := r.PathValue("crew")
	if _, err := s.task(ctx, project, crew, s.deps.now()); err != nil {
		return nil, err
	}
	turn, err := s.turnByID(ctx, timeline.CrewActorID(project, crew), r.PathValue("turn"))
	if err != nil {
		return nil, err
	}
	actions, err := s.actions(ctx, turn.ID)
	if err != nil {
		return nil, err
	}
	events, err := s.turnEvents(ctx, project, turn.ID)
	if err != nil {
		return nil, err
	}
	return TurnResponse{
		envelope: env, Project: project, Crew: crew,
		Turn: turn, Actions: actions, Events: events,
	}, nil
}

func (s *Server) handleDiff(ctx context.Context, r *http.Request, env envelope) (any, error) {
	project, err := s.project(r)
	if err != nil {
		return nil, err
	}
	crew := r.PathValue("crew")
	ledger, err := s.task(ctx, project, crew, s.deps.now())
	if err != nil {
		return nil, err
	}
	out := DiffResponse{envelope: env, Project: project, Crew: crew, Branch: ledger.Branch}
	branch := s.branch(ctx, project, ledger.Branch)
	out.Exists = branch.Exists
	if !branch.Exists {
		out.Reason = branch.Reason
		return out, nil
	}
	if s.deps.Diff == nil {
		out.Reason = "this server was started without a diff reader"
		return out, nil
	}
	text, err := s.deps.Diff(ctx, project, crew)
	if err != nil {
		// A diff that cannot be produced is this page's answer, not the
		// server's failure: the rest of the task page is still true, and a
		// 500 here would take it down with the branch.
		out.Reason = err.Error()
		return out, nil
	}
	out.Text = text
	return out, nil
}

// branch answers whether a crew's branch is still in the repo. With no
// BranchExists seam it says so in Reason rather than claiming either way.
func (s *Server) branch(ctx context.Context, project, name string) Branch {
	if name == "" {
		return Branch{Reason: "this crew recorded no branch"}
	}
	if s.deps.BranchExists == nil {
		return Branch{Name: name, Reason: "this server was started without a git reader"}
	}
	exists, err := s.deps.BranchExists(ctx, project, name)
	if err != nil {
		return Branch{Name: name, Reason: err.Error()}
	}
	out := Branch{Name: name, Exists: exists}
	if !exists {
		out.Reason = "branch " + name + " no longer exists; the crew was torn down and its branch is gone"
	}
	return out
}

// maxWaitSeconds bounds a long poll. Twenty-five seconds is under every
// default proxy and browser idle timeout a local page can meet, and a
// client that wants longer polls again - which costs one integer read.
const maxWaitSeconds = 25

// pollInterval is how often a waiting request asks whether the id moved.
// One second is what docs/mvp.md M5 names for a live reader and what
// `matev2 events --follow` already uses.
const pollInterval = time.Second

// handleEvents is GET /api/events?since=<id>&wait=<seconds>[&project=<p>]:
// the long poll the page keeps open so it never has to be refreshed.
//
// It is not cached. Every other endpoint answers "what is true now", which
// is a function of the generation; this one answers "what happened after
// the id you hold", which is a function of the caller's cursor, and a
// generation cache keyed by that cursor would only ever hold entries nobody
// asks for twice.
//
// It returns as soon as an event newer than `since` exists, and after `wait`
// seconds of nothing it returns an empty list rather than holding the
// connection - an empty answer is the honest "still nothing", and the
// client polls again with the same cursor.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	since, err := intParam(r, "since")
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "since must be an event id", err.Error())
		return
	}
	wait, err := intParam(r, "wait")
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "wait must be a number of seconds", err.Error())
		return
	}
	if wait > maxWaitSeconds {
		wait = maxWaitSeconds
	}
	if wait < 0 {
		wait = 0
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project != "" {
		if _, ok := s.ws.Project(project); !ok {
			s.writeError(w, r, http.StatusNotFound, unknownProject(s.ws, project), "")
			return
		}
	}

	// The wait is measured on the real clock, not on Deps.Now. Deps.Now is
	// the clock behind `generated_at` - a stamp on a snapshot, which a test
	// freezes so two runs compare - while this is how long a socket is held
	// open, which nothing may freeze: a frozen clock here would be a poll
	// that never returns.
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	for {
		id, err := s.lastEventID(ctx)
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, "the timeline could not be read", err.Error())
			return
		}
		if id > since || !time.Now().Before(deadline) {
			out, err := s.eventsSince(ctx, project, since, envelope{
				GeneratedAt: db.FormatTime(s.deps.now()), LastEventID: id,
			})
			if err != nil {
				s.writeError(w, r, http.StatusInternalServerError, "the timeline could not be read", err.Error())
				return
			}
			body, err := json.Marshal(out)
			if err != nil {
				s.writeError(w, r, http.StatusInternalServerError, "the answer could not be encoded", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, body)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// eventsSince reads the story after an id, and the scene rows of every
// actor that moved in it.
//
// The story goes through timeline.Story, one project at a time, so the JSON
// on the wire is the same shape and the same field order `matev2 events`
// prints - one contract, not two. `v_now` has no "changed since" of its
// own, so which actors moved is read off `transition`, the table the view's
// scene columns already come from: an actor with a transition caused by an
// event after `since` is an actor whose row on the page is out of date.
func (s *Server) eventsSince(ctx context.Context, project string, since int64, env envelope) (EventsResponse, error) {
	out := EventsResponse{envelope: env, Project: project, Since: since,
		Events: []timeline.StoryEvent{}, Now: []SceneRow{}}

	names := []string{project}
	if project == "" {
		names = nil
		for _, ref := range s.ws.Projects() {
			names = append(names, ref.Name)
		}
	}
	for _, name := range names {
		events, err := timeline.Story(ctx, s.db.SQL(), timeline.StoryQuery{Project: name, SinceID: since})
		if err != nil {
			return out, err
		}
		out.Events = append(out.Events, events...)

		moved, err := s.movedActors(ctx, name, since)
		if err != nil {
			return out, err
		}
		if len(moved) == 0 {
			continue
		}
		scenes, err := s.sceneRows(ctx, name)
		if err != nil {
			return out, err
		}
		for _, id := range moved {
			if sc, ok := scenes[id]; ok {
				out.Now = append(out.Now, sc)
			}
		}
	}
	sort.SliceStable(out.Events, func(i, j int) bool {
		if out.Events[i].At != out.Events[j].At {
			return out.Events[i].At < out.Events[j].At
		}
		return out.Events[i].ID < out.Events[j].ID
	})
	sort.SliceStable(out.Now, func(i, j int) bool { return out.Now[i].ActorID < out.Now[j].ActorID })
	return out, nil
}

func (s *Server) movedActors(ctx context.Context, project string, since int64) ([]string, error) {
	rows, err := s.db.SQL().QueryContext(ctx,
		`SELECT DISTINCT actor_id FROM transition WHERE project = ? AND event_id > ?`, project, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// project reads the {project} path value and checks it is registered. An
// unknown one is a 404 that names the ones there are, because the reader of
// this API is a page whose link was built from a name that used to work.
func (s *Server) project(r *http.Request) (string, error) {
	name := r.PathValue("project")
	if _, ok := s.ws.Project(name); !ok {
		return "", errNotFound{reason: unknownProject(s.ws, name)}
	}
	return name, nil
}

func unknownProject(ws *store.Workspace, name string) string {
	var have []string
	for _, ref := range ws.Projects() {
		have = append(have, ref.Name)
	}
	if len(have) == 0 {
		return "no project " + strconv.Quote(name) + " in this workspace; it has no projects at all"
	}
	return "no project " + strconv.Quote(name) + " in this workspace; it has " + strings.Join(have, ", ")
}

// modeWord is docs/mvp.md section 5's two modes, as the card prints them.
func modeWord(ws *store.Workspace, project string) string {
	if ws.Auto(project) {
		return "auto"
	}
	return "manual"
}

func intParam(r *http.Request, name string) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// No Access-Control-Allow-Origin of any kind: the only page that reads
	// this API is the one this server serves, and a header that let another
	// origin read it would put a workspace's whole timeline one visited
	// link away.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n"))
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, message, reason string) {
	id, _ := s.lastEventID(r.Context())
	body, err := json.Marshal(ErrorResponse{
		envelope: envelope{GeneratedAt: db.FormatTime(s.deps.now()), LastEventID: id},
		Error:    message, Reason: reason,
	})
	if err != nil {
		http.Error(w, message, status)
		return
	}
	writeJSON(w, status, body)
}
