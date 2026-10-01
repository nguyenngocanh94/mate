package harness

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Codex's hook review (docs/mvp.md section 7, task 37). A hook codex-cli has
// no persisted trust for - new, or changed since it was trusted - draws a
// "Hooks need review" dialog right after the directory-trust dialog. Its
// options are counts, not names: "1 hook is new or changed." says nothing
// about whose hook it is, so "2. Trust all and continue" would trust the
// operator's own untrusted hooks along with mate's. What does name each
// hook is the review behind option 1, measured 2026-09-24 on codex-cli
// 0.156.1 in a Herdr 0.8.2 lab pane (captures in testdata/startup):
//
//  1. the dialog, highlight on "1. Review hooks";
//  2. enter: a table of hook events with Installed, Active and Review
//     counts, the first event with a review selected, under a
//     "⚠ N hook(s) need(s) review" line, footer "t trust all · enter review
//     · esc close";
//  3. enter: that event's hooks as a list - "[!] Hook k · new" for one that
//     needs review, "[x] Hook k" for a trusted one - and the selected hook's
//     Event, Source ("Project config - <path>" or "User config - <path>",
//     wrapped over lines), Command, Mode, Timeout and Trust, footer
//     "t trust · esc back";
//  4. `t` trusts the selected hook only ("[x]", "Trust Trusted"), `down` and
//     `up` move one hook; once none needs review the header reads "Turn hooks
//     on or off..." and the footer "space/enter toggle · esc back";
//  5. esc back to the table (now without a Review column, footer "enter
//     details · esc close"), esc again to the composer.
//
// ReviewOwn (codex_review.go) walks exactly that, and trusts a hook only
// when every hook the review lists is one it was told is mate's own
// (OwnHook). It never
// presses "Trust all", and it never trusts anything it did not read.

// StartupScreenHooksReview is codex-cli's "Hooks need review" dialog.
const StartupScreenHooksReview StartupScreen = "hooks_review"

const (
	codexHooksQuestion     = "Hooks need review"
	codexHooksReview       = "1. Review hooks"
	codexHooksTrustAll     = "2. Trust all and continue"
	codexHooksSkip         = "3. Continue without trusting (hooks won't run)"
	codexHooksFooterV154   = "Press enter to confirm or esc to go back"
	codexHooksFooterV156   = "enter confirm · esc skip"
	codexHooksTableTitle   = "Hooks"
	codexHooksTableSub     = "Lifecycle hooks from config and enabled plugins."
	codexHooksTableReview  = "t trust all · enter review · esc close"
	codexHooksTableDone    = "enter details · esc close"
	codexHooksEventReview  = "t trust · esc back"
	codexHooksEventDone    = "space/enter toggle · esc back"
	codexHookTrusted       = "Trusted"
	codexHookProjectSource = "Project config - "
)

// hooksReviewDialogs are the two measured layouts of the dialog: 0.154.0's
// footer (captured in task 35) and 0.156.1's. Both answer with enter on
// option 1, which is where the highlight opens.
func hooksReviewDialogs() []dialogProfile {
	base := dialogProfile{
		screen:   StartupScreenHooksReview,
		question: codexHooksQuestion,
		options: []optionSpec{
			{label: codexHooksReview},
			{label: codexHooksTrustAll},
			{label: codexHooksSkip},
		},
		target:         0,
		marker:         codexHighlightMarker,
		questionWindow: 4,
		targetLabel:    codexHooksReview,
	}
	v154, v156 := base, base
	v154.footer = codexHooksFooterV154
	v156.footer = codexHooksFooterV156
	return []dialogProfile{v156, v154}
}

// ownHookMatches reports whether a reviewed hook is exactly own: the same
// event, a project config at this path, this command. The source must be a
// project config: mate never writes the operator's user config.
//
// The review wraps a long value over lines at the pane width, and the break
// may swallow the character it broke at: measured 2026-09-24 (codex-cli
// 0.156.1, task 37), `…/001/.mate/projects/…` drew as `…/001/.mate` and
// `projects/…` on the next line, the `/` gone, while `…/001/` + `codexlab/…`
// kept it and `mate-` + `session` broke after a hyphen. So a value is
// matched line by line, and at each line break exactly one space or `/` may
// be missing; nothing else is forgiven.
//
// A long command is also cut: measured 2026-09-24 (codex-cli 0.156.1), a
// 194-character command drew its first 178 or 179 characters and `…`, the
// cut at the space before ` --harness codex` both times. So a command whose
// drawing ends in `…` matches when what is drawn is the command up to a
// space; the Source, which says which file holds the hook, is never cut and
// must match whole.
func ownHookMatches(o OwnHook, h CodexReviewHook) bool {
	return h.Event == o.Event &&
		wrapMatch(h.SourceLines, codexHookProjectSource+o.Source) &&
		commandMatch(h.CommandLines, o.Command)
}

