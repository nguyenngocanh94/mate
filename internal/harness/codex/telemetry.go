package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

type TelemetryBatch struct {
	Facts    []telemetry.Fact
	State    harness.TelemetryState
	Consumed int64
	Error    string
}

// ParseCodexTelemetry extracts native observations independently of the token
// ledger parser. It never interprets or stores the contents of Reasoning items.
// Offsets are absolute, and a final partial line is retained for the next poll.
func ParseCodexTelemetry(state harness.TelemetryState, data []byte, offset int64) TelemetryBatch {
	b := TelemetryBatch{State: state}
	if b.State.Wrappers == nil {
		b.State.Wrappers = map[string]telemetry.Fact{}
	}
	if b.State.Responses == nil {
		b.State.Responses = map[string]telemetry.Fact{}
	}
	for pos := 0; pos < len(data); {
		n := bytes.IndexByte(data[pos:], '\n')
		if n < 0 {
			break
		}
		line := bytes.TrimSpace(data[pos : pos+n])
		start := offset + int64(pos)
		if len(line) == 0 {
			pos += n + 1
			b.Consumed = int64(pos)
			continue
		}
		var env struct {
			Timestamp time.Time       `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(line, &env); err != nil || env.Type == "" || env.Timestamp.IsZero() {
			b.Error = fmt.Sprintf("invalid telemetry record at byte %d", start)
			break
		}
		var p telemetryPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			b.Error = fmt.Sprintf("invalid telemetry payload at byte %d", start)
			break
		}
		f := telemetry.Fact{Version: telemetry.Version, ID: fmt.Sprint(start), SourceRef: fmt.Sprint(start), SourceOffset: start, OccurredAt: env.Timestamp, MeasurementKind: "native", Model: b.State.Model, Effort: b.State.Effort}
		switch env.Type {
		case "session_meta":
			f.Kind, f.Phase, f.HarnessVersion = "context", "session", p.CLIVersion
			b.Facts = append(b.Facts, f)
			if p.BaseInstructions.Text != "" {
				f.ID += ":base_instructions"
				f.Phase, f.Text = "instruction_input", "base_instructions"
				f.Output = &telemetry.Output{Bytes: int64(len(p.BaseInstructions.Text)), SHA256: harness.HashText(p.BaseInstructions.Text)}
				f.Targets = []telemetry.Target{{Kind: "base_instructions"}}
				f.Gaps = []string{"instruction_token_count_unavailable"}
				b.Facts = append(b.Facts, f)
			}
		case "world_state":
			var world struct {
				State struct {
					Agents struct {
						Directory string `json:"directory"`
						Text      string `json:"text"`
					} `json:"agents_md"`
					Skills struct {
						Body string `json:"body"`
					} `json:"host_skills"`
				} `json:"state"`
			}
			if json.Unmarshal(env.Payload, &world) == nil {
				for _, input := range []struct{ kind, path, text string }{{"agents_md", world.State.Agents.Directory, world.State.Agents.Text}, {"host_skills", "", world.State.Skills.Body}} {
					if input.text == "" {
						continue
					}
					observed := f
					observed.Kind, observed.Phase, observed.HarnessTurnRef = "context", "instruction_input", b.State.Turn
					observed.ID += ":" + input.kind
					observed.Text = input.kind
					observed.Targets = []telemetry.Target{{Path: input.path, Kind: input.kind}}
					observed.Output = &telemetry.Output{Bytes: int64(len(input.text)), SHA256: harness.HashText(input.text)}
					observed.Gaps = []string{"instruction_token_count_unavailable"}
					b.Facts = append(b.Facts, observed)
				}
			}
		case "turn_context":
			b.State.Model, b.State.Effort = p.Model, p.Effort
			f.Kind, f.Model, f.Effort, f.HarnessTurnRef, f.RootTurnRef, f.CWD = "context", p.Model, p.Effort, p.TurnID, p.RootTurnID, p.CWD
			f.Phase = "configuration"
			b.Facts = append(b.Facts, f)
		case "token_usage_record":
			if p.ResponseID != "" && p.Usage != nil {
				f.Kind, f.ID, f.SourceRef, f.ResponseID, f.HarnessTurnRef, f.RootTurnRef = "response", p.ResponseID, p.ResponseID, p.ResponseID, p.TurnID, p.RootTurnID
				f.InputTokens = p.Usage.Input - p.Usage.Cached
				if f.InputTokens < 0 {
					f.InputTokens = 0
					f.Gaps = append(f.Gaps, "cached_input_exceeds_input")
				}
				f.CacheReadTokens, f.CacheWriteTokens, f.OutputTokens, f.ReasoningTokens, f.ContextTokens = p.Usage.Cached, p.Usage.CacheWrite, p.Usage.Output, p.Usage.Reasoning, p.Usage.Input+p.Usage.CacheWrite
				b.Facts = append(b.Facts, f)
				if p.ThreadUsage != nil {
					b.State.Responses[p.ThreadUsage.key()] = f
					if len(b.State.Responses) > 128 {
						oldKey := ""
						var old telemetry.Fact
						for key, value := range b.State.Responses {
							if oldKey == "" || value.SourceOffset < old.SourceOffset {
								oldKey, old = key, value
							}
						}
						delete(b.State.Responses, oldKey)
						old.Kind = "gap"
						old.Gaps = []string{"native_usage_not_linked_to_ledger"}
						b.Facts = append(b.Facts, old)
					}
				}
			}
		case "event_msg":
			switch p.Type {
			case "task_started", "task_complete", "turn_aborted":
				f.HarnessTurnRef, f.RootTurnRef = p.TurnID, p.RootTurnID
				f.Kind = "turn_started"
				if p.Type != "task_started" {
					f.Kind = "turn_completed"
					f.Text = p.LastAgentMessage
					f.Status = p.Type
				}
				if p.StartedAt > 0 {
					t := time.Unix(p.StartedAt, 0).UTC()
					f.StartedAt = &t
				}
				if p.CompletedAt > 0 {
					t := time.Unix(p.CompletedAt, 0).UTC()
					f.CompletedAt = &t
				}
				f.DurationMS = p.DurationMS
				f.ContextWindowTokens = p.ModelContextWindow
				b.Facts = append(b.Facts, f)
				if p.Type == "task_started" {
					b.State.Turn = p.TurnID
				} else {
					b.State.Turn = ""
				}
			case "item_started", "item_completed":
				if p.Item == nil {
					break
				}
				it := p.Item
				f.ID, f.SourceRef, f.HarnessTurnRef = it.ID, it.ID, p.TurnID
				if f.ID == "" {
					f.ID, f.SourceRef = fmt.Sprint(start), fmt.Sprint(start)
				}
				if p.StartedMS != nil {
					t := time.UnixMilli(*p.StartedMS).UTC()
					f.StartedAt = &t
				}
				if p.CompletedMS != nil {
					t := time.UnixMilli(*p.CompletedMS).UTC()
					f.CompletedAt = &t
				}
				if p.Type == "item_started" && f.StartedAt == nil {
					at := env.Timestamp
					f.StartedAt = &at
				}
				switch it.Type {
				case "UserMessage", "AgentMessage":
					f.Kind = "prompt"
					if it.Type == "AgentMessage" {
						f.Kind = "message"
					}
					f.Text = harness.MessageText(it.Content)
					f.Phase = it.Phase
				case "CommandExecution":
					f.Kind, f.ExecutionID, f.ProcessID, f.Tool, f.Status = "execution", it.ID, it.ProcessID, "exec_command", it.Status
					if p.Type == "item_started" && f.Status == "" {
						f.Status = "running"
					}
					f.CWD = telemetryPath(it.CWD)
					f.Command = strings.Join(it.Command, " ")
					if len(it.Command) >= 3 && (it.Command[1] == "-lc" || it.Command[1] == "-c") {
						f.Command = it.Command[2]
					}
					f.ExitCode = it.ExitCode
					if it.Duration != nil {
						ms := it.Duration.Secs*1000 + it.Duration.Nanos/1000000
						f.DurationMS = &ms
					}
					for _, c := range it.Parsed {
						if c.Path != "" || c.Query != "" {
							f.Targets = append(f.Targets, telemetry.Target{Path: c.Path, Kind: c.Type, Query: c.Query})
						}
					}
					if it.Output != nil {
						f.Output = harness.OutputFact(*it.Output, nil)
					} else if it.Stdout != nil || it.Stderr != nil {
						s := ""
						if it.Stdout != nil {
							s += *it.Stdout
						}
						if it.Stderr != nil {
							s += *it.Stderr
						}
						f.Output = harness.OutputFact(s, nil)
					}
					f.Gaps = append(f.Gaps, "wrapper_parent_unavailable")
				case "FileChange":
					f.Kind, f.Status = "progress", it.Status
					f.Phase = "file_change"
					for path, c := range it.Changes {
						f.Changes = append(f.Changes, telemetry.Change{Path: path, Kind: c.Type, SHA256: harness.HashText(c.Diff), Bytes: int64(len(c.Diff))})
					}
					sort.Slice(f.Changes, func(i, j int) bool { return f.Changes[i].Path < f.Changes[j].Path })
				case "Reasoning":
					// No private reasoning content is persisted.
					f.Kind, f.Phase = "activity", "reasoning"
				case "ImageView":
					f.Kind, f.Tool = "activity", "view_image"
					f.Targets = []telemetry.Target{{Path: it.Path}}
				}
				if f.Kind != "" {
					b.Facts = append(b.Facts, f)
				}
			case "token_count":
				if p.Info.Total != nil {
					if prior, ok := b.State.Responses[p.Info.Total.key()]; ok {
						prior.LedgerRefOffset = &start
						b.Facts = append(b.Facts, prior)
						delete(b.State.Responses, p.Info.Total.key())
					}
				}
			case "context_compacted":
				f.Kind, f.Phase, f.HarnessTurnRef = "context", "compaction", p.TurnID
				b.Facts = append(b.Facts, f)
			}
		case "response_item":
			f.HarnessTurnRef = b.State.Turn
			switch p.Type {
			case "custom_tool_call", "function_call":
				f.Kind, f.ID, f.SourceRef, f.WrapperRef, f.Tool = "tool_call", p.CallID, p.CallID, p.CallID, p.Name
				f.Command = decodeTelemetryText(p.Input)
				if p.Type == "function_call" {
					f.Command = decodeTelemetryText(p.Arguments)
				}
				f.StartedAt = &env.Timestamp
				f.ProcessID, f.Poll = pollProcess(f.Tool, f.Command)
				b.State.Wrappers[p.CallID] = f
				b.Facts = append(b.Facts, f)
			case "custom_tool_call_output", "function_call_output":
				call, ok := b.State.Wrappers[p.CallID]
				f.Kind, f.ID, f.SourceRef, f.WrapperRef = "tool_result", p.CallID, p.CallID, p.CallID
				if ok {
					f.Tool, f.Command, f.ProcessID, f.Poll, f.HarnessTurnRef, f.StartedAt = call.Tool, call.Command, call.ProcessID, call.Poll, call.HarnessTurnRef, call.StartedAt
				}
				f.CompletedAt = &env.Timestamp
				f.Output = harness.OutputFact(codexOutputText(p.Output), nil)
				if output, exit, pid, ok := nestedCommandResult(codexOutputText(p.Output)); ok {
					f.ExitCode = exit
					if f.ProcessID == "" {
						f.ProcessID = pid
					}
					if f.Poll {
						n := int64(len(output))
						f.Output = harness.OutputFact(output, &n)
					}
				} else if f.Poll {
					f.Gaps = append(f.Gaps, "poll_output_unavailable")
				}
				if p.IsError != nil && *p.IsError {
					f.Status = "failed"
				}
				b.Facts = append(b.Facts, f)
				delete(b.State.Wrappers, p.CallID)
			}
		case "compacted":
			f.Kind, f.Phase, f.HarnessTurnRef = "context", "compaction", b.State.Turn
			b.Facts = append(b.Facts, f)
		}
		pos += n + 1
		b.Consumed = int64(pos)
	}
	return b
}

type telemetryUsage struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
	Total      int64 `json:"total_tokens"`
}

func (u telemetryUsage) key() string {
	return fmt.Sprintf("%d/%d/%d/%d/%d/%d", u.Input, u.Cached, u.CacheWrite, u.Output, u.Reasoning, u.Total)
}

type telemetryPayload struct {
	BaseInstructions struct {
		Text string `json:"text"`
	} `json:"base_instructions"`
	CLIVersion         string          `json:"cli_version"`
	ModelContextWindow *int64          `json:"model_context_window"`
	Type               string          `json:"type"`
	TurnID             string          `json:"turn_id"`
	RootTurnID         string          `json:"root_turn_id"`
	Model              string          `json:"model"`
	Effort             string          `json:"effort"`
	CWD                string          `json:"cwd"`
	ResponseID         string          `json:"response_id"`
	Usage              *telemetryUsage `json:"usage"`
	ThreadUsage        *telemetryUsage `json:"thread_token_usage"`
	Info               struct {
		Total *telemetryUsage `json:"total_token_usage"`
	} `json:"info"`
	StartedAt        int64           `json:"started_at"`
	CompletedAt      int64           `json:"completed_at"`
	DurationMS       *int64          `json:"duration_ms"`
	LastAgentMessage string          `json:"last_agent_message"`
	StartedMS        *int64          `json:"started_at_ms"`
	CompletedMS      *int64          `json:"completed_at_ms"`
	Item             *telemetryItem  `json:"item"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Input            json.RawMessage `json:"input"`
	Arguments        json.RawMessage `json:"arguments"`
	Output           json.RawMessage `json:"output"`
	IsError          *bool           `json:"is_error"`
}
type telemetryItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ProcessID string          `json:"process_id"`
	Command   []string        `json:"command"`
	CWD       string          `json:"cwd"`
	Status    string          `json:"status"`
	ExitCode  *int            `json:"exit_code"`
	Output    *string         `json:"aggregated_output"`
	Stdout    *string         `json:"stdout"`
	Stderr    *string         `json:"stderr"`
	Path      string          `json:"path"`
	Content   json.RawMessage `json:"content"`
	Phase     string          `json:"phase"`
	Duration  *struct {
		Secs  int64 `json:"secs"`
		Nanos int64 `json:"nanos"`
	} `json:"duration"`
	Parsed []struct {
		Type  string `json:"type"`
		Path  string `json:"path"`
		Query string `json:"query"`
	} `json:"parsed_cmd"`
	Changes map[string]struct {
		Type string `json:"type"`
		Diff string `json:"unified_diff"`
	} `json:"changes"`
}

func telemetryPath(s string) string {
	if u, err := url.Parse(s); err == nil && u.Scheme == "file" {
		return u.Path
	}
	return s
}
func decodeTelemetryText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

var pollSessionRE = regexp.MustCompile(`(?:session_id|["']session_id["'])\s*:\s*["']?([0-9]+)`)

func pollProcess(tool, input string) (string, bool) {
	poll := tool == "write_stdin" || strings.Contains(input, ".write_stdin(") || strings.Contains(input, ".write_stdin (")
	if !poll {
		return "", false
	}
	matches := pollSessionRE.FindAllStringSubmatch(input, -1)
	id := ""
	for _, m := range matches {
		if id != "" && id != m[1] {
			return "", true
		}
		id = m[1]
	}
	return id, true
}

// nestedCommandResult reads only structured result objects emitted by the tool.
// Wrapper prose is not output progress. Multiple child results are ambiguous.
func nestedCommandResult(s string) (string, *int, string, bool) {
	var found []map[string]json.RawMessage
	for pos := 0; pos < len(s); pos++ {
		if s[pos] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(s[pos:]))
		var m map[string]json.RawMessage
		if dec.Decode(&m) != nil {
			continue
		}
		if _, ok := m["output"]; ok {
			if _, a := m["chunk_id"]; a {
				found = append(found, m)
				pos += int(dec.InputOffset()) - 1
			}
		}
	}
	if len(found) != 1 {
		return "", nil, "", false
	}
	m := found[0]
	var output string
	if json.Unmarshal(m["output"], &output) != nil {
		return "", nil, "", false
	}
	var exit *int
	_ = json.Unmarshal(m["exit_code"], &exit)
	var pid json.Number
	_ = json.Unmarshal(m["session_id"], &pid)
	return output, exit, pid.String(), true
}
