// Package diagnostics projects recorded harness facts into reviewable activity.
// The ledger is its only token authority. Findings may overlap; their usage
// must never be added together to obtain a run total.
package diagnostics

import "time"

const Version = "crew-observability-v1"

type Ref struct {
	Path   string `json:"path,omitempty"`
	Offset int64  `json:"offset"`
}

type Tokens struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
	Thinking   int64 `json:"thinking"`
	Total      int64 `json:"total"`
}

func (t *Tokens) Add(u Tokens) {
	t.Input += u.Input
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
	t.Output += u.Output
	t.Thinking += u.Thinking
	t.Total = t.Input + t.CacheRead + t.CacheWrite + t.Output
}

// Call is one existing ledger turn, not one user prompt.
type Call struct {
	ID, SessionID, HarnessTurnRef, StartedAt, EndedAt, Model string
	Ordinal                                                  int64
	Tokens                                                   Tokens
	ContextAfter                                             int64
	Ref                                                      Ref
	// Outcome is why the call stopped, as the ledger recorded it.
	Outcome string
}

// Action is the ledger fallback. It supplies attribution even when the
// adapter cannot yet expose an execution nested inside a wrapper.
type Action struct {
	ID, CallID, SessionID, Tool, Target, Summary, StartedAt, EndedAt string
	DurationMs                                                       *int64
	OK                                                               *bool
	Ref                                                              Ref
}

type Decision struct {
	ID, Text, AskedAt, AnsweredAt string
	EventID                       int64
}

type Options struct {
	Now              time.Time
	Closed           bool
	Worktree         string
	PollThreshold    int
	ReadThreshold    int
	RetryThreshold   int
	SlowToolMs       int64
	LargeOutputBytes int64
	StaleAfter       time.Duration
}

func (o Options) defaults() Options {
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	if o.PollThreshold <= 0 {
		o.PollThreshold = 3
	}
	if o.ReadThreshold <= 0 {
		o.ReadThreshold = 3
	}
	if o.RetryThreshold <= 0 {
		o.RetryThreshold = 2
	}
	if o.SlowToolMs <= 0 {
		o.SlowToolMs = 60_000
	}
	if o.LargeOutputBytes <= 0 {
		o.LargeOutputBytes = 32_768
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = 30 * time.Second
	}
	return o
}

type Performance struct {
	Version          string         `json:"version"`
	GeneratedAt      string         `json:"generated_at"`
	Tokens           Tokens         `json:"tokens"`
	ModelCalls       int            `json:"model_calls"`
	PromptTurns      []Prompt       `json:"prompt_turns"`
	Segments         []Segment      `json:"segments"`
	CurrentSegmentID string         `json:"current_segment_id,omitempty"`
	TopSegmentIDs    []string       `json:"top_segment_ids"`
	Findings         []Finding      `json:"findings"`
	TopFindingIDs    []string       `json:"top_finding_ids"`
	Executions       []Execution    `json:"executions"`
	Processes        []Process      `json:"processes"`
	Freshness        Freshness      `json:"freshness"`
	Time             TimeSummary    `json:"time"`
	RecentTokens     Tokens         `json:"recent_tokens"`
	RecentWindowMs   int64          `json:"recent_window_ms"`
	Profile          map[string]any `json:"profile,omitempty"`
	ObservedInputs   []ContextInput `json:"observed_inputs"`
	Runtime          RuntimeProfile `json:"runtime"`
	Progress         []Evidence     `json:"progress"`
	// Overview is the prompt overviews summed over the whole Crew: same
	// classifier, same categories, no sequence. Fields are appended, never
	// inserted, so recorded JSON keeps its field order.
	Overview Overview `json:"overview"`
	// Ranked by output + thinking: cache reads dominate totals, so
	// top_segment_ids mostly finds the longest context, not the costliest step.
	TopOutputSegmentIDs []string    `json:"top_output_segment_ids"`
	TopOutputCallIDs    []string    `json:"top_output_call_ids"`
	Loops               LoopSummary `json:"loops"`
	// Skills are the skills the recording shows being loaded, by name, in
	// the order each was first loaded.
	Skills []SkillUse `json:"skills"`
}

type RuntimeProfile struct {
	HarnessVersion string `json:"harness_version,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
}

type ContextInput struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Path      string `json:"path,omitempty"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
	At        string `json:"at"`
	SourceRef Ref    `json:"source_ref"`
}

