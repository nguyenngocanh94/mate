package memory

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Problem is one shape problem `memory check` reports: a file, the line it
// is on (0 for a whole-file problem) and what is wrong, with what to do.
type Problem struct {
	File string
	Line int
	Msg  string
}

func (p Problem) String() string {
	if p.Line == 0 {
		return p.File + ": " + p.Msg
	}
	return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Msg)
}

// Parsed is one entry line Parse understood, with where it was.
type Parsed struct {
	Entry
	Section string
	Line    int
}

// markerPattern is a trailing tier marker. Only the two dated spellings of
// B3 are accepted; firstmate's `<!--P-->`, `<!--g-->` and `/N` counters
// have no use in matev2 (a Captain entry is pinned by default, there is no
// legacy memory to migrate, and there is no pass horizon).
var (
	markerPattern  = regexp.MustCompile(`\s*<!--([ap]):(\d{4}-\d{2}-\d{2})-->$`)
	anyMarkerAtEnd = regexp.MustCompile(`<!--.*-->$`)
)

// FileName is how problems name memory.md.
const FileName = "memory.md"

// Check parses memory.md's text and reports every shape problem, judged on
// today's date. Paths in a source are resolved against base (the project
// directory, where `crews/` and `sent.log` live) and must stay inside root
// (the workspace). It never judges what an entry says.
func Check(text string, today time.Time, root, base string) ([]Parsed, []Problem) {
	var entries []Parsed
	var problems []Problem
	add := func(line int, format string, a ...any) {
		problems = append(problems, Problem{File: FileName, Line: line, Msg: fmt.Sprintf(format, a...)})
	}
	section := ""
	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		line := strings.TrimRight(raw, " \t\r")
		switch {
		case strings.TrimSpace(line) == "":
			continue
		case strings.HasPrefix(line, "## "):
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			if !knownSection(section) {
				add(n, "unknown section `## %s`: memory.md holds only `## %s` and `## %s`; route anything else to its owner (manual section 2)", section, CaptainSection, LessonsSection)
			}
			continue
		case strings.HasPrefix(line, "# "):
			continue
		case strings.HasPrefix(line, "<!--") && strings.HasSuffix(line, "-->"):
			continue
		case !strings.HasPrefix(line, "- "):
			add(n, "not a one-line entry: every line under a section starts with \"- \" and holds one whole entry")
			continue
		}
		if section == "" {
			add(n, "entry before any section: put it under `## %s` or `## %s`", CaptainSection, LessonsSection)
			continue
		}
		if !knownSection(section) {
			continue
		}
		p, errs := parseEntry(strings.TrimPrefix(line, "- "), section)
		p.Line = n
		for _, e := range errs {
			add(n, "%s", e)
		}
		if len(errs) > 0 {
			continue
		}
		for _, e := range ageProblems(p.Entry, today) {
			add(n, "%s", e)
		}
		if err := CheckSourcePaths(p.Source, root, base); err != nil {
			add(n, "source %s", err)
		}
		entries = append(entries, p)
	}
	return entries, problems
}

func knownSection(s string) bool {
	for _, k := range Sections {
		if s == k {
			return true
		}
	}
	return false
}

// parseEntry splits one entry (without its "- ") into text, expiry, source
// and marker, and reports what is missing.
func parseEntry(body, section string) (Parsed, []string) {
	p := Parsed{Section: section}
	p.Tier = Pinned
	rest := body
	if m := markerPattern.FindStringSubmatchIndex(rest); m != nil {
		letter := rest[m[2]:m[3]]
		p.Date = rest[m[4]:m[5]]
		p.Tier = Aging
		if letter == "p" {
			p.Tier = Perishable
		}
		rest = strings.TrimRight(rest[:m[0]], " ")
	} else if anyMarkerAtEnd.MatchString(rest) {
		return p, []string{"unknown tier marker: the only markers are <!--a:YYYY-MM-DD--> (aging) and <!--p:YYYY-MM-DD--> (perishable)"}
	}
	var errs []string
	if p.Tier == Pinned && section == LessonsSection {
		errs = append(errs, "lesson has no tier marker: end it with <!--a:YYYY-MM-DD--> (aging), or <!--p:YYYY-MM-DD--> and an expiry condition (perishable)")
	}
	if p.Date != "" {
		if _, err := time.Parse(DateLayout, p.Date); err != nil {
			errs = append(errs, fmt.Sprintf("marker date %q is not a date", p.Date))
		}
	}
	src, before, ok := lastGroup(rest)
	if !ok || strings.TrimSpace(src) == "" {
		errs = append(errs, "entry has no source: end its text with the source in parentheses, before any marker, e.g. (captain, 2026-09-24) or (crews/k1/report.md §Durable facts)")
		p.Text = rest
		return p, errs
	}
	p.Source = strings.TrimSpace(src)
	rest = before
	if p.Tier == Perishable {
		if exp, b, ok := lastGroup(rest); ok && strings.HasPrefix(exp, ExpiresPrefix) && strings.TrimSpace(strings.TrimPrefix(exp, ExpiresPrefix)) != "" {
			p.Expiry = strings.TrimSpace(strings.TrimPrefix(exp, ExpiresPrefix))
			rest = b
		} else {
			errs = append(errs, "perishable entry names no expiry condition: put (expires: <checkable condition>) before its source, or make it aging")
		}
	}
	p.Text = strings.TrimSpace(rest)
	if p.Text == "" {
		errs = append(errs, "entry has a source but no text")
	}
	return p, errs
}

