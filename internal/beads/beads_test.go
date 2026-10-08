package beads

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

func fixture(t *testing.T, run Runner) (*Tracker, *store.Workspace) {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	tracker, err := Open(w, "shop", run)
	if err != nil {
		t.Fatal(err)
	}
	return tracker, w
}

func seed(t *testing.T, c Command) {
	t.Helper()
	dir := filepath.Join(c.Dir, ".beads")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectCommandsExportAndKeepLiteralArguments(t *testing.T) {
	t.Setenv("BEADS_DIR", "/wrong")
	t.Setenv("BEADS_DB", "/wrong/db")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "wrong")
	t.Setenv("BD_DB", "/wrong/db")
	var calls []Command
	tracker, w := fixture(t, func(ctx context.Context, c Command, in io.Reader, out, stderr io.Writer) error {
		calls = append(calls, c)
		switch c.Args[0] {
		case "init":
			seed(t, c)
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
	if err := tracker.Run(context.Background(), args, strings.NewReader("body\nfrom stdin"), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || !slices.Equal(calls[1].Args, args) || out.String() != "shop-abc\n" {
		t.Fatalf("calls=%+v out=%s", calls, &out)
	}
	for _, c := range calls {
		if c.Dir != w.ProjectDir("shop") || !slices.Contains(c.Env, "BEADS_DIR="+tracker.Dir()) {
			t.Fatalf("wrong scope: %+v", c)
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
	data, err := os.ReadFile(filepath.Join(tracker.Dir(), "issues.jsonl"))
	if err != nil || !bytes.Contains(data, []byte("shop-abc")) {
		t.Fatalf("export: %s %v", data, err)
	}
	if err := tracker.Init(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 || calls[3].Args[0] != "export" {
		t.Fatal("reinitialized existing store")
	}
	for _, args := range [][]string{{"list", "--db=/wrong"}, {"list", "--global"}, {"list", "-C/wrong"}} {
		if err := tracker.Run(context.Background(), args, nil, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted scope change %v", args)
		}
	}
}

func TestFailedMutationStillRefreshesAndExportFailureKeepsSnapshot(t *testing.T) {
	writeErr := errors.New("some records failed")
	exportErr := errors.New("export interrupted")
	failExport := false
	tracker, _ := fixture(t, func(ctx context.Context, c Command, in io.Reader, out, stderr io.Writer) error {
		switch c.Args[0] {
		case "init":
			seed(t, c)
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
	err := tracker.Run(context.Background(), []string{"update", "shop-abc"}, nil, io.Discard, io.Discard)
	if !errors.Is(err, writeErr) {
		t.Fatal(err)
	}
	path := filepath.Join(tracker.Dir(), "issues.jsonl")
	data, _ := os.ReadFile(path)
	if string(data) != "complete\n" {
		t.Fatalf("failed mutation not exported: %s", data)
	}
	failExport = true
	err = tracker.Run(context.Background(), []string{"update", "shop-abc"}, nil, io.Discard, io.Discard)
	if !errors.Is(err, writeErr) || !errors.Is(err, exportErr) || !strings.Contains(err.Error(), "do not repeat the write") {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "complete\n" {
		t.Fatalf("replaced good export with partial: %s", data)
	}
	left, _ := filepath.Glob(filepath.Join(tracker.Dir(), ".issues-*"))
	if len(left) != 0 {
		t.Fatalf("left partial export: %v", left)
	}
}

func TestRecallDoesNotInitializeAndReadsAuthoritativeWork(t *testing.T) {
	var calls []Command
	tracker, _ := fixture(t, func(ctx context.Context, c Command, in io.Reader, out, stderr io.Writer) error {
		calls = append(calls, c)
		if c.Args[0] == "list" {
			io.WriteString(out, `[{"id":"shop-a","status":"blocked","title":"Waiting","priority":1}]`)
		} else {
			io.WriteString(out, `[{"id":"shop-b","status":"open","title":"Ready","priority":2}]`)
		}
		return nil
	})
	active, ready, err := tracker.Work(context.Background())
	if err != nil || len(calls) != 0 || len(active)+len(ready) != 0 {
		t.Fatalf("initialized on recall: %v", err)
	}
	seed(t, tracker.command("bd"))
	// A bogus snapshot must never be the source for recall readiness.
	os.WriteFile(filepath.Join(tracker.Dir(), "issues.jsonl"), []byte("wrong"), 0600)
	active, ready, err = tracker.Work(context.Background())
	if err != nil || len(active) != 1 || len(ready) != 1 || active[0].ID != "shop-a" || ready[0].ID != "shop-b" {
		t.Fatalf("work: %+v %+v %v", active, ready, err)
	}
	for _, c := range calls {
		if !slices.Contains(c.Args, "--readonly") || !slices.Contains(c.Args, "10") {
			t.Fatalf("unbounded/write query: %+v", c)
		}
	}
}

func TestTrackerRefusesEscapingSymlinksAndPreservesLegacyPlan(t *testing.T) {
	for _, leaf := range []string{".beads", ".beads.lock", ".beads/issues.jsonl", ".beads/embeddeddolt"} {
		t.Run(leaf, func(t *testing.T) {
			tracker, w := fixture(t, func(context.Context, Command, io.Reader, io.Writer, io.Writer) error {
				t.Fatal("ran tool on escaping path")
				return nil
			})
			path := filepath.Join(w.ProjectDir("shop"), leaf)
			os.MkdirAll(filepath.Dir(path), 0755)
			if err := os.Symlink(t.TempDir(), path); err != nil {
				t.Fatal(err)
			}
			if err := tracker.Init(context.Background(), io.Discard); err == nil {
				t.Fatal("accepted escaping symlink")
			}
		})
	}
	tracker, w := fixture(t, func(context.Context, Command, io.Reader, io.Writer, io.Writer) error {
		t.Fatal("hid legacy plan")
		return nil
	})
	path := filepath.Join(w.ProjectDir("shop"), "tasks.yaml")
	os.WriteFile(path, []byte("keep me"), 0600)
	if err := tracker.Init(context.Background(), io.Discard); err == nil || !strings.Contains(err.Error(), "legacy plan") {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "keep me" {
		t.Fatal("legacy data changed")
	}
}

func TestViewerUsesFreshProjectDataWithoutHoldingWriterLock(t *testing.T) {
	var tracker *Tracker
	var calls []string
	tracker, _ = fixture(t, func(ctx context.Context, c Command, in io.Reader, out, stderr io.Writer) error {
		calls = append(calls, c.Name+" "+c.Args[0])
		if c.Args[0] == "init" {
			seed(t, c)
		}
		if c.Name == "bv" {
			if !slices.Equal(c.Args, []string{"--db", tracker.Dir(), "--robot-triage", "--brief"}) {
				t.Fatalf("viewer args: %v", c.Args)
			}
			unlock, err := tracker.w.LockBeads(ctx, "shop")
			if err != nil {
				t.Fatal(err)
			}
			unlock()
		}
		return nil
	})
	if err := tracker.Viewer(context.Background(), []string{"--robot-triage", "--brief"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"bd init", "bd export", "bv --db"}) {
		t.Fatalf("order: %v", calls)
	}
}
