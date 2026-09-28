package dashboard

import (
	"github.com/nguyenngocanh94/mate/internal/diagnostics"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// The JSON shapes of docs/dashboard.md. Field order here is the field order
// on the wire, and a field is added at the end rather than in the middle,
// the same rule timeline.StoryEvent follows.
//
// Two conventions run through all of them. A nullable number is a pointer
// and renders as `null`, never as 0: a cost with no price and a context
// percentage with no known window are unknown, and docs/timeline.md's
// "a missing price is not a price of zero" is the same rule `mate usage`
// prints as `?`. A timestamp is the database's own string - RFC3339 with
// nanoseconds, in UTC (db.TimeFormat) - passed through rather than
// reformatted, so a number on the page and a row in the database compare
// byte for byte.

// envelope is on every response. GeneratedAt is when the snapshot behind
// the bytes was computed, which for a cached response is when the
// generation it belongs to was first built - the honest reading, since
// nothing in it has changed since.
type envelope struct {
	GeneratedAt string `json:"generated_at"`
	LastEventID int64  `json:"last_event_id"`
}

// Ref is a transcript locator: the file an event, turn or action was read
// out of, and the byte offset inside it. It is what makes every number on
// the page traceable back to the harness's own record (docs/mvp.md M6).
type Ref struct {
	Path   string `json:"path,omitempty"`
	Offset int64  `json:"offset,omitempty"`
}

// Tokens is the four billed buckets plus thinking. Total is
// input+cache_read+cache_write+output and excludes thinking, which is what
// `mate usage`'s TOTAL column sums and what `v_now.tokens_today` sums:
// thinking tokens are reported by the harness inside the output it already
// counted, so adding them would count them twice.
type Tokens struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
	Thinking   int64 `json:"thinking"`
	Total      int64 `json:"total"`
}

// SceneRow is one row of `v_now`: where the scene projection last put an
// actor. State is empty for an actor no projection has placed - the
// captain, mate and the observer are in the story and not in the office
// (docs/timeline.md section 7).
type SceneRow struct {
	ActorID       string   `json:"actor_id"`
	Actor         string   `json:"actor"`
	ActorKind     string   `json:"actor_kind"`
	Project       string   `json:"project"`
	State         string   `json:"state"`
	Since         string   `json:"since,omitempty"`
	Target        string   `json:"target,omitempty"`
	Detail        string   `json:"detail,omitempty"`
	TokensToday   int64    `json:"tokens_today"`
	ContextPct    *float64 `json:"context_pct"`
	ContextTokens *int64   `json:"context_tokens"`
}

// MateCard is the Mate as a workspace card shows it (docs/mvp.md M6 tier 1).
type MateCard struct {
	Harness       string   `json:"harness"`
	Running       bool     `json:"running"`
	State         string   `json:"state"`
	Since         string   `json:"since,omitempty"`
	TokensToday   int64    `json:"tokens_today"`
	ContextPct    *float64 `json:"context_pct"`
	ContextTokens *int64   `json:"context_tokens"`
}

// Mate is the Mate as a project page shows it (tier 2): the card plus what
// it has spent and what its most recent turn was.
type Mate struct {
	Harness       string   `json:"harness"`
	Running       bool     `json:"running"`
	State         string   `json:"state"`
	Since         string   `json:"since,omitempty"`
	Target        string   `json:"target,omitempty"`
	Detail        string   `json:"detail,omitempty"`
	TokensToday   int64    `json:"tokens_today"`
	ContextPct    *float64 `json:"context_pct"`
	ContextTokens *int64   `json:"context_tokens"`
	Turns         int64    `json:"turns"`
	Tokens        Tokens   `json:"tokens"`
	Cost          *float64 `json:"cost"`
	LastTurn      *Turn    `json:"last_turn"`
}

// ProjectCard is one project on the workspace page.
type ProjectCard struct {
	Name string `json:"name"`
	// Mode is "auto" or "manual" - `.mate/projects/<p>/auto` (store.Auto).
	Mode string   `json:"mode"`
	Mate MateCard `json:"mate"`
	// CrewsByState counts this project's crew actors by scene state. A crew
	// no projection has placed counts under "unknown", which is a state of
	// the reader's knowledge and is named as one rather than dropped.
	CrewsByState map[string]int `json:"crews_by_state"`
	// InboxWaiting is box.Inbox's length: how many things are still waiting
	// on a decision, the same number the console's rail header shows.
	InboxWaiting int `json:"inbox_waiting"`
	// Error is why this card is thin - an unreadable box, a project with no
	// timeline yet. The card is still returned: one broken project must not
	// blank the workspace.
	Error string `json:"error,omitempty"`
}

