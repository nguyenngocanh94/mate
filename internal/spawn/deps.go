package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Meta keys written to `mate/mate.meta`. They are spelled once, here, so the
// CLI, the tests and later tasks cannot drift apart.
const (
	MetaHarness   = "harness"
	MetaAgent     = "agent"
	MetaPane      = "pane"
	MetaTab       = "tab"
	MetaWorkspace = "workspace"
	MetaSession   = "session"
	MetaSessionID = "session_id"
	MetaStartedAt = "started_at"
	// MetaLaunchedAt is when the harness process was started, taken just
	// before `agent start`. started_at is written only once the agent is
	// ready and has its first prompt, which for Codex is after the rollout
	// already exists, so it cannot anchor the rollout adoption rule
	// (harness.AdoptCodexRollout rejects a rollout older than its anchor;
	// measured 2026-09-24, task 34: a rollout opened 0.2s before started_at).
	MetaLaunchedAt = "launched_at"
	MetaStoppedAt  = "stopped_at"
	// MetaTranscript is the Claude transcript path the Stop hook reports
	// (docs/mvp.md decision 9: ".meta ghi transcript= và session_id= từ
	// ngày đầu"). Task 08's mate-stop hook writes it; StartMate does not.
	MetaTranscript = "transcript"
	// MetaResumed and MetaResumedFrom record task 10's resume decision.
	// Both are written only when this start resumed a previous harness
	// session; a fresh start's meta rewrite (StartMate always writes a
	// brand new map) leaves them absent, not "false"/"".
	MetaResumed     = "resumed"
	MetaResumedFrom = "resumed_from"
)

// AgentNamePrefix is the first half of a Mate's live Herdr agent name; the
// project name is the second, so a Mate is `mate-<project>`.
const AgentNamePrefix = "mate"

// cleanupTimeout bounds a compensation that runs after its caller's context
// was cancelled.
const cleanupTimeout = 30 * time.Second

// cleanupContext is the context a compensation runs under: the caller's
// values without its cancellation, bounded by cleanupTimeout. A start is
// most often compensated *because* it was cancelled (the Console quit
// mid-start), and undoing it with the cancelled context would fail every
// Herdr call at once and leave the agent running unrecorded.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

// MateTabLabel is the Herdr tab label of the Mate pane.
const MateTabLabel = "mate"

// Defaults for the timeouts a start depends on. They are generous because
// the cost of being wrong is asymmetric: a slow harness that is still
// drawing its banner must not be torn down, while a wedged one is reported
// with the screen in the error either way.
const (
	// DefaultReadinessTimeout is how long Herdr may take to report the agent
	// idle, done or blocked.
	DefaultReadinessTimeout = 90 * time.Second
	// DefaultStartTimeout is `agent start --timeout`. Herdr 0.8.2 accepts
	// > 3000 and <= 300000 ms.
	DefaultStartTimeout = 60 * time.Second
)

// Deps are the collaborators a start or stop needs. The zero value is not
// usable: Runtime is required. Everything else has a default.
type Deps struct {
	// Runtime is the Herdr adapter. Tests pass runtime.NewFake().
	Runtime runtime.Adapter
	// Names is the live agent-name registry. Nil means a fresh in-process
	// one, which is what a one-shot CLI invocation wants.
	Names runtime.LiveNameRegistry
	// ConfigHome is the Herdr config home used to resolve socket paths.
	// Empty means the environment's (HERDR_CONFIG_PATH, XDG_CONFIG_HOME,
	// then $HOME/.config).
	ConfigHome string
	// Binary is the absolute path of the mate binary the Mate invokes.
	// Empty means os.Executable.
	Binary string
	// ReadinessTimeout bounds the wait for Herdr to report the agent ready.
	ReadinessTimeout time.Duration
	// StartupPromptTimeout bounds the startup-screen settle. Zero means the
	// readiness timeout (ADR 0028).
	StartupPromptTimeout time.Duration
	// StartTimeout is passed to `agent start --timeout`.
	StartTimeout time.Duration
	// BriefDeliveryTimeout bounds the wait for a crew pane to leave idle
	// after the brief prompt. Zero means DefaultBriefDeliveryTimeout.
	BriefDeliveryTimeout time.Duration
	// Git runs the crew worktree commands. The zero value is the real git.
	Git gitx.Git
	// Worktrees acquires and releases crew working copies. Nil means
	// GitWorktrees over Git: linked git worktrees under `.worktrees/`.
	Worktrees Worktrees
	// Sleep is the pause between pane polls; tests shorten it.
	Sleep func(ctx context.Context, d time.Duration) error
	// Now is the clock the meta's timestamps come from.
	Now func() time.Time
	// NewSessionID mints the Claude session uuid; tests make it
	// deterministic. Nil means uuid.NewString.
	NewSessionID func() string
	// CodexSessionsDir is where Codex writes its rollouts, which a Codex
	// Mate's resume is checked against and its session id is recovered
	// from when Herdr has none (task 35). Empty means
	// harness.CodexSessionsDir("") - CODEX_HOME, then ~/.codex.
	CodexSessionsDir string
}

