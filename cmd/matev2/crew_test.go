package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

func TestRunUnknownCrewSubcommandIsUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"crew", "bogus"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestCrewSpawnRequiresProjectIDAndBrief(t *testing.T) {
	cases := [][]string{
		{"crew", "spawn"},
		{"crew", "spawn", "shop"},
		{"crew", "spawn", "shop", "k3"}, // no --brief
		{"crew", "spawn", "shop", "k3", "--brief", "b", "--harness", "pi"}, // unknown harness
	}
	for _, args := range cases {
		var out, errw bytes.Buffer
		err := run(args, &out, &errw)
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Fatalf("%v: err = %v, want *usageError", args, err)
		}
	}
}

func TestCrewListPrintsTheRecordedCrews(t *testing.T) {
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repo: repo}); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if err := run([]string{"crew", "list", "shop", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew list: %v", err)
	}
	if !strings.Contains(out.String(), "no crews recorded") {
		t.Fatalf("out = %q, want the empty-list line", out.String())
	}

	if err := w.WriteCrewMeta("shop", "k3", map[string]string{
		"harness": "codex",
		"branch":  "matev2/k3",
		"pane":    "w1:p2",
		"task":    "Add a healthcheck endpoint.",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "done: ready in branch matev2/k3"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"crew", "list", "shop", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew list: %v", err)
	}
	for _, want := range []string{"k3", "codex", "matev2/k3", "done: ready in branch matev2/k3", "w1:p2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want it to carry %q", out.String(), want)
		}
	}
}

func TestCrewListStatusColumnShowsTeardown(t *testing.T) {
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repo: repo}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		crew string
		meta map[string]string
		want string
	}{
		{"k1", map[string]string{"teardown": "refused_unlanded", "stopped_at": "2026-09-17T10:00:00Z"}, "stopped (unlanded)"},
		{"k2", map[string]string{"teardown": "clean", "stopped_at": "2026-09-17T10:00:00Z"}, "torn down"},
		{"k3", map[string]string{"teardown": "discarded", "stopped_at": "2026-09-17T10:00:00Z"}, "torn down"},
		{"k4", map[string]string{"stopped_at": "2026-09-17T10:00:00Z"}, "stopped"},
	}
	for _, tc := range cases {
		if err := w.WriteCrewMeta("shop", tc.crew, tc.meta); err != nil {
			t.Fatal(err)
		}
	}

	var out, errw bytes.Buffer
	if err := run([]string{"crew", "list", "shop", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew list: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	rows := map[string]string{}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		rows[fields[0]] = line
	}
	for _, tc := range cases {
		line, ok := rows[tc.crew]
		if !ok {
			t.Fatalf("out = %q, missing row for %s", out.String(), tc.crew)
		}
		if !strings.Contains(line, tc.want) {
			t.Errorf("row for %s = %q, want it to carry %q", tc.crew, line, tc.want)
		}
	}
}

func TestCrewStopReportLineDescribesEachOutcome(t *testing.T) {
	cases := []struct {
		name string
		res  spawn.StopResult
		want []string
	}{
		{
			name: "refused",
			res: spawn.StopResult{
				Agent: "crew-k3", TabClosed: true,
				Teardown: spawn.TeardownRefusedUnlanded, Branch: "matev2/k3", Ahead: 2, DirtyFiles: 1,
			},
			want: []string{"KEPT", "2 commit(s) ahead", "1 dirty file(s)", "--discard"},
		},
		{
			name: "clean",
			res: spawn.StopResult{
				Agent: "crew-k3", TabClosed: true,
				Teardown: spawn.TeardownClean, Branch: "matev2/k3",
			},
			want: []string{"removed", "already landed"},
		},
		{
			name: "discarded",
			res: spawn.StopResult{
				Agent: "crew-k3", TabClosed: true,
				Teardown: spawn.TeardownDiscarded, Ahead: 3, DirtyFiles: 2,
			},
			want: []string{"removed", "--discard", "3 commit(s)", "2 dirty file(s)"},
		},
		{
			name: "already gone",
			res: spawn.StopResult{
				Agent: "crew-k3", AlreadyGone: true, TabClosed: true,
			},
			want: []string{"already gone", "kept"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := crewStopReport("shop", "k3", tc.res)
			for _, want := range tc.want {
				if !strings.Contains(line, want) {
					t.Errorf("line = %q, want it to carry %q", line, want)
				}
			}
		})
	}
}

func TestCrewStopRequiresProjectAndID(t *testing.T) {
	cases := [][]string{
		{"crew", "stop"},
		{"crew", "stop", "shop"},
	}
	for _, args := range cases {
		var out, errw bytes.Buffer
		err := run(args, &out, &errw)
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Fatalf("%v: err = %v, want *usageError", args, err)
		}
	}
}
