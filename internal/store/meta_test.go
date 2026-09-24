package store_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestStoreMetaRoundTrip(t *testing.T) {
	w := newWorkspace(t)
	if err := w.AddProject("shop", store.ProjectConfig{Repo: "shop"}); err != nil {
		t.Fatal(err)
	}

	meta := map[string]string{
		"task":       "add checkout tests",
		"harness":    "codex",
		"pane":       "%12",
		"worktree":   w.WorktreeDir("shop", "k3"),
		"branch":     "mate/k3",
		"transcript": "",
		"session_id": "abc-123",
	}
	if err := w.WriteCrewMeta("shop", "k3", meta); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	got, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	if !reflect.DeepEqual(got, meta) {
		t.Fatalf("meta round trip: got %+v, want %+v", got, meta)
	}

	// Mate meta uses the same format at its own path.
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude"}); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	mate, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatalf("ReadMateMeta: %v", err)
	}
	if mate["harness"] != "claude" {
		t.Fatalf("mate meta = %+v", mate)
	}

	// A crew that never wrote meta reads as empty, not as an error.
	empty, err := w.ReadCrewMeta("shop", "zz")
	if err != nil {
		t.Fatalf("ReadCrewMeta for an unknown crew: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("unknown crew meta = %+v, want empty", empty)
	}
}

func TestStoreMetaWritesDeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "k3.meta")

	meta := map[string]string{
		"worktree": "/tmp/wt",
		"task":     "one",
		"harness":  "codex",
		"branch":   "mate/k3",
	}
	if err := store.WriteMeta(path, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "branch=mate/k3\nharness=codex\ntask=one\nworktree=/tmp/wt\n"
	if string(first) != want {
		t.Fatalf("meta file =\n%q\nwant\n%q", first, want)
	}

	// Rewriting the same map must produce the same bytes, whatever order the
	// map iterates in.
	for i := 0; i < 20; i++ {
		if err := store.WriteMeta(path, meta); err != nil {
			t.Fatalf("WriteMeta: %v", err)
		}
		again, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != want {
			t.Fatalf("meta file changed between writes: %q", again)
		}
	}
}

func TestStoreMetaRejectsBadKeysAndValues(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name string
		meta map[string]string
	}{
		{"newline in value", map[string]string{"task": "first\nsecond"}},
		{"carriage return in value", map[string]string{"task": "first\rsecond"}},
		{"uppercase key", map[string]string{"Task": "x"}},
		{"digit in key", map[string]string{"task1": "x"}},
		{"empty key", map[string]string{"": "x"}},
		{"dash in key", map[string]string{"session-id": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_")+".meta")
			if err := store.WriteMeta(path, tc.meta); err == nil {
				t.Fatalf("WriteMeta(%+v) succeeded, want an error", tc.meta)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("a rejected write created %s", path)
			}
			if leftovers := tempFiles(t, dir); len(leftovers) != 0 {
				t.Fatalf("a rejected write left temp files: %v", leftovers)
			}
		})
	}
}

func TestStoreMetaReadRejectsMalformedFile(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"no equals sign": "harness codex\n",
		"invalid key":    "SESSION=abc\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".meta")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReadMeta(path); err == nil {
				t.Fatalf("ReadMeta(%q) succeeded, want an error", content)
			}
		})
	}
}

func TestStoreAtomicWritesLeaveNoTempFiles(t *testing.T) {
	w := newWorkspace(t)
	if err := w.AddProject("shop", store.ProjectConfig{Repo: "shop"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{"task": "x"}); err != nil {
		t.Fatal(err)
	}
	// A rejected value must not leave debris next to the real file either.
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{"task": "a\nb"}); err == nil {
		t.Fatal("WriteCrewMeta with a newline value succeeded")
	}

	for _, dir := range []string{w.StateDir(), w.ProjectDir("shop"), w.CrewsDir("shop")} {
		if leftovers := tempFiles(t, dir); len(leftovers) != 0 {
			t.Fatalf("temp files left in %s: %v", dir, leftovers)
		}
	}
	// The earlier good write is intact.
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil || meta["task"] != "x" {
		t.Fatalf("meta after the rejected write = %+v, err = %v", meta, err)
	}
}

// tempFiles lists the atomic-write temp files still present in dir.
func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.Contains(e.Name(), ".tmp") {
			out = append(out, e.Name())
		}
	}
	return out
}
