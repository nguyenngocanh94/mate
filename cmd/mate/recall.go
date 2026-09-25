package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdRecall implements `mate recall <project> [--live] [--max-bytes N]`
// (docs/mvp.md M8 task 37, B1): the one ordered digest a Mate reads at a
// session start, in the order of docs/research/firstmate-memory-2026-09-24.md
// §12.5 - live state first, memory last - so a digest cut short loses the
// most stable, most recoverable part and never the crews.
//
// It is the read half of firstmate's bin/fm-session-start.sh and nothing
// else: it reads `.mate/` and git metadata, writes nothing, takes no lock.
func cmdRecall(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate recall <project> [--live] [--max-bytes <n>] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	liveFlag := fs.Bool("live", false, "print only part 1, the live state")
	maxFlag := fs.Int("max-bytes", 0, "cut the digest from the bottom to at most this many bytes; part 1 always survives whole (0: no limit)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate recall: want exactly 1 argument: <project>")
	}
	if *maxFlag < 0 {
		return newUsageError("mate recall: --max-bytes must not be negative")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	text, err := recall(context.Background(), w, gitx.New(), fs.Arg(0), time.Now(), recallOptions{
		LiveOnly: *liveFlag,
		MaxBytes: *maxFlag,
		Binary:   selfBinary(),
	})
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, text)
	return err
}

// selfBinary is the absolute path the digest tells the Mate to run, the
// same one its manual names.
func selfBinary() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "mate"
}

// recallOptions shape one digest.
type recallOptions struct {
	// LiveOnly prints only part 1: a resumed conversation still holds the
	// files, but the crews may have moved while the Mate was stopped.
	LiveOnly bool
	// MaxBytes, when positive, cuts the digest from the bottom. Part 1 is
	// always printed whole, whatever it costs.
	MaxBytes int
	// Occasion is what the header says the digest is for, e.g.
	// "session start: compact". Empty for a digest the Mate asked for.
	Occasion string
	// Binary is the mate path the cut notice tells the Mate to run.
	Binary string
}

// recallTimeLayout is how every time in the digest is written: the Mate
// compares ages, and a date is part of an age once a project runs for days.
const recallTimeLayout = "2006-01-02 15:04"

// The digest's framing. A part is a `== N. title ==` line; a file inside it
// is framed by begin and end lines naming it, because a file's own `#`
// headings would otherwise read as the digest's.
func partHeading(n int, title string) string { return fmt.Sprintf("\n== %d. %s ==\n", n, title) }

// recallPart is one numbered part of the digest.
type recallPart struct {
	n     int
	title string
	body  string
}

// recall builds the digest.
func recall(ctx context.Context, w *store.Workspace, git gitx.Git, project string, now time.Time, opts recallOptions) (string, error) {
	if err := requireProject(w, project); err != nil {
		return "", err
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return "", err
	}
	var head strings.Builder
	fmt.Fprintf(&head, "# mate recall %s · %s", project, now.Format(recallTimeLayout+" -0700"))
	if opts.Occasion != "" {
		fmt.Fprintf(&head, " · %s", opts.Occasion)
	}
	head.WriteString("\n")
	if opts.LiveOnly {
		head.WriteString("Contract: live state only. Your conversation was resumed and still holds the files you read before; this is what the crews, the inbox and your outbox are now. Act on it, and reconcile backlog.md against it.\n")
	} else {
		head.WriteString("Contract: read this once; it is your state, in the order you act on it; act on it. Do not re-read these files this session unless a part says ABSENT, looks corrupt, or was cut.\n")
	}

	live, err := recallLive(w, project, cfg, now)
	if err != nil {
		return "", err
	}
	parts := []recallPart{live}
	if !opts.LiveOnly {
		parts = append(parts, recallFacts(ctx, w, git, project, cfg))
		projectPart, err := recallProjectDoc(ctx, w, git, project, cfg)
		if err != nil {
			return "", err
		}
		backlogPart, err := recallBacklog(w, project)
		if err != nil {
			return "", err
		}
		parts = append(parts, projectPart, backlogPart,
			recallPart{5, "memory.md", framedFile("memory.md", w.MemoryFile(project))},
			recallWorkspace(w, project))
		checkPart, err := recallMemoryCheck(ctx, w, git, project, now)
		if err != nil {
			return "", err
		}
		parts = append(parts, checkPart)
		if hints, ok := recallTimeline(ctx, w, project, now); ok {
			parts = append(parts, hints)
		}
	}
	return assembleRecall(head.String(), parts, project, opts), nil
}

