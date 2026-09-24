package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/brief/brieftest"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

func writeTemp(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBriefCheckAcceptsAGoodBrief(t *testing.T) {
	path := writeTemp(t, brieftest.Ship("work"))
	var out, errw bytes.Buffer
	if code := mainRun([]string{"brief", "check", path}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errw.String())
	}
	if !strings.HasPrefix(out.String(), "brief ok: "+path+" has every section of a ship brief") {
		t.Fatalf("stdout = %q", out.String())
	}
}

// TestBriefCheckRefusesTheOldBuyesp32Brief runs the CLI on the brief the
// Mate actually wrote, and on the report's rewrite of it.
func TestBriefCheckRefusesTheOldBuyesp32Brief(t *testing.T) {
	var out, errw bytes.Buffer
	code := mainRun([]string{"brief", "check", "../../internal/brief/testdata/buyesp32-old-task.md"}, &out, &errw)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	lines := strings.Split(strings.TrimRight(errw.String(), "\n"), "\n")
	want := []string{
		`# Task: text before the first section belongs in one ("Implement the requested ESP32 landing-page purchase flow....")`,
		"## Captain's words: missing; every brief has one",
		"## What we already know: missing; every brief has one",
		"## Build: missing; every brief has one",
		"## Acceptance: missing; every brief has one",
		"## Open decisions: missing; every brief has one",
		"matev2: brief check: 6 shape problem(s) in ../../internal/brief/testdata/buyesp32-old-task.md as a ship brief",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stderr:\n%s\nwant:\n%s", errw.String(), strings.Join(want, "\n"))
	}

	out.Reset()
	errw.Reset()
	if code := mainRun([]string{"brief", "check", "../../internal/brief/testdata/buyesp32-rewrite-task.md"}, &out, &errw); code != 0 {
		t.Fatalf("the report's rewrite: exit %d\n%s", code, errw.String())
	}
}

func TestBriefCheckScoutFlag(t *testing.T) {
	path := writeTemp(t, brieftest.Ship("work"))
	var out, errw bytes.Buffer
	if code := mainRun([]string{"brief", "check", "--scout", path}, &out, &errw); code != 1 {
		t.Fatalf("a ship brief checked as a scout: exit %d", code)
	}
	if !strings.Contains(errw.String(), "## Deliverable: missing") {
		t.Fatalf("stderr = %q", errw.String())
	}
	scout := writeTemp(t, brieftest.Scout("work", "what?"))
	out.Reset()
	errw.Reset()
	if code := mainRun([]string{"brief", "check", scout, "--scout"}, &out, &errw); code != 0 {
		t.Fatalf("scout brief: exit %d\n%s", code, errw.String())
	}
}

func TestBriefCheckUsage(t *testing.T) {
	var out, errw bytes.Buffer
	if code := mainRun([]string{"brief", "check"}, &out, &errw); code != 2 {
		t.Fatalf("no file: exit %d, want 2", code)
	}
}

// TestBriefAppendAppendsAndTellsTheCrew: the words land at the end of
// ## Captain's words in the app's own copy of the brief, and the crew gets
// one verified line naming that file.
func TestBriefAppendAppendsAndTellsTheCrew(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}
	rt.SetReadOutput(handle, codexEmptyScreen)
	var typed []string
	rt.OnSendText = func(h runtime.AgentHandle, text string) {
		typed = append(typed, text)
		rt.SetReadOutput(h, codexPendingScreen(text))
	}
	rt.OnSendKeys = func(h runtime.AgentHandle, keys []string) { rt.SetReadOutput(h, codexBusyScreen) }

	at := time.Date(2026, 9, 24, 9, 30, 0, 0, time.UTC)
	got, err := briefAppend(context.Background(), w, deps, "shop", "k3", "Cả trang liên hệ nữa.\n", store.SourceMate, at)
	if err != nil {
		t.Fatalf("briefAppend: %v", err)
	}
	if !got.Appended || !got.Report.Delivered() {
		t.Fatalf("result = %+v", got)
	}
	data, err := os.ReadFile(w.CrewBrief("shop", "k3"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "do the thing\n\n(added 2026-09-24 09:30 UTC)\nCả trang liên hệ nữa.\n\n## What we already know") {
		t.Fatalf("words not appended under ## Captain's words:\n%s", data)
	}
	if len(typed) != 1 || typed[0] != got.Line || !strings.Contains(got.Line, w.CrewBrief("shop", "k3")) || strings.Contains(got.Line, "\n") {
		t.Fatalf("crew was sent %q, want the one pointer line %q", typed, got.Line)
	}
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Target != store.CrewTarget("k3") || entries[0].Text != got.Line {
		t.Fatalf("sent.log = %+v", entries)
	}
}

