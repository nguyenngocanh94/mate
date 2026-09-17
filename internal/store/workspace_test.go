package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

// newWorkspace initialises a workspace in a temp dir with a repo directory
// ready to register.
func newWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := store.Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return w
}

func TestStoreInitThenOpenRoundTrips(t *testing.T) {
	dir := t.TempDir()

	created, err := store.Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if created.Session() == "" || !strings.HasPrefix(created.Session(), "matev2-") {
		t.Fatalf("session name %q does not look like matev2-<id>", created.Session())
	}
	if got := created.Defaults(); got.MateHarness != store.DefaultMateHarness || got.CrewHarness != store.DefaultCrewHarness {
		t.Fatalf("defaults = %+v, want claude/codex", got)
	}

	opened, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.Session() != created.Session() {
		t.Fatalf("session changed: %q then %q", created.Session(), opened.Session())
	}
	if opened.Root() != created.Root() {
		t.Fatalf("root changed: %q then %q", created.Root(), opened.Root())
	}
	if opened.Config().Version != created.Config().Version {
		t.Fatalf("version changed")
	}

	// Init on an existing workspace keeps the stored session name.
	again, err := store.Init(dir)
	if err != nil {
		t.Fatalf("second Init: %v", err)
	}
	if again.Session() != created.Session() {
		t.Fatalf("re-init changed session: %q then %q", created.Session(), again.Session())
	}
}

func TestStoreOpenWithoutStateDirFails(t *testing.T) {
	dir := t.TempDir()

	if _, err := store.Open(dir); !errors.Is(err, store.ErrNotWorkspace) {
		t.Fatalf("Open on a bare dir: err = %v, want ErrNotWorkspace", err)
	}

	// A .matev2 directory without workspace.yaml is not a workspace either.
	if err := os.MkdirAll(filepath.Join(dir, store.StateDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(dir); !errors.Is(err, store.ErrNotWorkspace) {
		t.Fatalf("Open without workspace.yaml: err = %v, want ErrNotWorkspace", err)
	}
}

func TestStoreSessionNameIsStableAndPathSpecific(t *testing.T) {
	a := store.SessionName("/tmp/one")
	b := store.SessionName("/tmp/one")
	c := store.SessionName("/tmp/two")
	if a != b {
		t.Fatalf("session name is not stable: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("two workspaces share session name %q", a)
	}
	if !strings.HasPrefix(a, "matev2-") || len(a) != len("matev2-")+8 {
		t.Fatalf("unexpected session name %q", a)
	}
}

func TestStoreLayoutPathsAreInsideWorkspace(t *testing.T) {
	w := newWorkspace(t)
	root := w.Root()

	paths := map[string]string{
		"state dir":      w.StateDir(),
		"workspace.yaml": w.WorkspaceFile(),
		"WORKSPACE.md":   w.WorkspaceDoc(),
		"pricing.yaml":   w.PricingFile(),
		"project dir":    w.ProjectDir("shop"),
		"project.yaml":   w.ProjectFile("shop"),
		"PROJECT.md":     w.ProjectDoc("shop"),
		"sent.log":       w.SentLog("shop"),
		"mate dir":       w.MateDir("shop"),
		"mate.meta":      w.MateMeta("shop"),
		".auto":          w.AutoFlag("shop"),
		"memory.md":      w.MemoryFile("shop"),
		"backlog.md":     w.BacklogFile("shop"),
		"crews dir":      w.CrewsDir("shop"),
		"crew meta":      w.CrewMeta("shop", "k3"),
		"crew status":    w.CrewStatus("shop", "k3"),
		"crew dir":       w.CrewDir("shop", "k3"),
		"brief.md":       w.CrewBrief("shop", "k3"),
		"report.md":      w.CrewReport("shop", "k3"),
		"worktree":       w.WorktreeDir("shop", "k3"),
		"repo":           w.RepoDir("shop"),
	}
	for name, path := range paths {
		if !filepath.IsAbs(path) {
			t.Errorf("%s: %q is not absolute", name, path)
		}
		if rel, err := filepath.Rel(root, path); err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("%s: %q is outside the workspace", name, path)
		}
	}

	want := map[string]string{
		w.CrewMeta("shop", "k3"):    filepath.Join(root, ".matev2/projects/shop/crews/k3.meta"),
		w.CrewStatus("shop", "k3"):  filepath.Join(root, ".matev2/projects/shop/crews/k3.status"),
		w.CrewBrief("shop", "k3"):   filepath.Join(root, ".matev2/projects/shop/crews/k3/brief.md"),
		w.WorktreeDir("shop", "k3"): filepath.Join(root, ".worktrees/shop-k3"),
		w.AutoFlag("shop"):          filepath.Join(root, ".matev2/projects/shop/mate/.auto"),
		w.SentLog("shop"):           filepath.Join(root, ".matev2/projects/shop/sent.log"),
		w.MemoryFile("shop"):        filepath.Join(root, ".matev2/projects/shop/mate/memory.md"),
		w.BacklogFile("shop"):       filepath.Join(root, ".matev2/projects/shop/mate/backlog.md"),
		w.ProjectDoc("shop"):        filepath.Join(root, ".matev2/projects/shop/PROJECT.md"),
		w.CrewReport("shop", "k3"):  filepath.Join(root, ".matev2/projects/shop/crews/k3/report.md"),
		w.MateMeta("shop"):          filepath.Join(root, ".matev2/projects/shop/mate/mate.meta"),
		w.WorkspaceDoc():            filepath.Join(root, ".matev2/WORKSPACE.md"),
	}
	for got, expect := range want {
		if got != expect {
			t.Errorf("layout path = %q, want %q", got, expect)
		}
	}
}
