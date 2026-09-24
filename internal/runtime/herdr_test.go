package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/process"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
)

func TestWithSessionPutsFlagBeforeTerminator(t *testing.T) {
	t.Parallel()
	got := runtime.WithSession("lab-s", []string{"agent", "start", "n", "--kind", "claude", "--pane", "w1:p1", "--", "--bare"})
	if !runtime.SessionBeforeTerminator(got) {
		t.Fatalf("session not before --: %#v", got)
	}
	inOptions := true
	for _, a := range got {
		if a == "--" {
			inOptions = false
			continue
		}
		if !inOptions && a == "--session" {
			t.Fatalf("trailing --session after --: %#v", got)
		}
	}
}

func TestHerdrEnsureSessionBindsRunningNamedSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id, err := runtime.ParseWorkspaceID("ws_test1")
	if err != nil {
		t.Fatal(err)
	}
	wantName := runtime.SessionNameForWorkspace(id)
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if !hasSessionBeforeTerminator(args) {
			t.Fatalf("argv missing --session in option region: %#v", args)
		}
		switch {
		case argvHas(args, "session", "list"):
			return process.Result{Stdout: sessionListRunning(wantName)}, nil
		case argvHas(args, "status"):
			return process.Result{Stdout: statusRunning(wantName)}, nil
		default:
			t.Fatalf("unexpected argv %#v", args)
			return process.Result{}, nil
		}
	}}
	rt := runtime.NewHerdr(runner)
	cfg := t.TempDir()
	h, err := rt.EnsureSession(ctx, runtime.SessionSpec{
		WorkspaceID: id,
		ConfigHome:  cfg,
		Names:       runtime.NewMemoryNameRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != wantName {
		t.Fatalf("name = %q", h.Name)
	}
	if !strings.HasSuffix(h.SocketPath, "/herdr/sessions/"+wantName+"/herdr.sock") {
		t.Fatalf("socket = %q", h.SocketPath)
	}
}

func TestHerdrEnsureSessionStartsWhenNotRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id, err := runtime.ParseWorkspaceID("ws_test1")
	if err != nil {
		t.Fatal(err)
	}
	wantName := runtime.SessionNameForWorkspace(id)
	started := false
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "session", "list") {
			if started {
				return process.Result{Stdout: sessionListRunning(wantName)}, nil
			}
			return process.Result{Stdout: []byte(`{"sessions":[]}`)}, nil
		}
		if argvHas(spec.Args, "status") {
			if !started {
				t.Fatal("status polled before StartServer")
			}
			return process.Result{Stdout: statusRunning(wantName)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	rt.StartServer = func(_ context.Context, session string) error {
		if session != wantName {
			t.Fatalf("started %q", session)
		}
		started = true
		return nil
	}
	h, err := rt.EnsureSession(ctx, runtime.SessionSpec{
		WorkspaceID: id,
		ConfigHome:  t.TempDir(),
		Names:       runtime.NewMemoryNameRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("missing named session must start a headless server")
	}
	if h.Name != wantName {
		t.Fatalf("name = %q", h.Name)
	}
}

func TestHerdrEnsureSessionRefusesForeignOwnerMarker(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	idA, err := runtime.ParseWorkspaceID("ws_aaaa1111")
	if err != nil {
		t.Fatal(err)
	}
	idB, err := runtime.ParseWorkspaceID("ws_bbbb2222")
	if err != nil {
		t.Fatal(err)
	}
	rt := runtime.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "session", "list") {
			return process.Result{Stdout: sessionListRunning("shared-lab")}, nil
		}
		if argvHas(spec.Args, "status") {
			return process.Result{Stdout: statusRunning("shared-lab")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}})
	_, err = rt.EnsureSession(context.Background(), runtime.SessionSpec{
		WorkspaceID: idA, Name: "shared-lab", ConfigHome: cfg, Names: runtime.NewMemoryNameRegistry(),
	})
	if err != nil {
		t.Fatalf("first owner: %v", err)
	}
	_, err = rt.EnsureSession(context.Background(), runtime.SessionSpec{
		WorkspaceID: idB, Name: "shared-lab", ConfigHome: cfg, Names: runtime.NewMemoryNameRegistry(),
	})
	if err == nil {
		t.Fatal("a second workspace must not join a Herdr session owned by another")
	}
}

func TestHerdrWaitRunningReturnsLastStatusError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	id, err := runtime.ParseWorkspaceID("ws_test1")
	if err != nil {
		t.Fatal(err)
	}
	wantName := runtime.SessionNameForWorkspace(id)
	const sunPath = "local socket name length exceeds capacity of sun_path of sockaddr_un"
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "session", "list") {
			return process.Result{Stdout: []byte(`{"sessions":[]}`)}, nil
		}
		if argvHas(spec.Args, "status") {
			return process.Result{ExitCode: 1, Stderr: []byte(sunPath)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	rt.StartServer = func(context.Context, string) error { return nil }
	_, err = rt.EnsureSession(ctx, runtime.SessionSpec{
		WorkspaceID: id,
		ConfigHome:  t.TempDir(),
		Names:       runtime.NewMemoryNameRegistry(),
	})
	if err == nil {
		t.Fatal("expected waitRunning to fail")
	}
	if !strings.Contains(err.Error(), "sun_path") {
		t.Fatalf("err = %v, want the status --json error, not a generic timeout (session %s)", err, wantName)
	}
}

func TestHerdrEnsureDoesNotCloseWhenAgentListFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cwd := "/private/tmp/same-project"
	var closed []string
	listJSON := []byte(`{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[` +
		`{"active_tab_id":"w5:t1","label":"default","number":2,"pane_count":1,"tab_count":1,"workspace_id":"w5"},` +
		`{"active_tab_id":"w3:t1","label":"default","number":1,"pane_count":1,"tab_count":1,"workspace_id":"w3"}]}}`)
	paneJSON := []byte(`{"id":"cli:pane:list","result":{"type":"pane_list","panes":[` +
		`{"pane_id":"w3:p1","tab_id":"w3:t1","workspace_id":"w3","terminal_id":"term_a","cwd":"` + cwd + `"},` +
		`{"pane_id":"w5:p1","tab_id":"w5:t1","workspace_id":"w5","terminal_id":"term_b","cwd":"` + cwd + `"}]}}`)
	tabJSON := []byte(`{"id":"cli:tab:list","result":{"type":"tab_list","tabs":[` +
		`{"tab_id":"w3:t1","workspace_id":"w3","label":"1","number":1},` +
		`{"tab_id":"w5:t1","workspace_id":"w5","label":"1","number":1}]}}`)
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if argvHas(args, "agent", "list") {
			return process.Result{}, observability.NewError(observability.CodeRuntimeUnavailable, "agent list failed")
		}
		if argvHas(args, "workspace", "close") {
			for i, a := range args {
				if a == "close" && i+1 < len(args) {
					closed = append(closed, args[i+1])
				}
			}
			return process.Result{Stdout: []byte(`{"id":"cli:workspace:close","result":{"type":"workspace_closed"}}`)}, nil
		}
		if argvHas(args, "workspace", "create") {
			t.Fatal("duplicates already exist; must not create a third")
		}
		if argvHas(args, "workspace", "list") {
			return process.Result{Stdout: listJSON}, nil
		}
		if argvHas(args, "pane", "list") {
			return process.Result{Stdout: paneJSON}, nil
		}
		if argvHas(args, "tab", "list") {
			return process.Result{Stdout: tabJSON}, nil
		}
		if argvHas(args, "pane", "get") {
			return process.Result{Stdout: paneGetReady("w3:p1")}, nil
		}
		t.Fatalf("unexpected argv %#v", args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	spec := runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "default",
		Cwd:     cwd,
	}
	if _, err := rt.EnsureProjectWorkspace(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if len(closed) != 0 {
		t.Fatalf("collapse closed %v although occupancy could not be read; an occupied extra must not be closed", closed)
	}
}

func TestHerdrLookupSessionDoesNotStartServer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id, err := runtime.ParseWorkspaceID("ws_test1")
	if err != nil {
		t.Fatal(err)
	}
	wantName := runtime.SessionNameForWorkspace(id)
	started := false
	cfg := t.TempDir()
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "session", "list") {
			return process.Result{Stdout: []byte(`{"sessions":[]}`)}, nil
		}
		t.Fatalf("lookup must not run %#v", spec.Args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	rt.StartServer = func(context.Context, string) error {
		started = true
		return nil
	}
	h, ok, err := rt.LookupSession(ctx, runtime.SessionSpec{
		WorkspaceID: id,
		ConfigHome:  cfg,
		Names:       runtime.NewMemoryNameRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a session that is not running must not look running")
	}
	if started {
		t.Fatal("LookupSession started a herdr server")
	}
	if _, statErr := os.Stat(filepath.Join(cfg, "mate", "session-owners", wantName)); !os.IsNotExist(statErr) {
		t.Fatalf("LookupSession wrote an owner marker: %v", statErr)
	}
	if h.Name != wantName {
		t.Fatalf("name = %q", h.Name)
	}
}

func TestHerdrLookupSessionBindsWhenRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id, err := runtime.ParseWorkspaceID("ws_test1")
	if err != nil {
		t.Fatal(err)
	}
	wantName := runtime.SessionNameForWorkspace(id)
	started := false
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "session", "list") {
			return process.Result{Stdout: sessionListRunning(wantName)}, nil
		}
		t.Fatalf("lookup must not run %#v", spec.Args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	rt.StartServer = func(context.Context, string) error {
		started = true
		return nil
	}
	h, ok, err := rt.LookupSession(ctx, runtime.SessionSpec{
		WorkspaceID: id,
		ConfigHome:  t.TempDir(),
		Names:       runtime.NewMemoryNameRegistry(),
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if started {
		t.Fatal("LookupSession started a herdr server for a session that was already running")
	}
	if h.Name != wantName {
		t.Fatalf("name = %q", h.Name)
	}
}

func TestHerdrEnsureSessionRefusesDefault(t *testing.T) {
	t.Parallel()
	rt := runtime.NewHerdr(&process.FakeRunner{})
	_, err := rt.EnsureSession(context.Background(), runtime.SessionSpec{
		Name:       "default",
		ConfigHome: "/tmp/cfg",
		Names:      runtime.NewMemoryNameRegistry(),
	})
	if err == nil {
		t.Fatal("default session must not map an app Workspace")
	}
}

func TestHerdrEnsureProjectWorkspaceCreatesWhenEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var created bool
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if !hasSessionBeforeTerminator(args) {
			t.Fatalf("argv %#v", args)
		}
		if res, ok := herdrInventoryDefaults(t, args); ok {
			return res, nil
		}
		switch {
		case argvHas(args, "workspace", "list"):
			if created {
				return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-one.json")}, nil
			}
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-empty.json")}, nil
		case argvHas(args, "pane", "list"):
			return process.Result{Stdout: []byte(`{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"term_x","cwd":"/private/tmp/project-a"}],"type":"pane_list"}}`)}, nil
		case argvHas(args, "tab", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-list-root.json")}, nil
		case argvHas(args, "workspace", "create"):
			created = true
			if !argvHas(args, "--no-focus") {
				t.Fatalf("workspace create must not assume focus: %#v", args)
			}
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-create.json")}, nil
		default:
			t.Fatalf("unexpected argv %#v", args)
			return process.Result{}, nil
		}
	}}
	rt := runtime.NewHerdr(runner)
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "mate-ws_test1", ConfigHome: "/tmp/cfg"},
		Label:   "Project A",
		Cwd:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("headless server has zero workspaces; create is required")
	}
	if ws.WorkspaceID != "w1" || ws.Label != "Project A" {
		t.Fatalf("ws = %+v", ws)
	}
}