func (d Deps) codexSessionsDir() (string, error) {
	if d.CodexSessionsDir != "" {
		return d.CodexSessionsDir, nil
	}
	return harness.CodexSessionsDir("")
}

// LiveDeps is what the CLI uses: the real Herdr adapter over os/exec. The
// name registry is shared with the adapter, which re-reserves the name at
// start; without one StartAgent refuses outright.
func LiveDeps() Deps {
	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	return Deps{Runtime: rt, Names: names}
}

func (d Deps) names() runtime.LiveNameRegistry {
	if d.Names != nil {
		return d.Names
	}
	return runtime.NewMemoryNameRegistry()
}

func (d Deps) readinessTimeout() time.Duration {
	if d.ReadinessTimeout > 0 {
		return d.ReadinessTimeout
	}
	return DefaultReadinessTimeout
}

func (d Deps) startupPromptTimeout() time.Duration {
	if d.StartupPromptTimeout > 0 {
		return d.StartupPromptTimeout
	}
	return d.readinessTimeout()
}

func (d Deps) briefDeliveryTimeout() time.Duration {
	if d.BriefDeliveryTimeout > 0 {
		return d.BriefDeliveryTimeout
	}
	return DefaultBriefDeliveryTimeout
}

// git is the git command surface the crew saga uses. A Deps with no Git
// runs the real binary, which is what the CLI wants and what the package's
// own tests want too: they operate on real temporary repositories.
func (d Deps) git() gitx.Git {
	if d.Git.Runner != nil {
		return d.Git
	}
	return gitx.New()
}

// worktrees is the working-copy backend a spawn acquires from and a stop
// releases to.
func (d Deps) worktrees() Worktrees {
	if d.Worktrees != nil {
		return d.Worktrees
	}
	return GitWorktrees{Git: d.git()}
}

func (d Deps) startTimeout() time.Duration {
	if d.StartTimeout > 0 {
		return d.StartTimeout
	}
	return DefaultStartTimeout
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

func (d Deps) sleep() sleeper {
	if d.Sleep != nil {
		return d.Sleep
	}
	return sleepCtx
}

// binary is the absolute path the rendered manual tells the Mate to run.
func (d Deps) binary() (string, error) {
	if strings.TrimSpace(d.Binary) != "" {
		return filepath.Abs(d.Binary)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return exe, nil
}

// configHome resolves the Herdr config home the session socket lives under.
// It mirrors what the Herdr adapter does for its own default, because
// SessionSpec requires the caller to name it.
func (d Deps) configHome() (string, error) {
	home := strings.TrimSpace(d.ConfigHome)
	if home == "" {
		home = herdrConfigHomeFromEnv()
	}
	if home == "" {
		return "", errUsage("cannot resolve the Herdr config home: set HERDR_CONFIG_PATH, XDG_CONFIG_HOME or HOME")
	}
	return home, nil
}

func herdrConfigHomeFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("HERDR_CONFIG_PATH")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return v
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".config")
	}
	return ""
}

// sessionSpec names the workspace's Herdr session. The name is the one
// stored in workspace.yaml at Init, so moving or re-resolving the workspace
// never renames a live session; the owning id is derived from the resolved
// root path.
func (d Deps) sessionSpec(w *store.Workspace) (runtime.SessionSpec, error) {
	home, err := d.configHome()
	if err != nil {
		return runtime.SessionSpec{}, err
	}
	id, err := runtime.ParseWorkspaceID(store.SessionName(w.Root()))
	if err != nil {
		return runtime.SessionSpec{}, err
	}
	return runtime.SessionSpec{
		WorkspaceID: id,
		Name:        w.Session(),
		ConfigHome:  home,
		Names:       d.names(),
	}, nil
}
