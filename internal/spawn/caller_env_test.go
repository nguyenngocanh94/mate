package spawn_test

import (
	"context"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/brief/brieftest"
	"github.com/nguyenngocanh94/matev2/internal/config"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
)

// MATEV2_CALLER is how `matev2 merge` tells a Mate, a Crew and the captain
// apart (docs/mvp.md M4 decisions), and a pane is the only place it can come
// from: Herdr applies `--env` when a pane is made and never afterwards, so a
// variable missing here cannot be recovered later. A Mate whose pane lost it
// would look like the captain and merge while `yolo` is off, which is the
// one thing the flag exists to prevent - hence a test on the injection
// itself rather than only on MergeCrew's reaction to the value.

func paneEnv(t *testing.T, rt *runtime.Fake, pane string) map[string]string {
	t.Helper()
	tab, ok := rt.Tabs[pane]
	if !ok {
		t.Fatalf("the fake runtime has no pane %s", pane)
	}
	out := map[string]string{}
	for _, v := range tab.Env {
		out[v.Key] = v.Value
	}
	return out
}

func TestStartMateInjectsTheMateCallerIntoItsPane(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{
		Project: "shop", Harness: harness.KindClaude,
	})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	env := paneEnv(t, rt, res.Pane)
	if got := env[config.EnvCaller]; got != spawn.CallerMate {
		t.Fatalf("the Mate's pane carries %s=%q, want %q; a Mate whose pane lost it merges as the captain",
			config.EnvCaller, got, spawn.CallerMate)
	}
}

func TestSpawnCrewInjectsTheCrewCallerIntoItsPane(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	env := paneEnv(t, rt, res.Pane)
	if got := env[config.EnvCaller]; got != spawn.CallerCrew {
		t.Fatalf("the crew's pane carries %s=%q, want %q", config.EnvCaller, got, spawn.CallerCrew)
	}
	// The status path is injected the same way and through the same
	// allowlist; if one is present and the other is not, the allowlist has
	// silently dropped a key rather than refusing it.
	if env[config.EnvStatusFile] == "" {
		t.Fatalf("the crew's pane lost %s: %v", config.EnvStatusFile, env)
	}
}