func TestHerdrCreateAgentTabRenamesWorkspaceRootTab(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var createdTab, renamed bool
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if res, ok := herdrInventoryDefaults(t, args); ok {
			return res, nil
		}
		switch {
		case argvHas(args, "workspace", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-empty.json")}, nil
		case argvHas(args, "workspace", "create"):
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-create.json")}, nil
		case argvHas(args, "tab", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-list-root.json")}, nil
		case argvHas(args, "tab", "create"):
			createdTab = true
			t.Fatal("Mate tab must be the workspace-create root tab (ADR 0003), not a second tab create")
			return process.Result{}, nil
		case argvHas(args, "tab", "rename"):
			renamed = true
			if !argvHas(args, "w1:t1", "Mate") {
				t.Fatalf("rename argv %#v", args)
			}
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-rename.json")}, nil
		case argvHas(args, "pane", "list"):
			return process.Result{Stdout: []byte(`{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"term_x","cwd":"/private/tmp/gomate-g4-s3-cwd.R5Unrd"}],"type":"pane_list"}}`)}, nil
		case argvHas(args, "pane", "get"):
			return process.Result{Stdout: paneGetReady("w1:p1")}, nil
		default:
			t.Fatalf("unexpected argv %#v", args)
			return process.Result{}, nil
		}
	}}
	rt := runtime.NewHerdr(runner)
	cwd := "/private/tmp/gomate-g4-s3-cwd.R5Unrd"
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "Project A",
		Cwd:     cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: ws, Label: "Mate", Cwd: cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if createdTab {
		t.Fatal("tab create must not run for the unused workspace root tab")
	}
	if !renamed {
		t.Fatal("expected tab rename of the workspace-create root tab")
	}
	if tab.TabID != "w1:t1" || tab.PaneID != "w1:p1" {
		t.Fatalf("Mate tab must be the root pane, got %+v", tab)
	}
}

func TestHerdrEnsureProjectWorkspaceIgnoresFocusedCrewTab(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rootCwd := "/private/tmp/project-root"
	crewCwd := "/private/tmp/crew-worktree"
	created := false
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if res, ok := herdrInventoryDefaults(t, spec.Args); ok {
			return res, nil
		}
		if argvHas(spec.Args, "workspace", "create") {
			created = true
			t.Fatal("must not create a second workspace when the root tab still has the Project cwd")
			return process.Result{}, nil
		}
		if argvHas(spec.Args, "workspace", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[{"active_tab_id":"w1:t2","agent_status":"unknown","focused":true,"label":"Dup","number":1,"pane_count":2,"tab_count":2,"workspace_id":"w1"}]}}`)}, nil
		}
		if argvHas(spec.Args, "tab", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:tab:list","result":{"tabs":[{"tab_id":"w1:t1","workspace_id":"w1","label":"Mate","number":1},{"tab_id":"w1:t2","workspace_id":"w1","label":"Crew","number":2}],"type":"tab_list"}}`)}, nil
		}
		if argvHas(spec.Args, "pane", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"term_root","cwd":"` + rootCwd + `"},{"pane_id":"w1:p2","tab_id":"w1:t2","workspace_id":"w1","terminal_id":"term_crew","cwd":"` + crewCwd + `"}],"type":"pane_list"}}`)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "Dup",
		Cwd:     rootCwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("focused Crew tab must not participate in workspace identity")
	}
	if ws.WorkspaceID != "w1" {
		t.Fatalf("ensure for root cwd must return existing w1, got %+v", ws)
	}
	if ws.RootTab.TabID != "w1:t1" || ws.RootTab.PaneID != "w1:p1" {
		t.Fatalf("RootTab must be the workspace-create tab, not the focused Crew: %+v", ws.RootTab)
	}
}

func TestHerdrEnsureProjectWorkspaceMatchesLabelAndCwd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	wantCwd := "/private/tmp/g4-s3-b.FcSlxA"
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if res, ok := herdrInventoryDefaults(t, spec.Args); ok {
			return res, nil
		}
		if argvHas(spec.Args, "workspace", "create") {
			t.Fatal("must not create; w2 already has this label and cwd")
		}
		if argvHas(spec.Args, "workspace", "list") {
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-dup-label.json")}, nil
		}
		if argvHas(spec.Args, "pane", "list") {
			return process.Result{Stdout: readRuntimeTestdata(t, "pane-list-dup-label.json")}, nil
		}
		if argvHas(spec.Args, "tab", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:tab:list","result":{"tabs":[{"tab_id":"w1:t1","workspace_id":"w1","label":"1","number":1},{"tab_id":"w2:t1","workspace_id":"w2","label":"1","number":1}],"type":"tab_list"}}`)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "Duplicate",
		Cwd:     wantCwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.WorkspaceID != "w2" {
		t.Fatalf("duplicate labels are not identity; cwd %s belongs to w2, got %+v", wantCwd, ws)
	}
}

func TestHerdrLookupSameCwdDuplicatesPicksLowestAndEnsureClosesEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cwd := "/private/tmp/same-project"
	var closed []string
	listJSON := []byte(`{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[` +
		`{"active_tab_id":"w5:t1","label":"default","number":2,"pane_count":1,"tab_count":1,"workspace_id":"w5"},` +
		`{"active_tab_id":"w3:t1","label":"default","number":1,"pane_count":1,"tab_count":1,"workspace_id":"w3"}]}}`)
	paneJSON := []byte(`{"id":"cli:pane:list","result":{"type":"pane_list","panes":[` +
		`{"pane_id":"w3:p1","tab_id":"w3:t1","workspace_id":"w3","terminal_id":"term_a","cwd":"` + cwd + `"},` +
		`{"pane_id":"w5:p1","tab_id":"w5:t1","workspace_id":"w5","terminal_id":"term_b","cwd":"` + cwd + `"}]}}`)
	tabJSON := []byte(`{"id":"cli:tab:list","result":{"type":"tab_list","tabs":[` +
		`{"tab_id":"w3:t1","workspace_id":"w3","label":"1","number":1},` +
		`{"tab_id":"w5:t1","workspace_id":"w5","label":"1","number":1}]}}`)
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if argvHas(args, "agent", "list") {
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-list-empty.json")}, nil
		}
		if argvHas(args, "workspace", "close") {
			for i, a := range args {
				if a == "close" && i+1 < len(args) {
					closed = append(closed, args[i+1])
				}
			}
			return process.Result{Stdout: []byte(`{"id":"cli:workspace:close","result":{"type":"workspace_closed"}}`)}, nil
		}
		if argvHas(args, "workspace", "create") {
			t.Fatal("duplicates already exist; must not create a third")
		}
		if argvHas(args, "workspace", "list") {
			return process.Result{Stdout: listJSON}, nil
		}
		if argvHas(args, "pane", "list") {
			return process.Result{Stdout: paneJSON}, nil
		}
		if argvHas(args, "tab", "list") {
			return process.Result{Stdout: tabJSON}, nil
		}
		if argvHas(args, "pane", "get") {
			return process.Result{Stdout: paneGetReady("w3:p1")}, nil
		}
		t.Fatalf("unexpected argv %#v", args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	spec := runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "default",
		Cwd:     cwd,
	}
	found, ok, err := rt.LookupProjectWorkspace(ctx, spec)
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	if found.WorkspaceID != "w3" {
		t.Fatalf("lookup picked %q, want lowest id w3", found.WorkspaceID)
	}
	ws, err := rt.EnsureProjectWorkspace(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if ws.WorkspaceID != "w3" {
		t.Fatalf("ensure picked %q, want w3", ws.WorkspaceID)
	}
	if len(closed) != 1 || closed[0] != "w5" {
		t.Fatalf("empty extra must be closed, got %v", closed)
	}
}

