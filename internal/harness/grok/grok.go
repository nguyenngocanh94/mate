// Package grok is the Grok Build CLI (grok 1.0.46) as a mate harness: its
// Profile, launch, screens and session transcripts. Everything that knows
// grok lives here; the contract it implements is internal/harness.
//
// grok runs Crews only. It loads hooks, but mate has not measured a hook
// command against `mate hook`, and a Mate's memory and inbox rest on hooks.
// Every number here was measured on grok 1.0.46 in a 93x39 pane, read as
// visible text.
package grok

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// KindGrok is grok's kind, and what Herdr is told at `agent start --kind`.
const KindGrok harness.Kind = "grok"

// Grok is the Profile of grok 1.0.46.
type Grok struct {
	// Home is the grok home whose sessions directory holds sessions. Empty
	// means grok's own: $GROK_HOME, or ~/.grok (sessionsRoot). The launch
	// does not set it. Authentication lives in that home, so a Crew uses
	// the operator's.
	Home string
}

// homeEnv is the variable grok reads its home from. It is not one of
// Info.EnvKeys: a pane is not given a different home from the operator's,
// because that home is where grok keeps its credentials.
const homeEnv = "GROK_HOME"

// Kind implements Profile.
func (Grok) Kind() harness.Kind { return KindGrok }

// Info implements Profile.
func (Grok) Info() harness.Info {
	return harness.Info{
		Name:        "Grok",
		RuntimeKind: string(KindGrok),
		// grok reads project resources from `.grok/` in its cwd. The launch
		// passes --trust, which loads the project's AGENTS.md; spawn already
		// records that file for every Crew. `.grok/config.toml` is the
		// project file grok reads beside it.
		ConfigDir: ".grok",
		// grok has no brand mark that was measured as one cell, so it draws
		// the same letter in every alphabet.
		Icon: harness.Icon{Nerd: "G", Unicode: "G", ASCII: "G"},
		Documents: []harness.Document{
			{Path: ".grok/config.toml", Role: "repo_grok_config"},
		},
		// grok-4.7's menu is low, medium, high and xhigh. max is not in it,
		// so a requested max is recorded and left out of the argv.
		Efforts:      []harness.Effort{harness.EffortLow, harness.EffortMedium, harness.EffortHigh, harness.EffortXHigh},
		AdapterNotes: adapterNotes,
	}
}

// adapterNotes is grok's section of the harness-adapters skill.
//
//go:embed adapter.md
var adapterNotes string

// Launcher implements Profile.
func (g Grok) Launcher() harness.Launcher { return g }

// Screen implements Profile.
func (Grok) Screen() harness.ScreenProfile { return grokScreen{} }

func measured(proof string) harness.Evidence {
	return harness.Evidence{Version: "grok 1.0.46", Measured: "2026-10-04 (visible 93x39)", Proof: proof}
}

// hooksReason is why grok's Hooks are not verified, and so why it runs no Mate.
const hooksReason = "Grok loads Claude-compatible hooks from its home and from .grok/hooks, but mate has not measured a hook command whose stdin is the payload `mate hook` writes, so no HookInstaller is implemented"

// Capabilities implements Profile.
func (g Grok) Capabilities() harness.Capabilities {
	return harness.Capabilities{
		GracefulStop: harness.Cap[harness.GracefulStopper]{
			Status: harness.CapVerified,
			Impl:   grokExit{},
			Evidence: measured("one ctrl+c clears a draft; on an empty composer it arms \"press again to quit\" and does not exit; `/quit` then Enter exits and prints the --resume id. " +
				"A stop therefore presses ctrl+c once and then types /quit (TestGrokStopsByClearingOnceThenQuitting)"),
		},
		Session: harness.Cap[harness.SessionIdentity]{
			Status:   harness.CapVerified,
			Impl:     grokSessions{home: g.Home},
			Evidence: measured("--session-id with a new UUID created the session directory before the first prompt, quit printed `grok --resume <id>`, and --resume reopened that directory (TestGrokResumableLooksForTheDirectory)"),
		},
		Hooks: harness.Cap[harness.HookInstaller]{
			Status: harness.CapUnknown,
			Reason: hooksReason,
		},
		TurnEnd: harness.Cap[harness.TurnEndEvidence]{
			Status:   harness.CapVerified,
			Impl:     grokTurnEnd{},
			Evidence: measured("internal/harness/grok/testdata/session/updates.jsonl: the turn ends on sessionUpdate turn_completed, written when the turn ends (TestGrokTurnEndsOnTurnCompleted)"),
		},
		Transcript: harness.Cap[harness.TranscriptSource]{
			Status:   harness.CapVerified,
			Impl:     grokTranscripts{home: g.Home},
			Evidence: measured("internal/harness/grok/testdata/session/updates.jsonl through TestGrokTranscriptReadsTheMeasuredTurn and the catalog contract suite"),
		},
		Quota: harness.Cap[harness.QuotaProvider]{
			Status: harness.CapUnsupported,
			Reason: "quota-axi has no provider row for Grok; the account allowance is grok's own grok usage, which dispatch does not read",
		},
	}
}