// codexEllipsis is what Codex draws where it cut a long value.
const codexEllipsis = "…"

// commandMatch is wrapMatch for a command, which Codex may cut at a word
// boundary and end with codexEllipsis.
func commandMatch(lines []string, want string) bool {
	if wrapMatch(lines, want) {
		return true
	}
	if len(lines) == 0 {
		return false
	}
	last := lines[len(lines)-1]
	if !strings.HasSuffix(last, codexEllipsis) {
		return false
	}
	drawn := append(append([]string(nil), lines[:len(lines)-1]...), strings.TrimSuffix(last, codexEllipsis))
	for cut := strings.IndexByte(want, ' '); cut > 0; {
		if wrapMatch(drawn, want[:cut]) {
			return true
		}
		next := strings.IndexByte(want[cut+1:], ' ')
		if next < 0 {
			break
		}
		cut += 1 + next
	}
	return false
}

// wrapMatch reports whether lines, as drawn, are want wrapped: each line a
// consecutive piece of want, with at most one space or `/` of want dropped
// at each break.
func wrapMatch(lines []string, want string) bool {
	var match func(i int, rest string) bool
	match = func(i int, rest string) bool {
		seg := lines[i]
		if !strings.HasPrefix(rest, seg) {
			return false
		}
		rest = rest[len(seg):]
		if i+1 == len(lines) {
			return rest == ""
		}
		if match(i+1, rest) {
			return true
		}
		return rest != "" && (rest[0] == ' ' || rest[0] == '/') && match(i+1, rest[1:])
	}
	return len(lines) > 0 && match(0, want)
}

// CodexHooksTable is the review's event table.
type CodexHooksTable struct {
	// NeedReview is the count the "⚠ N hook(s) need(s) review" line gives;
	// zero once nothing needs review.
	NeedReview int
	// Reviewing is true while the table offers a review (its footer names
	// "t trust all"); false once everything is trusted.
	Reviewing bool
	Rows      []CodexHooksRow
}

// CodexHooksRow is one event of the table.
type CodexHooksRow struct {
	Event     string
	Installed int
	Active    int
	// Review is the event's hooks needing review; -1 when the table has
	// no Review column (nothing needs review).
	Review   int
	Selected bool
}

// Selected returns the highlighted row.
func (t CodexHooksTable) Selected() (CodexHooksRow, bool) {
	for _, r := range t.Rows {
		if r.Selected {
			return r, true
		}
	}
	return CodexHooksRow{}, false
}

// Row returns the named event's row.
func (t CodexHooksTable) Row(event string) (CodexHooksRow, bool) {
	for _, r := range t.Rows {
		if r.Event == event {
			return r, true
		}
	}
	return CodexHooksRow{}, false
}

var (
	codexNeedReviewLine = regexp.MustCompile(`^⚠ (\d+) hooks? needs? review before (?:it|they) can run\.$`)
	codexEventCountLine = regexp.MustCompile(`^(\d+) hooks? needs? review before (?:it|they) can run\.$`)
	codexHookItemLine   = regexp.MustCompile(`^(›\s*)?\[(.)\] Hook (\d+)(?: · (\S+))?$`)
	codexEventTitle     = regexp.MustCompile(`^(\w+) hooks$`)
)

// ParseCodexHooksTable reads the event table, or reports false for any
// other screen. The rows are those on screen; the table scrolls ("↓"), so a
// caller that needs every event's count reads NeedReview, not the rows.
func ParseCodexHooksTable(screen string) (CodexHooksTable, bool) {
	lines := nonEmptyTrimmed(screen)
	if len(lines) < 4 {
		return CodexHooksTable{}, false
	}
	footer := lines[len(lines)-1]
	var t CodexHooksTable
	switch footer {
	case codexHooksTableReview:
		t.Reviewing = true
	case codexHooksTableDone:
	default:
		return CodexHooksTable{}, false
	}
	title := -1
	for i := 0; i+1 < len(lines); i++ {
		if lines[i] == codexHooksTableTitle && lines[i+1] == codexHooksTableSub {
			title = i
		}
	}
	if title < 0 {
		return CodexHooksTable{}, false
	}
	header := -1
	for i := title + 2; i < len(lines)-1; i++ {
		l := lines[i]
		if m := codexNeedReviewLine.FindStringSubmatch(l); m != nil {
			t.NeedReview, _ = strconv.Atoi(m[1])
			continue
		}
		if strings.HasPrefix(l, "Event ") && strings.Contains(l, "Installed") {
			header = i
			break
		}
	}
	if header < 0 {
		return CodexHooksTable{}, false
	}
	hasReview := strings.Contains(lines[header], " Review ")
	if hasReview != t.Reviewing || (t.Reviewing && t.NeedReview == 0) {
		return CodexHooksTable{}, false
	}
	selected := 0
	for _, l := range lines[header+1 : len(lines)-1] {
		if l == "↓" || l == "↑" {
			continue
		}
		row, ok := parseHooksRow(l, hasReview)
		if !ok {
			return CodexHooksTable{}, false
		}
		if row.Selected {
			selected++
		}
		t.Rows = append(t.Rows, row)
	}
	if len(t.Rows) == 0 || selected != 1 {
		return CodexHooksTable{}, false
	}
	return t, true
}

