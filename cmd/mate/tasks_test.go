package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// fakeBD puts a bd on PATH that records exactly what crosses the process
// boundary. No test here runs the real bd.
func fakeBD(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
init) mkdir -p "$BEADS_DIR"; printf '{"backend":"dolt"}' > "$BEADS_DIR/metadata.json" ;;
export) printf '{"id":"shop-abc"}\n' ;;
create) printf '%s\n' "$BEADS_DIR" "$PWD" "$@" > "$BEADS_DIR/argv"; printf '{"id":"shop-abc"}\n' ;;
list) printf '[]\n' ;;
ready) printf '[]\n' ;;
*) exit 17 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestToolCLISeparatesMateAndUpstreamFlags(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	fakeBD(t)
	home := w.ProjectHome("shop")
	dir := filepath.Join(home, ".beads")
	for _, alias := range [][]string{{"tool", "beads"}, {"beads"}} {
		var out, stderr bytes.Buffer
		args := append(append([]string{}, alias...), "shop", "--workspace", w.Root(), "--", "create", "--title", "Việt Nam có dấu", "--description", "one\ntwo", "--json")
		if err := run(args, &out, &stderr); err != nil {
			t.Fatalf("%v: %v: %s", alias, err, &stderr)
		}
		var issue map[string]string
		if err := json.Unmarshal(out.Bytes(), &issue); err != nil || issue["id"] != "shop-abc" {
			t.Fatalf("%v: JSON polluted: %s %v", alias, &out, err)
		}
		argv, err := os.ReadFile(filepath.Join(dir, "argv"))
		if err != nil || !strings.Contains(string(argv), "Việt Nam có dấu\n--description\none\ntwo\n--json") || !strings.HasPrefix(string(argv), dir+"\n"+home+"\n") {
			t.Fatalf("%v: arguments: %s %v", alias, argv, err)
		}
		wantStderr := ""
		if alias[0] == "beads" {
			wantStderr = "note: mate beads is now mate tool beads\n"
		}
		if stderr.String() != wantStderr {
			t.Fatalf("%v: stderr = %q, want %q", alias, &stderr, wantStderr)
		}
	}
	if _, err := os.Stat(filepath.Join(w.ProjectDir("shop"), ".beads")); !os.IsNotExist(err) {
		t.Fatalf("the tracker was made under .mate: %v", err)
	}
	if _, err := os.Stat(w.ToolLockFile("shop", "beads")); err != nil {
		t.Fatalf("no tool lock under .mate: %v", err)
	}

	var out, stderr bytes.Buffer
	for _, args := range [][]string{
		{"tasks", "shop", "--json", "--workspace", w.Root()},
		{"tasks", "shop", "--list", "--workspace", w.Root()},
		{"tasks", "shop", "--init", "--workspace", w.Root()},
	} {
		out.Reset()
		stderr.Reset()
		if err := run(args, &out, &stderr); err != nil {
			t.Fatalf("%v: %v %s", args, err, &stderr)
		}
	}
	if out.String() != dir+"\n" {
		t.Fatalf("tasks --init printed %q, want the tracker %s", &out, dir)
	}
	for _, args := range [][]string{
		{"beads", "shop", "--workspace", w.Root(), "create"},
		{"beads", "shop", "--workspace", w.Root(), "--"},
		{"beads", "missing", "--workspace", w.Root(), "--", "list"},
		{"tool", "shop", "--workspace", w.Root(), "--", "list"},
		{"tool", "nope", "shop", "--workspace", w.Root(), "--", "list"},
		{"epic", "add", "shop"}, {"task", "add", "shop"},
	} {
		if err := run(args, &out, &stderr); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	err := run([]string{"tool", "fresh", "shop", "--workspace", w.Root(), "--", "x"}, &out, &stderr)
	if err == nil || err.Error() != "Fresh has no command: Fresh is an editor; it owns no data of mate's" {
		t.Fatalf("a tool with no command = %v", err)
	}
}

// A tracker mate kept under .mate before layout 2 is never moved and never
// hidden behind a new empty one: the commands say where it is and where it
// goes, until the captain moves it or starts empty with --init.
func TestOldTrackerIsRefusedNotMoved(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	fakeBD(t)
	old := filepath.Join(w.ProjectDir("shop"), ".beads")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := "a Beads tracker from before layout 2 sits at " + old + "; move it to " +
		filepath.Join(w.ProjectHome("shop"), ".beads") + " by hand (mv), or run mate tasks shop --init to start empty"
	var out, stderr bytes.Buffer
	for _, args := range [][]string{
		{"tool", "beads", "shop", "--workspace", w.Root(), "--", "list"},
		{"beads", "shop", "--workspace", w.Root(), "--", "list"},
		{"tasks", "shop", "--list", "--workspace", w.Root()},
		{"task-triage", "shop", "--workspace", w.Root()},
	} {
		if err := run(args, &out, &stderr); err == nil || err.Error() != want {
			t.Fatalf("%v = %v, want %q", args, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(w.ProjectHome("shop"), ".beads")); !os.IsNotExist(err) {
		t.Fatalf("a refused command made a tracker: %v", err)
	}
	if err := run([]string{"tasks", "shop", "--init", "--workspace", w.Root()}, &out, &stderr); err != nil {
		t.Fatalf("tasks --init beside an old tracker: %v %s", err, &stderr)
	}
	if data, err := os.ReadFile(filepath.Join(old, "metadata.json")); err != nil || string(data) != `{"backend":"dolt"}` {
		t.Fatalf("the old tracker changed: %q %v", data, err)
	}
	if err := run([]string{"tool", "beads", "shop", "--workspace", w.Root(), "--", "list"}, &out, &stderr); err != nil {
		t.Fatalf("once the new tracker exists the old one is no obstacle: %v", err)
	}
}

// mate's own task plan from before Beads is never hidden behind an empty
// tracker, even by --init.
func TestLegacyTaskPlanIsRefused(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	fakeBD(t)
	legacy := filepath.Join(w.ProjectDir("shop"), "tasks.yaml")
	if err := os.WriteFile(legacy, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	for _, args := range [][]string{
		{"tasks", "shop", "--init", "--workspace", w.Root()},
		{"tool", "beads", "shop", "--workspace", w.Root(), "--", "list"},
	} {
		err := run(args, &out, &stderr)
		if err == nil || err.Error() != "legacy plan exists at "+legacy+"; import its tasks into Beads first (docs/beads.md)" {
			t.Fatalf("%v = %v", args, err)
		}
	}
	if data, _ := os.ReadFile(legacy); string(data) != "keep me" {
		t.Fatal("legacy data changed")
	}
}

// On the old layout the project directory may be one of its repos: no
// tracker is made there.
func TestToolCommandsRefuseTheOldLayout(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	fakeBD(t)
	raw, err := os.ReadFile(w.WorkspaceFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.WorkspaceFile(), []byte(strings.Replace(string(raw), "layout: 2\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	for _, args := range [][]string{
		{"tool", "beads", "shop", "--workspace", w.Root(), "--", "list"},
		{"tasks", "shop", "--init", "--workspace", w.Root()},
	} {
		if err := run(args, &out, &stderr); !errors.Is(err, store.ErrLayoutOld) {
			t.Fatalf("%v on the old layout = %v", args, err)
		}
	}
}

// t on a project opens Beads Viewer on the project's tracker in its own
// tab, and the second t reuses it.
func TestConsoleTasksOpensAndReusesIndependentTab(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	rec := newRecordingColumns(t)
	tab := rec.tabs[roleTasks]
	live := tab.socket
	absent := filepath.Join(rec.dir, "absent.sock")
	tab.socket = absent
	rec.tabs[roleTasks] = tab
	opened := 0
	rec.h = reviewHost{layout: func([]host.Column) { t.Fatal("tasks changed stage layout") }, open: func(col host.Column) {
		opened++
		if col.Role != roleTasks {
			t.Fatalf("opened role %s", col.Role)
		}
		if err := os.Symlink(live, absent); err != nil {
			t.Fatal(err)
		}
	}}
	fn := consoleToolView(w, rec.consoleColumns, tools)
	for i := 0; i < 2; i++ {
		if err := fn(context.Background(), "t", console.StageTarget{ProjectID: "shop"}); err != nil {
			t.Fatal(err)
		}
	}
	shown := rec.of(roleTasks)
	if opened != 1 || len(shown) != 2 || len(rec.of(roleStage)) != 0 || len(rec.of(roleReview)) != 0 {
		t.Fatalf("opened=%d tasks=%d", opened, len(shown))
	}
	home := w.ProjectHome("shop")
	if !slices.Equal(shown[0].Argv, []string{"/opt/bv", "--db", filepath.Join(home, ".beads")}) || shown[0].Dir != home {
		t.Fatalf("argv: %+v", shown[0])
	}
}