// grokExit is grok's GracefulStopper. `/quit` is a line typed into the
// composer. One ctrl+c clears a draft; on an empty composer the same key
// only arms a second press to quit, so it is pressed once and the exit
// line follows.
type grokExit struct{}

// ClearKeys implements GracefulStopper.
func (grokExit) ClearKeys() []string { return []string{"ctrl+c"} }

// ExitPrompt implements GracefulStopper.
func (grokExit) ExitPrompt() string { return "/quit" }

// grokSessions is grok's SessionIdentity. A launch names its session, so
// nothing is left to read at stop.
type grokSessions struct{ home string }

// AtStop implements SessionIdentity.
func (grokSessions) AtStop(string, time.Time, string) string { return "" }

// Resumable implements SessionIdentity. The session directory exists before
// the first prompt, and updates.jsonl's turn_completed does not, so the
// directory is what a resume looks for.
func (s grokSessions) Resumable(id string) error {
	root, err := sessionsRoot(s.home)
	if err != nil {
		return fmt.Errorf("cannot look for the grok session %s to resume (%v)", id, err)
	}
	if _, ok := findSessionDir(root, id); !ok {
		return &harness.NoSessionError{Session: "the grok session " + id, Missing: root + " has no session directory for it"}
	}
	return nil
}

// PaneEnv implements Launcher. grok finds its home and its session from
// the process environment and the cwd, so a pane needs nothing pinned.
func (Grok) PaneEnv() ([]harness.EnvVar, error) { return nil, nil }

// grokLaunch is what grok's Prepare hands its Build. A fresh session sets
// sessionID (--session-id). A resumed one sets resumeID (--resume). The
// two flags are mutually exclusive.
type grokLaunch struct {
	sessionID string
	resumeID  string
}

// refuseMate is the refusal of a Mate on grok, naming the capability it lacks.
func refuseMate() error {
	return observability.NewError(observability.CodeUsage,
		"the Grok harness cannot run a Mate: it declares no verified Hooks capability ("+hooksReason+"); it can still run a Crew")
}

// Prepare implements Launcher. A grok Crew needs no file of its own: the
// brief is inlined on the command line, and grok writes its session itself.
// The launch names the session, so the id is minted here.
func (g Grok) Prepare(_ context.Context, req harness.PrepareRequest) (harness.Prepared, error) {
	if err := req.Check(KindGrok); err != nil {
		return harness.Prepared{}, err
	}
	if req.Role == harness.RoleMate {
		return harness.Prepared{}, refuseMate()
	}
	var launch grokLaunch
	sessionID := strings.TrimSpace(req.ResumeSessionID)
	if sessionID == "" {
		sessionID = req.MintSessionID()
		launch.sessionID = sessionID
	} else {
		launch.resumeID = sessionID
	}
	return harness.Prepared{SessionID: sessionID, ContextPath: req.ContextPath, Launch: launch}, nil
}

// contextFlag is the flag whose value is the brief's bytes. grok's --rules
// is an alias; the delivery check looks for this spelling.
const contextFlag = "--append-system-prompt"

