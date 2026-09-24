// Package brief owns the shape of the `# Task` part of a Crew brief: the
// sections the Mate fills (docs/mvp.md M7, schema from
// docs/research/firstmate-prompting-2026-09-24.md section 11), the machine
// check `matev2 brief check` and `matev2 crew spawn` run on it, and the one
// edit the app makes to a brief after spawn, `matev2 brief append`.
//
// The check is shape only, never meaning, after firstmate's "Logic that can
// be exact lives in deterministic scripts; work that requires understanding
// lives in an agent" (VISION.md): it decides by headings, list items and a
// handful of literal markers, and it never judges whether a sentence is a
// good acceptance criterion. firstmate's own check (bin/fm-spawn.sh 2830-2842)
// covers two sections; matev2 checks more because its Mate reviews without a
// pipeline and writes briefs without reading the repository (decision 1), so
// `## Acceptance` and `## Open decisions` carry what firstmate gets from
// no-mistakes and from reading the code.
package brief

import (
	"fmt"
	"regexp"
	"strings"
)

// The section names the Mate fills under `# Task`. They are the single
// source of truth: the check enforces exactly these, the crew template and
// the Mate's manual spell them, and internal/mateassets tests that the
// manual's section 6 names match.
const (
	CaptainsWords = "Captain's words"
	AlreadyKnow   = "What we already know"
	Build         = "Build"
	Acceptance    = "Acceptance"
	OpenDecisions = "Open decisions"
	Deliverable   = "Deliverable"
)

// TaskHeading is the level-one heading the Mate's sections live under.
const TaskHeading = "# Task"

// RoleHeading is the first line of every brief the app renders
// (assets/crew/brief.md.tmpl). CheckFile uses it to tell a rendered brief,
// whose `# Task` sits among the template's own sections, from the Mate's
// own file, which is nothing but the task.
const RoleHeading = "# Current worker role"

// Markers the check looks for inside sections. Each is a literal the manual
// and the template teach verbatim.
const (
	OutOfScopeMarker = "Out of scope:"
	VerifyMarker     = "verify:"
	DecidesMarker    = "decides:"
	UnknownMarker    = "Unknown:"
	NoneWord         = "none"
	// TaskPlaceholder is the old template's placeholder. A brief that still
	// carries it was copied from a template rather than written.
	TaskPlaceholder = "{TASK}"
)

// Deciders are the only two values `decides:` may take.
var Deciders = []string{"captain", "mate"}

// Kind is which of the two task shapes a brief is for (manual section 5).
type Kind int

const (
	// Ship is a change to the repository; the default.
	Ship Kind = iota
	// Scout is knowledge in a report; it has a `## Deliverable`.
	Scout
)

func (k Kind) String() string {
	if k == Scout {
		return "scout"
	}
	return "ship"
}

// Sections lists the sections a brief of kind k must have, in the order the
// manual teaches them.
func Sections(k Kind) []string {
	out := []string{CaptainsWords, AlreadyKnow, Build, Acceptance, OpenDecisions}
	if k == Scout {
		out = append(out, Deliverable)
	}
	return out
}

// AllSections is every section name the check recognises, scout included.
func AllSections() []string { return Sections(Scout) }

// Problem is one violation: the section it is about and what is wrong.
type Problem struct {
	// Section is the heading the problem is about, as written in a brief
	// (`## Build`, `# Task`, `# Setup`).
	Section string
	Message string
}

// String is the one error line `brief check` prints.
func (p Problem) String() string { return p.Section + ": " + p.Message }

// Error joins problems into the refusal `crew spawn` returns: a count, then
// one line per problem, so a Mate reading stderr can fix every one of them
// in a single pass rather than one per spawn attempt.
type Error struct {
	Kind     Kind
	Problems []Problem
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d shape problem(s) in this %s brief; fix every one and run the same command again", len(e.Problems), e.Kind)
	for _, p := range e.Problems {
		b.WriteString("\n  ")
		b.WriteString(p.String())
	}
	return b.String()
}

// CheckFile checks the text of a brief file, which is either the Mate's own
// file (the task sections, optionally under a leading `# Task`) or a brief
// the app rendered, recognised by its first line being RoleHeading. For a
// rendered brief only its `# Task` section is checked: the rest is the
// template's, and the app wrote it.
func CheckFile(text string, k Kind) []Problem {
	if IsRendered(text) {
		task, ok := ExtractTask(text)
		if !ok {
			return []Problem{{Section: TaskHeading, Message: "missing; a rendered brief always has one, so this file was edited by hand"}}
		}
		return Check(task, k)
	}
	return Check(text, k)
}

// IsRendered reports whether text is a brief the app rendered.
func IsRendered(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return strings.TrimRight(line, " \t\r") == RoleHeading
	}
	return false
}

