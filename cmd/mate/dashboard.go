package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdDashboard is `mate dashboard [<workspace>] [--addr 127.0.0.1:7777]
// [--open] [--allow-remote]` (docs/mvp.md M6 task 28): the workspace's
// timeline in a browser, read-only.
//
// It opens the database with db.OpenRead, which takes no lock, so it runs
// beside an open console rather than instead of it. It prints the URL and
// serves until Ctrl+C.
func cmdDashboard(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate dashboard [<workspace>] [--addr host:port] [--open] [--allow-remote]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	addr := fs.String("addr", dashboard.DefaultAddr, "address to bind")
	open := fs.Bool("open", false, "open the dashboard in the platform browser")
	allowRemote := fs.Bool("allow-remote", false, "permit a non-loopback bind (the timeline quotes everything the captain typed)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	dir := *workspaceFlag
	switch fs.NArg() {
	case 0:
	case 1:
		if dir != "" {
			fs.Usage()
			return newUsageError("mate dashboard: give the workspace once, as an argument or as --workspace")
		}
		dir = fs.Arg(0)
	default:
		fs.Usage()
		return newUsageError("mate dashboard: want at most 1 argument: [<workspace>]")
	}

	w, err := resolveWorkspace(dir)
	if err != nil {
		return err
	}
	handle, err := db.OpenRead(w)
	if err != nil {
		return err
	}
	defer handle.Close()

	server, err := dashboard.New(dashboard.Options{
		Workspace: w, DB: handle, Addr: *addr, AllowRemote: *allowRemote,
		Deps: dashboardDeps(w),
	})
	if err != nil {
		return err
	}
	ln, err := dashboard.Listen(dashboard.Options{Addr: *addr, AllowRemote: *allowRemote})
	if err != nil {
		return err
	}
	defer ln.Close()

	url := dashboard.URL(ln)
	fmt.Fprintf(stdout, "mate dashboard on %s (read-only; Ctrl+C to stop)\n", url)
	if *open {
		if err := openInBrowser(url); err != nil {
			// Not fatal: the URL is already on the terminal, and a machine
			// with no opener is not a reason to refuse to serve.
			fmt.Fprintf(stderr, "could not open a browser (%v); the address is above\n", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Serve(ctx, ln)
}

// dashboardDeps wires the two answers the database does not hold. Diff is
// the CLI's own crewDiffText, so the page shows byte for byte what
// `mate diff` prints; BranchExists is `internal/gitx` over the project's
// primary checkout, which is where a branch outliving its worktree lives
// (see crewDiffText's own note).
func dashboardDeps(w *store.Workspace) dashboard.Deps {
	git := gitx.New()
	return dashboard.Deps{
		Diff: func(ctx context.Context, project, crew string) (string, error) {
			return crewDiffText(ctx, w, git, project, crew, false)
		},
		BranchExists: func(ctx context.Context, project, branch string) (bool, error) {
			cfg, err := w.LoadProject(project)
			if err != nil {
				return false, err
			}
			return git.BranchExists(ctx, w.RepoDir(cfg.Repo), branch)
		},
	}
}

// openInBrowser is `--open`. One command per platform, the same three every
// tool uses; a machine where none of them exists says so rather than
// pretending it opened something.
func openInBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		name = "xdg-open"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("no %s on PATH", name)
	}
	return exec.Command(path, append(args, url)...).Start()
}
