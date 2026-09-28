// Package telemetry defines observer facts used to diagnose harness behaviour.
// Usage here describes source evidence; turn remains the token ledger.
package telemetry

import "time"

const Version = 1

// Fact is one immutable observation. An execution can have multiple observations
// with the same ID, allowing a result arriving in a later poll to complete it.
// Unknown measurements are omitted, never encoded as a successful zero.
type Fact struct {
	Version             int        `json:"version"`
	ID                  string     `json:"id"`
	Kind                string     `json:"kind"`
	SessionID           string     `json:"session_id,omitempty"`
	HarnessTurnRef      string     `json:"harness_turn_ref,omitempty"`
	RootTurnRef         string     `json:"root_turn_ref,omitempty"`
	SourceRef           string     `json:"source_ref"`
	SourcePath          string     `json:"source_path,omitempty"`
	SourceOffset        int64      `json:"source_offset"`
	OccurredAt          time.Time  `json:"occurred_at"`
	ObservedAt          time.Time  `json:"observed_at"`
	MeasurementKind     string     `json:"measurement_kind"`
	Model               string     `json:"model,omitempty"`
	Effort              string     `json:"effort,omitempty"`
	HarnessVersion      string     `json:"harness_version,omitempty"`
	ContextWindowTokens *int64     `json:"context_window_tokens,omitempty"`
	Text                string     `json:"text,omitempty"`
	Phase               string     `json:"phase,omitempty"`
	ResponseID          string     `json:"response_id,omitempty"`
	InputTokens         int64      `json:"input_tokens,omitempty"`
	CacheReadTokens     int64      `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens    int64      `json:"cache_write_tokens,omitempty"`
	OutputTokens        int64      `json:"output_tokens,omitempty"`
	ReasoningTokens     int64      `json:"reasoning_tokens,omitempty"`
	ContextTokens       int64      `json:"context_tokens,omitempty"`
	LedgerRefOffset     *int64     `json:"ledger_ref_offset,omitempty"`
	ExecutionID         string     `json:"execution_id,omitempty"`
	ProcessID           string     `json:"process_id,omitempty"`
	WrapperRef          string     `json:"wrapper_ref,omitempty"`
	Tool                string     `json:"tool,omitempty"`
	Command             string     `json:"command,omitempty"`
	CWD                 string     `json:"cwd,omitempty"`
	Targets             []Target   `json:"targets,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	DurationMS          *int64     `json:"duration_ms,omitempty"`
	ExitCode            *int       `json:"exit_code,omitempty"`
	Status              string     `json:"status,omitempty"`
	Poll                bool       `json:"poll,omitempty"`
	Output              *Output    `json:"output,omitempty"`
	Changes             []Change   `json:"changes,omitempty"`
	Gaps                []string   `json:"gaps,omitempty"`
}

type Target struct {
	Path  string `json:"path,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Query string `json:"query,omitempty"`
	Range string `json:"range,omitempty"`
}

type Output struct {
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
	NewBytes  *int64 `json:"new_bytes,omitempty"`
	Truncated *bool  `json:"truncated,omitempty"`
	Preview   string `json:"preview,omitempty"`
}

type Change struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
	Bytes  int64  `json:"bytes"`
}