// lastGroup finds the final balanced "( ... )" group at the end of s and
// returns its inside and what precedes it.
func lastGroup(s string) (inside, before string, ok bool) {
	s = strings.TrimRight(s, " ")
	if !strings.HasSuffix(s, ")") {
		return "", s, false
	}
	depth := 0
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return s[i+1 : len(s)-1], strings.TrimRight(s[:i], " "), true
			}
		}
	}
	return "", s, false
}

// ageProblems reports a dated entry whose clock has run out.
func ageProblems(e Entry, today time.Time) []string {
	if e.Date == "" {
		return nil
	}
	d, err := time.ParseInLocation(DateLayout, e.Date, today.Location())
	if err != nil {
		return nil
	}
	t := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	age := int(t.Sub(d).Hours() / 24)
	switch {
	case age < 0:
		return []string{fmt.Sprintf("marker date %s is in the future", e.Date)}
	case e.Tier == Aging && age >= AgingDays:
		return []string{fmt.Sprintf("aging entry last reinforced %s, %d days ago (stale at %d): re-validate it from this session's evidence and refresh the date, or move it to memory-archive.md (skill stow)", e.Date, age, AgingDays)}
	case e.Tier == Perishable && age >= PerishableDays:
		return []string{fmt.Sprintf("perishable entry last reinforced %s, %d days ago (stale at %d): check its expiry condition; still open means refresh the date, met means move it to memory-archive.md (skill stow)", e.Date, age, PerishableDays)}
	}
	return nil
}

// quoted is a double-quoted run, which is somebody's words rather than a
// path: the captain's quote in `(captain, "...", 2026-09-18)`.
var quoted = regexp.MustCompile(`"[^"]*"|“[^”]*”`)

// CheckSourcePaths refuses a source that names a path the next session
// cannot follow: an absolute path (a temporary scratchpad, a home
// directory), or a relative one that climbs out of the workspace. Relative
// paths are read from base, the project directory. Words that are not
// paths - "captain", a date, `main@3f2a91c` - pass untouched; so does
// anything inside double quotes.
func CheckSourcePaths(source, root, base string) error {
	for _, tok := range strings.FieldsFunc(quoted.ReplaceAllString(source, " "), func(r rune) bool {
		return r == ' ' || r == '\t' || r == ',' || r == ';'
	}) {
		tok = strings.TrimRight(strings.TrimLeft(tok, "`'"), "`'.:")
		if tok == "" || strings.Contains(tok, "://") {
			continue
		}
		if strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~") {
			if strings.HasPrefix(tok, "/") && within(root, tok) {
				if rel, err := filepath.Rel(base, filepath.Clean(tok)); err == nil {
					return fmt.Errorf("%q is an absolute path; write it relative to the project directory: %s", tok, filepath.ToSlash(rel))
				}
			}
			return fmt.Errorf("%q is an absolute path outside the workspace; cite something inside the workspace, relative to the project directory", tok)
		}
		if tok == ".." || strings.HasPrefix(tok, "../") || strings.Contains(tok, "/../") {
			if !within(root, filepath.Join(base, filepath.FromSlash(tok))) {
				return fmt.Errorf("%q climbs out of the workspace; cite something inside it, relative to the project directory", tok)
			}
		}
	}
	return nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
