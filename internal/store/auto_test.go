package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

func autoCursorWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	return w
}

// A project the daemon has never digested has no cursor, and that is not a
// failure: the first tick must read an empty cursor and treat everything it
// finds as new.
func TestAutoCursorMissingFileIsEmpty(t *testing.T) {
	w := autoCursorWorkspace(t)
	got, err := w.ReadAutoCursor("shop")
	if err != nil {
		t.Fatalf("ReadAutoCursor: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("cursor = %v, want empty", got)
	}
}

func TestAutoCursorRoundTripsAbsolutePaths(t *testing.T) {
	w := autoCursorWorkspace(t)
	want := map[string]int64{
		w.CrewStatus("shop", "k3"): 128,
		w.CrewStatus("shop", "k9"): 0,
		w.IncidentsLog("shop"):     4096,
	}
	if err := w.WriteAutoCursor("shop", want); err != nil {
		t.Fatalf("WriteAutoCursor: %v", err)
	}
	got, err := w.ReadAutoCursor("shop")
	if err != nil {
		t.Fatalf("ReadAutoCursor: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("cursor = %v, want %v", got, want)
	}
	for path, offset := range want {
		if got[path] != offset {
			t.Fatalf("cursor[%s] = %d, want %d", path, got[path], offset)
		}
	}
}

// The file is the project's own relative spelling, so it is readable by a
// human debugging a daemon that re-sent something, and it does not break when
// a workspace is moved.
func TestAutoCursorIsRelativeAndSortedOnDisk(t *testing.T) {
	w := autoCursorWorkspace(t)
	if err := w.WriteAutoCursor("shop", map[string]int64{
		w.IncidentsLog("shop"):     7,
		w.CrewStatus("shop", "k3"): 42,
	}); err != nil {
		t.Fatalf("WriteAutoCursor: %v", err)
	}
	data, err := os.ReadFile(w.AutoCursorFile("shop"))
	if err != nil {
		t.Fatalf("read cursor file: %v", err)
	}
	want := "crews/k3.status=42\nincidents.log=7\n"
	if string(data) != want {
		t.Fatalf("cursor file =\n%q\nwant\n%q", data, want)
	}
	if base := filepath.Base(w.AutoCursorFile("shop")); base != ".auto-cursor" {
		t.Fatalf("cursor file is %s, want .auto-cursor beside .auto", base)
	}
}

// The cursor must never be able to name a file outside the project it
// belongs to - not on the way in and not on the way out.
func TestAutoCursorRefusesPathsOutsideTheProject(t *testing.T) {
	w := autoCursorWorkspace(t)
	for _, path := range []string{
		w.SentLog("other"),
		filepath.Join(w.Root(), "escape.log"),
		"crews/k3.status",
	} {
		if err := w.WriteAutoCursor("shop", map[string]int64{path: 1}); err == nil {
			t.Fatalf("WriteAutoCursor accepted %q, want a refusal", path)
		}
	}
}

func TestAutoCursorSkipsLinesItCannotRead(t *testing.T) {
	w := autoCursorWorkspace(t)
	if err := os.MkdirAll(w.MateDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		"crews/k3.status=42",
		"no-equals-sign",
		"crews/k9.status=not-a-number",
		"crews/k7.status=-5",
		"../../escape=9",
		"incidents.log=7",
		"",
	}, "\n")
	if err := os.WriteFile(w.AutoCursorFile("shop"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := w.ReadAutoCursor("shop")
	if err != nil {
		t.Fatalf("ReadAutoCursor: %v", err)
	}
	want := map[string]int64{
		w.CrewStatus("shop", "k3"): 42,
		w.IncidentsLog("shop"):     7,
	}
	if len(got) != len(want) {
		t.Fatalf("cursor = %v, want %v - one bad line must not cost the whole cursor", got, want)
	}
	for path, offset := range want {
		if got[path] != offset {
			t.Fatalf("cursor[%s] = %d, want %d", path, got[path], offset)
		}
	}
}

func TestAutoCursorRefusesAnInvalidProjectName(t *testing.T) {
	w := autoCursorWorkspace(t)
	if _, err := w.ReadAutoCursor("Not-A-Name"); err == nil {
		t.Fatal("ReadAutoCursor accepted an invalid project name")
	}
	if err := w.WriteAutoCursor("Not-A-Name", nil); err == nil {
		t.Fatal("WriteAutoCursor accepted an invalid project name")
	}
}
