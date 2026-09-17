package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/matev2/internal/hook"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// cmdHook dispatches `matev2 hook <mate-prompt|mate-stop>` (docs/mvp.md task
// 08). These are hidden, agent-facing subcommands Claude Code's own hook
// runner invokes on the Mate pane; a human never types them, and they are
// deliberately left out of every usage string in this package.
func cmdHook(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: matev2 hook <mate-prompt|mate-stop>")
	}
	switch args[0] {
	case "mate-prompt":
		runHook(stdin, stderr, "mate-prompt", hook.HandlePrompt)
	case "mate-stop":
		runHook(stdin, stderr, "mate-stop", hook.HandleStop)
	default:
		return newUsageErrorf("unknown hook subcommand %q", args[0])
	}
	return nil
}

// runHook is the shared shape of both hook commands: resolve the project's
// workspace, read the payload whole, run the handler, and report any
// failure to stderr only. Both hooks always exit 0 and never print to
// stdout: a Claude Code UserPromptSubmit hook's stdout becomes model
// context, and Stop already fires unreliably (docs/mvp.md section 7), so
// this path must never itself be the thing that blocks or corrupts a turn.
func runHook(stdin io.Reader, stderr io.Writer, name string, handle func(*store.Workspace, string, []byte) error) {
	root, project, err := resolveHookTarget()
	if err != nil {
		fmt.Fprintf(stderr, "matev2 hook %s: %v\n", name, err)
		return
	}
	w, err := store.Open(root)
	if err != nil {
		fmt.Fprintf(stderr, "matev2 hook %s: %v\n", name, err)
		return
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "matev2 hook %s: read stdin: %v\n", name, err)
		return
	}
	if err := handle(w, project, data); err != nil {
		fmt.Fprintf(stderr, "matev2 hook %s: %v\n", name, err)
	}
}

// resolveHookTarget finds the workspace root and project a hook process
// runs for. MATEV2_WORKSPACE names the root directly when the pane
// environment carries it (findWorkspaceDir checks it); MATEV2_PROJECT names
// the project directly the same way. Neither is guaranteed to be set - see
// cmd/matev2/hook.go's sibling note in the task 08 report about the
// allowlist - so the fallback is what always works: a Mate's cwd is always
// exactly `<root>/.matev2/projects/<project>/mate` (docs/mvp.md section 3),
// so once the root is found the project is just the path component right
// after `.matev2/projects`.
func resolveHookTarget() (root, project string, err error) {
	root, err = findWorkspaceDir("")
	if err != nil {
		return "", "", err
	}
	project = strings.TrimSpace(os.Getenv("MATEV2_PROJECT"))
	if project != "" {
		return root, project, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	project, err = projectFromCwd(root, cwd)
	if err != nil {
		return "", "", err
	}
	return root, project, nil
}

// projectFromCwd derives the project name from a Mate's cwd, which is
// `<root>/.matev2/projects/<project>/mate`.
func projectFromCwd(root, cwd string) (string, error) {
	projectsDir := filepath.Join(root, store.StateDirName, "projects")
	rel, err := filepath.Rel(projectsDir, cwd)
	if err != nil {
		return "", err
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 || parts[0] == "" || parts[0] == "." || strings.HasPrefix(parts[0], "..") {
		return "", fmt.Errorf("cannot find the project for cwd %s under %s (set MATEV2_PROJECT)", cwd, projectsDir)
	}
	if err := store.ValidateProjectName(parts[0]); err != nil {
		return "", err
	}
	return parts[0], nil
}