// assembleRecall joins the parts, cutting from the bottom to fit
// opts.MaxBytes: part 1 whole, then every later part that fits whole, then
// as many whole lines of the first one that does not, then one line saying
// what is missing and how to get it.
func assembleRecall(head string, parts []recallPart, project string, opts recallOptions) string {
	var out strings.Builder
	out.WriteString(head)
	out.WriteString(partHeading(parts[0].n, parts[0].title))
	out.WriteString(parts[0].body)
	if opts.MaxBytes <= 0 {
		for _, p := range parts[1:] {
			out.WriteString(partHeading(p.n, p.title))
			out.WriteString(p.body)
		}
		return out.String()
	}
	// Room for the notice itself, which names every missing part.
	const reserve = 420
	budget := opts.MaxBytes - reserve
	var missing []string
	for i, p := range parts[1:] {
		block := partHeading(p.n, p.title) + p.body
		if out.Len()+len(block) <= budget {
			out.WriteString(block)
			continue
		}
		// The first part that does not fit: its whole lines while they do,
		// but only if at least one line of its body goes with its heading.
		lines := strings.SplitAfter(block, "\n")
		kept, size := 0, out.Len()
		for _, l := range lines {
			if size+len(l) > budget {
				break
			}
			size += len(l)
			kept++
		}
		// lines[0] is the blank line before the heading, lines[1] the heading.
		if kept > 2 {
			for _, l := range lines[:kept] {
				out.WriteString(l)
			}
			missing = append(missing, fmt.Sprintf("the rest of part %d (%s)", p.n, p.title))
		} else {
			missing = append(missing, fmt.Sprintf("part %d (%s)", p.n, p.title))
		}
		for _, q := range parts[i+2:] {
			missing = append(missing, fmt.Sprintf("part %d (%s)", q.n, q.title))
		}
		break
	}
	if len(missing) == 0 {
		return out.String()
	}
	bin := opts.Binary
	if bin == "" {
		bin = "mate"
	}
	fmt.Fprintf(&out, "\nrecall cut to fit %d bytes; not shown: %s. Run `%s recall %s` once before acting on anything those parts cover.\n",
		opts.MaxBytes, strings.Join(missing, ", "), bin, project)
	return out.String()
}

// recallLive is part 1: the mode, the crews, the inbox and the outbox.
func recallLive(w *store.Workspace, project string, cfg store.ProjectConfig, now time.Time) (recallPart, error) {
	var b strings.Builder
	mode := "manual"
	if w.Auto(project) {
		mode = "auto"
	}
	yolo := "off"
	if cfg.Yolo {
		yolo = "on"
	}
	fmt.Fprintf(&b, "mode: %s · yolo %s\n", mode, yolo)

	fmt.Fprintf(&b, "crews (mate backlog %s):\n", project)
	if err := printBacklog(w, project, false, now, &b); err != nil {
		return recallPart{}, err
	}

	box := query.LoadBox(w, project)
	switch {
	case !box.IsKnown():
		fmt.Fprintf(&b, "inbox: unreadable (%s)\n", box.Reason)
	case len(box.Value.Inbox) == 0:
		b.WriteString("inbox: nothing unresolved\n")
	default:
		fmt.Fprintf(&b, "inbox: %d unresolved\n", len(box.Value.Inbox))
		for _, e := range box.Value.Inbox {
			b.WriteString(inboxLine(w, project, e))
		}
	}

	items, err := w.ReadOutbox(project)
	if err != nil {
		return recallPart{}, err
	}
	var queued []store.OutboxItem
	for _, item := range items {
		if item.Queued() {
			queued = append(queued, item)
		}
	}
	if len(queued) == 0 {
		b.WriteString("outbox: nothing queued for your composer\n")
	} else {
		fmt.Fprintf(&b, "outbox: %d queued for your composer (the app types each when it is empty)\n", len(queued))
		for _, item := range queued {
			fmt.Fprintf(&b, "- %s %s: %s\n", item.At.Local().Format(recallTimeLayout), item.Source, recallClip(item.Text, 200))
		}
	}
	return recallPart{1, "Live state", b.String()}, nil
}

