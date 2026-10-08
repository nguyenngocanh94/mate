package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/prwatch"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// prStarter is how `mate pr watch` starts the detached watcher; tests swap it
// for one that starts nothing.
var prStarter prwatch.Starter = prwatch.ExecStarter{}

// pullURL is a GitHub pull request URL, the one thing `mate pr watch`
// accepts: gh prints it when `gh pr create` succeeds.
var pullURL = regexp.MustCompile(`^https://[^/\s]+/[^/\s]+/[^/\s]+/pull/[0-9]+$`)

// cmdPR dispatches `mate pr <watch>`.
func cmdPR(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate pr watch <project> <crew> <pull-request-url>")
	}
	switch args[0] {
	case "watch":
		return cmdPRWatch(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown pr subcommand %q", args[0])
	}
}

// cmdPRWatch implements `mate pr watch <project> <crew> <url>`
// (docs/mvp.md M18): record the pull request in the crew's meta and start a
// detached watcher, then return at once. The watcher polls `gh pr view` every
// minute and, when the pull request is merged or closed, updates the primary
// checkout and the crew's meta and status and wakes the Mate
// (internal/prwatch). The hidden `--run` flag is the detached child itself.
func cmdPRWatch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("pr watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate pr watch <project> <crew> <pull-request-url> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	runFlag := fs.Bool("run", false, "run the watcher in the foreground (the detached child; not for typing)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 3 {
		fs.Usage()
		return newUsageError("mate pr watch: want exactly 3 arguments: <project> <crew> <pull-request-url>")
	}
	project, crew, url := fs.Arg(0), fs.Arg(1), strings.TrimSpace(fs.Arg(2))
	if !pullURL.MatchString(url) {
		return newUsageErrorf("mate pr watch: %q is not a pull request URL (want https://github.com/<owner>/<repo>/pull/<n>)", url)
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if _, ok := w.Project(project); !ok {
		return fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return err
	}
	if len(meta) == 0 {
		return fmt.Errorf("no crew %s is recorded for project %s", crew, project)
	}
	if crewstate.Declare(crewstate.Declaration{Meta: meta}).Closed() {
		return fmt.Errorf("crew %s/%s is closed; there is nothing to watch", project, crew)
	}

	if *runFlag {
		return runPRWatch(w, project, crew, url, stdout, stderr)
	}

	// A merged pull request is over for good. A closed one is watched
	// again: it may have been reopened (docs/mvp.md M19), and if it is still
	// closed the watcher's line to the Mate is the one already delivered,
	// which the outbox does not type twice.
	if meta[crewstate.MetaPRURL] == url && meta[crewstate.MetaPRState] == crewstate.PRStateMerged {
		fmt.Fprintf(stdout, "%s/%s: %s is already merged; nothing to watch\n", project, crew, url)
		return nil
	}
	if err := w.UpdateCrewMeta(project, crew, map[string]string{
		crewstate.MetaPRURL:   url,
		crewstate.MetaPRState: crewstate.PRStateOpen,
	}); err != nil {
		return err
	}
	started, err := prwatch.Ensure(w, prStarter, project, crew, url)
	if err != nil {
		return err
	}
	if started.Already {
		fmt.Fprintf(stdout, "%s/%s: already watching %s (pid %d)\n", project, crew, url, started.PID)
		return nil
	}
	fmt.Fprintf(stdout, "%s/%s: watching %s in the background (pid %d, log %s); the Mate is woken when it is merged or closed\n",
		project, crew, url, started.PID, w.CrewPRWatchLog(project, crew))
	return nil
}

// runPRWatch is the detached child: it records its own pid, watches until
// the pull request ends or it is signalled, and removes its pid file. A Jev
// configuration problem goes to stderr, which the detached child appends to
// its log as it does stdout.
func runPRWatch(w *store.Workspace, project, crew, url string, stdout, stderr io.Writer) error {
	pid := os.Getpid()
	if err := prwatch.WritePID(w, project, crew, pid); err != nil {
		return err
	}
	defer prwatch.RemovePID(w, project, crew, pid)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	deps := prwatch.Deps{
		WS:     w,
		GH:     ghClient,
		Git:    gitx.New(),
		Outbox: consoleOutbox(w, liveDeps(w, stderr)),
		Log:    stdout,
	}
	if err := prwatch.Watch(ctx, deps, project, crew, url); err != nil {
		fmt.Fprintf(stdout, "%s watch of %s ended: %v\n", project, url, err)
		return err
	}
	return nil
}