// TestBriefAppendRefusesAStoppedCrewWithoutChangingTheBrief: there is
// nobody to tell, so the brief must not start disagreeing with the work.
func TestBriefAppendRefusesAStoppedCrewWithoutChangingTheBrief(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	spawnFakeCrew(t, w, deps, "shop", "k3")
	if _, err := stopFakeCrew(t, w, deps, "shop", "k3"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	before, err := os.ReadFile(w.CrewBrief("shop", "k3"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := briefAppend(context.Background(), w, deps, "shop", "k3", "more", store.SourceMate, time.Now())
	if err == nil || got.Appended {
		t.Fatalf("appended to a stopped crew: %+v, %v", got, err)
	}
	after, _ := os.ReadFile(w.CrewBrief("shop", "k3"))
	if !bytes.Equal(before, after) {
		t.Fatal("the brief changed although the crew could not be told")
	}
}

// TestBriefAppendKeepsTheWordsWhenTheSendIsRefused: a busy crew refuses the
// line; the words stay appended and the result carries the exact line to
// send by hand, so a retry never appends twice.
func TestBriefAppendKeepsTheWordsWhenTheSendIsRefused(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	rt.SetReadOutput(runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}, codexBusyScreen)

	got, err := briefAppend(context.Background(), w, deps, "shop", "k3", "more", store.SourceMate, time.Now())
	if err == nil {
		t.Fatal("a busy crew accepted the line")
	}
	if !got.Appended || got.Line == "" || got.Report.Delivered() {
		t.Fatalf("result = %+v", got)
	}
}

func TestBriefAppendUsage(t *testing.T) {
	var out, errw bytes.Buffer
	for _, args := range [][]string{{"brief", "append", "shop"}, {"brief", "append", "shop", "k3", "words"}} {
		if code := mainRun(args, &out, &errw); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestProjectFactsCLI(t *testing.T) {
	ws := t.TempDir()
	empty := filepath.Join(ws, "shop")
	full := filepath.Join(ws, "blog")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, empty, "init", "-b", "main")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, full)
	if err := os.WriteFile(filepath.Join(full, "go.mod"), []byte("module secret-module-name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, full, "add", "go.mod")
	runGitOrFatal(t, full, "commit", "-m", "mod")

	var out, errw bytes.Buffer
	if code := mainRun([]string{"init", ws}, &out, &errw); code != 0 {
		t.Fatalf("init: %s", errw.String())
	}
	for _, name := range []string{"shop", "blog"} {
		if code := mainRun([]string{"project", "add", "--workspace", ws, name, filepath.Join(ws, name)}, &out, &errw); code != 0 {
			t.Fatalf("add %s: %s", name, errw.String())
		}
	}
	out.Reset()
	if code := mainRun([]string{"project", "facts", "shop", "--workspace", ws}, &out, &errw); code != 0 {
		t.Fatalf("facts shop: %s", errw.String())
	}
	if !strings.Contains(out.String(), "commits: 0 (main has no commit yet)\ntree: empty\n") {
		t.Fatalf("empty repo facts:\n%s", out.String())
	}
	out.Reset()
	if code := mainRun([]string{"project", "facts", "blog", "--workspace", ws}, &out, &errw); code != 0 {
		t.Fatalf("facts blog: %s", errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "commits: 2 on main\ntree: 1 file(s)\ntop level: go.mod\nbuild/test files: go.mod\n") {
		t.Fatalf("populated repo facts:\n%s", got)
	}
	if strings.Contains(got, "secret-module-name") {
		t.Fatalf("facts printed a file's content:\n%s", got)
	}
}
