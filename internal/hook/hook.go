// Package hook implements the Mate side of Claude Code's own hooks
// (docs/mvp.md task 08): UserPromptSubmit and Stop. Both handlers are pure
// functions of an already-open *store.Workspace, a project name, and the
// hook's raw JSON stdin payload, so the CLI glue in cmd/matev2 - which
// resolves the workspace and project and never blocks a prompt on a
// failure here - is the only part that touches the process environment.
package hook

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// PromptMarker is the sentinel the app prefixes onto every line it types
// into Mate's own pane (docs/mvp.md section 4), so Mate - and this hook -
// can tell an app-injected line apart from the user's own typing. It must
// stay equal to send.Marker; internal/send cannot be imported here (it pulls
// in the runtime/harness stack this pure hook package must not depend on),
// so the two are kept in sync by hook_test.go's TestPromptMarkerMatchesSend.
//
// This used to be the single control byte 0x1f: a task 15 live run found
// that byte never reached Claude Code's own UserPromptSubmit payload
// (send.Marker's doc comment has the measurement). legacyPromptMarker is
// still recognised for one release so an in-flight digest sent with the old
// byte, from a Mate started before this change, is still read as the app's.
const PromptMarker = "⟦matev2⟧ "

// legacyPromptMarker is the abandoned 0x1f byte, tolerated as a leading
// marker for one release (see PromptMarker's doc comment). Remove this once
// no Mate still running predates the sentinel switch.
const legacyPromptMarker = 0x1f

// AutoOffText is the second sent.log line HandlePrompt appends when a plain
// user prompt (no marker) turns auto mode off.
const AutoOffText = "auto mode off: user prompt"

// maxLastAssistantRunes bounds the Stop hook's recorded answer so one huge
// turn cannot blow up sent.log or the message box that reads it.
const maxLastAssistantRunes = 2000

// promptPayload is the subset of Claude Code's UserPromptSubmit payload this
// hook reads (docs/phase1/inbox-protocol.md question 1: the payload also
// carries session_id, which mate-prompt does not need).
type promptPayload struct {
	Prompt string `json:"prompt"`
}