func TestHerdrEnsureDoesNotCloseOccupiedExtra(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cwd := "/private/tmp/same-project"
	var closed []string
	listJSON := []byte(`{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[` +
		`{"active_tab_id":"w5:t1","label":"default","number":2,"pane_count":1,"tab_count":1,"workspace_id":"w5"},` +
		`{"active_tab_id":"w3:t1","label":"default","number":1,"pane_count":1,"tab_count":1,"workspace_id":"w3"}]}}`)
	paneJSON := []byte(`{"id":"cli:pane:list","result":{"type":"pane_list","panes":[` +
		`{"pane_id":"w3:p1","tab_id":"w3:t1","workspace_id":"w3","terminal_id":"term_a","cwd":"` + cwd + `"},` +
		`{"pane_id":"w5:p1","tab_id":"w5:t1","workspace_id":"w5","terminal_id":"term_b","cwd":"` + cwd + `"}]}}`)
	tabJSON := []byte(`{"id":"cli:tab:list","result":{"type":"tab_list","tabs":[` +
		`{"tab_id":"w3:t1","workspace_id":"w3","label":"1","number":1},` +
		`{"tab_id":"w5:t1","workspace_id":"w5","label":"1","number":1}]}}`)
	agentJSON := []byte(`{"id":"cli:agent:list","result":{"type":"agent_list","agents":[{` +
		`"name":"mate-occupied","agent":"claude","agent_status":"blocked",` +
		`"pane_id":"w5:p1","tab_id":"w5:t1","workspace_id":"w5","terminal_id":"term_b",` +
		`"cwd":"` + cwd + `"}]}}`)
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if argvHas(args, "agent", "list") {
			return process.Result{Stdout: agentJSON}, nil
		}
		if argvHas(args, "workspace", "close") {
			for i, a := range args {
				if a == "close" && i+1 < len(args) {
					closed = append(closed, args[i+1])
				}
			}
			return process.Result{Stdout: []byte(`{"id":"cli:workspace:close","result":{"type":"workspace_closed"}}`)}, nil
		}
		if argvHas(args, "workspace", "create") {
			t.Fatal("duplicates already exist; must not create a third")
		}
		if argvHas(args, "workspace", "list") {
			return process.Result{Stdout: listJSON}, nil
		}
		if argvHas(args, "pane", "list") {
			return process.Result{Stdout: paneJSON}, nil
		}
		if argvHas(args, "tab", "list") {
			return process.Result{Stdout: tabJSON}, nil
		}
		if argvHas(args, "pane", "get") {
			return process.Result{Stdout: paneGetReady("w5:p1")}, nil
		}
		t.Fatalf("unexpected argv %#v", args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	spec := runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "default",
		Cwd:     cwd,
	}
	found, ok, err := rt.LookupProjectWorkspace(ctx, spec)
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	if found.WorkspaceID != "w5" {
		t.Fatalf("lookup picked %q, want occupied w5", found.WorkspaceID)
	}
	ws, err := rt.EnsureProjectWorkspace(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if ws.WorkspaceID != "w5" {
		t.Fatalf("ensure picked %q, want occupied w5", ws.WorkspaceID)
	}
	if len(closed) != 1 || closed[0] != "w3" {
		t.Fatalf("empty extra w3 must be closed, occupied w5 must not; got %v", closed)
	}
}

func TestHerdrCreateAgentTabDoesNotReuseDeadMatePane(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cwd := "/private/tmp/gomate-g4-s3-cwd.R5Unrd"
	var createdTab, renamed bool
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if argvHas(args, "pane", "get") {
			if argvHas(args, "w1:p1") {
				return process.Result{ExitCode: 1, Stdout: readRuntimeTestdata(t, "error-pane-not-found-get.json")}, nil
			}
			return process.Result{Stdout: paneGetReady("w1:p2")}, nil
		}
		if res, ok := herdrInventoryDefaults(t, args); ok {
			return res, nil
		}
		switch {
		case argvHas(args, "tab", "list"):
			return process.Result{Stdout: []byte(`{"id":"cli:tab:list","result":{"type":"tab_list","tabs":[` +
				`{"tab_id":"w1:t1","workspace_id":"w1","label":"Mate","number":1}]}}`)}, nil
		case argvHas(args, "tab", "create"):
			createdTab = true
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-create.json")}, nil
		case argvHas(args, "tab", "rename"):
			renamed = true
			t.Fatal("dead Mate pane must not be renamed; create a new tab")
			return process.Result{}, nil
		case argvHas(args, "pane", "list"):
			return process.Result{Stdout: []byte(`{"id":"cli:pane:list","result":{"type":"pane_list","panes":[` +
				`{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"term_x","cwd":"` + cwd + `"}]}}`)}, nil
		default:
			t.Fatalf("unexpected argv %#v", args)
			return process.Result{}, nil
		}
	}}
	rt := runtime.NewHerdr(runner)
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: runtime.WorkspaceHandle{
			Session:     runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
			WorkspaceID: "w1",
			Cwd:         cwd,
		},
		Label: "Mate",
		Cwd:   cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if renamed {
		t.Fatal("dead pane must not be renamed")
	}
	if !createdTab {
		t.Fatal("dead Mate pane must fall through to tab create")
	}
	if tab.PaneID != "w1:p2" {
		t.Fatalf("got pane %s, want w1:p2 from tab create", tab.PaneID)
	}
}

func TestHerdrCreateAgentTabCreatesWhenRootPaneIsDead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cwd := "/private/tmp/gomate-g4-s3-cwd.R5Unrd"
	var createdTab, renamed bool
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if argvHas(args, "pane", "get") {
			if argvHas(args, "w1:p1") {
				return process.Result{ExitCode: 1, Stdout: readRuntimeTestdata(t, "error-pane-not-found-get.json")}, nil
			}
			return process.Result{Stdout: paneGetReady("w1:p2")}, nil
		}
		if res, ok := herdrInventoryDefaults(t, args); ok {
			return res, nil
		}
		switch {
		case argvHas(args, "tab", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-list-root.json")}, nil
		case argvHas(args, "tab", "create"):
			createdTab = true
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-create.json")}, nil
		case argvHas(args, "tab", "rename"):
			renamed = true
			t.Fatal("dead root pane must not be renamed")
			return process.Result{}, nil
		case argvHas(args, "pane", "list"):
			return process.Result{Stdout: []byte(`{"id":"cli:pane:list","result":{"type":"pane_list","panes":[` +
				`{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"term_x","cwd":"` + cwd + `"}]}}`)}, nil
		default:
			t.Fatalf("unexpected argv %#v", args)
			return process.Result{}, nil
		}
	}}
	rt := runtime.NewHerdr(runner)
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: runtime.WorkspaceHandle{
			Session:     runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
			WorkspaceID: "w1",
			Cwd:         cwd,
		},
		Label: "Mate",
		Cwd:   cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if renamed {
		t.Fatal("dead root pane must not be renamed")
	}
	if !createdTab {
		t.Fatal("dead root pane must fall through to tab create")
	}
	if tab.PaneID != "w1:p2" {
		t.Fatalf("got pane %s, want w1:p2", tab.PaneID)
	}
}

func TestHerdrEnsureProjectWorkspaceReuseRefusesEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	wantCwd := "/private/tmp/g4-s3-b.FcSlxA"
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if res, ok := herdrInventoryDefaults(t, spec.Args); ok {
			return res, nil
		}
		if argvHas(spec.Args, "workspace", "create") {
			t.Fatal("reuse must not create, and must not reach create with env")
		}
		if argvHas(spec.Args, "workspace", "list") {
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-dup-label.json")}, nil
		}
		if argvHas(spec.Args, "pane", "list") {
			return process.Result{Stdout: readRuntimeTestdata(t, "pane-list-dup-label.json")}, nil
		}
		if argvHas(spec.Args, "tab", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:tab:list","result":{"tabs":[{"tab_id":"w1:t1","workspace_id":"w1","label":"1","number":1},{"tab_id":"w2:t1","workspace_id":"w2","label":"1","number":1}],"type":"tab_list"}}`)}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	_, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "Duplicate",
		Cwd:     wantCwd,
		Env:     []runtime.EnvVar{{Key: "MATEV2_AGENT_ID", Value: "mate_001"}},
	})
	if err == nil {
		t.Fatal("reuse with env would not inject it; refuse rather than drop")
	}
}

