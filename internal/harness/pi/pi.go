// Package pi is pi (https://pi.dev, @earendil-works/pi-coding-agent) as a
// mate harness: its Profile, launch, screens and session transcripts.
// Everything that knows pi lives here; the contract it implements is
// internal/harness.
//
// pi runs Crews only. It has no hook mate can install yet, and a Mate's
// memory and inbox rest on hooks (docs/plans/harness-registry-2026-09-30.md,
// section 3.2). Every number here was measured on pi 0.99.1 in a Herdr 0.8.2
// pane, docs/evidence/pi-contract-2026-10-01.md.
package pi

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// KindPi is pi's kind, and what Herdr is told at `agent start --kind`.
const KindPi harness.Kind = "pi"

// Pi is the Profile of pi 0.99.1.
type Pi struct {
	// SessionsDir is where mate keeps pi's sessions, one directory per
	// cwd. Empty means pi's own: <agent dir>/sessions (sessionsRoot).
	SessionsDir string
}

// AgentDirEnv is the variable pi reads its agent directory from: auth,
// settings, extensions, skills and sessions.
const AgentDirEnv = "PI_CODING_AGENT_DIR"

// Kind implements Profile.
func (Pi) Kind() harness.Kind { return KindPi }

// Info implements Profile.
func (Pi) Info() harness.Info {
	return harness.Info{
		Name:        "pi",
		RuntimeKind: string(KindPi),
		// pi reads project resources from `.pi/` in its cwd. A launch passes
		// --no-approve, which ignores them, so no file of a working tree
		// shapes a pi Crew beyond the AGENTS.md and CLAUDE.md the other
		// harnesses' documents already record.
		ConfigDir: ".pi",
		// pi has no brand mark in Nerd Fonts 3.5, so it draws the same
		// letter wherever Unicode is drawn.
		Icon: harness.Icon{Nerd: "π", Unicode: "π", ASCII: "p"},
		// `--thinking` takes off, minimal, low, medium, high, xhigh and max,
		// so every Effort is passed as it is. pi then clamps the level to
		// what the model supports without saying so; the applied level is
		// read back from the session (piTranscripts.Telemetry).
		Efforts:      harness.Efforts,
		AdapterNotes: adapterNotes,
	}
}

// adapterNotes is pi's section of the harness-adapters skill.
//
//go:embed adapter.md
var adapterNotes string

// Launcher implements Profile.
func (p Pi) Launcher() harness.Launcher { return p }

// Screen implements Profile.
func (Pi) Screen() harness.ScreenProfile { return piScreen{} }

// evidence is where every verified capability below was measured.
const evidenceDoc = "docs/evidence/pi-contract-2026-10-01.md"

func measured(proof string) harness.Evidence {
	return harness.Evidence{Version: "pi 0.99.1", Measured: "2026-10-01 (" + evidenceDoc + ")", Proof: proof}
}

// hooksReason is why pi has no Hooks, and so why it runs no Mate.
const hooksReason = "pi runs no hook command: its only extension point is a TypeScript extension loaded with -e, and mate does not yet generate one. " +
	"A Mate on pi needs a versioned mate-owned extension that calls `mate hook` and injects recall through before_agent_start, " +
	"which HookInstaller does not yet take as a hook, and session_start reports reason \"startup\" on a resume too, so LiveOnly cannot rest on it " +
	"(docs/plans/harness-registry-2026-09-30.md, section 3.2)"

// Capabilities implements Profile.
func (p Pi) Capabilities() harness.Capabilities {
	return harness.Capabilities{
		GracefulStop: harness.Cap[harness.GracefulStopper]{
			Status:   harness.CapVerified,
			Impl:     piExit{},
			Evidence: measured("screens/run3-after-quit.txt: `/quit` and Enter ended pi and Herdr dropped the agent; ctrl+d quits only an empty composer, and ctrl+u empties one (screens/run1-after-ctrl-u.txt)"),
		},
		Session: harness.Cap[harness.SessionIdentity]{
			Status:   harness.CapVerified,
			Impl:     piSessions{root: p.SessionsDir},
			Evidence: measured("probe/probe-run3-sessid.jsonl and probe-run4-resume-sessid.jsonl: --session-id with a caller's UUID created the session and, given again, resumed the same file with no dialog"),
		},
		Hooks: harness.Cap[harness.HookInstaller]{
			Status: harness.CapUnsupported,
			Reason: hooksReason,
		},
		TurnEnd: harness.Cap[harness.TurnEndEvidence]{
			Status:   harness.CapVerified,
			Impl:     piTurnEnd{},
			Evidence: measured("session/*3f0c5a8e*.jsonl: each turn ends on an assistant message whose stopReason is not toolUse, written when the turn ends; TestPiTurnEndsInTheSession"),
		},
		Transcript: harness.Cap[harness.TranscriptSource]{
			Status:   harness.CapVerified,
			Impl:     piTranscripts{root: p.SessionsDir},
			Evidence: measured("internal/harness/pi/testdata/session through TestPiTranscriptReadsTheMeasuredSessions and the catalog contract suite"),
		},
		Quota: harness.Cap[harness.QuotaProvider]{
			Status: harness.CapUnsupported,
			Reason: "pi has no allowance of its own: it calls whichever provider the --model names (deepseek, openrouter, ...), so no quota-axi row follows from the harness",
		},
	}
}