// Build implements Launcher.
func (g Grok) Build(_ context.Context, spec harness.AgentSpec) (harness.LaunchSpec, error) {
	if spec.Kind != "" && spec.Kind != KindGrok {
		return harness.LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("grok adapter got kind %q", spec.Kind))
	}
	launch, ok := spec.Launch.(grokLaunch)
	if spec.Launch != nil && !ok {
		return harness.LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("grok adapter got launch data %T that its Prepare did not make", spec.Launch))
	}
	if spec.Role == harness.RoleMate {
		return harness.LaunchSpec{}, refuseMate()
	}
	cwd, path := spec.Cwd, spec.ContextPath
	if cwd == "" || !filepath.IsAbs(cwd) {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, "grok launch requires the absolute cwd the agent will run in", harness.ErrContextRequired)
	}
	if path == "" || !filepath.IsAbs(path) {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, fmt.Sprintf("grok launch requires an absolute context path, got %q", path), harness.ErrContextRequired)
	}
	resumeID := launch.resumeID
	if spec.ResumeSessionID != "" {
		if resumeID != "" && strings.TrimSpace(spec.ResumeSessionID) != resumeID {
			return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				"grok resume id and the launch's resume id disagree", harness.ErrContextRequired)
		}
		resumeID = strings.TrimSpace(spec.ResumeSessionID)
	}
	if launch.sessionID != "" && resumeID != "" {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"grok session id and resume session id are mutually exclusive", harness.ErrContextRequired)
	}
	var sessionFlag, sessionID string
	switch {
	case resumeID != "":
		sessionFlag, sessionID = "--resume", resumeID
	case launch.sessionID != "":
		sessionFlag, sessionID = "--session-id", launch.sessionID
	default:
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"grok launch needs a session id", harness.ErrContextRequired)
	}
	if _, err := uuid.Parse(sessionID); err != nil {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			fmt.Sprintf("grok session id %q is not a UUID", sessionID), harness.ErrContextRequired)
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, path+" not found", harness.ErrContextRequired)
		}
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, "stat "+path, fmt.Errorf("%w: %w", harness.ErrContextRequired, err))
	}
	if !info.Mode().IsRegular() {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, path+" is not a regular file", harness.ErrContextRequired)
	}
	if info.Size() == 0 {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, path+" is empty", harness.ErrContextRequired)
	}
	if info.Size() > int64(harness.DefaultMaxInlineBytes) {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			fmt.Sprintf("%s is %d bytes, inline limit %d", path, info.Size(), harness.DefaultMaxInlineBytes),
			harness.ErrContextTooLarge)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, "read "+path, fmt.Errorf("%w: %w", harness.ErrContextRequired, err))
	}
	args := []string{
		// --fullscreen pins the layout the screens were measured in, against
		// a config that might select another screen mode.
		"--fullscreen",
		// --always-approve is what the captain's rule requires: a Crew asks
		// no permission. The measuring machine already had that mode in its
		// config; the flag keeps a launch the same on one that does not.
		"--always-approve",
		// --no-plan keeps plan mode off.
		"--no-plan",
		// --trust skips the folder question and loads the project's AGENTS.md.
		"--trust",
		sessionFlag, sessionID,
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if g.Info().SupportsEffort(spec.Effort) {
		args = append(args, "--effort", string(spec.Effort))
	}
	args = append(args, contextFlag, string(data))
	return harness.NewLaunchSpec(harness.LaunchPlan{
		RuntimeKind:     g.Info().RuntimeKind,
		Screen:          g.Screen(),
		Args:            args,
		Cwd:             cwd,
		Env:             spec.Env,
		EnvKeys:         spec.EnvKeys,
		ContextFiles:    []harness.GeneratedFile{{Path: path, Role: "canonical_context"}},
		Delivery:        harness.DeliveryAppendSystemPrompt,
		ContextPath:     path,
		ContextRequired: true,
		MaxInlineBytes:  harness.DefaultMaxInlineBytes,
		TaskPrompt:      spec.TaskPrompt,
		Model:           spec.Model,
		Effort:          spec.Effort,
		EffortOmitted:   spec.Effort != "" && !g.Info().SupportsEffort(spec.Effort),
		Notes: []string{
			"the captain ruled on 2026-09-14 that Crew launches run with no permission prompt; grok is passed --always-approve so the launch does not depend on the operator's permission_mode",
			"--append-system-prompt inlines the brief; grok's --rules is an alias and is not what the delivery check looks for",
			"--trust loads the project's AGENTS.md into the prompt context; the operator's home rules load too, because the Crew uses the operator's grok home",
			"--fullscreen, --no-plan and --trust keep the measured layout, plan mode off, and the worktree trusted",
			"--effort passes low, medium, high and xhigh; max is not in grok-4.7's menu and is left out",
		},
	})
}

// sessionsRoot is where grok keeps sessions: the profile's home, else
// $GROK_HOME, else ~/.grok, and then its sessions directory.
func sessionsRoot(home string) (string, error) {
	dir := strings.TrimSpace(home)
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv(homeEnv))
	}
	if dir == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("grok home: %w", err)
		}
		dir = filepath.Join(user, ".grok")
	} else if rest, ok := strings.CutPrefix(dir, "~"); ok {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("grok home: %w", err)
		}
		dir = filepath.Join(user, rest)
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("grok home %q is not absolute", dir)
	}
	return filepath.Join(filepath.Clean(dir), "sessions"), nil
}

// SessionDir is one session's directory when grok names it by escaping the
// cwd: `<root>/<url.PathEscape(cwd)>/<id>`. Locate does not use this. grok
// resolves symlinks before escaping, and a long name may be hashed, so a
// session is found by its id (findSessionDir).
func SessionDir(root, cwd, id string) string {
	return filepath.Join(root, url.PathEscape(cwd), id)
}

// findSessionDir is the one directory named id under root. Exactly one
// match counts: none, or two worktrees that somehow share an id, is not a
// session mate can resume.
func findSessionDir(root, id string) (string, bool) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return "", false
	}
	id = parsed.String()
	matches, err := filepath.Glob(filepath.Join(root, "*", id))
	if err != nil || len(matches) != 1 {
		return "", false
	}
	fi, err := os.Stat(matches[0])
	if err != nil || !fi.IsDir() {
		return "", false
	}
	return matches[0], true
}

// findUpdates is updates.jsonl inside that directory.
func findUpdates(root, id string) (string, bool) {
	dir, ok := findSessionDir(root, id)
	if !ok {
		return "", false
	}
	path := filepath.Join(dir, "updates.jsonl")
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	return path, true
}