func TestHerdrEnsureProjectWorkspaceDoesNotReuseMismatchedCwd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	created := false
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if res, ok := herdrInventoryDefaults(t, spec.Args); ok {
			return res, nil
		}
		if argvHas(spec.Args, "workspace", "list") {
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-one.json")}, nil
		}
		if argvHas(spec.Args, "pane", "list") {
			return process.Result{Stdout: readRuntimeTestdata(t, "pane-list-dup-label.json")}, nil
		}
		if argvHas(spec.Args, "tab", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:tab:list","result":{"tabs":[{"tab_id":"w1:t1","workspace_id":"w1","label":"1","number":1}],"type":"tab_list"}}`)}, nil
		}
		if argvHas(spec.Args, "workspace", "create") {
			created = true
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-create.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "Project A",
		Cwd:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("a same-label workspace at a different cwd must not be reused")
	}
	if ws.WorkspaceID != "w1" {
		t.Fatalf("ws = %+v", ws)
	}
}

func TestHerdrCreateAgentTabPassesAllowlistedEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cwd := t.TempDir()
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if argvHas(args, "pane", "get") {
			return process.Result{Stdout: paneGetReady("w1:p2")}, nil
		}
		if argvHas(args, "tab", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:tab:list","result":{"tabs":[],"type":"tab_list"}}`)}, nil
		}
		if !argvHas(args, "tab", "create") {
			t.Fatalf("argv %#v", args)
		}
		if !hasSessionBeforeTerminator(args) {
			t.Fatalf("session placement %#v", args)
		}
		if !argvHas(args, "--env", "MATEV2_AGENT_ID=mate_001") {
			t.Fatalf("missing allowlisted env: %#v", args)
		}
		for i, a := range args {
			if a == "--env" && i+1 < len(args) && strings.HasPrefix(args[i+1], "SECRET=") {
				t.Fatalf("secret leaked onto tab create: %#v", args)
			}
		}
		return process.Result{Stdout: tabCreatedJSON(cwd)}, nil
	}}
	rt := runtime.NewHerdr(runner)
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: runtime.WorkspaceHandle{Session: runtime.SessionHandle{Name: "s"}, WorkspaceID: "w1"},
		Label:     "Mate",
		Cwd:       cwd,
		Env:       []runtime.EnvVar{{Key: "MATEV2_AGENT_ID", Value: "mate_001"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tab.TabID != "w1:t2" || tab.PaneID != "w1:p2" {
		t.Fatalf("tab = %+v", tab)
	}
	if len(tab.Env) != 1 || tab.Env[0].Key != "MATEV2_AGENT_ID" {
		t.Fatalf("tab env = %#v", tab.Env)
	}
}

func TestHerdrStartRetriesPaneBusyThenStarts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	starts := 0
	rt, launch, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "start") {
			starts++
			if starts == 1 {
				return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-pane-busy.json")}, nil
			}
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-ready.json")}, nil
		}
		if argvHas(spec.Args, "agent", "get") {
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-get.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	res := reserveOn(t, rt.Names, session, "mate_001")
	if _, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch)); err != nil {
		t.Fatalf("pane_busy must be retried: %v", err)
	}
	if starts < 2 {
		t.Fatalf("starts = %d, want a retry after agent_pane_busy", starts)
	}
}

func TestHerdrStartClearsDefaultClaudeConfigInPaneBeforeAgentStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var calls [][]string
	rt, launch, session, tab := bootHerdrWithOptions(t, true, func(_ context.Context, spec process.Spec) (process.Result, error) {
		calls = append(calls, append([]string(nil), spec.Args...))
		if argvHas(spec.Args, "pane", "run") {
			if !argvHas(spec.Args, "unset", "CLAUDE_CONFIG_DIR") {
				t.Fatalf("default Claude launch clear command = %#v", spec.Args)
			}
			for _, k := range harness.NestedSessionEnv {
				if !argvHas(spec.Args, k) {
					t.Fatalf("clear command must also unset nested-session var %s: %#v", k, spec.Args)
				}
			}
			return process.Result{}, nil
		}
		if argvHas(spec.Args, "agent", "start") {
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-get.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	res := reserveOn(t, rt.Names, session, "mate_001")
	if _, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch)); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	if len(calls) < 2 || !argvHas(calls[0], "pane", "run", tab.PaneID) || !argvHas(calls[1], "agent", "start") {
		t.Fatalf("calls = %#v, want a pane clear targeting %s before agent start", calls, tab.PaneID)
	}
	// The clear must target the pane the agent is started in - a clear sent
	// anywhere else silently leaves the stale value on the agent's pane and
	// every other assertion here still passes.
	if !argvHas(calls[1], "--pane", tab.PaneID) {
		t.Fatalf("agent start argv = %#v, want --pane %s", calls[1], tab.PaneID)
	}
}

// A launch's own environment reaches the agent's pane at every start, not
// only at pane create: a Mate restarted while a Crew holds its workspace is
// a fresh `tab create` that inherits nothing, and a Codex agent must run in
// the CODEX_HOME its launch pinned, whatever the Herdr server was started
// with. The export comes after the unset and before agent start, targets
// the agent's own pane, and quotes each value for the pane's shell.
func TestHerdrStartExportsTheLaunchEnvIntoThePane(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cwd := t.TempDir()
	if err := os.WriteFile(harness.CodexInstructionPath(cwd), []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "it's a home")
	launch, err := harness.Codex{}.BuildLaunchSpec(ctx, harness.AgentSpec{
		Kind: harness.KindCodex, Cwd: cwd,
		Env:    []harness.EnvVar{{Key: "MATEV2_AGENT_ROLE", Value: "mate"}},
		Config: harness.Config{CodexHome: home},
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	rt, _, session, tab := bootHerdrWithOptions(t, true, func(_ context.Context, spec process.Spec) (process.Result, error) {
		calls = append(calls, append([]string(nil), spec.Args...))
		if argvHas(spec.Args, "pane", "run") {
			return process.Result{}, nil
		}
		if argvHas(spec.Args, "agent", "start") {
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-get.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	tab.Cwd = cwd
	res := reserveOn(t, rt.Names, session, "mate_001")
	if _, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch)); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	if len(calls) != 3 || !argvHas(calls[0], "pane", "run", tab.PaneID, "unset") ||
		!argvHas(calls[1], "pane", "run", tab.PaneID, "export") || !argvHas(calls[2], "agent", "start") {
		t.Fatalf("calls = %#v, want unset, then export, then agent start, on pane %s", calls, tab.PaneID)
	}
	want := []string{"MATEV2_AGENT_ROLE='mate'", "CODEX_HOME='" + strings.ReplaceAll(home, "'", `'\''`) + "'"}
	if !argvHas(calls[1], append([]string{"export"}, want...)...) {
		t.Fatalf("export argv = %#v, want %q", calls[1], want)
	}
}

// TestHerdrStartPaneBusyExhaustedIsActionable pins the honest-error
// requirement: when Herdr never answers anything but agent_pane_busy for the
// whole retry budget, the caller-facing message must name what mate was
// doing, name the pane, and say what an operator can do next - not just
// repeat Herdr's own opaque "pane is not an available shell" wording with no
// context. It must NOT tell the operator to go inspect or attach to the named
// pane: orchestration's reconcileStartFailure always removes that pane's tab
// (and, being the sole tab of a freshly created workspace, the workspace)
// before this error reaches a caller, so by the time anyone reads the
// message there is nothing left at that id to check. Herdr's original
// message and the pane id are still recoverable (in Details), so nothing
// observable is lost.
func TestHerdrStartPaneBusyExhaustedIsActionable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "start") {
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-pane-busy.json")}, nil
		}
		if argvHas(spec.Args, "pane", "process-info") {
			return process.Result{Stdout: readRuntimeTestdata(t, "pane-process-info-wrapped-shell.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	res := reserveOn(t, rt.Names, session, "mate_001")
	_, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err == nil {
		t.Fatal("expected an error once the pane_busy retry budget is exhausted")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) {
		t.Fatalf("error is not a coded observability.Error: %v", err)
	}
	if coded.Code != observability.CodeStateConflict {
		t.Fatalf("code = %q, want state_conflict", coded.Code)
	}
	msg := coded.Message
	for _, want := range []string{res.Name(), tab.PaneID, "retrying", "not an available shell"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not mention %q", msg, want)
		}
	}
	if !strings.Contains(msg, "check") && !strings.Contains(msg, "Check") {
		t.Fatalf("message %q gives no actionable next step", msg)
	}
	for _, mustNot := range []string{"pane read", "attach to it", "attach to the pane"} {
		if strings.Contains(msg, mustNot) {
			t.Fatalf("message %q tells the operator to inspect a pane mate has already torn down (%q)", msg, mustNot)
		}
	}
	if !strings.Contains(msg, "already removed") {
		t.Fatalf("message %q does not say mate already cleaned up the pane/tab", msg)
	}
	if paneID, _ := coded.Details["pane_id"].(string); paneID != tab.PaneID {
		t.Fatalf("details pane_id = %q, want %q", paneID, tab.PaneID)
	}
	if herdrMsg, _ := coded.Details["herdr_message"].(string); !strings.Contains(herdrMsg, "not an available shell") {
		t.Fatalf("details herdr_message = %q, want Herdr's original wording preserved", herdrMsg)
	}
}

func TestHerdrStartArgvPutsSessionBeforeDashDash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if argvHas(args, "agent", "start") {
			if !runtime.SessionBeforeTerminator(args) {
				t.Fatalf("session not before --: %#v", args)
			}
			inOptions := true
			for _, a := range args {
				if a == "--" {
					inOptions = false
					continue
				}
				if !inOptions && a == "--session" {
					t.Fatalf("production start leaked --session after --: %#v", args)
				}
			}
			if !argvHas(args, "--", "--dangerously-skip-permissions", "--append-system-prompt-file") {
				t.Fatalf("extra harness args missing after --: %#v", args)
			}
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-get.json")}, nil
		}
		if argvHas(args, "agent", "get") {
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-get.json")}, nil
		}
		return process.Result{}, nil
	})
	res := reserveOn(t, rt.Names, session, "mate_001")
	if _, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch)); err != nil {
		t.Fatal(err)
	}
}

