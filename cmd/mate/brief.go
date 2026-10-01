package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdBrief dispatches `mate brief <check|append>` (docs/mvp.md M7 task 32).
func cmdBrief(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate brief <check|append> ...")
	}
	switch args[0] {
	case "check":
		return cmdBriefCheck(args[1:], stdout, stderr)
	case "append":
		return cmdBriefAppend(args[1:], stdin, stdout, stderr)
	default:
		return newUsageErrorf("unknown brief subcommand %q", args[0])
	}
}

// cmdBriefCheck implements `mate brief check <file> [--scout]`: the same
// shape check `crew spawn` runs, on its own, so the Mate can fix a brief
// before it spawns anything. It prints one line per problem on stderr and
// exits 1 on any; it judges shape, never meaning.
func cmdBriefCheck(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("brief check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate brief check <file> [--scout]")
	}
	scoutFlag := fs.Bool("scout", false, "check it as a scout brief, which needs ## Deliverable")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate brief check: want exactly 1 argument: <file>")
	}
	path := fs.Arg(0)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("brief check: %w", err)
	}
	kind := brief.Ship
	if *scoutFlag {
		kind = brief.Scout
	}
	problems := brief.CheckFile(string(data), kind)
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(stderr, p.String())
		}
		return fmt.Errorf("brief check: %d shape problem(s) in %s as a %s brief", len(problems), path, kind)
	}
	fmt.Fprintf(stdout, "brief ok: %s has every section of a %s brief (%s)\n",
		path, kind, strings.Join(brief.Sections(kind), ", "))
	return nil
}

// cmdBriefAppend implements `mate brief append <project> <crew> [-]`: the
// captain's later words, read from stdin, appended verbatim to the end of
// `## Captain's words` in `crews/<id>/brief.md`, and the Crew told in one
// verified line where to read them. It exists because the brief the Crew
// reads lives under `crews/`, where the Mate may not write.
func cmdBriefAppend(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("brief append", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate brief append <project> <crew> [-] [--workspace <dir>]   (the captain's words on stdin)")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if n := fs.NArg(); n < 2 || n > 3 || (n == 3 && fs.Arg(2) != "-") {
		fs.Usage()
		return newUsageError("mate brief append: want <project> <crew> and optionally -, with the captain's words on stdin")
	}
	words, err := spawn.ReadBriefStdin(stdin)
	if err != nil {
		return err
	}
	if strings.TrimSpace(words) == "" {
		return newUsageError("mate brief append: stdin is empty; pipe the captain's words in")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	source, err := resolveSendSource("")
	if err != nil {
		return err
	}
	res, err := briefAppend(context.Background(), w, spawn.LiveDeps(harnesses), fs.Arg(0), fs.Arg(1), words, source, time.Now())
	if res.Appended {
		fmt.Fprintf(stdout, "appended the captain's words to %s %s\n", res.BriefPath, res.Added)
	}
	if err != nil {
		if res.Appended {
			printSendRefusalDetails(stderr, err)
			fmt.Fprintf(stderr, "the words are in the brief but the crew was not told; once it is free, run: mate send %s %s %q\n", fs.Arg(0), fs.Arg(1), res.Line)
		}
		return err
	}
	fmt.Fprintln(stdout, sendSummaryLine(res.Report))
	return nil
}

// briefAppendResult is what briefAppend did, reported even on failure so
// the caller can say that the brief changed while the send did not.
type briefAppendResult struct {
	Appended  bool
	BriefPath string
	Added     string
	Line      string
	Report    send.Report
}

// briefAppend is brief append's core, separate from flag parsing so a test
// can drive it over a fake runtime. It refuses before touching the brief
// when the crew has no live agent to tell, because words nobody is told
// about are a brief that disagrees with the work in flight.
func briefAppend(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, crew, words, source string, now time.Time) (briefAppendResult, error) {
	res := briefAppendResult{BriefPath: w.CrewBrief(project, crew)}
	if err := store.ValidateProjectName(project); err != nil {
		return res, err
	}
	if err := store.ValidateCrewID(crew); err != nil {
		return res, err
	}
	resolved, err := resolveCrewHandle(ctx, w, deps, project, crew)
	if err != nil {
		return res, err
	}
	if !resolved.AgentRecorded || !resolved.SessionRunning {
		return res, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("crew %s/%s has no live agent to tell; the brief was not changed", project, crew))
	}
	current, err := os.ReadFile(res.BriefPath)
	if err != nil {
		return res, fmt.Errorf("brief append: %w", err)
	}
	updated, err := brief.AppendCaptainsWords(string(current), words, now)
	if err != nil {
		return res, fmt.Errorf("brief append: %s: %w", res.BriefPath, err)
	}
	// Only crews/<id>/brief.md changes. A Codex crew's AGENTS.override.md
	// copy in the worktree is its launch context and stays as launched; the
	// line below points the crew at the brief, which is the record, exactly
	// as the first prompt did (spawn.BriefPrompt).
	if err := w.ReplaceCrewBrief(project, crew, []byte(updated)); err != nil {
		return res, err
	}
	res.Appended = true
	res.Added = brief.AddedLine(now)
	res.Line = briefAppendLine(res.BriefPath, res.Added)
	res.Report, err = sendToCrew(ctx, w, deps, project, crew, res.Line, source, send.Options{})
	return res, err
}

// briefAppendLine is the one line a Crew receives after `brief append`.
func briefAppendLine(briefPath, added string) string {
	return fmt.Sprintf("The captain added to your task: read the words marked %s at the end of ## %s in %s, then carry on with them.",
		added, brief.CaptainsWords, briefPath)
}
