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

// PromptMarker is the byte the app prefixes onto every line it types into
// Mate's own pane (docs/mvp.md section 4), so Mate - and this hook - can
// tell an app-injected line apart from the user's own typing.
const PromptMarker = 0x1f

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
// because the byte's whole purpose is telling the difference.
func HandlePrompt(w *store.Workspace, project string, raw []byte) error {
	var payload promptPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("hook: malformed UserPromptSubmit payload: %w", err)
	}
	prompt := payload.Prompt
	source := store.SourceUser
	text := prompt
	if len(prompt) > 0 && prompt[0] == PromptMarker {
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