func TestHerdrStartNotReadyReturnsLiveHandle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "start") {
			return process.Result{
				ExitCode: 1,
				Stderr:   readRuntimeTestdata(t, "error-agent-not-ready.json"),
			}, nil
		}
		if argvHas(spec.Args, "agent", "get") {
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-get.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	res := reserveOn(t, rt.Names, session, "mate_001")
	h, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err != nil {
		t.Fatalf("blocked-during-startup is a live agent, not a failed start: %v", err)
	}
	if h.Name != res.Name() {
		t.Fatalf("handle = %+v", h)
	}
}

func TestHerdrWaitUntilBlockedIsSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, _, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "wait") {
			if !argvHas(spec.Args, "--until", "blocked") {
				t.Fatalf("wait argv %#v", spec.Args)
			}
			return process.Result{Stdout: readRuntimeTestdata(t, "wait-blocked.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	obs, err := rt.WaitAgent(ctx, runtime.AgentHandle{Session: session, Name: "mate-g4-01", Tab: tab}, runtime.WaitCondition{
		Until:   []runtime.AgentStatus{runtime.AgentBlocked},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("blocked wait must succeed: %v", err)
	}
	if obs.Status != runtime.AgentBlocked {
		t.Fatalf("status = %q", obs.Status)
	}
}

func TestHerdrInspectMissingAgentIsNotFound(t *testing.T) {
	t.Parallel()
	rt, _, session, _ := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-found-get.json")}, nil
	})
	_, err := rt.InspectAgent(context.Background(), runtime.AgentHandle{Session: session, Name: "no-such-agent"})
	if err == nil {
		t.Fatal("expected agent not found")
	}
	coded, ok := err.(*observability.Error)
	if !ok || coded.Code != observability.CodeNotFound || coded.Details["herdr_code"] != runtime.HerdrAgentNotFound {
		t.Fatalf("err = %+v", err)
	}
}

func TestHerdrStartMissingPaneIsUsage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-pane-not-found.json")}, nil
	})
	res := reserveOn(t, rt.Names, session, "mate_001")
	_, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err == nil {
		t.Fatal("expected pane not found")
	}
	coded, ok := err.(*observability.Error)
	if !ok || coded.Code != observability.CodeUsage || coded.Details["herdr_code"] != runtime.HerdrAgentPaneNotFound {
		t.Fatalf("pane missing must be usage/agent_pane_not_found, got %+v", err)
	}
}

func TestHerdrStartRefusesNilSpec(t *testing.T) {
	t.Parallel()
	rt := runtime.NewHerdr(&process.FakeRunner{})
	_, err := rt.StartAgent(context.Background(), nil)
	if err == nil {
		t.Fatal("nil spec must not start")
	}
	if observability.ExitCode(err) != observability.ExitUsage {
		t.Fatalf("err = %v", err)
	}
}

func TestHerdrPromptBlockedIsTargetBlocked(t *testing.T) {
	t.Parallel()
	rt, _, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-blocked.json")}, nil
	})
	err := rt.PromptAgent(context.Background(), runtime.AgentHandle{Session: session, Name: "mate-g4-01", Tab: tab}, "hello")
	if err == nil {
		t.Fatal("expected blocked")
	}
	coded, ok := err.(*observability.Error)
	if !ok || coded.Code != observability.CodeTargetBlocked || coded.Details["herdr_code"] != runtime.HerdrAgentBlocked {
		t.Fatalf("err = %+v", err)
	}
}

func TestHerdrWaitAgentRefusesNonPositiveTimeout(t *testing.T) {
	t.Parallel()
	rt, _, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		t.Fatalf("wait without a timeout must not reach Herdr (unbounded): %#v", spec.Args)
		return process.Result{}, nil
	})
	h := runtime.AgentHandle{Session: session, Name: "mate-g4-01", Tab: tab}
	if _, err := rt.WaitAgent(context.Background(), h, runtime.WaitCondition{Until: []runtime.AgentStatus{runtime.AgentBlocked}}); err == nil {
		t.Fatal("zero timeout must be refused")
	}
	if _, err := rt.WaitAgent(context.Background(), h, runtime.WaitCondition{
		Until:   []runtime.AgentStatus{runtime.AgentBlocked},
		Timeout: -time.Second,
	}); err == nil {
		t.Fatal("negative timeout must be refused")
	}
}

func TestHerdrWaitAgentAlwaysEmitsTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, _, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "wait") {
			if !argvHas(spec.Args, "--timeout", "2000") {
				t.Fatalf("wait must emit --timeout; Herdr waits forever without it: %#v", spec.Args)
			}
			return process.Result{Stdout: readRuntimeTestdata(t, "wait-blocked.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	obs, err := rt.WaitAgent(ctx, runtime.AgentHandle{Session: session, Name: "mate-g4-01", Tab: tab}, runtime.WaitCondition{
		Until:   []runtime.AgentStatus{runtime.AgentBlocked},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("blocked wait must succeed: %v", err)
	}
	got := runtime.ClassifyObservation(obs)
	if got.Kind != runtime.ReadinessBlocked {
		t.Fatalf("readiness = %+v", got)
	}
}

func TestHerdrEnsureProjectWorkspaceRefusesUnknownEnv(t *testing.T) {
	t.Parallel()
	rt := runtime.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		t.Fatalf("unknown env must not reach argv: %#v", spec.Args)
		return process.Result{}, nil
	}})
	_, err := rt.EnsureProjectWorkspace(context.Background(), runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "Project A",
		Cwd:     t.TempDir(),
		Env:     []runtime.EnvVar{{Key: "MATEV2_AGENT_ID", Value: "mate_001"}, {Key: "SECRET", Value: "nope"}},
	})
	if err == nil {
		t.Fatal("unknown env must be refused")
	}
}

func TestHerdrEnsureProjectWorkspacePassesAllowlistedEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if res, ok := herdrInventoryDefaults(t, args); ok {
			return res, nil
		}
		switch {
		case argvHas(args, "workspace", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-empty.json")}, nil
		case argvHas(args, "workspace", "create"):
			if !argvHas(args, "--env", "MATEV2_AGENT_ID=mate_001") || !argvHas(args, "--env", "MATEV2_AGENT_ROLE=mate") {
				t.Fatalf("workspace create missing identity env: %#v", args)
			}
			for i, a := range args {
				if a == "--env" && i+1 < len(args) && strings.HasPrefix(args[i+1], "SECRET=") {
					t.Fatalf("secret leaked: %#v", args)
				}
			}
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-create.json")}, nil
		default:
			t.Fatalf("unexpected argv %#v", args)
			return process.Result{}, nil
		}
	}}
	rt := runtime.NewHerdr(runner)
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "Project A",
		Cwd:     t.TempDir(),
		Env: []runtime.EnvVar{
			{Key: "MATEV2_AGENT_ID", Value: "mate_001"},
			{Key: "MATEV2_AGENT_ROLE", Value: "mate"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.RootTab.Env) != 2 {
		t.Fatalf("root tab env = %#v", ws.RootTab.Env)
	}
}

