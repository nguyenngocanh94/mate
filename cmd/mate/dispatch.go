package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// usageError marks an error as a CLI usage mistake: bad flags, wrong number
// of arguments, an unknown subcommand. mainRun exits 2 for these and 1 for
// everything else.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func newUsageError(msg string) error { return &usageError{errors.New(msg)} }

func newUsageErrorf(format string, a ...any) error { return &usageError{fmt.Errorf(format, a...)} }

// run dispatches the top-level subcommand. Each subcommand is a small,
// independently testable function of the form
// func(args []string, stdout, stderr io.Writer) error.
func run(args []string, stdout, stderr io.Writer) error {
	// `mate` with no arguments opens the console on the workspace the
	// current directory sits in. A subcommand always wins over a directory
	// of the same name: the subcommand names are reserved words, and an
	// explicit `./init` still reaches the console.
	if len(args) == 0 {
		dir, err := findWorkspaceDir("")
		if err != nil {
			return newUsageError("usage: mate <workspace-dir> | mate <init|project|mate|crew|brief|remember|memory|recall|send|peek|state|diff|merge|backlog|events|reindex|usage|dashboard|console|--version> ...")
		}
		return cmdConsole(dir, stdout, stderr)
	}
	switch args[0] {
	case "--version", "-V":
		return cmdVersion(args[1:], stdout, stderr)
	case "init":
		return cmdInit(args[1:], stdout, stderr)
	case "project":
		return cmdProject(args[1:], stdout, stderr)
	case "mate":
		return cmdMate(args[1:], stdout, stderr)
	case "crew":
		return cmdCrew(args[1:], os.Stdin, stdout, stderr)
	case "brief":
		return cmdBrief(args[1:], os.Stdin, stdout, stderr)
	case "remember":
		return cmdRemember(args[1:], stdout, stderr)
	case "memory":
		return cmdMemory(args[1:], stdout, stderr)
	case "recall":
		return cmdRecall(args[1:], stdout, stderr)
	case "hook":
		return cmdHook(args[1:], os.Stdin, stdout, stderr)
	case "send":
		return cmdSend(args[1:], stdout, stderr)
	case "peek":
		return cmdPeek(args[1:], stdout, stderr)
	case "state":
		return cmdState(args[1:], stdout, stderr)
	case "diff":
		return cmdDiff(args[1:], stdout, stderr)
	case "merge":
		return cmdMerge(args[1:], stdout, stderr)
	case "backlog":
		return cmdBacklog(args[1:], stdout, stderr)
	case "events":
		return cmdEvents(args[1:], stdout, stderr)
	case "reindex":
		return cmdReindex(args[1:], stdout, stderr)
	case "usage":
		return cmdUsage(args[1:], stdout, stderr)
	case "dashboard":
		return cmdDashboard(args[1:], stdout, stderr)
	case "console":
		return cmdConsoleLaunch(args[1:], stdout, stderr)
	}
	// A single argument naming an existing directory is a workspace to open.
	if len(args) == 1 && isDir(args[0]) {
		return cmdConsole(args[0], stdout, stderr)
	}
	return newUsageErrorf("unknown command %q", args[0])
}

// isDir reports whether path names an existing directory, following
// symlinks the way every other path in the CLI does.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// cmdProject dispatches `mate project <add|list|remove>`.
func cmdProject(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate project <add|list|remove|repo|yolo|facts> ...")
	}
	switch args[0] {
	case "facts":
		return cmdProjectFacts(args[1:], stdout, stderr)
	case "add":
		return cmdProjectAdd(args[1:], stdout, stderr)
	case "list":
		return cmdProjectList(args[1:], stdout, stderr)
	case "remove":
		return cmdProjectRemove(args[1:], stdout, stderr)
	case "repo":
		return cmdProjectRepo(args[1:], stdout, stderr)
	case "yolo":
		return cmdProjectYolo(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown project subcommand %q", args[0])
	}
}

// reorderArgs moves every flag (and its value, if it takes one) in front of
// the positional arguments, in the order the caller wrote them, so a
// subcommand accepts its flags before, after, or interspersed with its
// positional arguments. The standard flag package otherwise stops parsing at
// the first non-flag token, which would make `mate project add <name>
// <repo-path> --workspace <dir>` silently ignore --workspace.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue // value is already part of this token
		}
		fl := fs.Lookup(name)
		if fl != nil {
			if bf, ok := fl.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
				continue // boolean flags take no separate value
			}
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

// resolveWorkspace opens the workspace named by flagVal, or, if flagVal is
// empty, the one found by findWorkspaceDir.
func resolveWorkspace(flagVal string) (*store.Workspace, error) {
	dir, err := findWorkspaceDir(flagVal)
	if err != nil {
		return nil, err
	}
	return store.Open(dir)
}

// findWorkspaceDir resolves the workspace directory per docs/mvp.md task 04:
// the --workspace flag if given, else MATE_WORKSPACE, else the nearest
// ancestor of the current directory that contains `.mate/`.
func findWorkspaceDir(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if env := os.Getenv("MATE_WORKSPACE"); env != "" {
		return env, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if fi, statErr := os.Stat(filepath.Join(dir, store.StateDirName)); statErr == nil && fi.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s workspace found in %s or any parent directory (use --workspace or set MATE_WORKSPACE)", store.StateDirName, cwd)
		}
		dir = parent
	}
}