// inboxLine is one unresolved item: when, who, what it needs, what it
// said, where to read it, and what [assign] has done with it.
func inboxLine(w *store.Workspace, project string, e query.BoxEntry) string {
	who := e.Crew
	if who == "" {
		who = "-"
	}
	need := e.Verb
	where := ""
	switch e.Kind {
	case query.BoxIncident:
		switch e.Verb {
		case "stale":
			need = "stuck, quiet too long"
		case "runtime_lost":
			need = "agent gone"
		case "wedged":
			need = "send wedged"
		case "budget":
			need = "over budget"
		default:
			need = "incident " + e.Verb
		}
	case query.BoxStatus:
		if e.Verb == "needs-decision" || e.Verb == "blocked" {
			need = "needs an answer"
		}
		if e.Crew != "" {
			where = " (" + relToProject(w, project, w.CrewStatus(project, e.Crew)) + ")"
		}
	}
	line := fmt.Sprintf("- %s %s %s", e.At.Local().Format(recallTimeLayout), who, need)
	if e.Text != "" {
		line += ": " + strconv.Quote(recallClip(e.Text, 200))
	}
	line += where
	switch e.Assigned.State {
	case query.BoxAssignQueued:
		line += " · assigned, queued"
	case query.BoxAssignSent:
		line += " · assigned " + e.Assigned.SentAt.Local().Format("15:04")
	}
	return line + "\n"
}

// recallFacts is part 2. A git failure is reported in place: the rest of
// the digest is still worth reading.
func recallFacts(ctx context.Context, w *store.Workspace, git gitx.Git, project string, cfg store.ProjectConfig) recallPart {
	title := "Project facts (mate project facts " + project + ")"
	lines, _ := projectFactsLines(ctx, w, git, project, cfg, true)
	return recallPart{2, title, strings.Join(lines, "\n") + "\n"}
}

// recallProjectDoc is part 3: PROJECT.md, and which of its anchors their
// own repo's default branch has moved past.
func recallProjectDoc(ctx context.Context, w *store.Workspace, git gitx.Git, project string, cfg store.ProjectConfig) (recallPart, error) {
	body := framedFile(memory.ProjectFileName, w.ProjectDoc(project))
	doc, err := readOptional(w.ProjectDoc(project))
	if err != nil {
		return recallPart{}, err
	}
	anchors, _ := memory.CheckProject(doc, anchorRepos(cfg))
	for _, r := range cfg.Repos {
		warnings, err := anchorWarnings(ctx, git, w.RepoDir(r.Path), r, anchorsIn(anchors, r.Name))
		if err != nil {
			warnings = []string{fmt.Sprintf("warning: could not compare %s's anchors with %s:%s: %v", memory.ProjectFileName, r.Name, r.DefaultBranch, err)}
		}
		for _, line := range warnings {
			body += line + "\n"
		}
	}
	return recallPart{3, memory.ProjectFileName, body}, nil
}

// framedFile prints one file between begin and end lines, or says ABSENT or
// (empty): a missing file and an empty one mean different things (the
// first was never created, or was deleted; the second exists and says
// nothing), so they never print alike. A file holding only its headings
// says so too, because a seeded file is how every file starts.
func framedFile(name, path string) string {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("----- %s: ABSENT (%s) -----\n", name, path)
	case err != nil:
		return fmt.Sprintf("----- %s: unreadable (%v) -----\n", name, err)
	case len(bytes.TrimSpace(data)) == 0:
		return fmt.Sprintf("----- %s: (empty) (%s) -----\n", name, path)
	}
	return framed(name, path, string(data), "")
}

func framed(name, path, text, endNote string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "----- begin %s (%s) -----\n", name, path)
	b.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		b.WriteString("\n")
	}
	end := "----- end " + name
	if endNote != "" {
		end += " (" + endNote + ")"
	}
	b.WriteString(end + " -----\n")
	if headingsOnly(text) {
		fmt.Fprintf(&b, "note: %s holds its headings and nothing else yet\n", name)
	}
	return b.String()
}

// headingsOnly reports a file whose every non-blank line is a heading or an
// HTML comment: a seed nobody has written into.
func headingsOnly(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || (strings.HasPrefix(t, "<!--") && strings.HasSuffix(t, "-->")) {
			continue
		}
		return false
	}
	return true
}