// piExit is pi's GracefulStopper. `/quit` is a line typed into the composer,
// so typed after a draft it would join the draft (and ctrl+d quits only an
// empty composer): whatever draft is there is cleared first.
type piExit struct{}

// ClearKeys implements GracefulStopper.
func (piExit) ClearKeys() []string { return []string{"ctrl+u"} }

// ExitPrompt implements GracefulStopper.
func (piExit) ExitPrompt() string { return "/quit" }

// piSessions is pi's SessionIdentity: a launch names its session
// (--session-id), so nothing is left to read at stop.
type piSessions struct{ root string }

// AtStop implements SessionIdentity.
func (piSessions) AtStop(string, time.Time, string) string { return "" }

// Resumable implements SessionIdentity. `--session-id` with an id that has
// no file does not fail: pi prints a warning and starts a new, empty
// session under that id (screens/run3-sessid-startup.txt), so the file is
// looked for before anything is launched.
func (s piSessions) Resumable(id string) error {
	root, err := sessionsRoot(s.root)
	if err != nil {
		return fmt.Errorf("cannot look for the pi session %s to resume (%v)", id, err)
	}
	if _, ok := findSession(root, id); !ok {
		return &harness.NoSessionError{Session: "the pi session " + id, Missing: root + " has no session file for it"}
	}
	return nil
}

// piTurnEnd is pi's TurnEndEvidence: no Stop hook, but the session records
// every assistant message once it is complete (sessionTurnEnded).
type piTurnEnd struct{}

// LogsAnswers implements TurnEndEvidence.
func (piTurnEnd) LogsAnswers() bool { return false }

// EndsInTranscript implements TurnEndEvidence.
func (piTurnEnd) EndsInTranscript() bool { return true }

// TranscriptTurnEnded implements TurnEndEvidence.
func (piTurnEnd) TranscriptTurnEnded(session []byte, after time.Time) bool {
	return sessionTurnEnded(session, after)
}

// PaneEnv implements Launcher. Every pi launch names its session directory
// on the command line, so a pane needs nothing pinned.
func (Pi) PaneEnv() ([]harness.EnvVar, error) { return nil, nil }

// piLaunch is what pi's Prepare hands its Build.
type piLaunch struct {
	// sessionID names a fresh session (--session-id). A resumed one comes
	// from AgentSpec.ResumeSessionID.
	sessionID string
}

// refuseMate is the refusal of a Mate on pi, naming the capability it
// lacks.
func refuseMate() error {
	return observability.NewError(observability.CodeUsage,
		"the pi harness cannot run a Mate: it declares no verified Hooks capability ("+hooksReason+"); it can still run a Crew")
}

// Prepare implements Launcher. A pi Crew needs no file of its own: the
// brief reaches pi by path on the command line, and pi writes its session
// itself. The launch names the session, so the id is minted here.
func (p Pi) Prepare(_ context.Context, req harness.PrepareRequest) (harness.Prepared, error) {
	if err := req.Check(KindPi); err != nil {
		return harness.Prepared{}, err
	}
	if req.Role == harness.RoleMate {
		return harness.Prepared{}, refuseMate()
	}
	var launch piLaunch
	sessionID := req.ResumeSessionID
	if sessionID == "" {
		sessionID = req.MintSessionID()
		launch.sessionID = sessionID
	}
	return harness.Prepared{SessionID: sessionID, ContextPath: req.ContextPath, Launch: launch}, nil
}

// contextFlag is the flag pi appends a file to its system prompt with. It
// takes text as well as a path: a path that does not exist is appended as
// the text of the path, with no warning (probe-ctx-c4-missing.jsonl), which
// is why the generic check that the file exists is what stands between a
// lost brief and a Crew that never got one.
const contextFlag = "--append-system-prompt"