// stopPayload is the subset of Claude Code's Stop payload this hook reads
// (herdr_claude_settings_live_test.go: session_id, transcript_path,
// hook_event_name, last_assistant_message).
type stopPayload struct {
	SessionID            string `json:"session_id"`
	TranscriptPath       string `json:"transcript_path"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

// HandlePrompt implements `matev2 hook mate-prompt`. It appends one
// sent.log line for the prompt and, when a plain user prompt (no marker)
// finds auto mode on, clears `.auto` and appends a second app line
// recording that.
//
// A marker-prefixed prompt is the app's own doing (docs/mvp.md section 5's
// auto-mode digest, or its user-mode confirmation send): it is recorded as
// Source: app with the marker stripped, and never turns auto mode off,
// because the sentinel's whole purpose is telling the difference.
func HandlePrompt(w *store.Workspace, project string, raw []byte) error {
	var payload promptPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("hook: malformed UserPromptSubmit payload: %w", err)
	}
	prompt := payload.Prompt
	source := store.SourceUser
	text := prompt
	switch {
	case strings.HasPrefix(prompt, PromptMarker):
		source = store.SourceApp
		text = strings.TrimPrefix(prompt, PromptMarker)
	case len(prompt) > 0 && prompt[0] == legacyPromptMarker:
		source = store.SourceApp
		text = prompt[1:]
	}
	if err := w.AppendSent(project, store.SentEntry{Source: source, Target: store.TargetMate, Text: text}); err != nil {
		return fmt.Errorf("hook: append sent.log: %w", err)
	}
	if source != store.SourceUser {
		return nil
	}
	if !w.Auto(project) {
		return nil
	}
	if err := w.SetAuto(project, false); err != nil {
		return fmt.Errorf("hook: clear .auto: %w", err)
	}
	if err := w.AppendSent(project, store.SentEntry{Source: store.SourceApp, Target: store.TargetMate, Text: AutoOffText}); err != nil {
		return fmt.Errorf("hook: append auto-off sent.log: %w", err)
	}
	return nil
}

// HandleStop implements `matev2 hook mate-stop`. It appends one sent.log
// line for Claude's last answer and updates `mate.meta` with the session id
// and transcript path the payload carries, keeping every other key as-is.
func HandleStop(w *store.Workspace, project string, raw []byte) error {
	var payload stopPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("hook: malformed Stop payload: %w", err)
	}
	text := truncateRunes(oneLine(payload.LastAssistantMessage), maxLastAssistantRunes)
	if err := w.AppendSent(project, store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Text: text}); err != nil {
		return fmt.Errorf("hook: append sent.log: %w", err)
	}
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		return fmt.Errorf("hook: read mate.meta: %w", err)
	}
	meta[spawn.MetaSessionID] = payload.SessionID
	meta[spawn.MetaTranscript] = payload.TranscriptPath
	if err := w.WriteMateMeta(project, meta); err != nil {
		return fmt.Errorf("hook: write mate.meta: %w", err)
	}
	return nil
}

// sessionPayload is the subset of a SessionStart payload this hook reads.
// Claude Code 2.1.281 and codex-cli 0.154/0.156.1 both send `source` and
// `session_id` (docs/mvp.md section 7, task 35, A2 and A3), and both a
// `transcript_path`: Claude's session transcript, Codex's rollout.
type sessionPayload struct {
	Source         string `json:"source"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// The SessionStart sources the hook treats by name. Anything else, or no
// source at all, is read as a fresh start (docs/research/firstmate-memory-
// 2026-09-24.md B2, after firstmate's sessionstart-nudge.md): printing the
// whole digest redundantly is cheap, missing it is the bug the hook exists
// to prevent.
const (
	SourceStartup = "startup"
	SourceResume  = "resume"
	SourceClear   = "clear"
	SourceCompact = "compact"
)

// SessionStart is what one SessionStart payload said.
type SessionStart struct {
	// Source is the payload's own word, or "" when it named none.
	Source string
	// SessionID and TranscriptPath are the session the hook fired in.
	SessionID      string
	TranscriptPath string
	// Recorded is true when mate.meta was updated with SessionID.
	Recorded bool
}

// LiveOnly reports whether the digest for this start is part 1 only. A
// resumed conversation still holds everything the files said; what may
// have moved while the Mate was stopped is the crews, the inbox and the
// outbox. Every other source lost the digest from context, or never had it.
func (s SessionStart) LiveOnly() bool { return s.Source == SourceResume }

// HandleSessionStart implements the record half of `matev2 hook
// mate-session`: it parses the payload and, when `mate.meta` records a
// Mate, writes the payload's session id and transcript into it, keeping
// every other key.
//
// This closes the gap task 35 measured: `/clear` mints a new Claude session
// id, and until now only the Stop hook of the first turn after it recorded
// the id, so a restart in between resumed the conversation from before the
// clear. A Codex Mate's id is its rollout's uuid, which exists only once the
// first prompt opens the rollout - exactly when Codex runs this hook.
//
// A meta with no `harness=` is not a Mate's record (a first start has not
// written one yet, and StartMate writes the same id itself), so it is left
// alone. A payload that does not parse is still a session start: the caller
// prints the full digest.
func HandleSessionStart(w *store.Workspace, project string, raw []byte) (SessionStart, error) {
	var payload sessionPayload
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			return SessionStart{}, fmt.Errorf("hook: malformed SessionStart payload: %w", err)
		}
	}
	out := SessionStart{
		Source:         strings.TrimSpace(payload.Source),
		SessionID:      strings.TrimSpace(payload.SessionID),
		TranscriptPath: strings.TrimSpace(payload.TranscriptPath),
	}
	if out.SessionID == "" {
		return out, nil
	}
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		return out, fmt.Errorf("hook: read mate.meta: %w", err)
	}
	if strings.TrimSpace(meta[spawn.MetaHarness]) == "" {
		return out, nil
	}
	if meta[spawn.MetaSessionID] == out.SessionID && (out.TranscriptPath == "" || meta[spawn.MetaTranscript] == out.TranscriptPath) {
		return out, nil
	}
	meta[spawn.MetaSessionID] = out.SessionID
	if out.TranscriptPath != "" {
		meta[spawn.MetaTranscript] = out.TranscriptPath
	}
	if err := w.WriteMateMeta(project, meta); err != nil {
		return out, fmt.Errorf("hook: write mate.meta: %w", err)
	}
	out.Recorded = true
	return out, nil
}

// oneLine flattens text the way store.AppendSent will anyway (log.go's
// unexported oneLine), so a rune-count truncation below counts the line
// sent.log will actually hold rather than one a stray newline could shift.
func oneLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
}

// truncateRunes returns s unchanged if it holds at most max runes, or its
// first max runes followed by an ellipsis.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