func parseHooksRow(l string, hasReview bool) (CodexHooksRow, bool) {
	var r CodexHooksRow
	if rest, ok := strings.CutPrefix(l, codexHighlightMarker); ok {
		r.Selected = true
		l = strings.TrimSpace(rest)
	}
	f := strings.Fields(l)
	need := 3
	if hasReview {
		need = 4
	}
	if len(f) < need+1 { // and a description
		return r, false
	}
	nums := make([]int, need-1)
	for i := range nums {
		n, err := strconv.Atoi(f[i+1])
		if err != nil {
			return r, false
		}
		nums[i] = n
	}
	r.Event, r.Installed, r.Active, r.Review = f[0], nums[0], nums[1], -1
	if hasReview {
		r.Review = nums[2]
	}
	return r, true
}

// CodexHookEvent is one event's hook list.
type CodexHookEvent struct {
	Event string
	// NeedReview is the header's count; zero once none needs review.
	NeedReview int
	Hooks      []CodexReviewHookItem
	// Detail is the selected hook, as drawn below the list.
	Detail CodexReviewHook
	// Reviewing is true while the footer offers `t trust`, i.e. the
	// selected hook needs review.
	Reviewing bool
}

// CodexReviewHookItem is one line of the list.
type CodexReviewHookItem struct {
	Index int // the drawn number, from 1
	// NeedsReview is the "[!]" mark; a trusted hook is "[x]" (enabled) or
	// "[ ]" (disabled).
	NeedsReview bool
	Selected    bool
}

// CodexReviewHook is the detail block of the selected hook. Source and
// Command are the drawn lines joined, for messages; SourceLines and
// CommandLines are the lines as drawn, which is what Matches reads.
type CodexReviewHook struct {
	Event        string
	Source       string
	Command      string
	Trust        string
	SourceLines  []string
	CommandLines []string
}

// Selected returns the index in Hooks of the highlighted item.
func (e CodexHookEvent) Selected() int {
	for i, h := range e.Hooks {
		if h.Selected {
			return i
		}
	}
	return -1
}

// ParseCodexHookEvent reads one event's hook list, or reports false.
func ParseCodexHookEvent(screen string) (CodexHookEvent, bool) {
	lines := nonEmptyTrimmed(screen)
	if len(lines) < 5 {
		return CodexHookEvent{}, false
	}
	var e CodexHookEvent
	switch lines[len(lines)-1] {
	case codexHooksEventReview:
		e.Reviewing = true
	case codexHooksEventDone:
	default:
		return CodexHookEvent{}, false
	}
	title := -1
	for i, l := range lines {
		if m := codexEventTitle.FindStringSubmatch(l); m != nil && m[1] != "Lifecycle" {
			title, e.Event = i, m[1]
		}
	}
	if title < 0 {
		return CodexHookEvent{}, false
	}
	i := title + 1
	if i < len(lines) {
		if m := codexEventCountLine.FindStringSubmatch(lines[i]); m != nil {
			e.NeedReview, _ = strconv.Atoi(m[1])
			i++
		} else if strings.HasPrefix(lines[i], "Turn hooks on or off") {
			i++
		}
	}
	selected := 0
	for ; i < len(lines); i++ {
		m := codexHookItemLine.FindStringSubmatch(lines[i])
		if m == nil {
			break
		}
		n, _ := strconv.Atoi(m[3])
		item := CodexReviewHookItem{Index: n, NeedsReview: m[2] == "!", Selected: m[1] != ""}
		if item.Selected {
			selected++
		}
		e.Hooks = append(e.Hooks, item)
	}
	if len(e.Hooks) == 0 || selected != 1 {
		return CodexHookEvent{}, false
	}
	// The detail block: `Key   value`, a value wrapped onto indented
	// continuation lines. The raw lines are read again for the indentation.
	detail, ok := parseHookDetail(screen)
	if !ok {
		return CodexHookEvent{}, false
	}
	e.Detail = detail
	if e.Reviewing != e.Hooks[e.Selected()].NeedsReview {
		return CodexHookEvent{}, false
	}
	return e, true
}

