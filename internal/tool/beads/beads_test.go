package beads

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/capability"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

// The contract suite (internal/tool/catalog) holds Beads to the rules every
// tool keeps; these pin what Beads itself does. No test here runs the real
// bd or bv: every process goes to a fake Runner.

// fixture is a workspace root with a project directory `shop` and the
// CommandEnv the core would hand Beads for it. held is whether the
// tracker lock is taken; taking it twice fails the test.
type fixture struct {
	root  string
	env   tool.CommandEnv
	held  *bool
	locks *int
}

func newFixture(t *testing.T, run tool.Runner) fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(root, "shop")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	held, locks := false, 0
	f := fixture{root: root, held: &held, locks: &locks}
	f.env = tool.CommandEnv{
		ProjectDir: projectDir,
		DataDir:    data{}.Dir(projectDir),
		Run:        run,
		Lock: func(ctx context.Context) (func(), error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if held {
				t.Fatal("the tracker lock was taken twice")
			}
			held = true
			locks++
			return func() { held = false }, nil
		},
	}
	return f
}

// seed is what `bd init` leaves: the tracker with its metadata.
func seed(t *testing.T, inv tool.Invocation) {
	t.Helper()
	dir := filepath.Join(inv.Dir, ".beads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBeadsIsTheTaskTracker(t *testing.T) {
	p := New()
	info := p.Info()
	if p.Name() != "beads" || info.Name != "beads" || info.Title != "Beads" ||
		!slices.Equal(info.Binaries, []string{"bd", "bv"}) || info.Install != "see docs/beads.md" ||
		info.Docs != "docs/beads.md" || info.Measured != "bd 1.3.1 + bv 0.25.2" {
		t.Fatalf("Info() = %+v", info)
	}
	caps := p.Capabilities()
	if err := capability.Check(caps); err != nil {
		t.Fatal(err)
	}
	want := capability.Evidence{Version: "bd 1.3.1 + bv 0.25.2", Measured: "2026-10-07", Proof: "docs/beads.md; docs/mvp.md §1"}
	decls, err := capability.Declarations(caps)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range decls {
		if d.Status != capability.Verified || d.Evidence != want {
			t.Errorf("%s = %+v, want verified on %+v", d.Name, d, want)
		}
	}
	if got := caps.Data.Impl.Dir("/ws/shop"); got != "/ws/shop/.beads" {
		t.Errorf("Data.Dir = %q, want the project's .beads", got)
	}
}

func TestDataExistsIsTheTrackerDirectory(t *testing.T) {
	f := newFixture(t, nil)
	d := data{}
	if ok, err := d.Exists(f.env.ProjectDir); ok || err != nil {
		t.Fatalf("Exists before = %v, %v", ok, err)
	}
	if err := os.WriteFile(d.Dir(f.env.ProjectDir), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.Exists(f.env.ProjectDir); ok || err == nil {
		t.Fatalf("Exists on a file = %v, %v; want an error", ok, err)
	}
	os.Remove(d.Dir(f.env.ProjectDir))
	if err := os.Mkdir(d.Dir(f.env.ProjectDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.Exists(f.env.ProjectDir); !ok || err != nil {
		t.Fatalf("Exists after = %v, %v", ok, err)
	}
}

func TestProjectCommandsExportAndKeepLiteralArguments(t *testing.T) {
	t.Setenv("BEADS_DIR", "/wrong")
	t.Setenv("BEADS_DB", "/wrong/db")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "wrong")
	t.Setenv("BD_DB", "/wrong/db")
	var calls []tool.Invocation
	var f fixture
	f = newFixture(t, func(_ context.Context, inv tool.Invocation, in io.Reader, out, _ io.Writer) error {
		calls = append(calls, inv)
		if !*f.held {
			t.Fatalf("%s %v ran without the tracker lock", inv.Name, inv.Args)
		}
		switch inv.Args[0] {
		case "init":
			seed(t, inv)
		case "create":
			data, _ := io.ReadAll(in)
			if string(data) != "body\nfrom stdin" {
				t.Fatalf("stdin: %q", data)
			}
			io.WriteString(out, "shop-abc\n")
		case "export":
			io.WriteString(out, "{\"id\":\"shop-abc\"}\n")
		}
		return nil
	})
	args := []string{"create", "--title", "Việt Nam có dấu", "--description", "--db", "--body-file", "file with spaces.md", "--json"}
	var out bytes.Buffer
	if err := (command{}).Run(context.Background(), f.env, args, strings.NewReader("body\nfrom stdin"), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || !slices.Equal(calls[1].Args, args) || out.String() != "shop-abc\n" {
		t.Fatalf("calls=%+v out=%s", calls, &out)
	}
	for _, c := range calls {
		if c.Dir != f.env.ProjectDir || !slices.Contains(c.Env, "BEADS_DIR="+f.env.DataDir) {
			t.Fatalf("wrong scope: %+v", c)
		}
		// Every ambient tracker variable is cleared over what bd inherits.
		for _, key := range []string{"BEADS_DB", "BD_DB", "BEADS_DOLT_SERVER_HOST"} {
			if !slices.Contains(c.Env, key+"=") {
				t.Fatalf("ambient %s not cleared: %q", key, c.Env)
			}
		}
		for _, e := range c.Env {
			if strings.Contains(e, "wrong") {
				t.Fatalf("ambient tracker leaked: %s", e)
			}
		}
	}
	if !slices.Contains(calls[0].Args, "--skip-agents") || !slices.Contains(calls[0].Args, "--skip-hooks") {
		t.Fatal("init modified instructions/hooks")
	}
	if *f.held {
		t.Fatal("Run kept the tracker lock")
	}
	data, err := os.ReadFile(filepath.Join(f.env.DataDir, "issues.jsonl"))
	if err != nil || !bytes.Contains(data, []byte("shop-abc")) {
		t.Fatalf("export: %s %v", data, err)
	}
	if err := initData(f.env); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 || calls[3].Args[0] != "export" {
		t.Fatal("reinitialized existing store")
	}
	for _, args := range [][]string{{"list", "--db=/wrong"}, {"list", "--global"}, {"list", "-C/wrong"}, {"init"}, {}} {
		if err := (command{}).Run(context.Background(), f.env, args, nil, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if len(calls) != 4 {
		t.Fatalf("a refused command ran: %+v", calls[4:])
	}
}

// initData is Data.Init with a background context and no stderr.
func initData(env tool.CommandEnv) error {
	return data{}.Init(context.Background(), env, io.Discard)
}

func TestFailedMutationStillRefreshesAndExportFailureKeepsSnapshot(t *testing.T) {
	writeErr := errors.New("some records failed")
	exportErr := errors.New("export interrupted")
	failExport := false
	f := newFixture(t, func(_ context.Context, inv tool.Invocation, _ io.Reader, out, _ io.Writer) error {
		switch inv.Args[0] {
		case "init":
			seed(t, inv)
		case "update":
			return writeErr
		case "export":
			if failExport {
				io.WriteString(out, "partial")
				return exportErr
			}
			io.WriteString(out, "complete\n")
		}
		return nil
	})
	err := (command{}).Run(context.Background(), f.env, []string{"update", "shop-abc"}, nil, io.Discard, io.Discard)
	if !errors.Is(err, writeErr) {
		t.Fatal(err)
	}
	path := filepath.Join(f.env.DataDir, "issues.jsonl")
	data, _ := os.ReadFile(path)
	if string(data) != "complete\n" {
		t.Fatalf("failed mutation not exported: %s", data)
	}
	failExport = true
	err = (command{}).Run(context.Background(), f.env, []string{"update", "shop-abc"}, nil, io.Discard, io.Discard)
	if !errors.Is(err, writeErr) || !errors.Is(err, exportErr) || !strings.Contains(err.Error(), "do not repeat the write") ||
		!strings.Contains(err.Error(), "mate tasks shop --init") {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "complete\n" {
		t.Fatalf("replaced good export with partial: %s", data)
	}
	left, _ := filepath.Glob(filepath.Join(f.env.DataDir, ".issues-*"))
	if len(left) != 0 {
		t.Fatalf("left partial export: %v", left)
	}
}

func TestRecallDoesNotInitializeAndReadsAuthoritativeWork(t *testing.T) {
	var calls []tool.Invocation
	f := newFixture(t, func(_ context.Context, inv tool.Invocation, _ io.Reader, out, _ io.Writer) error {
		calls = append(calls, inv)
		if inv.Args[0] == "list" {
			io.WriteString(out, `[{"id":"shop-a","status":"blocked","title":"Waiting","priority":1}]`)
		} else {
			io.WriteString(out, `[{"id":"shop-b","status":"open","title":"Ready","priority":2}]`)
		}
		return nil
	})
	text, present, err := recall{}.Render(context.Background(), f.env, 4096)
	if err != nil || present || text != "" || len(calls) != 0 || *f.locks != 0 {
		t.Fatalf("initialized on recall: %q %v %v, %d calls, %d locks", text, present, err, len(calls), *f.locks)
	}
	seed(t, tool.Invocation{Dir: f.env.ProjectDir})
	// A bogus snapshot must never be the source for recall readiness.
	os.WriteFile(filepath.Join(f.env.DataDir, "issues.jsonl"), []byte("wrong"), 0o600)
	text, present, err = recall{}.Render(context.Background(), f.env, 4096)
	want := "Beads work (up to 10 active/blocked and 10 ready; mate tasks shop --list):\n" +
		"  shop-a [blocked P1] Waiting\n" +
		"  shop-b [ready P2] Ready\n"
	if err != nil || !present || text != want {
		t.Fatalf("Render = %q, %v, %v; want %q", text, present, err, want)
	}
	for _, c := range calls {
		if !slices.Contains(c.Args, "--readonly") || !slices.Contains(c.Args, "10") {
			t.Fatalf("unbounded/write query: %+v", c)
		}
	}
	if *f.held || *f.locks != 1 {
		t.Fatalf("recall locks = %d, held %v; want one, released", *f.locks, *f.held)
	}

	// maxBytes keeps whole lines only.
	text, present, err = recall{}.Render(context.Background(), f.env, len(want)-1)
	if err != nil || !present || text != strings.TrimSuffix(want, "  shop-b [ready P2] Ready\n") {
		t.Fatalf("Render under %d bytes = %q, %v, %v", len(want)-1, text, present, err)
	}
}

func TestRecallWithNothingToDoIsAbsentAndAFailureIsAnError(t *testing.T) {
	fail := false
	f := newFixture(t, func(_ context.Context, _ tool.Invocation, _ io.Reader, out, stderr io.Writer) error {
		if fail {
			io.WriteString(stderr, "database is locked\n")
			return errors.New("bd: exit status 3")
		}
		io.WriteString(out, "[]")
		return nil
	})
	seed(t, tool.Invocation{Dir: f.env.ProjectDir})
	if text, present, err := (recall{}).Render(context.Background(), f.env, 4096); err != nil || present || text != "" {
		t.Fatalf("empty tracker: %q, %v, %v; want absent", text, present, err)
	}
	fail = true
	if _, present, err := (recall{}).Render(context.Background(), f.env, 4096); present || err == nil || err.Error() != "bd: exit status 3: database is locked; run mate tool beads shop -- ready" {
		t.Fatalf("failing bd: %v, %v", present, err)
	}
}

func TestTrackerRefusesEscapingSymlinks(t *testing.T) {
	for _, leaf := range []string{".beads", ".beads/issues.jsonl", ".beads/embeddeddolt"} {
		t.Run(leaf, func(t *testing.T) {
			f := newFixture(t, func(context.Context, tool.Invocation, io.Reader, io.Writer, io.Writer) error {
				t.Fatal("ran tool on escaping path")
				return nil
			})
			path := filepath.Join(f.env.ProjectDir, leaf)
			os.MkdirAll(filepath.Dir(path), 0o755)
			if err := os.Symlink(t.TempDir(), path); err != nil {
				t.Fatal(err)
			}
			if err := initData(f.env); err == nil || !strings.Contains(err.Error(), "outside the project directory") {
				t.Fatalf("Init = %v, want the escaping symlink refused", err)
			}
		})
	}
}

// A project directory not made yet is made before bd init runs in it.
func TestInitMakesTheProjectDirectory(t *testing.T) {
	var dirs []string
	f := newFixture(t, func(_ context.Context, inv tool.Invocation, _ io.Reader, _, _ io.Writer) error {
		if st, err := os.Stat(inv.Dir); err != nil || !st.IsDir() {
			t.Fatalf("%v ran in a missing directory %s", inv.Args, inv.Dir)
		}
		dirs = append(dirs, inv.Dir)
		if inv.Args[0] == "init" {
			seed(t, inv)
		}
		return nil
	})
	f.env.ProjectDir = filepath.Join(f.root, "notes")
	f.env.DataDir = data{}.Dir(f.env.ProjectDir)
	if err := initData(f.env); err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 {
		t.Fatalf("calls in %v, want init and export", dirs)
	}
}

func TestMissingBinaryAndMissingLockAreSaid(t *testing.T) {
	f := newFixture(t, func(context.Context, tool.Invocation, io.Reader, io.Writer, io.Writer) error {
		return &exec.Error{Name: "bd", Err: exec.ErrNotFound}
	})
	err := initData(f.env)
	if !errors.Is(err, exec.ErrNotFound) || !strings.HasPrefix(err.Error(), "bd is not installed; see docs/beads.md for installation: ") {
		t.Fatalf("Init with no bd = %v", err)
	}
	f.env.Lock = nil
	if err := initData(f.env); err == nil || !strings.Contains(err.Error(), "no lock") {
		t.Fatalf("Init with no lock = %v", err)
	}
	locked := errors.New("context deadline exceeded")
	f.env.Lock = func(context.Context) (func(), error) { return nil, locked }
	if err := (command{}).Run(context.Background(), f.env, []string{"list"}, nil, io.Discard, io.Discard); !errors.Is(err, locked) {
		t.Fatalf("Run while the lock is held = %v", err)
	}
}

// t on a project row opens bv on the project's tracker, bv found by the
// Console's findTool; without bv it says where to get it.
func TestViewerIsBeadsViewerOnTheProject(t *testing.T) {
	v := viewer{}
	if got, want := v.Bindings(), []tool.Binding{{Key: "t", Label: "tasks", Scope: tool.ScopeProject, Role: "tasks"}}; !slices.Equal(got, want) {
		t.Fatalf("Bindings() = %+v, want %+v", got, want)
	}
	if got := v.Placeholder(); got != "mate · tasks\r\n\r\nt on a project opens Beads Viewer here." {
		t.Fatalf("Placeholder() = %q", got)
	}
	ctx := tool.ViewerContext{ProjectDir: "/ws/shop"}
	inv, err := v.Argv(ctx, func(name string) string { return "/opt/bin/" + name })
	if err != nil || inv.Name != "/opt/bin/bv" || !slices.Equal(inv.Args, []string{"--db", "/ws/shop/.beads"}) || inv.Dir != "/ws/shop" {
		t.Fatalf("Argv = %+v, %v", inv, err)
	}
	// bv runs with the environment bd does: one place spells it.
	if !slices.Equal(inv.Env, environment("/ws/shop/.beads")) || !slices.Contains(inv.Env, "BEADS_DIR=/ws/shop/.beads") ||
		!slices.Contains(inv.Env, "BV_NO_UPDATE_CHECK=1") || !slices.Contains(inv.Env, "BV_NO_GITIGNORE=1") || !slices.Contains(inv.Env, "BEADS_DB=") {
		t.Fatalf("Argv env = %q", inv.Env)
	}
	const missing = "a project's tasks need Beads Viewer (bv): see docs/beads.md"
	for name, find := range map[string]func(string) string{
		"not found": func(string) string { return "" },
		"relative":  func(name string) string { return name },
	} {
		if inv, err := v.Argv(ctx, find); err == nil || err.Error() != missing {
			t.Errorf("%s: Argv = %+v, %v; want %q", name, inv, err, missing)
		}
	}
}

func TestSkillIsTaskManagement(t *testing.T) {
	s := skill{}
	if s.SkillName() != "task-management" || !strings.HasPrefix(s.SkillMarkdown(), "---\nname: task-management\n") {
		t.Fatalf("skill %q: %.60q", s.SkillName(), s.SkillMarkdown())
	}
	if !strings.HasPrefix(s.ManualSection(), "- `task-management` - ") {
		t.Fatalf("ManualSection() = %q", s.ManualSection())
	}
}
