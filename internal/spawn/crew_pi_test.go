package spawn_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/pi"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// A pi Crew goes through the same spawn and stop as every harness: the
// brief reaches pi by path, the session is named at launch where pi keeps
// it, nothing is written into the worktree, and a stop empties the
// composer before it quits (docs/plans/harness-registry-2026-09-30.md,
// PR 7).
func TestSpawnCrewOnPi(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(pi.AgentDirEnv, agentDir)
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
		Harness: pi.KindPi, Model: "deepseek/deepseek-flash", Effort: harness.EffortHigh,
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if res.Harness != pi.KindPi || !res.BriefDelivered {
		t.Fatalf("spawn = %+v", res)
	}
	if len(rt.StartLaunches) != 1 {
		t.Fatalf("%d launches", len(rt.StartLaunches))
	}
	args := rt.StartLaunches[0].Args()
	value := func(flag string) string {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) {
			return ""
		}
		return args[i+1]
	}
	if value("--append-system-prompt") != res.BriefPath {
		t.Errorf("args %q do not hand pi the brief %s", args, res.BriefPath)
	}
	if res.SessionID == "" || value("--session-id") != res.SessionID {
		t.Errorf("args %q do not name the session %q", args, res.SessionID)
	}
	if want := pi.SessionDir(filepath.Join(agentDir, "sessions"), res.Worktree); value("--session-dir") != want {
		t.Errorf("session dir = %q, want %q", value("--session-dir"), want)
	}
	if value("--thinking") != "high" || value("--model") != "deepseek/deepseek-flash" {
		t.Errorf("args %q do not carry the launch profile", args)
	}
	if status := git(t, res.Worktree, "status", "--porcelain", "--ignored"); strings.TrimSpace(status) != "" {
		t.Errorf("a pi Crew's worktree holds files of mate's:\n%s", status)
	}

	if _, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if len(rt.ExitClearKeys) != 1 || !slices.Equal(rt.ExitClearKeys[0], []string{"ctrl+u"}) || !slices.Equal(rt.ExitPrompts, []string{"/quit"}) {
		t.Fatalf("stop pressed %q then typed %q, want ctrl+u then /quit", rt.ExitClearKeys, rt.ExitPrompts)
	}
}

// A Mate on pi is refused before anything is created, naming Hooks.
func TestStartMateOnPiIsRefused(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	_, err := spawn.StartMate(context.Background(), w, fakeDeps(t, rt), spawn.StartRequest{Project: "shop", Harness: pi.KindPi})
	if err == nil || !strings.Contains(err.Error(), "Hooks") {
		t.Fatalf("StartMate on pi = %v, want a refusal naming Hooks", err)
	}
	if len(rt.StartLaunches) != 0 {
		t.Fatal("a refused Mate was launched")
	}
}