// Build implements Launcher: a startable pi spec, or an error.
func (p Pi) Build(_ context.Context, spec harness.AgentSpec) (harness.LaunchSpec, error) {
	if spec.Kind != "" && spec.Kind != KindPi {
		return harness.LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("pi adapter got kind %q", spec.Kind))
	}
	launch, ok := spec.Launch.(piLaunch)
	if spec.Launch != nil && !ok {
		return harness.LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("pi adapter got launch data %T that its Prepare did not make", spec.Launch))
	}
	if spec.Role == harness.RoleMate {
		return harness.LaunchSpec{}, refuseMate()
	}
	cwd, path := spec.Cwd, spec.ContextPath
	if cwd == "" || !filepath.IsAbs(cwd) {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, "pi launch requires the absolute cwd the agent will run in", harness.ErrContextRequired)
	}
	if path == "" || !filepath.IsAbs(path) {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, fmt.Sprintf("pi launch requires an absolute context path, got %q", path), harness.ErrContextRequired)
	}
	if launch.sessionID != "" && spec.ResumeSessionID != "" {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"pi session id and resume session id are mutually exclusive", harness.ErrContextRequired)
	}
	sessionID := launch.sessionID
	if spec.ResumeSessionID != "" {
		sessionID = strings.TrimSpace(spec.ResumeSessionID)
	}
	args := []string{
		// The operator's own extensions and skills (~/.pi/agent/extensions,
		// ~/.agents/skills) load into every pane without these; measured,
		// on the measuring machine, three Orca extensions.
		"--no-extensions", "--no-skills",
		// --no-approve skips the trust dialog and ignores the project's
		// `.pi/` resources, so the launch is the same in every worktree;
		// the project's AGENTS.md is not a protected resource and loads.
		"--no-approve",
		// --offline drops the startup update check and its banner; the
		// model is still reached.
		"--offline",
	}
	if sessionID != "" {
		if _, err := uuid.Parse(sessionID); err != nil {
			return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				fmt.Sprintf("pi session id %q is not a UUID", sessionID), harness.ErrContextRequired)
		}
		root, err := sessionsRoot(p.SessionsDir)
		if err != nil {
			return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, "pi sessions directory", err)
		}
		// --session-id creates the session when its file does not exist
		// and resumes it when it does, in the --session-dir given.
		args = append(args, "--session-dir", SessionDir(root, cwd), "--session-id", sessionID)
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if p.Info().SupportsEffort(spec.Effort) {
		args = append(args, "--thinking", string(spec.Effort))
	}
	args = append(args, contextFlag, path)
	return harness.NewLaunchSpec(harness.LaunchPlan{
		RuntimeKind:     p.Info().RuntimeKind,
		Screen:          p.Screen(),
		Args:            args,
		Cwd:             cwd,
		Env:             spec.Env,
		EnvKeys:         spec.EnvKeys,
		ContextFiles:    []harness.GeneratedFile{{Path: path, Role: "canonical_context"}},
		Delivery:        harness.DeliveryAppendSystemPromptFile,
		ContextFlag:     contextFlag,
		ContextPath:     path,
		ContextRequired: true,
		TaskPrompt:      spec.TaskPrompt,
		Model:           spec.Model,
		Effort:          spec.Effort,
		EffortOmitted:   spec.Effort != "" && !p.Info().SupportsEffort(spec.Effort),
		Notes: []string{
			"the captain ruled on 2026-09-14 that Crew launches run with no permission prompt and no external sandbox; pi asks no permission before a tool runs, so it needs no flag for that (measured: a bash tool call ran at once and Herdr never reported blocked)",
			"--append-system-prompt takes a path or text; a missing path is appended as text with no warning, so mate checks the file before the launch",
			"pi loads AGENTS.md (else CLAUDE.md) from the cwd and every parent directory, and ~/.pi/agent/AGENTS.md; the brief is appended after them, and they are not turned off, because --no-context-files would drop the project's own instructions as well",
			"--thinking passes the requested effort as it is; pi clamps it to what the model supports without saying so, and the level applied is read back from the session's thinking_level_change record",
			"--no-extensions, --no-skills, --no-approve and --offline keep the operator's own extensions and skills, the project's .pi resources and the update check out of the pane",
		},
	})
}

// sessionsRoot is where pi sessions are kept: the profile's own, else the
// one pi itself uses, $PI_CODING_AGENT_DIR/sessions or ~/.pi/agent/sessions
// (pi 0.99.1 dist/config.js getAgentDir).
func sessionsRoot(configured string) (string, error) {
	if dir := strings.TrimSpace(configured); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("pi sessions directory %q is not absolute", dir)
		}
		return filepath.Clean(dir), nil
	}
	agent := strings.TrimSpace(os.Getenv(AgentDirEnv))
	if agent == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("pi agent directory: %w", err)
		}
		agent = filepath.Join(home, ".pi", "agent")
	} else if rest, ok := strings.CutPrefix(agent, "~"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("pi agent directory: %w", err)
		}
		agent = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(agent) {
		return "", fmt.Errorf("%s %q is not absolute", AgentDirEnv, agent)
	}
	return filepath.Join(filepath.Clean(agent), "sessions"), nil
}

// SessionDir is the directory a launch in cwd keeps its session in: pi's own
// name for it under root, `--<cwd with each / and : as ->--`
// (pi 0.99.1 dist/core/session-manager.js getDefaultSessionDirPath), so a
// mate session is where `pi --resume` in that directory looks.
func SessionDir(root, cwd string) string {
	slug := strings.TrimLeft(filepath.Clean(cwd), `/\`)
	slug = strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(slug)
	return filepath.Join(root, "--"+slug+"--")
}

// findSession is the one session file of id under root: pi names it
// `<ISO timestamp with - for :>_<id>.jsonl`, with the time first, so it is
// found by its id, in any cwd's directory.
func findSession(root, id string) (string, bool) {
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	matches, err := filepath.Glob(filepath.Join(root, "*", "*_"+id+".jsonl"))
	if err != nil || len(matches) != 1 {
		return "", false
	}
	if fi, err := os.Stat(matches[0]); err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	return matches[0], true
}