// recallBacklog is part 4: backlog.md without its Done section, and the
// In flight ids that do not match the live table.
func recallBacklog(w *store.Workspace, project string) (recallPart, error) {
	const title = "backlog.md, without Done"
	path := w.BacklogFile(project)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return recallPart{4, title, fmt.Sprintf("----- backlog.md: ABSENT (%s) -----\n", path)}, nil
	}
	if err != nil {
		return recallPart{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return recallPart{4, title, fmt.Sprintf("----- backlog.md: (empty) (%s) -----\n", path)}, nil
	}
	kept, done, inFlight := splitBacklog(string(data))
	note := ""
	if done > 0 {
		note = fmt.Sprintf("## %s: %d entr%s not shown", memory.BacklogDone, done, plural(done, "y", "ies"))
	}
	var body strings.Builder
	body.WriteString(framed("backlog.md", path, kept, note))

	summaries, err := spawn.ListCrews(w, project)
	if err != nil {
		return recallPart{}, err
	}
	open := map[string]bool{}
	var openOrder []string
	for _, s := range summaries {
		if !s.Closed {
			open[s.Crew] = true
			openOrder = append(openOrder, s.Crew)
		}
	}
	listed := map[string]bool{}
	var stale []string
	for _, id := range inFlight {
		listed[id] = true
		if !open[id] {
			stale = append(stale, id)
		}
	}
	var missing []string
	for _, id := range openOrder {
		if !listed[id] {
			missing = append(missing, id)
		}
	}
	if len(stale) > 0 {
		fmt.Fprintf(&body, "In flight in backlog.md but not an open crew in part 1: %s\n", strings.Join(stale, ", "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(&body, "Open crews in part 1 with no `- <id>` line under In flight: %s\n", strings.Join(missing, ", "))
	}
	if len(stale) == 0 && len(missing) == 0 {
		body.WriteString("In flight matches the open crews in part 1.\n")
	}
	return recallPart{4, title, body.String()}, nil
}

// inFlightID is the structural match of an In flight entry: `- <id>` where
// <id> is a crew id, followed by the end of the line, a space, a colon, a
// comma or a parenthesis. What the rest of the line says is not read.
var inFlightID = regexp.MustCompile(`^- ([a-z][a-z0-9]{1,15})(?:$|[\s:,(])`)

// splitBacklog returns backlog.md without its Done section, how many
// entries that section held, and the ids the In flight section names.
func splitBacklog(text string) (kept string, done int, inFlight []string) {
	var b strings.Builder
	section := ""
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
		} else if strings.HasPrefix(trimmed, "# ") {
			section = ""
		}
		if isDoneSection(section) {
			if strings.HasPrefix(trimmed, "- ") {
				done++
			}
			continue
		}
		if section == memory.BacklogInFlight {
			if m := inFlightID.FindStringSubmatch(trimmed); m != nil {
				inFlight = append(inFlight, m[1])
			}
		}
		b.WriteString(line)
	}
	return b.String(), done, inFlight
}

// isDoneSection matches `## Done` and the seeded `## Done (10 most recent)`.
func isDoneSection(section string) bool {
	return section == memory.BacklogDone || strings.HasPrefix(section, memory.BacklogDone+" ")
}

// recallWorkspace is part 6: the captain's rules for every Mate, then one
// pointer line to the two crew-rule files the app appends to every brief.
func recallWorkspace(w *store.Workspace, project string) recallPart {
	body := framedFile("WORKSPACE.md", w.WorkspaceDoc())
	body += fmt.Sprintf("Crew rules, appended by the app to every brief and not shown here (never copy them into a brief): %s (%s), %s (%s)\n",
		w.WorkspaceCrewDoc(), crewDocState(w.WorkspaceCrewDoc()), w.ProjectCrewDoc(project), crewDocState(w.ProjectCrewDoc(project)))
	return recallPart{6, "WORKSPACE.md", body}
}

func crewDocState(path string) string {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "ABSENT"
	case err != nil:
		return "unreadable"
	case strings.TrimSpace(store.StripComments(string(data))) == "":
		return "no rules yet"
	}
	return "has rules"
}

// recallMemoryCheck is part 7: what `mate memory check` says, minus the
// anchor warnings part 3 already carries.
func recallMemoryCheck(ctx context.Context, w *store.Workspace, git gitx.Git, project string, now time.Time) (recallPart, error) {
	title := "Memory check (mate memory check " + project + ")"
	// memoryCheck compares anchors with the repository last, so a failure
	// there still leaves the budget and the shape problems filled in; part 3
	// already says the comparison failed.
	rep, err := memoryCheck(ctx, w, git, project, now)
	if err != nil && rep.Budget.Budget == 0 {
		return recallPart{7, title, fmt.Sprintf("memory check failed: %v\n", err)}, nil
	}
	var b strings.Builder
	b.WriteString(rep.Budget.Line() + "\n")
	for _, p := range rep.Problems {
		b.WriteString(p + "\n")
	}
	if len(rep.Problems) == 0 {
		fmt.Fprintf(&b, "memory ok: %d captain entr%s, %d lesson(s), %d anchored %s line(s)\n",
			rep.Captain, plural(rep.Captain, "y", "ies"), rep.Lessons, rep.Anchored, memory.ProjectFileName)
	} else {
		fmt.Fprintf(&b, "%d problem(s): load skill stow and fix them before the files grow further\n", len(rep.Problems))
	}
	return recallPart{7, title, b.String()}, nil
}

