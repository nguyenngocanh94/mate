package spawn_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// A crew is launched with the harness, model and effort the Mate chose, and
// the record says which: `model=` and `effort=` beside `harness=`.
func TestSpawnCrewLaunchesAndRecordsTheProfile(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: codex.KindCodex, Model: "gpt-5.5", Effort: harness.EffortHigh,
		BriefFile: briefFile(t, w, brieftest.Ship("Add a healthcheck endpoint.\n")),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if len(rt.StartArgv) != 1 {
		t.Fatalf("started %d agents, want 1", len(rt.StartArgv))
	}
	argv := rt.StartArgv[0]
	if i := slices.Index(argv, "-m"); i < 0 || argv[i+1] != "gpt-5.5" {
		t.Fatalf("argv has no -m gpt-5.5: %q", argv)
	}
	if !slices.Contains(argv, `model_reasoning_effort="high"`) {
		t.Fatalf("argv has no reasoning effort: %q", argv)
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaModel] != "gpt-5.5" || meta[spawn.MetaEffort] != "high" {
		t.Fatalf("meta model/effort = %q/%q", meta[spawn.MetaModel], meta[spawn.MetaEffort])
	}
	if res.Model != "gpt-5.5" || res.Effort != harness.EffortHigh || res.EffortOmitted {
		t.Fatalf("result = %+v", res)
	}
}

// An effort the harness does not take is recorded - the Mate asked for it -
// and not passed, and the result says it was left out (firstmate's
// record-and-omit contract).
func TestSpawnCrewRecordsButOmitsAnEffortTheHarnessDoesNotTake(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: codex.KindCodex, Effort: harness.EffortMax,
		BriefFile: briefFile(t, w, brieftest.Ship("Add a healthcheck endpoint.\n")),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if strings.Contains(strings.Join(rt.StartArgv[0], " "), "model_reasoning_effort") {
		t.Fatalf("codex was passed an effort it does not take: %q", rt.StartArgv[0])
	}
	meta, _ := w.ReadCrewMeta("shop", "k3")
	if meta[spawn.MetaEffort] != "max" || !res.EffortOmitted {
		t.Fatalf("effort meta = %q, omitted = %v; want max recorded and omitted", meta[spawn.MetaEffort], res.EffortOmitted)
	}
}

// No model and no effort is the harness's default, and the record carries
// no empty keys for them.
func TestSpawnCrewWithoutAProfileRecordsNone(t *testing.T) {
	w := crewWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	if _, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefFile: briefFile(t, w, brieftest.Ship("Add a healthcheck endpoint.\n")),
	}); err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	meta, _ := w.ReadCrewMeta("shop", "k3")
	if _, ok := meta[spawn.MetaModel]; ok {
		t.Fatalf("meta carries model= with no model chosen: %v", meta)
	}
	if _, ok := meta[spawn.MetaEffort]; ok {
		t.Fatalf("meta carries effort= with no effort chosen: %v", meta)
	}
}