// WorkspaceResponse is GET /api/workspace.
type WorkspaceResponse struct {
	envelope
	Root     string        `json:"root"`
	Projects []ProjectCard `json:"projects"`
}

// Task is one row of the project page's table: `v_task_ledger` joined to
// the crew's current scene state.
type Task struct {
	Crew       string `json:"crew"`
	Text       string `json:"text"`
	Branch     string `json:"branch,omitempty"`
	State      string `json:"state"`
	Since      string `json:"since,omitempty"`
	Target     string `json:"target,omitempty"`
	Detail     string `json:"detail,omitempty"`
	CloseState string `json:"close_state,omitempty"`
	Closed     bool   `json:"closed"`
	SpawnedAt  string `json:"spawned_at,omitempty"`
	ClosedAt   string `json:"closed_at,omitempty"`
	MergedAt   string `json:"merged_at,omitempty"`
	// AgeMs is closed_at-spawned_at for a closed task and now-spawned_at
	// for an open one: how long the task has been alive, not how long ago
	// it started.
	AgeMs             int64    `json:"age_ms"`
	Turns             int64    `json:"turns"`
	Tokens            Tokens   `json:"tokens"`
	Cost              *float64 `json:"cost"`
	LastModel         string   `json:"last_model,omitempty"`
	ContextTokensLast int64    `json:"context_tokens_last"`
	ContextPct        *float64 `json:"context_pct"`
	QuestionCount     int64    `json:"question_count"`
	HandbackCount     int64    `json:"handback_count"`
	WaitedMs          int64    `json:"waited_ms"`
	ToolCount         int64    `json:"tool_count"`
}

// InboxItem is one thing still waiting on a decision, flattened from
// query.BoxView.Inbox - the same loader, the same order and the same
// vocabulary the console's rail draws.
type InboxItem struct {
	Seq       int    `json:"seq"`
	At        string `json:"at,omitempty"`
	Kind      string `json:"kind"`
	Source    string `json:"source,omitempty"`
	Target    string `json:"target,omitempty"`
	Crew      string `json:"crew,omitempty"`
	Verb      string `json:"verb,omitempty"`
	Text      string `json:"text"`
	Attention bool   `json:"attention"`
}

// ProjectResponse is GET /api/projects/{project}.
type ProjectResponse struct {
	envelope
	Project string      `json:"project"`
	Mode    string      `json:"mode"`
	Mate    Mate        `json:"mate"`
	Tasks   []Task      `json:"tasks"`
	Inbox   []InboxItem `json:"inbox"`
	// InboxError is why Inbox is empty when it should not be: the box read
	// failed. An unreadable sent.log must not blank the page.
	InboxError string `json:"inbox_error,omitempty"`
}

// MateResponse is the project's Mate ledger and its prompt-level exchanges.
type MateResponse struct {
	envelope
	Project   string         `json:"project"`
	Mate      Mate           `json:"mate"`
	Exchanges []MateExchange `json:"exchanges"`
}

// MateExchange is one prompt to the Mate and the work and reply it caused.
// Calls groups model calls by (session, harness_turn_ref); a missing ref is
// isolated as one call so unrelated work is never merged by a guess.
type MateExchange struct {
	ID           string               `json:"id"`
	Source       string               `json:"source"`
	Prompt       string               `json:"prompt"`
	PromptAt     string               `json:"prompt_at"`
	Response     string               `json:"response"`
	ResponseAt   string               `json:"response_at,omitempty"`
	StartedAt    string               `json:"started_at"`
	EndedAt      string               `json:"ended_at"`
	DurationMs   *int64               `json:"duration_ms"`
	ModelCalls   int64                `json:"model_calls"`
	ToolCalls    int64                `json:"tool_calls"`
	Model        string               `json:"model,omitempty"`
	Tokens       Tokens               `json:"tokens"`
	ContextAfter int64                `json:"context_tokens_after"`
	Activities   []MateActivity       `json:"activities"`
	PromptRef    Ref                  `json:"prompt_ref"`
	ResponseRef  Ref                  `json:"response_ref"`
	Overview     diagnostics.Overview `json:"overview"`
}

type MateActivity struct {
	At   string `json:"at"`
	Kind string `json:"kind"`
	Crew string `json:"crew,omitempty"`
	Text string `json:"text"`
}