// Part 8's bounds.
const (
	recallHintDays  = 14
	recallHintLimit = 10
)

// recallTimeline is part 8, present only when `mate.db` exists: what the
// timeline saw that might be worth remembering, and how full the Mate's
// context is. Hints only: the database is derived (docs/mvp.md decision 6)
// and holds no memory, so nothing here is ever a record.
func recallTimeline(ctx context.Context, w *store.Workspace, project string, now time.Time) (recallPart, bool) {
	path, err := db.Path(w)
	if err != nil {
		return recallPart{}, false
	}
	if _, err := os.Stat(path); err != nil {
		return recallPart{}, false
	}
	const title = "Timeline hints (mate.db: hints only, never memory)"
	d, err := db.OpenReadPath(path)
	if err != nil {
		return recallPart{8, title, fmt.Sprintf("timeline unreadable: %v\n", err)}, true
	}
	defer d.Close()
	var b strings.Builder
	since := db.FormatTime(now.Add(-recallHintDays * 24 * time.Hour))
	if err := timelineHints(ctx, d.SQL(), project, since, &b); err != nil {
		b.WriteString(fmt.Sprintf("timeline query failed: %v\n", err))
	}
	b.WriteString("Nothing above is memory: decide yourself whether any of it is a lesson worth filing (skill stow).\n")
	return recallPart{8, title, b.String()}, true
}

func timelineHints(ctx context.Context, q *sql.DB, project, since string, b *strings.Builder) error {
	var pct sql.NullFloat64
	err := q.QueryRowContext(ctx, `SELECT n.context_pct FROM v_now n WHERE n.project = ? AND n.actor_kind = 'mate' LIMIT 1`, project).Scan(&pct)
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && !pct.Valid):
		b.WriteString("Mate context: not measured yet\n")
	case err != nil:
		return err
	default:
		fmt.Fprintf(b, "Mate context: %.0f%% of the window after your latest turn\n", pct.Float64)
	}

	rows, err := q.QueryContext(ctx, `
		SELECT e.at, c.name, m.text
		  FROM message m
		  JOIN event e ON e.id = m.event_id
		  JOIN actor f ON f.id = m.from_actor_id
		  JOIN actor c ON c.id = m.to_actor_id
		  LEFT JOIN task t ON t.crew_actor_id = c.id
		 WHERE e.project = ? AND f.kind = 'mate' AND c.kind = 'crew' AND e.at >= ?
		   AND (t.spawned_at IS NULL OR t.spawned_at = '' OR e.at > t.spawned_at)
		 ORDER BY e.at DESC LIMIT ?`, project, since, recallHintLimit)
	if err != nil {
		return err
	}
	corrections, err := hintRows(rows)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "lines you sent crews after their spawn, last %d days: %d\n", recallHintDays, len(corrections))
	for _, r := range corrections {
		fmt.Fprintf(b, "- %s to %s: %s\n", r.at, r.who, strconv.Quote(recallClip(r.text, 160)))
	}

	rows, err = q.QueryContext(ctx, `
		SELECT q.asked_at, c.name, q.text, COALESCE(q.answered_at, '')
		  FROM question q
		  JOIN actor c ON c.id = q.crew_actor_id
		 WHERE c.project = ? AND q.asked_at >= ?
		 ORDER BY q.asked_at DESC LIMIT ?`, project, since, recallHintLimit)
	if err != nil {
		return err
	}
	questions, err := hintRows(rows)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "questions crews asked, last %d days: %d\n", recallHintDays, len(questions))
	for _, r := range questions {
		state := "answered"
		if r.extra == "" {
			state = "unanswered"
		}
		fmt.Fprintf(b, "- %s %s (%s): %s\n", r.at, r.who, state, strconv.Quote(recallClip(r.text, 160)))
	}
	return nil
}

type hintRow struct{ at, who, text, extra string }

func hintRows(rows *sql.Rows) ([]hintRow, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []hintRow
	for rows.Next() {
		var r hintRow
		var at string
		dest := []any{&at, &r.who, &r.text}
		if len(cols) > 3 {
			dest = append(dest, &r.extra)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		r.at = db.ParseTime(at).Local().Format(recallTimeLayout)
		out = append(out, r)
	}
	return out, rows.Err()
}

// recallClip flattens s to one line and bounds it to n runes.
func recallClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// relToProject writes path relative to the project directory when it is
// inside it, the way every source in the Mate's memory is written.
func relToProject(w *store.Workspace, project, path string) string {
	rel, err := filepath.Rel(w.ProjectDir(project), path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}
