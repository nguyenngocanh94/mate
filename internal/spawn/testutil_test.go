package spawn_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/harness/harnesstest"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/tool"
	toolcatalog "github.com/nguyenngocanh94/mate/internal/tool/catalog"
)

// newWorkspace creates a workspace with one registered git project. It is
// the fixture every test in this package starts from: `store.Init`, a real
// `git init` repository under the project's directory (`<root>/<p>/<p>`), and
// the registration between them.
func newWorkspace(t *testing.T, project string) *store.Workspace {
	t.Helper()
	root := t.TempDir()
	w, err := store.Init(root, store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.ProjectHome(project), project)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	if err := w.AddProject(project, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
}

// fakeDeps wires the fake runtime with instant sleeps and a frozen clock, so
// every unit test runs at memory speed and writes a byte-identical meta.
func fakeDeps(t *testing.T, rt *runtime.Fake) spawn.Deps {
	t.Helper()
	return spawn.Deps{
		Harnesses:            catalog.Default(),
		Tools:                toolRegistry(t),
		Runtime:              rt,
		Names:                rt.Names,
		ConfigHome:           t.TempDir(),
		Binary:               filepath.Join(t.TempDir(), "mate"),
		ReadinessTimeout:     time.Second,
		StartupPromptTimeout: 50 * time.Millisecond,
		StartTimeout:         10 * time.Second,
		Sleep:                func(context.Context, time.Duration) error { return nil },
		Now:                  func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) },
		NewSessionID:         func() string { return "11111111-2222-3333-4444-555555555555" },
	}
}

// readingVisible is fakeDeps with every harness's screens read through
// harness.ReadVisible, the source neither Claude nor Codex uses, so a read
// that spells recent-unwrapped instead of asking the profile shows up in
// assertReadVisible.
func readingVisible(t *testing.T, rt *runtime.Fake) spawn.Deps {
	t.Helper()
	deps := fakeDeps(t, rt)
	deps.Harnesses = harnesstest.ReadingVisible(deps.Harnesses)
	return deps
}

// assertReadVisible fails unless the pane was read, and every read went
// through harness.ReadVisible.
func assertReadVisible(t *testing.T, rt *runtime.Fake) {
	t.Helper()
	if len(rt.ReadSources) == 0 {
		t.Fatal("the pane was never read")
	}
	for i, src := range rt.ReadSources {
		if src != harness.ReadVisible {
			t.Fatalf("read %d went through %q, want the profile's %q", i, src, harness.ReadVisible)
		}
	}
}

// codexSessionsIn is catalog.Default() with Codex reading its rollouts from
// sessions, so a test that resumes or stops a Codex Mate never reads the
// operator's ~/.codex.
func codexSessionsIn(t *testing.T, sessions string) harness.Registry {
	t.Helper()
	cat := catalog.Default()
	defaults := map[harness.AgentRole]harness.Kind{}
	for _, role := range []harness.AgentRole{harness.RoleMate, harness.RoleCrew} {
		if k, err := cat.Default(role); err == nil {
			defaults[role] = k
		}
	}
	var profiles []harness.Profile
	for _, k := range cat.Kinds() {
		p, err := cat.Lookup(k)
		if err != nil {
			t.Fatal(err)
		}
		if codex, ok := p.(codex.Codex); ok {
			codex.SessionsDir = sessions
			p = codex
		}
		profiles = append(profiles, p)
	}
	reg, err := harness.NewRegistry(defaults, profiles...)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// screen loads one captured startup screen from the testdata of the harness
// that drew it, which its name begins with.
func screen(t *testing.T, name string) string {
	t.Helper()
	kind, _, _ := strings.Cut(strings.ReplaceAll(name, "_", "-"), "-")
	data, err := os.ReadFile(filepath.Join("..", "harness", kind, "testdata", "startup", name))
	if err != nil {
		t.Fatalf("read capture %s: %v", name, err)
	}
	return string(data)
}

func readMeta(t *testing.T, w *store.Workspace, project string) map[string]string {
	t.Helper()
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		t.Fatalf("ReadMateMeta: %v", err)
	}
	return meta
}

// toolRegistry is the binary's tools, so a started Mate installs their
// skills as it does live.
func toolRegistry(t *testing.T) tool.Registry {
	t.Helper()
	r, err := tool.NewRegistry(toolcatalog.Default()...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
