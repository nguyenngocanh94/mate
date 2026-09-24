package brief

import (
	"errors"
	"strings"
	"time"
)

// AddedLayout is the date line `brief append` writes above the captain's
// later words. It is provenance, not a speaker label: it says when, and the
// section heading already says who.
const AddedLayout = "2006-01-02 15:04 MST"

// AddedLine is the line written above words appended at t.
func AddedLine(t time.Time) string { return "(added " + t.Format(AddedLayout) + ")" }

// ErrNoCaptainsWords means the brief has no `## Captain's words` under its
// `# Task`, so there is nowhere for the captain's later words to go.
var ErrNoCaptainsWords = errors.New("brief has no ## " + CaptainsWords + " section under " + TaskHeading)

// AppendCaptainsWords returns brief with words appended to the end of its
// `## Captain's words` section, below a date line for at, and every other
// byte unchanged. This is firstmate's "When the captain adds or changes an
// ask mid-task, append the captain's words without added speaker labels or
// direct address to that brief's `## Captain's intent`" (AGENTS.md 376),
// done by the app rather than by the Mate, because the brief the Crew reads
// lives under `crews/`, where the Mate may not write.
//
// brief may be a rendered brief or the Mate's own file; either way the
// section is found under `# Task` when there is one. words are written as
// given, trailing blank lines trimmed; nothing in them is rewritten.
func AppendCaptainsWords(brief, words string, at time.Time) (string, error) {
	words = strings.TrimRight(strings.ReplaceAll(words, "\r\n", "\n"), "\n \t")
	if strings.TrimSpace(words) == "" {
		return "", errors.New("the captain's words are empty")
	}
	lines := parseLines(brief)
	start, end := captainsWordsSpan(lines)
	if start < 0 {
		return "", ErrNoCaptainsWords
	}
	// Insert after the section's last non-blank line, so the blank lines
	// that separated it from the next heading still separate the new words.
	last := start
	for i := start + 1; i < end; i++ {
		if lines[i].trimmed != "" {
			last = i
		}
	}
	raw := make([]string, 0, len(lines)+4)
	for _, l := range lines[:last+1] {
		raw = append(raw, l.text)
	}
	raw = append(raw, "", AddedLine(at), words)
	for _, l := range lines[last+1:] {
		raw = append(raw, l.text)
	}
	return strings.Join(raw, "\n"), nil
}

// captainsWordsSpan finds the `## Captain's words` heading and the index of
// the line that ends its section: the next recognised section heading or
// level-one heading, or the end of the text. Inside a rendered brief the
// search starts at `# Task`, so a heading of the same name elsewhere in the
// template could never be the one written to.
func captainsWordsSpan(lines []line) (start, end int) {
	from := 0
	for i, l := range lines {
		if l.level1 && l.trimmed == TaskHeading {
			from = i + 1
			break
		}
	}
	start = -1
	for i := from; i < len(lines); i++ {
		l := lines[i]
		if start < 0 {
			if l.level1 {
				return -1, -1 // left # Task without finding it
			}
			if l.section == CaptainsWords {
				start = i
			}
			continue
		}
		if l.level1 || l.section != "" {
			return start, i
		}
	}
	if start < 0 {
		return -1, -1
	}
	return start, len(lines)
}