// TaskBody is the Mate's text as the template's `# Task` holds it: a leading
// `# Task` heading the Mate wrote is dropped (the template supplies its
// own), and surrounding blank lines are trimmed. Nothing else changes.
func TaskBody(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	trimmed := strings.TrimLeft(text, "\n \t")
	if first, rest, _ := strings.Cut(trimmed, "\n"); strings.TrimRight(first, " \t") == TaskHeading {
		trimmed = rest
	}
	return strings.Trim(trimmed, "\n")
}

// CaptainsFirstLine is the first non-blank line of `## Captain's words`
// outside a fence, or "" when there is none. It is what a one-line label
// for the task should be: the captain's own opening words, not a heading.
func CaptainsFirstLine(text string) string {
	for _, s := range parse(text).sections {
		if s.name != CaptainsWords {
			continue
		}
		for _, l := range s.body {
			if !l.fenced && l.trimmed != "" {
				return l.trimmed
			}
		}
		return ""
	}
	return ""
}

// ExtractTask returns the body of the `# Task` section of a rendered brief:
// everything after the heading up to the next level-one heading.
func ExtractTask(rendered string) (string, bool) {
	lines := parseLines(rendered)
	start := -1
	for i, l := range lines {
		if l.level1 && l.trimmed == TaskHeading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if lines[i].level1 {
			end = i
			break
		}
	}
	raw := make([]string, 0, end-start)
	for _, l := range lines[start:end] {
		raw = append(raw, l.text)
	}
	return strings.Join(raw, "\n"), true
}

// Check checks the Mate's task text for a brief of kind k and returns every
// problem found, in document order within each rule. An empty result means
// the brief has the shape; it says nothing about whether it is a good brief.
func Check(text string, k Kind) []Problem {
	doc := parse(text)
	var problems []Problem
	problems = append(problems, doc.structural...)

	for _, name := range Sections(k) {
		if doc.count[norm(name)] == 0 {
			problems = append(problems, Problem{Section: heading(name), Message: missingMessage(name)})
		}
	}
	for _, name := range AllSections() {
		if n := doc.count[norm(name)]; n > 1 {
			problems = append(problems, Problem{Section: heading(name),
				Message: fmt.Sprintf("appears %d times; write each section once", n)})
		}
	}
	if k == Ship && doc.count[norm(Deliverable)] > 0 {
		problems = append(problems, Problem{Section: heading(Deliverable),
			Message: "only a scout brief has one; spawn with --scout, or move what it says into ## " + Build})
	}

	for _, s := range doc.sections {
		if !s.first {
			continue // a duplicate is already reported above
		}
		if s.name == Deliverable && k == Ship {
			continue
		}
		if isEmpty(s.body) {
			problems = append(problems, Problem{Section: heading(s.name), Message: "empty"})
			continue
		}
		if p, ok := placeholder(s); ok {
			problems = append(problems, p)
			continue
		}
		switch s.name {
		case CaptainsWords:
			problems = append(problems, checkCaptainsWords(s)...)
		case AlreadyKnow:
			problems = append(problems, checkAlreadyKnow(s)...)
		case Build:
			problems = append(problems, checkBuild(s)...)
		case Acceptance:
			problems = append(problems, checkAcceptance(s)...)
		case OpenDecisions:
			problems = append(problems, checkOpenDecisions(s)...)
		}
	}
	return problems
}

func missingMessage(name string) string {
	if name == Deliverable {
		return "missing; a scout brief names here the questions its report must answer"
	}
	return "missing; every brief has one"
}

func heading(name string) string { return "## " + name }

// ---- parsing ---------------------------------------------------------------

// line is one physical line with the facts the check needs about it.
type line struct {
	text    string
	trimmed string
	// fenced is true for a line inside (or opening/closing) a ``` or ~~~
	// block: nothing in a fence is a heading or a list item.
	fenced bool
	// level1 is a `# ` heading outside a fence.
	level1 bool
	// section is the recognised section name when this line is a `## `
	// heading naming one; empty otherwise.
	section string
}

func parseLines(text string) []line {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]line, 0, len(raw))
	inFence := false
	for _, r := range raw {
		l := line{text: r, trimmed: strings.TrimSpace(r)}
		left := strings.TrimLeft(r, " \t")
		if strings.HasPrefix(left, "```") || strings.HasPrefix(left, "~~~") {
			l.fenced = true
			inFence = !inFence
			out = append(out, l)
			continue
		}
		if inFence {
			l.fenced = true
			out = append(out, l)
			continue
		}
		if strings.HasPrefix(r, "# ") {
			l.level1 = true
		}
		if strings.HasPrefix(r, "## ") {
			l.section = recognise(strings.TrimSpace(r[3:]))
		}
		out = append(out, l)
	}
	return out
}

