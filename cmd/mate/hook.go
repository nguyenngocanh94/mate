package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/hook"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdHook dispatches `mate hook <mate-prompt|mate-stop|mate-session>`
// (docs/mvp.md tasks 08 and 37). These are hidden, agent-facing subcommands Claude Code's own hook
// runner invokes on the Mate pane; a human never types them, and they are
// deliberately left out of every usage string in this package.
func cmdHook(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate hook <mate-prompt|mate-stop|mate-session>")
	}
	switch args[0] {
	case "mate-prompt":
		runHook(stdin, stderr, "mate-prompt", hook.HandlePrompt)
	case "mate-stop":
		runHook(stdin, stderr, "mate-stop", hook.HandleStop)
	case harness.SessionHookName:
		fs := flag.NewFlagSet("hook "+harness.SessionHookName, flag.ContinueOnError)
		fs.SetOutput(stderr)
		harnessFlag := fs.String("harness", "", "the registered harness running the hook (default: the one whose hook names none)")
		if err := fs.Parse(args[1:]); err != nil {
			return &usageError{err}
		}
		kind, err := sessionHookHarness(*harnessFlag)
		if err != nil {
			return &usageError{err}
		}
		runSessionHook(stdin, stdout, stderr, kind, time.Now())
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
		fmt.Fprintf(stderr, "mate hook %s: %v\n", name, err)
		return
	}
	w, err := store.Open(root)
	if err != nil {
		fmt.Fprintf(stderr, "mate hook %s: %v\n", name, err)
		return
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "mate hook %s: read stdin: %v\n", name, err)
		return
	}
	if err := handle(w, project, data); err != nil {
		fmt.Fprintf(stderr, "mate hook %s: %v\n", name, err)
	}
}

// sessionHookHarness is the harness a `hook mate-session` runs for: the one
// --harness names, or without it the one harness whose hook names none
// (HookInstaller.BareSessionHook) - a hook in a settings file written
// before its harness learnt to name itself, or by the captain, says `hook
// mate-session` and nothing more.
func sessionHookHarness(flag string) (harness.Kind, error) {
	if flag != "" {
		return harnesses.Parse(flag)
	}
	var bare []harness.Kind
	for _, k := range harnesses.Kinds() {
		p, err := harnesses.Lookup(k)
		if err != nil {
			return "", err
		}
		if hooks := p.Capabilities().Hooks; hooks.Verified() && hooks.Impl.BareSessionHook() {
			bare = append(bare, k)
		}
	}
	if len(bare) != 1 {
		return "", fmt.Errorf("hook %s: --harness is required, because %d registered harnesses run this hook without naming themselves", harness.SessionHookName, len(bare))
	}
	return bare[0], nil
}

// runSessionHook is `mate hook mate-session` (docs/mvp.md task 37, B2):
// the SessionStart hook of both harnesses. It records the session id the
// payload carries (hook.HandleSessionStart), then prints the digest the
// source calls for - part 1 of `mate recall` for a resume, the whole
// digest for startup, clear, compact and anything else - sized to what the
// harness puts in context. Its stdout is the model's context, so every
// failure after the workspace is found still prints one line telling the
// Mate to run recall itself; like the other hooks it always exits 0.
func runSessionHook(stdin io.Reader, stdout, stderr io.Writer, kind harness.Kind, now time.Time) {
	const name = harness.SessionHookName
	root, project, err := resolveHookTarget()
	if err != nil {
		fmt.Fprintf(stderr, "mate hook %s: %v\n", name, err)
		return
	}
	bin := selfBinary()
	fallback := func(err error) {
		fmt.Fprintf(stderr, "mate hook %s: %v\n", name, err)
		fmt.Fprintf(stdout, "mate could not print your session-start digest (%v). Run `%s recall %s` before acting on anything.\n", oneLineMsg(err), bin, project)
	}
	w, err := store.Open(root)
	if err != nil {
		fallback(err)
		return
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		fallback(fmt.Errorf("read stdin: %w", err))
		return
	}
	start, err := hook.HandleSessionStart(w, project, data)
	if err != nil {
		// The session still started: say so, and print the digest anyway.
		fmt.Fprintf(stderr, "mate hook %s: %v\n", name, err)
	}
	source := start.Source
	if source == "" {
		source = "unknown source"
	}
	// The digest is sized to what the harness puts in context whole. A
	// harness without verified hooks never runs a Mate (spawn.StartMate),
	// so a hook that names one is told to recall by hand rather than given
	// a digest of a guessed size.
	profile, err := harnesses.Lookup(kind)
	if err != nil {
		fallback(err)
		return
	}
	hooks := profile.Capabilities().Hooks
	if !hooks.Verified() {
		fallback(fmt.Errorf("the %s harness declares no verified Hooks capability", kind))
		return
	}
	opts := recallOptions{
		LiveOnly: start.LiveOnly(),
		MaxBytes: hooks.Impl.DigestMaxBytes(),
		Occasion: "session start: " + source,
		Binary:   bin,
	}
	text, err := recall(context.Background(), w, gitx.New(), project, now, opts)
	if err != nil {
		fallback(err)
		return
	}
	_, _ = io.WriteString(stdout, text)
}

// oneLineMsg is an error's first line.
func oneLineMsg(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return strings.TrimSpace(msg)
}

// resolveHookTarget finds the workspace root and project a hook process
// runs for. MATE_WORKSPACE names the root directly when the pane
// environment carries it (findWorkspaceDir checks it); MATE_PROJECT names
// the project directly the same way. Neither is guaranteed to be set - see
// cmd/mate/hook.go's sibling note in the task 08 report about the
// allowlist - so the fallback is what always works: a Mate's cwd is always
// exactly `<root>/.mate/projects/<project>/mate` (docs/mvp.md section 3),
// so once the root is found the project is just the path component right
// after `.mate/projects`.
func resolveHookTarget() (root, project string, err error) {
	root, err = findWorkspaceDir("")
	if err != nil {
		return "", "", err
	}
	project = strings.TrimSpace(os.Getenv("MATE_PROJECT"))
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
// `<root>/.mate/projects/<project>/mate`.
func projectFromCwd(root, cwd string) (string, error) {
	projectsDir := filepath.Join(root, store.StateDirName, "projects")
	rel, err := filepath.Rel(projectsDir, cwd)
	if err != nil {
		return "", err
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 || parts[0] == "" || parts[0] == "." || strings.HasPrefix(parts[0], "..") {
		return "", fmt.Errorf("cannot find the project for cwd %s under %s (set MATE_PROJECT)", cwd, projectsDir)
	}
	if err := store.ValidateProjectName(parts[0]); err != nil {
		return "", err
	}
	return parts[0], nil
}
