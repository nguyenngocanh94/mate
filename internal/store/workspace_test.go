package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// newWorkspace initialises a workspace in a temp dir with a repo directory,
// `shop/shop` (project shop's directory, then the repo), ready to register.
func newWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shop", "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := store.Init(dir, store.Defaults{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return w
}

func TestStoreInitThenOpenRoundTrips(t *testing.T) {
	dir := t.TempDir()

	want := store.Defaults{MateHarness: "harness-a", CrewHarness: "harness-b"}
	created, err := store.Init(dir, want)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if created.Session() == "" || !strings.HasPrefix(created.Session(), "mate-") {
		t.Fatalf("session name %q does not look like mate-<id>", created.Session())
	}
	if got := created.Defaults(); got != want {
		t.Fatalf("defaults = %+v, want what Init was given, %+v", got, want)
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
	if got := opened.Defaults(); got != want {
		t.Fatalf("defaults read back as %+v, want %+v", got, want)
	}

	// Init on an existing workspace keeps the stored session name.
	again, err := store.Init(dir, store.Defaults{})
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

	// A .mate directory without workspace.yaml is not a workspace either.
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
	if !strings.HasPrefix(a, "mate-") || len(a) != len("mate-")+8 {
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
		w.CrewMeta("shop", "k3"):    filepath.Join(root, ".mate/projects/shop/crews/k3.meta"),
		w.CrewStatus("shop", "k3"):  filepath.Join(root, ".mate/projects/shop/crews/k3.status"),
		w.CrewBrief("shop", "k3"):   filepath.Join(root, ".mate/projects/shop/crews/k3/brief.md"),
		w.WorktreeDir("shop", "k3"): filepath.Join(root, ".worktrees/shop-k3"),
		w.AutoFlag("shop"):          filepath.Join(root, ".mate/projects/shop/mate/.auto"),
		w.SentLog("shop"):           filepath.Join(root, ".mate/projects/shop/sent.log"),
		w.MemoryFile("shop"):        filepath.Join(root, ".mate/projects/shop/mate/memory.md"),
		w.BacklogFile("shop"):       filepath.Join(root, ".mate/projects/shop/mate/backlog.md"),
		w.ProjectDoc("shop"):        filepath.Join(root, ".mate/projects/shop/PROJECT.md"),
		w.CrewReport("shop", "k3"):  filepath.Join(root, ".mate/projects/shop/crews/k3/report.md"),
		w.MateMeta("shop"):          filepath.Join(root, ".mate/projects/shop/mate/mate.meta"),
		w.WorkspaceDoc():            filepath.Join(root, ".mate/WORKSPACE.md"),
	}
	for got, expect := range want {
		if got != expect {
			t.Errorf("layout path = %q, want %q", got, expect)
		}
	}
}

// TestInitSeedsTheWorkspaceDoc: every Mate reads WORKSPACE.md at bootstrap,
// so Init must create it; a second Init must not overwrite the captain's
// edits.
func TestInitSeedsTheWorkspaceDoc(t *testing.T) {
	root := t.TempDir()
	w, err := store.Init(root, store.Defaults{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	body, err := os.ReadFile(w.WorkspaceDoc())
	if err != nil {
		t.Fatalf("WORKSPACE.md was not created: %v", err)
	}
	if !strings.Contains(string(body), "Workspace rules") {
		t.Fatalf("seed = %q, want the rules heading", body)
	}
	if err := os.WriteFile(w.WorkspaceDoc(), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Init(root, store.Defaults{}); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	if got, _ := os.ReadFile(w.WorkspaceDoc()); string(got) != "# mine\n" {
		t.Fatalf("second Init rewrote the captain's file: %q", got)
	}
}

// TestInitCreatesAMissingDirectory: `mate init <dir>` on a directory that
// does not exist yet makes it, as `git init <dir>` does, instead of failing
// with a bare lstat error.
func TestInitCreatesAMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "ws")
	w, err := store.Init(dir, store.Defaults{})
	if err != nil {
		t.Fatalf("Init(%s): %v", dir, err)
	}
	if _, err := os.Stat(w.WorkspaceFile()); err != nil {
		t.Fatalf("no workspace file after Init: %v", err)
	}
}

// The store records the defaults it is given and fills in none of its own:
// a workspace.yaml without them reads back empty, and the binary's harness
// registry decides (docs/plans/harness-registry-2026-09-30.md, section 3.4).
func TestStoreFillsInNoHarnessDefault(t *testing.T) {
	dir := t.TempDir()
	if _, err := store.Init(dir, store.Defaults{}); err != nil {
		t.Fatal(err)
	}
	w, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Defaults(); got != (store.Defaults{}) {
		t.Fatalf("defaults = %+v, want none: the store names no harness", got)
	}
}