// Turn is one model call.
type Turn struct {
	ID             string   `json:"id"`
	SessionID      string   `json:"-"`
	HarnessTurnRef string   `json:"-"`
	Ordinal        int64    `json:"ordinal"`
	StartedAt      string   `json:"started_at,omitempty"`
	EndedAt        string   `json:"ended_at,omitempty"`
	DurationMs     int64    `json:"duration_ms"`
	TriggerEventID int64    `json:"trigger_event_id,omitempty"`
	TriggerKind    string   `json:"trigger_kind,omitempty"`
	Outcome        string   `json:"outcome,omitempty"`
	Model          string   `json:"model,omitempty"`
	Tokens         Tokens   `json:"tokens"`
	ContextAfter   int64    `json:"context_tokens_after"`
	ContextPct     *float64 `json:"context_pct"`
	ToolCount      int64    `json:"tool_count"`
	Ref            Ref      `json:"ref"`
}

// StatusLine is one line the crew wrote to `crews/<id>.status`, as the
// `status.appended` event recorded it.
type StatusLine struct {
	EventID int64  `json:"event_id"`
	At      string `json:"at"`
	TurnID  string `json:"turn_id,omitempty"`
	Verb    string `json:"verb,omitempty"`
	Text    string `json:"text"`
	Line    string `json:"line,omitempty"`
	Ref     Ref    `json:"ref"`
}

// Question is one thing the crew stopped and asked, with the answer it got.
// WaitedMs is null while it is still waiting, which is not the same as
// having waited zero.
type Question struct {
	ID              string `json:"id"`
	AskedEventID    int64  `json:"asked_event_id"`
	AskedAt         string `json:"asked_at"`
	Text            string `json:"text"`
	AnsweredEventID int64  `json:"answered_event_id,omitempty"`
	AnsweredAt      string `json:"answered_at,omitempty"`
	AnsweredBy      string `json:"answered_by,omitempty"`
	Answer          string `json:"answer,omitempty"`
	WaitedMs        *int64 `json:"waited_ms"`
}

// Branch is the crew's branch and whether git still has it.
type Branch struct {
	Name   string `json:"name,omitempty"`
	Exists bool   `json:"exists"`
	// Reason is why Exists is false, or why it could not be established.
	Reason string `json:"reason,omitempty"`
}

// TaskResponse is GET /api/projects/{project}/tasks/{crew}.
type TaskResponse struct {
	envelope
	Project     string                  `json:"project"`
	Crew        string                  `json:"crew"`
	Ledger      Task                    `json:"ledger"`
	Turns       []Turn                  `json:"turns"`
	StatusLines []StatusLine            `json:"status_lines"`
	Questions   []Question              `json:"questions"`
	Branch      Branch                  `json:"branch"`
	Performance diagnostics.Performance `json:"performance"`
}

// Action is one tool call inside a turn.
type Action struct {
	ID         string `json:"id"`
	At         string `json:"at"`
	EndedAt    string `json:"ended_at,omitempty"`
	Tool       string `json:"tool"`
	Target     string `json:"target,omitempty"`
	Summary    string `json:"summary,omitempty"`
	DurationMs *int64 `json:"duration_ms"`
	OK         *bool  `json:"ok"`
	EventID    int64  `json:"event_id,omitempty"`
	Ref        Ref    `json:"ref"`
}

// TurnResponse is GET /api/projects/{project}/tasks/{crew}/turns/{id}.
type TurnResponse struct {
	envelope
	Project string                `json:"project"`
	Crew    string                `json:"crew"`
	Turn    Turn                  `json:"turn"`
	Actions []Action              `json:"actions"`
	Events  []timeline.StoryEvent `json:"events"`
}

// DiffResponse is GET /api/projects/{project}/tasks/{crew}/diff. Text is
// `mate diff <project> <crew>` verbatim, and empty with a Reason when
// there is nothing to show.
type DiffResponse struct {
	envelope
	Project string `json:"project"`
	Crew    string `json:"crew"`
	Branch  string `json:"branch,omitempty"`
	Exists  bool   `json:"exists"`
	Text    string `json:"text"`
	Reason  string `json:"reason,omitempty"`
}

// EventsResponse is GET /api/events.
type EventsResponse struct {
	envelope
	Project string                `json:"project,omitempty"`
	Since   int64                 `json:"since"`
	Events  []timeline.StoryEvent `json:"events"`
	// Now are the `v_now` rows of the actors that moved since `since`: one
	// call refreshes both the story and the scene.
	Now []SceneRow `json:"now"`
}

// ErrorResponse is every 4xx and 5xx body. Reason is a sentence, not a
// code: the reader of this API is a page, and the page shows the sentence.
type ErrorResponse struct {
	envelope
	Error  string `json:"error"`
	Reason string `json:"reason,omitempty"`
}
