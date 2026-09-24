// Package memory owns the shape of the Mate's own memory (docs/mvp.md M8,
// docs/research/firstmate-memory-2026-09-24.md B3, B5, B8): the one-line
// entry `mate remember` writes into `mate/memory.md`, and the shape gate
// `mate memory check` runs over it and over `PROJECT.md`.
//
// It follows firstmate's split (VISION.md "Scripts own the mechanics, agents
// own the judgment"): this package formats, parses, dates and counts, and
// never decides what an entry means. It never merges, rewrites or deletes an
// entry; the Mate does that itself when it curates with the `stow` skill.
package memory

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The two sections of memory.md and what each defaults to.
const (
	// CaptainSection holds the captain's preferences, working style and
	// authority boundaries. Entries are pinned by default and carry no
	// marker: preferences do not age.
	CaptainSection = "Captain"
	// LessonsSection holds operational lessons about Crews, harnesses and
	// the app on this project. Entries are aging by default and always
	// carry a dated marker.
	LessonsSection = "Lessons"
)

// Sections is every section memory.md may hold, in file order.
var Sections = []string{CaptainSection, LessonsSection}

// Header is what a fresh memory.md starts with: the title, the one-line
// pointer naming the stow skill as the owner of the tier scheme (firstmate
// `.agents/skills/stow/SKILL.md`, "Marking rules"), and the two sections.
const Header = "# Memory\n" + HeaderPointer + "\n\n## " + CaptainSection + "\n\n## " + LessonsSection + "\n"

// HeaderPointer names the scheme owner and nothing else; the tiers, markers
// and clocks live in the stow skill only.
const HeaderPointer = "<!-- memory tiers: see the stow skill -->"

// Tier is how an entry decays.
type Tier string

const (
	// Pinned never decays and carries no marker.
	Pinned Tier = "pinned"
	// Aging is stale AgingDays after its last-reinforced date.
	Aging Tier = "aging"
	// Perishable is stale PerishableDays after its last-reinforced date and
	// names a checkable expiry condition.
	Perishable Tier = "perishable"
)

// The clocks, firstmate's defaults: an entry whose age is greater than or
// equal to these many days since its last-reinforced date is stale.
const (
	AgingDays      = 30
	PerishableDays = 7
)

// DateLayout is the marker's date.
const DateLayout = "2006-01-02"

// ExpiresPrefix opens the parenthesised expiry condition of a perishable
// entry, which sits just before its source.
const ExpiresPrefix = "expires: "

// Entry is one memory line.
type Entry struct {
	// Text is the fact itself, one line.
	Text string
	// Source is where it came from: the captain and a date, or a path
	// relative to the project directory and its section.
	Source string
	// Tier is how the entry decays.
	Tier Tier
	// Date is the last-reinforced date, YYYY-MM-DD; empty for pinned.
	Date string
	// Expiry is a perishable entry's checkable expiry condition.
	Expiry string
}

// Line renders e in the one shape memory.md holds:
//
//   - <text> (<source>)
//   - <text> (<source>) <!--a:YYYY-MM-DD-->
//   - <text> (expires: <condition>) (<source>) <!--p:YYYY-MM-DD-->
func (e Entry) Line() string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(e.Text)
	if e.Tier == Perishable {
		b.WriteString(" (" + ExpiresPrefix + e.Expiry + ")")
	}
	b.WriteString(" (" + e.Source + ")")
	if m := e.Marker(); m != "" {
		b.WriteString(" " + m)
	}
	return b.String()
}

// Marker is the entry's trailing tier marker, or "" for pinned.
func (e Entry) Marker() string {
	switch e.Tier {
	case Aging:
		return "<!--a:" + e.Date + "-->"
	case Perishable:
		return "<!--p:" + e.Date + "-->"
	}
	return ""
}

// NewEntry builds the entry `mate remember` writes, dated today, and
// refuses any part that would not survive a round trip through Parse: a
// second line, a marker of its own, a parenthesis in the source or the
// expiry. The section decides the default tier: pinned for the captain,
// aging for a lesson, perishable only when an expiry condition is named.
func NewEntry(section, text, source, expiry string, today time.Time) (Entry, error) {
	text, source, expiry = strings.TrimSpace(text), strings.TrimSpace(source), strings.TrimSpace(expiry)
	if err := oneLine("the entry", text); err != nil {
		return Entry{}, err
	}
	if strings.HasPrefix(text, "- ") || strings.HasPrefix(text, "#") {
		return Entry{}, errors.New(`the entry is the fact itself: drop the leading "- " or "#"`)
	}
	if err := oneLine("--source", source); err != nil {
		return Entry{}, err
	}
	if strings.ContainsAny(source, "()") {
		return Entry{}, errors.New("--source may not contain parentheses: they delimit it in the entry")
	}
	e := Entry{Text: text, Source: source}
	switch section {
	case CaptainSection:
		if expiry != "" {
			return Entry{}, errors.New("--perishable goes with --lesson: a captain preference does not expire")
		}
		e.Tier = Pinned
	case LessonsSection:
		e.Tier, e.Date = Aging, today.Format(DateLayout)
		if expiry != "" {
			if err := oneLine("--perishable", expiry); err != nil {
				return Entry{}, err
			}
			if strings.ContainsAny(expiry, "()") {
				return Entry{}, errors.New("--perishable may not contain parentheses: they delimit it in the entry")
			}
			e.Tier, e.Expiry = Perishable, expiry
		}
	default:
		return Entry{}, fmt.Errorf("unknown memory section %q", section)
	}
	return e, nil
}

func oneLine(what, s string) error {
	switch {
	case s == "":
		return fmt.Errorf("%s is empty", what)
	case strings.ContainsAny(s, "\r\n"):
		return fmt.Errorf("%s must be one line", what)
	case strings.Contains(s, "<!--") || strings.Contains(s, "-->"):
		return fmt.Errorf("%s may not contain an HTML comment: the tier marker is the app's to write", what)
	}
	return nil
}

// Append returns content with e's line added at the end of its section,
// creating the section when it is absent (Captain before Lessons) and the
// whole file when content is empty. Nothing already in content is merged,
// reordered or removed.
func Append(content, section string, e Entry) string {
	if strings.TrimSpace(content) == "" {
		content = Header
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	heading := "## " + section
	start := -1
	for i, l := range lines {
		if strings.TrimRight(l, " \t") == heading {
			start = i
			break
		}
	}
	if start < 0 {
		// A missing Captain section goes in front of Lessons; anything else
		// goes at the end.
		insertAt := len(lines)
		if section == CaptainSection {
			for i, l := range lines {
				if strings.TrimRight(l, " \t") == "## "+LessonsSection {
					insertAt = i
					break
				}
			}
		}
		block := []string{heading, e.Line()}
		if insertAt == len(lines) {
			block = append([]string{""}, block...)
		} else {
			block = append(block, "")
		}
		lines = append(lines[:insertAt], append(block, lines[insertAt:]...)...)
		return strings.Join(lines, "\n") + "\n"
	}
	// The section ends at the next heading; the entry goes after its last
	// non-blank line.
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "#") {
			end = i
			break
		}
	}
	at := start + 1
	for i := end - 1; i > start; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			at = i + 1
			break
		}
	}
	out := append([]string{}, lines[:at]...)
	out = append(out, e.Line())
	if at == end && end < len(lines) {
		out = append(out, "")
	}
	out = append(out, lines[at:]...)
	return strings.Join(out, "\n") + "\n"
}
