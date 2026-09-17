package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/process"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/store"
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
	MetaStoppedAt = "stopped_at"
)

// AgentNamePrefix is the first half of a Mate's live Herdr agent name; the
// project name is the second, so a Mate is `mate-<project>`.
const AgentNamePrefix = "mate"

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
	// Binary is the absolute path of the matev2 binary the Mate invokes.
	// Empty means os.Executable.
	Binary string
	// ReadinessTimeout bounds the wait for Herdr to report the agent ready.
	ReadinessTimeout time.Duration
	// StartupPromptTimeout bounds the startup-screen settle. Zero means the
	// readiness timeout (ADR 0028).
	StartupPromptTimeout time.Duration
	// StartTimeout is passed to `agent start --timeout`.
	StartTimeout time.Duration
	// Sleep is the pause between pane polls; tests shorten it.
	Sleep func(ctx context.Context, d time.Duration) error
	// Now is the clock the meta's timestamps come from.
	Now func() time.Time
	// NewSessionID mints the Claude session uuid; tests make it
	// deterministic. Nil means uuid.NewString.
	NewSessionID func() string
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