// recognise maps a `## ` heading's text to a known section name. Case, a
// curly apostrophe and a trailing colon are forgiven, because they change
// nothing about the shape; any other text is not a section boundary but
// content of the section it sits in, which is what keeps a captain's own
// Markdown headings, pasted verbatim into `## Captain's words`, from
// splitting that section in two.
func recognise(text string) string {
	key := norm(strings.TrimSuffix(strings.TrimSpace(text), ":"))
	for _, name := range AllSections() {
		if norm(name) == key {
			return name
		}
	}
	return ""
}

func norm(s string) string {
	s = strings.ReplaceAll(s, "’", "'")
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

type section struct {
	name string
	// first is false for the second and later copies of a duplicated section.
	first bool
	body  []line
}

type document struct {
	sections   []section
	count      map[string]int
	structural []Problem
}

// parse splits the Mate's text into its sections. A leading `# Task` is
// allowed and dropped (the Mate may write it; the template supplies it
// anyway); any other level-one heading is refused, because every other
// top-level section belongs to the template, and text before the first
// section is refused because it belongs to none.
func parse(text string) document {
	lines := parseLines(text)
	doc := document{count: map[string]int{}}
	seenContent, foreign := false, false
	var cur *section
	var stray []string
	for _, l := range lines {
		if l.level1 {
			if l.trimmed == TaskHeading && !seenContent {
				seenContent = true
				continue
			}
			doc.structural = append(doc.structural, Problem{Section: l.trimmed,
				Message: "a brief fills only # Task's sections; every other top-level section is the template's"})
			// Its body is the refused section's, not the one above it:
			// skip it up to the next recognised section.
			foreign, cur = true, nil
			continue
		}
		if l.section != "" {
			foreign = false
			seenContent = true
			key := norm(l.section)
			doc.count[key]++
			doc.sections = append(doc.sections, section{name: l.section, first: doc.count[key] == 1})
			cur = &doc.sections[len(doc.sections)-1]
			continue
		}
		if foreign {
			continue
		}
		if cur == nil {
			if l.trimmed != "" {
				seenContent = true
				stray = append(stray, l.trimmed)
			}
			continue
		}
		cur.body = append(cur.body, l)
	}
	if len(stray) > 0 {
		doc.structural = append([]Problem{{Section: TaskHeading,
			Message: fmt.Sprintf("text before the first section belongs in one (%s)", excerpt(stray[0]))}},
			doc.structural...)
	}
	return doc
}

func isEmpty(body []line) bool {
	for _, l := range body {
		if l.trimmed != "" {
			return false
		}
	}
	return true
}

// item is one top-level entry of a list-shaped section: a line that starts
// at column 0, plus the indented lines that continue it.
type item struct {
	n    int
	text string
}

// items splits a section body into its entries. One criterion, fact or
// decision per line is the rule the manual teaches; an indented line
// continues the entry above it, and a fenced block belongs to the entry it
// follows.
func items(body []line) []item {
	var out []item
	for _, l := range body {
		if l.trimmed == "" {
			continue
		}
		continuation := l.fenced || strings.HasPrefix(l.text, " ") || strings.HasPrefix(l.text, "\t")
		if continuation && len(out) > 0 {
			out[len(out)-1].text += " " + l.trimmed
			continue
		}
		out = append(out, item{n: len(out) + 1, text: l.trimmed})
	}
	return out
}

// listMarker matches a leading `-`, `*`, `+`, `1.` or `1)` list marker.
var listMarker = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s+`)

func stripMarker(s string) string { return listMarker.ReplaceAllString(s, "") }

func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 60 {
		s = string(r[:57]) + "..."
	}
	return fmt.Sprintf("%q", s)
}

// ---- rules -----------------------------------------------------------------

// placeholderBodies are whole-section bodies that mean "not written yet".
var placeholderBodies = map[string]bool{
	"todo": true, "tbd": true, "...": true, "…": true, "tk": true, "fixme": true,
}

func placeholder(s section) (Problem, bool) {
	var parts []string
	for _, l := range s.body {
		if strings.Contains(l.text, TaskPlaceholder) {
			return Problem{Section: heading(s.name),
				Message: fmt.Sprintf("still holds the template placeholder %s; write the section", TaskPlaceholder)}, true
		}
		if l.trimmed != "" {
			parts = append(parts, stripMarker(l.trimmed))
		}
	}
	if len(parts) == 1 && placeholderBodies[strings.ToLower(strings.TrimSpace(parts[0]))] {
		return Problem{Section: heading(s.name),
			Message: fmt.Sprintf("holds only the placeholder %q; write the section", parts[0])}, true
	}
	return Problem{}, false
}

// speakerLabel matches a line that opens with a label saying who spoke,
// after firstmate's fm_brief_intent_address_line ("Captain:", "Captain's
// words:", "Captain,", "[captain]"), widened to the spellings a matev2 Mate
// has actually produced or could: "The captain said:", "User:", and the
// Vietnamese the captain writes in ("Người dùng nói:", "Thuyền trưởng:").
var speakerLabel = regexp.MustCompile(`(?i)^(?:\[captain\]|(?:the\s+)?(?:captain|user)(?:'s\s+(?:words|ask|intent|message|request))?(?:\s+(?:said|says|wrote|writes|asked|asks))?\s*[:,]|(?:người dùng|thuyền trưởng)(?:\s+(?:nói|viết|hỏi))?\s*:)`)

// labelDecoration is Markdown a Mate wraps a label in: a quote marker, a
// list marker, bold or italics.
var labelDecoration = regexp.MustCompile(`^(?:>\s*)*(?:(?:[-*+]|\d+[.)])\s+)?[*_]{0,2}`)

func checkCaptainsWords(s section) []Problem {
	for i, l := range s.body {
		if l.fenced || l.trimmed == "" {
			continue
		}
		bare := labelDecoration.ReplaceAllString(l.trimmed, "")
		if speakerLabel.MatchString(bare) {
			return []Problem{{Section: heading(s.name), Message: fmt.Sprintf(
				"line %d opens with a speaker label (%s); write the captain's actual words, the heading already says whose they are",
				i+1, excerpt(l.trimmed))}}
		}
	}
	return nil
}

func checkAlreadyKnow(s section) []Problem {
	its := items(s.body)
	last := stripMarker(its[len(its)-1].text)
	if !strings.HasPrefix(strings.ToLower(last), strings.ToLower(UnknownMarker)) {
		return []Problem{{Section: heading(s.name), Message: fmt.Sprintf(
			"the last line must say what is not known, starting %q (got %s)", UnknownMarker, excerpt(last))}}
	}
	if strings.TrimSpace(last[len(UnknownMarker):]) == "" {
		return []Problem{{Section: heading(s.name), Message: fmt.Sprintf("%q is empty; say what is not known, or %q", UnknownMarker, "Unknown: nothing material")}}
	}
	return nil
}

func checkBuild(s section) []Problem {
	for _, l := range s.body {
		if l.fenced {
			continue
		}
		bare := strings.TrimLeft(stripMarker(l.trimmed), "*_")
		if !strings.HasPrefix(strings.ToLower(bare), strings.ToLower(OutOfScopeMarker)) {
			continue
		}
		rest := strings.TrimLeft(bare[len(OutOfScopeMarker):], "*_ ")
		if rest == "" {
			return []Problem{{Section: heading(s.name), Message: fmt.Sprintf("%q is empty; name what stays out", OutOfScopeMarker)}}
		}
		return nil
	}
	return []Problem{{Section: heading(s.name), Message: fmt.Sprintf(
		"no %q line; name what deliberately stays out of this task", OutOfScopeMarker)}}
}

func checkAcceptance(s section) []Problem {
	var problems []Problem
	for _, it := range items(s.body) {
		idx := strings.Index(strings.ToLower(it.text), VerifyMarker)
		if idx < 0 {
			problems = append(problems, Problem{Section: heading(s.name), Message: fmt.Sprintf(
				"criterion %d has no %q (%s); say how the result is checked", it.n, VerifyMarker, excerpt(stripMarker(it.text)))})
			continue
		}
		if strings.TrimSpace(it.text[idx+len(VerifyMarker):]) == "" {
			problems = append(problems, Problem{Section: heading(s.name), Message: fmt.Sprintf(
				"criterion %d has an empty %q", it.n, VerifyMarker)})
		}
	}
	return problems
}

var decidesValue = regexp.MustCompile(`(?i)decides:\s*([^\s.,;)]*)`)

func checkOpenDecisions(s section) []Problem {
	its := items(s.body)
	if len(its) == 1 && isNone(stripMarker(its[0].text)) {
		return nil
	}
	var problems []Problem
	for _, it := range its {
		text := stripMarker(it.text)
		if isNone(text) {
			problems = append(problems, Problem{Section: heading(s.name), Message: fmt.Sprintf(
				"%q must stand alone; with decisions listed, drop it", NoneWord)})
			continue
		}
		m := decidesValue.FindStringSubmatch(text)
		if m == nil {
			problems = append(problems, Problem{Section: heading(s.name), Message: fmt.Sprintf(
				"decision %d has no \"decides: captain\" or \"decides: mate\" (%s)", it.n, excerpt(text))})
			continue
		}
		if who := strings.ToLower(m[1]); who != Deciders[0] && who != Deciders[1] {
			problems = append(problems, Problem{Section: heading(s.name), Message: fmt.Sprintf(
				"decision %d says %q; %q takes captain or mate", it.n, m[0], DecidesMarker)})
		}
	}
	return problems
}

func isNone(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimRight(s, ".")
	return s == NoneWord
}