type Prompt struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"session_id"`
	Prompt     string   `json:"prompt"`
	PromptAt   string   `json:"prompt_at,omitempty"`
	StartedAt  string   `json:"started_at,omitempty"`
	EndedAt    string   `json:"ended_at,omitempty"`
	ElapsedMs  *int64   `json:"elapsed_ms"`
	Tokens     Tokens   `json:"tokens"`
	ModelCalls int      `json:"model_calls"`
	SegmentIDs []string `json:"segment_ids"`
	SourceRef  Ref      `json:"source_ref"`
	Coverage   string   `json:"coverage"`
	Outcome    string   `json:"outcome,omitempty"`
	Overview   Overview `json:"overview"`
}

// Overview explains recorded work types inside one prompt. Category token
// ownership is exclusive per ledger call, even when execution evidence spans
// several types. It is never inferred from what the prompt asked for.
type Overview struct {
	Summary    string         `json:"summary"`
	Categories []WorkCategory `json:"categories"`
	Sequence   []WorkStep     `json:"sequence"`
	Rule       string         `json:"rule"`
	Coverage   string         `json:"coverage"`
	// Steps are the prompt's model calls in order, consecutive calls of one
	// type folded into one step. They partition the prompt's calls, so their
	// tokens add up to the prompt's. The Crew overview has none, for the
	// reason it has no Sequence.
	Steps []Step `json:"steps"`
}

// Step is a run of consecutive model calls that did one type of work. Its
// kind is the kind its calls are charged to in Categories, so the two can
// never disagree. Executions counts the operations those calls ran: native
// commands and direct tools, and a wrapper only when it is the sole record
// of what ran inside it.
type Step struct {
	Kind       string   `json:"kind"`
	Label      string   `json:"label"`
	StartedAt  string   `json:"started_at,omitempty"`
	EndedAt    string   `json:"ended_at,omitempty"`
	ElapsedMs  *int64   `json:"elapsed_ms"`
	ModelCalls int      `json:"model_calls"`
	Executions int      `json:"executions"`
	Tokens     Tokens   `json:"tokens"`
	CallIDs    []string `json:"call_ids"`
	SegmentIDs []string `json:"segment_ids"`
	// Skills names the skills loaded during the step, once each.
	Skills []string `json:"skills"`
	// Parts are the types of work recorded inside the step's calls, sorted.
	// A mixed step has several; they say what was mixed and split no token.
	Parts []string `json:"parts"`
	// Open marks the last step of a prompt that is still running: its
	// numbers can still grow.
	Open bool `json:"open"`
}

// SkillUse is one skill and every recorded load of it. A load is the
// harness's skill tool being called, or a SKILL.md being read; neither says
// the agent then followed the skill.
type SkillUse struct {
	Name  string      `json:"name"`
	Count int         `json:"count"`
	Loads []SkillLoad `json:"loads"`
}

type SkillLoad struct {
	At          string `json:"at,omitempty"`
	PromptID    string `json:"prompt_id,omitempty"`
	CallID      string `json:"call_id,omitempty"`
	ExecutionID string `json:"execution_id"`
	// Via is "tool" for the harness's skill tool and "read" for a SKILL.md
	// read by a file tool or a shell command.
	Via  string `json:"via"`
	Path string `json:"path,omitempty"`
}

type WorkCategory struct {
	Kind           string     `json:"kind"`
	Label          string     `json:"label"`
	ModelCalls     int        `json:"model_calls"`
	ExecutionCount int        `json:"execution_count"`
	Tokens         *Tokens    `json:"tokens"`
	ElapsedMs      *int64     `json:"elapsed_ms"`
	CallIDs        []string   `json:"call_ids"`
	SegmentIDs     []string   `json:"segment_ids"`
	ExecutionIDs   []string   `json:"execution_ids"`
	Evidence       []Evidence `json:"evidence"`
}

type WorkStep struct {
	Kind         string   `json:"kind"`
	Label        string   `json:"label"`
	StartedAt    string   `json:"started_at,omitempty"`
	SegmentIDs   []string `json:"segment_ids"`
	ExecutionIDs []string `json:"execution_ids"`
}

type Segment struct {
	ID            string   `json:"id"`
	PromptID      string   `json:"prompt_id"`
	Kind          string   `json:"kind"`
	Label         string   `json:"label"`
	Target        string   `json:"target,omitempty"`
	StartedAt     string   `json:"started_at,omitempty"`
	EndedAt       string   `json:"ended_at,omitempty"`
	ElapsedMs     *int64   `json:"elapsed_ms"`
	ToolElapsedMs *int64   `json:"tool_elapsed_ms"`
	InvocationMs  *int64   `json:"invocation_ms"`
	Tokens        Tokens   `json:"tokens"`
	ModelCalls    int      `json:"model_calls"`
	CallIDs       []string `json:"call_ids"`
	ExecutionIDs  []string `json:"execution_ids"`
	RepeatCount   int      `json:"repeat_count"`
	Outcome       string   `json:"outcome"`
	Rule          string   `json:"rule"`
}

type Execution struct {
	ID              string `json:"id"`
	SessionID       string `json:"session_id"`
	PromptID        string `json:"prompt_id,omitempty"`
	CallID          string `json:"call_id,omitempty"`
	WrapperID       string `json:"wrapper_id,omitempty"`
	ProcessID       string `json:"process_id,omitempty"`
	Tool            string `json:"tool"`
	Command         string `json:"command,omitempty"`
	Cwd             string `json:"cwd,omitempty"`
	Target          string `json:"target,omitempty"`
	StartedAt       string `json:"started_at,omitempty"`
	EndedAt         string `json:"ended_at,omitempty"`
	DurationMs      *int64 `json:"duration_ms"`
	Status          string `json:"status"`
	ExitCode        *int   `json:"exit_code"`
	OutputBytes     *int64 `json:"output_bytes"`
	NewOutputBytes  *int64 `json:"new_output_bytes"`
	OutputHash      string `json:"output_hash,omitempty"`
	Truncated       *bool  `json:"truncated"`
	Poll            bool   `json:"poll"`
	IsWrapper       bool   `json:"is_wrapper"`
	OutputExcerpt   string `json:"output_excerpt,omitempty"`
	ErrorSignature  string `json:"error_signature,omitempty"`
	SourceRef       Ref    `json:"source_ref"`
	MeasurementKind string `json:"measurement_kind"`
	// ParentLink is set when WrapperID and CallID were inferred rather than
	// named by the harness; see linkNativeParents.
	ParentLink string `json:"parent_link,omitempty"`
}

type Process struct {
	ID             string   `json:"id"`
	SessionID      string   `json:"session_id"`
	Command        string   `json:"command,omitempty"`
	ExecutionIDs   []string `json:"execution_ids"`
	PollIDs        []string `json:"poll_ids"`
	Polls          int      `json:"polls"`
	UnchangedPolls int      `json:"unchanged_polls"`
	ProgressPolls  int      `json:"progress_polls"`
	UnknownPolls   int      `json:"unknown_polls"`
	ElapsedMs      *int64   `json:"elapsed_ms"`
	Tokens         *Tokens  `json:"tokens"`
}

type Evidence struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	At            string `json:"at,omitempty"`
	SourceRef     Ref    `json:"source_ref"`
	Tool          string `json:"tool,omitempty"`
	Command       string `json:"command,omitempty"`
	Status        string `json:"status,omitempty"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	OutputExcerpt string `json:"output_excerpt,omitempty"`
}

