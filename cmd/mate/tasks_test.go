package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/host"
)

func TestBeadsCLISeparatesMateAndUpstreamFlags(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	bin := t.TempDir()
	// This executable records exactly what crosses the process boundary.
	script := `#!/bin/sh
case "$1" in
init) mkdir -p "$BEADS_DIR"; printf '{"backend":"dolt"}' > "$BEADS_DIR/metadata.json" ;;
export) printf '{"id":"shop-abc"}\n' ;;
create) printf '%s\n' "$BEADS_DIR" "$@" > "$BEADS_DIR/argv"; printf '{"id":"shop-abc"}\n' ;;
list) printf '[]\n' ;;
ready) printf '[]\n' ;;
*) exit 17 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "bd"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, stderr bytes.Buffer
	err := run([]string{"beads", "shop", "--workspace", w.Root(), "--", "create", "--title", "Việt Nam có dấu", "--description", "one\ntwo", "--json"}, &out, &stderr)
	if err != nil {
		t.Fatalf("%v: %s", err, &stderr)
	}
	var issue map[string]string
	if err := json.Unmarshal(out.Bytes(), &issue); err != nil || issue["id"] != "shop-abc" {
		t.Fatalf("JSON polluted: %s %v", &out, err)
	}
	dir, _ := w.BeadsDir("shop")
	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	if err != nil || !strings.Contains(string(argv), "Việt Nam có dấu\n--description\none\ntwo\n--json") || !strings.HasPrefix(string(argv), dir+"\n") {
		t.Fatalf("arguments: %s %v", argv, err)
	}
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
	for _, args := range [][]string{
		{"beads", "shop", "--workspace", w.Root(), "create"},
		{"beads", "shop", "--workspace", w.Root(), "--"},
		{"beads", "missing", "--workspace", w.Root(), "--", "list"},
		{"epic", "add", "shop"}, {"task", "add", "shop"},
	} {
		if err := run(args, &out, &stderr); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestConsoleTasksOpensAndReusesIndependentTab(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	rec := newRecordingColumns(t)
	live := rec.tasks
	absent := filepath.Join(rec.dir, "absent.sock")
	rec.tasks = absent
	rec.editor, rec.review = "", "" // No Fresh installation is needed.
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
	fn := consoleTasks(w, rec.consoleColumns)
	for i := 0; i < 2; i++ {
		if err := fn(context.Background(), "shop"); err != nil {
			t.Fatal(err)
		}
	}
	shown := rec.of(roleTasks)
	if opened != 1 || len(shown) != 2 || len(rec.of(roleStage)) != 0 || len(rec.of(roleReview)) != 0 {
		t.Fatalf("opened=%d tasks=%d", opened, len(shown))
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(shown[0].Argv, []string{exe, "tasks", "shop", "--workspace", w.Root()}) || shown[0].Dir != w.Root() {
		t.Fatalf("argv: %+v", shown[0])
	}
	if err := fn(context.Background(), "missing"); err == nil || len(rec.of(roleTasks)) != 2 {
		t.Fatal("opened nonexistent project")
	}
}
