package spawn_test

import (
	"context"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// MATE_CALLER is how `mate merge` tells a Mate, a Crew and the captain
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

// The Mate's identity and CODEX_HOME ride on its launch, which the runtime
// exports into the pane before every start: a Mate restarted while a Crew
// holds the project's workspace is a new `tab create` that inherits nothing
// from the workspace create, and it must still record its sends as the
// Mate's, merge under the Mate's rules and launch Crews in the same
// CODEX_HOME. Both harnesses: a Claude Mate's `crew spawn` launches Codex
// Crews too.
func TestStartMateCarriesItsEnvironmentOnEveryLaunch(t *testing.T) {
	for _, kind := range []harness.Kind{harness.KindClaude, harness.KindCodex} {
		t.Run(string(kind), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(harness.CodexHomeEnv, home)
			w := newWorkspace(t, "shop")
			rt := runtime.NewFake()
			deps := fakeDeps(t, rt)
			res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: kind})
			if err != nil {
				t.Fatalf("StartMate: %v", err)
			}
			env := map[string]string{}
			for _, v := range rt.StartEnv[res.Pane] {
				env[v.Key] = v.Value
			}
			for key, want := range map[string]string{
				config.EnvCaller:     spawn.CallerMate,
				config.EnvAgentRole:  string(harness.RoleMate),
				config.EnvProjectID:  "shop",
				harness.CodexHomeEnv: home,
			} {
				if env[key] != want {
					t.Fatalf("the %s Mate's launch exports %s=%q, want %q (all: %v)", kind, key, env[key], want, env)
				}
			}
			if got := paneEnv(t, rt, res.Pane)[harness.CodexHomeEnv]; got != home {
				t.Fatalf("the workspace create gave the Mate pane CODEX_HOME=%q, want %q", got, home)
			}
		})
	}
}

// The outbox finds a Codex Mate's rollout under the same mate.meta key the
// SessionStart hook writes; it names the key itself to avoid an import cycle.
func TestOutboxReadsTheTranscriptKeySpawnWrites(t *testing.T) {
	if outbox.MateMetaTranscript != spawn.MetaTranscript {
		t.Fatalf("outbox reads mate.meta %q, spawn writes %q", outbox.MateMetaTranscript, spawn.MetaTranscript)
	}
}

// In a live test run the Mate is refused before anything is created when
// CODEX_HOME is the operator's own: its pane would hand that home to every
// Crew the Mate spawns.
func TestStartMateRefusesTheOperatorsCodexHomeInALiveRun(t *testing.T) {
	t.Setenv(config.EnvLive, "1")
	t.Setenv(harness.CodexHomeEnv, "")
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
	if err == nil {
		t.Fatal("a live run started a Mate whose pane carries the operator's CODEX_HOME")
	}
	if len(rt.StartArgv) != 0 || len(rt.Tabs) != 0 {
		t.Fatalf("the refusal came after the launch: starts %v, tabs %v", rt.StartArgv, rt.Tabs)
	}
}