func TestHerdrCreateAgentTabWithEnvCreatesNewTabInsteadOfRenamingRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cwd := "/private/tmp/gomate-g4-s3-cwd.R5Unrd"
	crewCwd := "/private/tmp/gomate-crew-wt"
	created := false
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if res, ok := herdrInventoryDefaults(t, args); ok {
			return res, nil
		}
		switch {
		case argvHas(args, "workspace", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-empty.json")}, nil
		case argvHas(args, "workspace", "create"):
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-create.json")}, nil
		case argvHas(args, "tab", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-list-root.json")}, nil
		case argvHas(args, "tab", "rename"):
			t.Fatal("rename must not run: Crew env is applied by tab create, not by renaming the Mate root pane")
			return process.Result{}, nil
		case argvHas(args, "tab", "create"):
			created = true
			if !argvHas(args, "--env") {
				t.Fatalf("crew tab create must carry --env: %#v", args)
			}
			return process.Result{Stdout: tabCreatedJSON(crewCwd)}, nil
		case argvHas(args, "pane", "list"):
			return process.Result{Stdout: []byte(`{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"term_x","cwd":"` + cwd + `"}],"type":"pane_list"}}`)}, nil
		case argvHas(args, "pane", "get"):
			return process.Result{Stdout: paneGetReady("w1:p1")}, nil
		default:
			t.Fatalf("unexpected argv %#v", args)
			return process.Result{}, nil
		}
	}}
	rt := runtime.NewHerdr(runner)
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "Project A",
		Cwd:     cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: ws, Label: "crew_001", Cwd: crewCwd,
		Env: []runtime.EnvVar{{Key: "MATEV2_AGENT_ID", Value: "crew_001"}, {Key: "MATEV2_AGENT_ROLE", Value: "crew"}, {Key: "MATEV2_CREW_ID", Value: "crew_001"}},
	})
	if err != nil {
		t.Fatalf("crew tab create: %v", err)
	}
	if !created || tab.Label != "crew_001" {
		t.Fatalf("created=%v tab=%+v", created, tab)
	}
}

func TestHerdrCreateAgentTabRenameRefusesEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cwd := "/private/tmp/gomate-g4-s3-cwd.R5Unrd"
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if res, ok := herdrInventoryDefaults(t, args); ok {
			return res, nil
		}
		switch {
		case argvHas(args, "workspace", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-empty.json")}, nil
		case argvHas(args, "workspace", "create"):
			if argvHas(args, "--env") {
				t.Fatalf("this test injects env on the rename, not workspace create: %#v", args)
			}
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-create.json")}, nil
		case argvHas(args, "tab", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-list-root.json")}, nil
		case argvHas(args, "tab", "rename"):
			t.Fatal("rename must not run: env cannot be applied after workspace create")
			return process.Result{}, nil
		case argvHas(args, "pane", "list"):
			return process.Result{Stdout: []byte(`{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"term_x","cwd":"` + cwd + `"}],"type":"pane_list"}}`)}, nil
		case argvHas(args, "pane", "get"):
			return process.Result{Stdout: paneGetReady("w1:p1")}, nil
		default:
			t.Fatalf("unexpected argv %#v", args)
			return process.Result{}, nil
		}
	}}
	rt := runtime.NewHerdr(runner)
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		Label:   "Project A",
		Cwd:     cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: ws, Label: "Mate", Cwd: cwd,
		Env: []runtime.EnvVar{{Key: "MATEV2_AGENT_ID", Value: "mate_001"}},
	})
	if err == nil {
		t.Fatal("Mate env on tab rename would not reach the pane process")
	}
}

func TestHerdrCreateAgentTabRefusesUnknownEnv(t *testing.T) {
	t.Parallel()
	_, err := runtime.NewHerdr(&process.FakeRunner{}).CreateAgentTab(context.Background(), runtime.TabSpec{
		Workspace: runtime.WorkspaceHandle{Session: runtime.SessionHandle{Name: "s"}, WorkspaceID: "w1"},
		Label:     "Crew",
		Cwd:       t.TempDir(),
		Env:       []runtime.EnvVar{{Key: "MATEV2_AGENT_ID", Value: "crew_001"}, {Key: "SECRET", Value: "nope"}},
	})
	if err == nil {
		t.Fatal("unknown env must be refused")
	}
}

