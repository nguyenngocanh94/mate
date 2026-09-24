package console

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// ADR 0025: "Mate/Crew dùng harness_kind đã ghi nhận để chọn parser/
// rendering profile. Không có parser chung ép Claude và Codex thành một
// style; nếu provider output không parse được, hiển thị plain bounded text
// và trạng thái unknown." This file is that per-harness profile dispatch,
// plus the one profile that exists today.
//
// claudeCodeTurnMarker/ResultMarker/StatusMarker are the literal glyphs the
// real claude-code CLI itself draws in its terminal UI (design/mate-tui.js's
// GLYPHS.unicode: bul/elb/spark) - not this package's own glyphSet. The two
// happen to agree for Bullet and Spark, but glyphSet.Elbow ("⎟") is a
// deliberate rendering divergence from the harness's own elbow ("⎿"), per
// session-view-contract.md - a parser reading the harness's real output
// must match what the harness actually emits, not what this package chooses
// to draw when it renders a parsed entry back out.
const (
	claudeCodeTurnMarker   = "⏺"
	claudeCodeResultMarker = "⎿"
	claudeCodeStatusMarker = "✻"
)

// ParseTranscript is the harness_kind -> parsing profile dispatch. Only
// query.HarnessClaude has a profile today; every other kind - Codex
// included, whose plainer prompt-and-log shape is deliberately out of scope
// (session-view-contract.md) - falls back to SessionTranscriptUnknown, so
// its Raw is rendered as plain bounded text rather than guessed at.
func ParseTranscript(kind query.HarnessKind, raw string) SessionTranscript {
	t := SessionTranscript{
		Source:      SessionTranscriptPolled,
		HarnessKind: kind,
		Raw:         raw,
		Status:      SessionTranscriptUnknown,
	}
	if kind != query.HarnessClaude {
		return t
	}
	entries, ok := parseClaudeCodeTranscript(raw)
	if !ok {
		return t
	}
	t.Status = SessionTranscriptParsed
	t.Entries = entries
	return t
}

// parseClaudeCodeTranscript reads claude-code's own terminal shape: a unit
// starts on a line whose first non-space content is the turn, result or
// status marker; a blank line ends the current unit and becomes a Gap; any
// other line continues the unit currently open (the harness itself wraps
// its own long lines, so a continuation is rejoined with a single space
// rather than kept as a hard break - the renderer re-wraps at its own pane
// width). Raw output with no recognised marker anywhere is refused
// (ok=false): a caller with truly unstructured output (a login shell, a
// crashed process, a harness this profile does not actually know) must
// fall back to plain bounded text rather than be guessed at, per ADR 0025.
func parseClaudeCodeTranscript(raw string) ([]SessionTranscriptEntry, bool) {
	clean := strings.ReplaceAll(ansi.Strip(raw), "\r\n", "\n")
	lines := strings.Split(clean, "\n")

	var entries []SessionTranscriptEntry
	var cur *SessionTranscriptEntry
	sawMarker := false

	flush := func() {
		if cur != nil {
			entries = append(entries, *cur)
			cur = nil
		}
	}

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			flush()
			entries = append(entries, SessionTranscriptEntry{Kind: SessionTranscriptEntryGap})
			continue
		}
		if kind, text, ok := stripClaudeCodeMarker(trimmed); ok {
			flush()
			sawMarker = true
			e := SessionTranscriptEntry{Kind: kind, Text: text}
			if kind == SessionTranscriptEntryStatus {
				e.Text, e.Hint = splitStatusHint(text)
			}
			cur = &e
			continue
		}
		if cur == nil {
			return nil, false
		}
		if cur.Text == "" {
			cur.Text = trimmed
		} else {
			cur.Text += " " + trimmed
		}
	}
	flush()

	if !sawMarker {
		return nil, false
	}
	return trimTrailingGaps(entries), true
}

func stripClaudeCodeMarker(line string) (SessionTranscriptEntryKind, string, bool) {
	switch {
	case strings.HasPrefix(line, claudeCodeTurnMarker):
		return SessionTranscriptEntryTurn, strings.TrimSpace(strings.TrimPrefix(line, claudeCodeTurnMarker)), true
	case strings.HasPrefix(line, claudeCodeResultMarker):
		return SessionTranscriptEntryResult, strings.TrimSpace(strings.TrimPrefix(line, claudeCodeResultMarker)), true
	case strings.HasPrefix(line, claudeCodeStatusMarker):
		return SessionTranscriptEntryStatus, strings.TrimSpace(strings.TrimPrefix(line, claudeCodeStatusMarker)), true
	default:
		return "", "", false
	}
}

// splitStatusHint separates a status line's trailing parenthesised hint
// ("(esc to interrupt)") from its main text, matching the shape the
// renderer draws a Status entry in: Fg text, then a Dim hint.
func splitStatusHint(text string) (main, hint string) {
	if i := strings.LastIndex(text, " ("); i >= 0 && strings.HasSuffix(text, ")") {
		return text[:i], text[i:]
	}
	return text, ""
}

// trimTrailingGaps drops Gap entries at the very end - a transcript's tail
// is content, not the blank lines a bounded snapshot happened to capture
// after it.
func trimTrailingGaps(entries []SessionTranscriptEntry) []SessionTranscriptEntry {
	i := len(entries)
	for i > 0 && entries[i-1].Kind == SessionTranscriptEntryGap {
		i--
	}
	return entries[:i]
}