var hookDetailKeys = []string{"Event", "Source", "Command", "Mode", "Timeout", "Trust"}

func parseHookDetail(screen string) (CodexReviewHook, bool) {
	fields := map[string][]string{}
	current := ""
	valueCol := -1
	for _, raw := range strings.Split(screen, "\n") {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			current = ""
			continue
		}
		key := ""
		for _, k := range hookDetailKeys {
			if strings.HasPrefix(trimmed, k+" ") {
				key = k
				break
			}
		}
		if key != "" {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, key))
			fields[key] = []string{value}
			current = key
			indent := len(line) - len(strings.TrimLeft(line, " "))
			valueCol = indent + len(key) + (len(trimmed) - len(key) - len(value))
			continue
		}
		// A continuation starts at the value column of the key above it.
		if current != "" && valueCol > 0 && len(line)-len(strings.TrimLeft(line, " ")) == valueCol {
			fields[current] = append(fields[current], trimmed)
			continue
		}
		current = ""
	}
	for _, k := range []string{"Event", "Source", "Command", "Trust"} {
		if len(fields[k]) == 0 || fields[k][0] == "" {
			return CodexReviewHook{}, false
		}
	}
	join := func(k string) string { return strings.Join(fields[k], "") }
	return CodexReviewHook{
		Event: join("Event"), Source: join("Source"), Command: join("Command"), Trust: join("Trust"),
		SourceLines: fields["Source"], CommandLines: fields["Command"],
	}, true
}

// Trusted reports whether the detail says the hook is trusted.
func (h CodexReviewHook) Trusted() bool { return h.Trust == codexHookTrusted }

func nonEmptyTrimmed(screen string) []string {
	var out []string
	for _, l := range strings.Split(screen, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return out
}

const (
	// CodexHooksFile is the Codex Mate's hook file, in the `.codex/` of its
	// cwd, which Codex loads once the directory is trusted (task 35, A3).
	// The operator's own `$CODEX_HOME/hooks.json` is never read or written.
	CodexHooksFile = "hooks.json"
)

// CodexHooksPath is the hook file of a Codex Mate in mateDir.
func CodexHooksPath(mateDir string) string {
	return filepath.Join(mateDir, Codex{}.Info().ConfigDir, CodexHooksFile)
}

const (
	// CodexHookContextLimit is the additionalContextLimit, in tokens, the
	// Codex Mate's hook is installed with: codex-cli 0.156.1 keeps the head
	// and the tail of a SessionStart hook's output and drops the middle past
	// about 2.5K tokens unless the hook raises it (docs/mvp.md section 7,
	// task 37).
	CodexHookContextLimit = 32000
	// CodexHookTimeoutSeconds bounds the hook, which reads files and git
	// metadata only.
	CodexHookTimeoutSeconds = 30
)

// The SessionStart digest's size, per harness (HookInstaller.DigestMaxBytes).
// Both are measured limits on what a hook's output puts in context
// (docs/mvp.md section 7, task 37).
const (
	// CodexSessionHookMaxBytes: codex-cli 0.156.1 keeps the head and the
	// tail of a SessionStart hook's output and drops the middle past about
	// 2.5K tokens unless the hook raises additionalContextLimit; with the
	// limit at 20000, 28K characters arrived whole. The digest is bounded
	// well inside CodexHookContextLimit.
	CodexSessionHookMaxBytes = 48000
)

// CodexSessionHookCommand is the exact command a Codex Mate's SessionStart
// hook runs. It names its harness so the digest is sized for it, and the
// hook review compares a hook Codex asks to trust against this string, so it
// is the one place it is spelled.
func CodexSessionHookCommand(binary string) string {
	return SessionHookCommand(binary) + " --harness " + string(KindCodex)
}

// CodexHooks is `mate/.codex/hooks.json` for a Codex Mate: the same
// SessionStart hook, with its output limit raised so the digest arrives
// whole (see CodexHookContextLimit). Codex runs it at the first prompt
// after a launch, resume, /compact or /clear (task 35, A3). It is the app's
// file, rewritten on every start; the same bytes keep the same trust.
func CodexHooks(binary string) []byte {
	data, _ := json.MarshalIndent(map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type":                   "command",
					"command":                CodexSessionHookCommand(binary),
					"timeout":                CodexHookTimeoutSeconds,
					"additionalContextLimit": CodexHookContextLimit,
				}},
			}},
		},
	}, "", "  ")
	return append(data, '\n')
}