func TestHerdrListAgentsUsesAgentListNotFocus(t *testing.T) {
	t.Parallel()
	rt := runtime.NewHerdr(&process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "list") {
			if !hasSessionBeforeTerminator(spec.Args) {
				t.Fatalf("list missing --session in option region: %#v", spec.Args)
			}
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-list-after-start.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	}})
	list, err := rt.ListAgents(context.Background(), runtime.SessionHandle{Name: "lab"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Handle.Name != "mate-g4-01" || list[0].Handle.Tab.PaneID != "w1:p2" {
		t.Fatalf("list = %#v", list)
	}
	if list[0].Handle.Tab.WorkspaceID != "w1" {
		t.Fatalf("workspace id missing: %#v", list[0].Handle)
	}
}

func TestHerdrStopDoesNotCallAgentStop(t *testing.T) {
	t.Parallel()
	rt, _, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "stop") {
			t.Fatal("there is no herdr agent stop")
		}
		if argvHas(spec.Args, "pane", "close") {
			return process.Result{Stdout: []byte(`{"id":"cli:pane:close","result":{"type":"pane_closed"}}`)}, nil
		}
		if argvHas(spec.Args, "agent", "prompt") {
			return process.Result{Stdout: []byte(`{"id":"cli:agent:prompt","result":{"type":"agent_prompted"}}`)}, nil
		}
		if argvHas(spec.Args, "agent", "get") || argvHas(spec.Args, "agent", "wait") {
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-agent-not-found-get.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	h := runtime.AgentHandle{Session: session, Name: "mate-g4-01", Kind: harness.KindClaude, Tab: tab}
	if err := rt.StopAgent(context.Background(), h, runtime.StopForce); err != nil {
		t.Fatal(err)
	}
}

func reserveOn(t *testing.T, names runtime.LiveNameRegistry, session runtime.SessionHandle, raw string) runtime.NameReservation {
	t.Helper()
	res, err := runtime.AllocateAgentName(names, session.Name, "m", raw, runtime.FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func bootHerdr(t *testing.T, handler func(context.Context, process.Spec) (process.Result, error)) (*runtime.Herdr, harness.LaunchSpec, runtime.SessionHandle, runtime.TabHandle) {
	return bootHerdrWithOptions(t, false, handler)
}

func bootHerdrWithOptions(t *testing.T, observePaneEnvClear bool, handler func(context.Context, process.Spec) (process.Result, error)) (*runtime.Herdr, harness.LaunchSpec, runtime.SessionHandle, runtime.TabHandle) {
	t.Helper()
	cwd := t.TempDir()
	path := filepath.Join(cwd, "context.md")
	if err := os.WriteFile(path, []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch, err := harness.Claude{}.BuildLaunchSpec(context.Background(), harness.AgentSpec{
		Kind: harness.KindClaude, Cwd: cwd, ContextPath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &process.FakeRunner{Handler: func(ctx context.Context, spec process.Spec) (process.Result, error) {
		if !observePaneEnvClear && argvHas(spec.Args, "pane", "run") {
			return process.Result{}, nil
		}
		return handler(ctx, spec)
	}}
	rt := runtime.NewHerdr(runner)
	rt.Names = runtime.NewMemoryNameRegistry()
	session := runtime.SessionHandle{Name: "mate-ws_test1", ConfigHome: "/tmp/cfg", SocketPath: "/tmp/cfg/herdr/sessions/mate-ws_test1/herdr.sock"}
	tab := runtime.TabHandle{
		Session:     session,
		WorkspaceID: "w1",
		TabID:       "w1:t2",
		PaneID:      "w1:p2",
		TerminalID:  "term_test",
		Cwd:         cwd,
		Label:       "Mate",
	}
	return rt, launch, session, tab
}

func herdrInventoryDefaults(t *testing.T, args []string) (process.Result, bool) {
	t.Helper()
	if argvHas(args, "agent", "list") {
		return process.Result{Stdout: readRuntimeTestdata(t, "agent-list-empty.json")}, true
	}
	if argvHas(args, "workspace", "close") {
		return process.Result{Stdout: []byte(`{"id":"cli:workspace:close","result":{"type":"workspace_closed"}}`)}, true
	}
	if argvHas(args, "pane", "get") {
		id := "w1:p1"
		for i, a := range args {
			if a == "get" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				id = args[i+1]
				break
			}
		}
		return process.Result{Stdout: paneGetReady(id)}, true
	}
	return process.Result{}, false
}

func readRuntimeTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func argvHas(args []string, want ...string) bool {
	if len(want) == 0 {
		return false
	}
	for i := 0; i+len(want) <= len(args); i++ {
		ok := true
		for j := range want {
			if args[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func hasSessionBeforeTerminator(args []string) bool {
	return runtime.SessionBeforeTerminator(args)
}

func sessionListRunning(name string) []byte {
	return []byte(`{"sessions":[{"default":false,"name":"` + name + `","running":true,"socket_path":"/tmp/cfg/herdr/sessions/` + name + `/herdr.sock"}]}`)
}

func statusRunning(name string) []byte {
	return []byte(`{"server":{"running":true,"session":"` + name + `","socket":"/tmp/cfg/herdr/sessions/` + name + `/herdr.sock","version":"0.8.2"}}`)
}

// tabCreatedJSON is tab-create.json with the root pane cwd substituted, so a
// test can assert the tab handle carries whatever Herdr actually reports
// instead of the shared fixture's fixed cwd.
func tabCreatedJSON(cwd string) []byte {
	return []byte(`{"id":"cli:tab:create","result":{"root_pane":{"agent_status":"unknown","cwd":"` + cwd + `","focused":false,"foreground_cwd":"` + cwd + `","pane_id":"w1:p2","revision":0,"tab_id":"w1:t2","terminal_id":"term_65abf501349f72","workspace_id":"w1"},"tab":{"agent_status":"unknown","focused":false,"label":"Mate","number":2,"pane_count":1,"tab_id":"w1:t2","workspace_id":"w1"},"type":"tab_created"}}`)
}

func paneGetReady(id string) []byte {
	return []byte(`{"id":"cli:pane:get","result":{"pane":{"pane_id":"` + id + `","tab_id":"w1:t2","terminal_id":"term_test","workspace_id":"w1","terminal_title":"shell","terminal_title_stripped":"shell"},"type":"pane_info"}}`)
}

// paneGetTitleless is a live pane whose shell has produced no title. Live
// cause (lab, ADR 0011): zsh sitting on "[oh-my-zsh] Would you like to
// update? [Y/n]" never draws one, and `pane get` reports revision 0 forever.
func paneGetTitleless(id string) []byte {
	return []byte(`{"id":"cli:pane:get","result":{"pane":{"pane_id":"` + id + `","tab_id":"w1:t1","terminal_id":"term_test","workspace_id":"w1","revision":0},"type":"pane_info"}}`)
}

func TestHerdrEnsureProjectWorkspaceDoesNotGateOnAPaneTitle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	creates, closes, paneGets := 0, 0, 0
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		switch {
		case argvHas(args, "agent", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-list-empty.json")}, nil
		case argvHas(args, "pane", "get"):
			paneGets++
			return process.Result{Stdout: paneGetTitleless("w1:p1")}, nil
		case argvHas(args, "workspace", "close"):
			closes++
			return process.Result{Stdout: []byte(`{"id":"cli:workspace:close","result":{"type":"workspace_closed"}}`)}, nil
		case argvHas(args, "workspace", "list"):
			if creates == 0 {
				return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-empty.json")}, nil
			}
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-list-one.json")}, nil
		case argvHas(args, "pane", "list"):
			return process.Result{Stdout: []byte(`{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"term_x","cwd":"/private/tmp/project-a"}],"type":"pane_list"}}`)}, nil
		case argvHas(args, "tab", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-list-root.json")}, nil
		case argvHas(args, "workspace", "create"):
			creates++
			return process.Result{Stdout: readRuntimeTestdata(t, "workspace-create.json")}, nil
		default:
			t.Fatalf("unexpected argv %#v", args)
			return process.Result{}, nil
		}
	}}
	rt := runtime.NewHerdr(runner)
	rt.PaneShellBudget = 10 * time.Millisecond
	ws, err := rt.EnsureProjectWorkspace(ctx, runtime.WorkspaceSpec{
		Session: runtime.SessionHandle{Name: "mate-ws_test1", ConfigHome: "/tmp/cfg"},
		Label:   "Project A",
		Cwd:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("a titleless pane is not a reason to refuse the Project workspace: %v", err)
	}
	if ws.WorkspaceID != "w1" {
		t.Fatalf("ws = %+v", ws)
	}
	if creates != 1 {
		t.Fatalf("workspace create ran %d times, want exactly 1; recreating on a pane heuristic is how duplicates are made", creates)
	}
	if closes != 0 {
		t.Fatalf("workspace close ran %d times; a title is not evidence a workspace should be destroyed", closes)
	}
	if paneGets != 0 {
		t.Fatalf("ensure polled pane get %d times; the pane gate belongs to the tab that uses the pane", paneGets)
	}
}

func TestHerdrCreateAgentTabReusesRootTabWithNoPaneTitle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cwd := "/private/tmp/gomate-g4-s3-cwd.R5Unrd"
	renamed, createdTab := false, false
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		switch {
		case argvHas(args, "agent", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "agent-list-empty.json")}, nil
		case argvHas(args, "pane", "get"):
			return process.Result{Stdout: paneGetTitleless("w1:p1")}, nil
		case argvHas(args, "tab", "list"):
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-list-root.json")}, nil
		case argvHas(args, "tab", "rename"):
			renamed = true
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-rename.json")}, nil
		case argvHas(args, "tab", "create"):
			createdTab = true
			return process.Result{Stdout: readRuntimeTestdata(t, "tab-create.json")}, nil
		case argvHas(args, "pane", "list"):
			return process.Result{Stdout: []byte(`{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"term_x","cwd":"` + cwd + `"}],"type":"pane_list"}}`)}, nil
		default:
			t.Fatalf("unexpected argv %#v", args)
			return process.Result{}, nil
		}
	}}
	rt := runtime.NewHerdr(runner)
	rt.PaneShellBudget = 10 * time.Millisecond
	ws := runtime.WorkspaceHandle{
		Session:     runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"},
		WorkspaceID: "w1", Label: "Project A", Cwd: cwd,
	}
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{Workspace: ws, Label: "Mate", Cwd: cwd})
	if err != nil {
		t.Fatalf("a titleless root pane is not a missing pane: %v", err)
	}
	if !renamed {
		t.Fatal("the unused root tab must still be renamed for Mate")
	}
	if createdTab {
		t.Fatal("a second tab leaves the root tab behind after stop")
	}
	if tab.PaneID != "w1:p1" || tab.TabID != "w1:t1" {
		t.Fatalf("tab = %+v, want the workspace root", tab)
	}
}

// assertWouldHaveStarted proves the pre-fix mechanism: before this change,
// every CreateAgentTab return path used TabHandle{Cwd: spec.Cwd} regardless
// of what Herdr reported, so NewAgentStartSpec's launch-vs-pane cwd guard
// (adapter.go, Validate) compared the request against itself and could never
// disagree. It reconstructs that exact pre-fix shape - the same TabID/PaneID
// the real response named, but with spec.Cwd as the observed cwd - and shows
// Validate passes it, which is why `herdr agent start` would have been
// issued on a real Herdr divergence.
func assertWouldHaveStarted(t *testing.T, tab runtime.TabHandle, requestedCwd string, launch harness.LaunchSpec, names runtime.LiveNameRegistry, session string) {
	t.Helper()
	preFix := tab
	preFix.Cwd = requestedCwd
	reservation, err := runtime.AllocateAgentName(names, session, "m", "raw-"+t.Name(), runtime.FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.NewAgentStartSpec(preFix, reservation, launch, 0); err != nil {
		t.Fatalf("pre-fix handle (Cwd: requested) failed validation; the guard was supposed to be unable to catch this: %v", err)
	}
}

// TestHerdrCreateAgentTabRefusesMismatchedCwdFromCreate is the captain's
// first case: `herdr tab create` succeeds but names a different cwd than
// requested. Without checkObservedTabCwd in the `tab create` return path,
// CreateAgentTab returned TabHandle{Cwd: spec.Cwd} unconditionally (proven
// live by two now-fixed test fixtures in this file that requested a cwd the
// tab-create.json fixture did not name and still passed), so
// NewAgentStartSpec would validate cleanly and `herdr agent start` would run
// against a pane whose real cwd never matched.
func TestHerdrCreateAgentTabRefusesMismatchedCwdFromCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	requestedCwd := t.TempDir()
	observedCwd := t.TempDir()
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if argvHas(args, "tab", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:tab:list","result":{"tabs":[],"type":"tab_list"}}`)}, nil
		}
		if argvHas(args, "tab", "create") {
			return process.Result{Stdout: tabCreatedJSON(observedCwd)}, nil
		}
		t.Fatalf("unexpected argv %#v", args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: runtime.WorkspaceHandle{Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"}, WorkspaceID: "w1"},
		Label:     "Mate",
		Cwd:       requestedCwd,
	})
	if err == nil {
		t.Fatal("a tab whose reported cwd disagrees with the request must be refused")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("err = %v, want state_conflict", err)
	}
	if !strings.Contains(err.Error(), observedCwd) || !strings.Contains(err.Error(), requestedCwd) {
		t.Fatalf("err must name both cwds so the mismatch is distinguishable from a missing one: %v", err)
	}
	// Leak prevention: the tab is real (herdr tab create already succeeded),
	// so the handle must still name it for a saga to compensate.
	if tab.TabID != "w1:t2" || tab.PaneID != "w1:p2" {
		t.Fatalf("refusal after a real tab create must still return the created handle so it is not leaked; got %+v", tab)
	}
	if err := os.WriteFile(filepath.Join(requestedCwd, "context.md"), []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch, err := harness.Claude{}.BuildLaunchSpec(ctx, harness.AgentSpec{Kind: harness.KindClaude, Cwd: requestedCwd, ContextPath: filepath.Join(requestedCwd, "context.md")})
	if err != nil {
		t.Fatal(err)
	}
	assertWouldHaveStarted(t, tab, requestedCwd, launch, runtime.NewMemoryNameRegistry(), "s")
}

// TestHerdrCreateAgentTabRefusesEmptyCwdFromCreate is the captain's second
// case: `herdr tab create` succeeds but its root_pane carries no cwd at all.
// This is a different diagnosis from a mismatch - Herdr said nothing, rather
// than disagreeing - but must refuse for the same reason: an unverified pane
// cannot prove context delivery.
func TestHerdrCreateAgentTabRefusesEmptyCwdFromCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	requestedCwd := t.TempDir()
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if argvHas(args, "tab", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:tab:list","result":{"tabs":[],"type":"tab_list"}}`)}, nil
		}
		if argvHas(args, "tab", "create") {
			return process.Result{Stdout: tabCreatedJSON("")}, nil
		}
		t.Fatalf("unexpected argv %#v", args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: runtime.WorkspaceHandle{Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"}, WorkspaceID: "w1"},
		Label:     "Mate",
		Cwd:       requestedCwd,
	})
	if err == nil {
		t.Fatal("a tab create response with no cwd must be refused, not trusted")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("err = %v, want state_conflict", err)
	}
	if strings.Contains(err.Error(), "not the requested") {
		t.Fatalf("a missing cwd must read as \"herdr did not say\", not as a reported mismatch: %v", err)
	}
	if tab.TabID != "w1:t2" || tab.PaneID != "w1:p2" {
		t.Fatalf("refusal after a real tab create must still return the created handle so it is not leaked; got %+v", tab)
	}
	if err := os.WriteFile(filepath.Join(requestedCwd, "context.md"), []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch, err := harness.Claude{}.BuildLaunchSpec(ctx, harness.AgentSpec{Kind: harness.KindClaude, Cwd: requestedCwd, ContextPath: filepath.Join(requestedCwd, "context.md")})
	if err != nil {
		t.Fatal(err)
	}
	assertWouldHaveStarted(t, tab, requestedCwd, launch, runtime.NewMemoryNameRegistry(), "s")
}

// TestHerdrCreateAgentTabRefusesMismatchedCwdOnLabelReuse is the captain's
// third case: `tab list` finds an existing tab already carrying the
// requested label, but its pane's real cwd differs from what is being asked
// for now. This path (h.tabByLabel) never even read pane.Cwd into the
// comparison before this fix - it stored spec.Cwd on the handle unconditionally.
// No new tab is created on this path, so there is nothing to leak: a
// zero-value TabHandle on refusal is correct here.
func TestHerdrCreateAgentTabRefusesMismatchedCwdOnLabelReuse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	requestedCwd := t.TempDir()
	observedCwd := t.TempDir()
	runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
		args := spec.Args
		if argvHas(args, "tab", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:tab:list","result":{"tabs":[{"tab_id":"w1:t9","workspace_id":"w1","label":"crew_001","number":2}],"type":"tab_list"}}`)}, nil
		}
		if argvHas(args, "pane", "list") {
			return process.Result{Stdout: []byte(`{"id":"cli:pane:list","result":{"panes":[{"pane_id":"w1:p9","tab_id":"w1:t9","workspace_id":"w1","terminal_id":"term_9","cwd":"` + observedCwd + `"}],"type":"pane_list"}}`)}, nil
		}
		if argvHas(args, "pane", "get") {
			return process.Result{Stdout: paneGetReady("w1:p9")}, nil
		}
		t.Fatalf("unexpected argv %#v", args)
		return process.Result{}, nil
	}}
	rt := runtime.NewHerdr(runner)
	tab, err := rt.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: runtime.WorkspaceHandle{Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"}, WorkspaceID: "w1"},
		Label:     "crew_001",
		Cwd:       requestedCwd,
	})
	if err == nil {
		t.Fatal("reusing a labeled tab whose observed pane cwd disagrees with the request must be refused")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("err = %v, want state_conflict", err)
	}
	if !strings.Contains(err.Error(), observedCwd) || !strings.Contains(err.Error(), requestedCwd) {
		t.Fatalf("err must name both cwds: %v", err)
	}
	if tab.TabID != "" || tab.PaneID != "" {
		t.Fatalf("no new tab was created on this path; refusal must not fabricate a handle: %+v", tab)
	}
	if err := os.WriteFile(filepath.Join(requestedCwd, "context.md"), []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch, err := harness.Claude{}.BuildLaunchSpec(ctx, harness.AgentSpec{Kind: harness.KindClaude, Cwd: requestedCwd, ContextPath: filepath.Join(requestedCwd, "context.md")})
	if err != nil {
		t.Fatal(err)
	}
	preFix := runtime.TabHandle{
		Session: runtime.SessionHandle{Name: "s", ConfigHome: "/tmp/cfg"}, WorkspaceID: "w1",
		TabID: "w1:t9", PaneID: "w1:p9", TerminalID: "term_9",
		Cwd: requestedCwd, Label: "crew_001",
	}
	assertWouldHaveStarted(t, preFix, requestedCwd, launch, runtime.NewMemoryNameRegistry(), "s")
}

// Herdr's own words for an unusable pane are "agent target pane w4:p1 is not
// an available shell". A user reading that in the Console learns nothing they
// can act on: not what mate was doing, not how long it tried, not what is
// actually running in that pane. The live cause on the reporter's machine was
// Kiro CLI's zshrc block re-executing the login shell inside a
// `zsh (kiro-cli-term)` PTY wrapper, which Herdr does not recognize as a
// shell - visible only in `pane process-info`, which mate never asked for.
//
// So an exhausted retry asks once, and reports the occupant it found.
func TestHerdrStartPaneBusyExhaustedNamesTheProcessHoldingThePane(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	infos := 0
	rt, launch, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "start") {
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-pane-busy.json")}, nil
		}
		if argvHas(spec.Args, "pane", "process-info") {
			infos++
			return process.Result{Stdout: readRuntimeTestdata(t, "pane-process-info-wrapped-shell.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	res := reserveOn(t, rt.Names, session, "mate_001")
	_, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err == nil {
		t.Fatal("an always-busy pane must fail the start")
	}
	if infos != 1 {
		t.Fatalf("pane process-info calls = %d, want exactly one diagnostic read after the budget is spent", infos)
	}

	var coded *observability.Error
	if !errors.As(err, &coded) {
		t.Fatalf("err = %v, want a coded error", err)
	}
	if coded.Code != observability.CodeStateConflict {
		t.Fatalf("code = %s, want state_conflict", coded.Code)
	}
	if got, _ := coded.Details["herdr_code"].(string); got != runtime.HerdrAgentPaneBusy {
		t.Fatalf("details.herdr_code = %q, want the original Herdr code", got)
	}
	if got, _ := coded.Details["pane_id"].(string); got != tab.PaneID {
		t.Fatalf("details.pane_id = %q, want %q", got, tab.PaneID)
	}
	if got, _ := coded.Details["pane_process"].(string); got != "zsh (kiro-cli-term)" {
		t.Fatalf("details.pane_process = %q, want the process Herdr reported in the pane", got)
	}
	msg := coded.Message
	for _, want := range []string{
		tab.PaneID,            // which pane
		"zsh (kiro-cli-term)", // what is in it
		"shell prompt",        // what Herdr requires instead
		"retrying",            // for how long it tried
		"already removed",     // and that the pane is gone, so do not go looking
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
	// The Console draws a footer message on one line and cuts it at the
	// terminal edge (internal/ui/console/frame.go), so the diagnosis has to
	// come before the remedy or the only reader who sees it is reading JSON.
	// 100 cells is what an 80-column Console leaves after its own prefix and
	// the " - r re-reads" hint.
	head := msg
	if len(head) > 100 {
		head = head[:100]
	}
	if !strings.Contains(head, tab.PaneID) || !strings.Contains(head, "zsh (kiro-cli-term)") {
		t.Errorf("first 100 chars %q lose the pane or its occupant; a cut footer must still carry the diagnosis", head)
	}
}

// A pane that cannot be inspected must still produce the refusal, with the
// reason the inspection failed - never a bare "not an available shell" and
// never the inspection error in place of the refusal.
func TestHerdrStartPaneBusyExhaustedSurvivesAFailedInspection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, launch, session, tab := bootHerdr(t, func(_ context.Context, spec process.Spec) (process.Result, error) {
		if argvHas(spec.Args, "agent", "start") {
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-pane-busy.json")}, nil
		}
		if argvHas(spec.Args, "pane", "process-info") {
			return process.Result{ExitCode: 1, Stderr: readRuntimeTestdata(t, "error-pane-not-found-get.json")}, nil
		}
		t.Fatalf("unexpected argv %#v", spec.Args)
		return process.Result{}, nil
	})
	res := reserveOn(t, rt.Names, session, "mate_001")
	_, err := rt.StartAgent(ctx, mustStartSpec(t, tab, res, launch))
	if err == nil {
		t.Fatal("an always-busy pane must fail the start")
	}
	var coded *observability.Error
	if !errors.As(err, &coded) {
		t.Fatalf("err = %v, want a coded error", err)
	}
	if coded.Code != observability.CodeStateConflict {
		t.Fatalf("code = %s, want the pane-busy refusal, not the inspection failure", coded.Code)
	}
	if got, _ := coded.Details["pane_process_error"].(string); got == "" {
		t.Fatal("details.pane_process_error must record why mate could not name the occupant")
	}
	if !strings.Contains(coded.Message, tab.PaneID) || !strings.Contains(coded.Message, "retrying") {
		t.Fatalf("message = %q", coded.Message)
	}
}