type Finding struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Title        string     `json:"title"`
	Detail       string     `json:"detail"`
	Severity     string     `json:"severity"`
	Confidence   string     `json:"confidence"`
	Ongoing      bool       `json:"ongoing"`
	StartedAt    string     `json:"started_at,omitempty"`
	EndedAt      string     `json:"ended_at,omitempty"`
	Count        int        `json:"count"`
	Tokens       *Tokens    `json:"tokens"`
	SegmentIDs   []string   `json:"segment_ids"`
	CallIDs      []string   `json:"call_ids"`
	ExecutionIDs []string   `json:"execution_ids"`
	Evidence     []Evidence `json:"evidence"`
	Review       string     `json:"review"`
	Rule         string     `json:"rule"`
}

type Freshness struct {
	LastObservedAt string   `json:"last_observed_at,omitempty"`
	LastIngestedAt string   `json:"last_ingested_at,omitempty"`
	LastUsageAt    string   `json:"last_usage_at,omitempty"`
	AgeMs          *int64   `json:"age_ms"`
	Stale          bool     `json:"stale"`
	Capabilities   []string `json:"capabilities"`
	Missing        []string `json:"missing"`
}

// These lanes may overlap. They are deliberately not exposed as portions
// of a pie chart; only interval union can establish elapsed time.
type TimeSummary struct {
	PromptElapsedMs *int64 `json:"prompt_elapsed_ms"`
	ToolElapsedMs   *int64 `json:"tool_elapsed_ms"`
	InvocationMs    *int64 `json:"invocation_ms"`
	DecisionWaitMs  *int64 `json:"decision_wait_ms"`
	UnallocatedMs   *int64 `json:"unallocated_ms"`
}
