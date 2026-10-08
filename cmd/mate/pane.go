package main

import (
	"context"
	"flag"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/nguyenngocanh94/mate/internal/panerun"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

// Column roles mate owns: the agent stage, and the tasks tab (docs/mvp.md
// M13). A tool's tab takes the role its key binding names (toolRoles).
const (
	roleStage = "stage"
	roleTasks = "tasks"
)

// paneIdle is what each column of a fixed role says while it shows
// nothing. A tool's tab says its Viewer's Placeholder (paneIdleOf).
var paneIdle = map[string]string{
	roleStage: "mate · agent\r\n\r\nEnter on a Mate or Crew row in the console shows it here.",
	roleTasks: "mate · tasks\r\n\r\nt on a project opens Beads Viewer here.",
}

// paneIdleOf is what the pane of role says while it shows nothing: a
// fixed role's line, or the placeholder of the tool whose key binds role.
func paneIdleOf(role string, tools tool.Registry) (string, bool) {
	if idle, ok := paneIdle[role]; ok {
		return idle, true
	}
	for _, b := range tools.Bindings() {
		if b.Role != role {
			continue
		}
		if p, err := tools.Lookup(b.Tool); err == nil {
			return p.Capabilities().Viewer.Impl.Placeholder(), true
		}
	}
	return "", false
}

// paneRoles is every role a pane can serve, for the usage line:
// "stage|review|tasks".
func paneRoles(tools tool.Registry) string {
	return strings.Join(slices.Concat([]string{roleStage}, toolRoles(tools), []string{roleTasks}), "|")
}

// cmdPane dispatches `mate pane serve`, the program the Console's columns
// run. It is not for people: the Console starts it in the panes it lays
// out.
func cmdPane(args []string, stdout, stderr io.Writer) error {
	roles := paneRoles(tools)
	if len(args) == 0 || args[0] != "serve" {
		return newUsageError("usage: mate pane serve --socket <path> --role " + roles + " [--owner <pid>]")
	}
	fs := flag.NewFlagSet("pane serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", "", "unix socket the Console sends to")
	role := fs.String("role", "", "one of "+strings.ReplaceAll(roles, "|", ", "))
	owner := fs.Int("owner", 0, "the Console's pid; the pane ends when it is gone")
	if err := fs.Parse(args[1:]); err != nil {
		return &usageError{err}
	}
	idle, ok := paneIdleOf(*role, tools)
	if *socket == "" || !ok {
		return newUsageError("mate pane serve: --socket and --role " + roles + " are required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGTERM)
	defer cancel()
	// Caught, not ignored: an ignored signal stays ignored across exec, and
	// the child must get its own default handling.
	swallow := make(chan os.Signal, 1)
	signal.Notify(swallow, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP)
	go func() {
		for range swallow {
		}
	}()
	s := &panerun.Server{Idle: idle, Owner: *owner, Term: paneTerminal(), Occupants: ttyOccupants(os.Getpid())}
	// main prints the error once, prefixed; printing it here as well put it
	// on the pane twice.
	return s.Serve(ctx, *socket)
}

// paneTerminal is this process's tty, with the modes it started in saved
// so every child after the first starts from them.
func paneTerminal() panerun.Terminal {
	t := panerun.Terminal{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	fd := os.Stdin.Fd()
	if state, err := term.GetState(fd); err == nil {
		t.Restore = func() { _ = term.Restore(fd, state) }
	}
	return t
}

// ttyOccupants lists the processes on this pane's terminal other than the
// runner and the processes it runs under (Ghostty's login wrapper): what a
// program left there after its own process exited. nil off a tty.
func ttyOccupants(self int) func() []int {
	tty := strings.TrimSpace(psOut("-o", "tty=", "-p", strconv.Itoa(self)))
	if tty == "" || strings.HasPrefix(tty, "?") {
		return nil
	}
	keep := []int{self}
	for pid := os.Getppid(); pid > 1 && len(keep) < 16; {
		keep = append(keep, pid)
		next, err := strconv.Atoi(strings.TrimSpace(psOut("-o", "ppid=", "-p", strconv.Itoa(pid))))
		if err != nil {
			break
		}
		pid = next
	}
	return func() []int {
		var out []int
		for _, line := range strings.Split(psOut("-t", tty, "-o", "pid=,ppid="), "\n") {
			f := strings.Fields(line)
			if len(f) != 2 {
				continue
			}
			pid, err1 := strconv.Atoi(f[0])
			ppid, err2 := strconv.Atoi(f[1])
			// The runner's own children are its business, not the sweep's:
			// the child it started, and this very ps listing.
			if err1 != nil || err2 != nil || slices.Contains(keep, pid) || ppid == self {
				continue
			}
			out = append(out, pid)
		}
		return out
	}
}

func psOut(args ...string) string {
	out, err := exec.Command("ps", args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}
