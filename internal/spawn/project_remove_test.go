package spawn_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// runningProject is a project with a live Mate and two live crews.
func runningProject(t *testing.T) (*store.Workspace, *runtime.Fake, spawn.Deps) {
	t.Helper()
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	for _, id := range []string{"k1", "k2"} {
		if _, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
			Project: "shop", Crew: id, BriefText: brieftest.Ship("work"),
		}); err != nil {
			t.Fatalf("SpawnCrew %s: %v", id, err)
		}
	}
	return w, rt, deps
}

func TestRemoveProjectStopsEveryAgentBeforeUnregistering(t *testing.T) {
	w, rt, deps := runningProject(t)
	var events []string
	res, err := spawn.RemoveProject(context.Background(), w, deps, "shop", spawn.RemoveProjectOptions{
		Stow: func(_ context.Context, project string) error {
			events = append(events, "stow "+project+" with "+itoa(rt.LiveAgentCount())+" agents up")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	if !res.Removed || len(res.Stopped) != 3 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want two crews and the Mate stopped and the project removed", res)
	}
	if res.Stopped[2].Crew != "" {
		t.Fatalf("stopped = %+v, want the Mate last", res.Stopped)
	}
	if n := rt.LiveAgentCount(); n != 0 {
		t.Fatalf("%d agents still running after the project was removed", n)
	}
	if len(events) != 1 || !strings.Contains(events[0], "with 1 agents up") {
		t.Fatalf("events = %v, want the stow after the crews and before the Mate stopped", events)
	}
	if _, ok := w.Project("shop"); ok {
		t.Fatal("project is still registered")
	}
	if _, err := os.Stat(w.ProjectDir("shop")); err != nil {
		t.Fatalf("projects/shop was not kept: %v", err)
	}
}

func TestRemoveProjectKeepsTheProjectWhenACrewCannotStop(t *testing.T) {
	w, rt, deps := runningProject(t)
	// k2 has work no default branch holds, so a plain stop refuses it.
	wt := filepath.Join(w.Root(), strings.TrimSpace(readCrewMeta(t, w, "k2")[spawn.MetaWorktree]))
	commitInWorktree(t, wt, "wip.txt", "wip\n")

	res, err := spawn.RemoveProject(context.Background(), w, deps, "shop", spawn.RemoveProjectOptions{})
	if err == nil || !strings.Contains(err.Error(), "crew k2") || strings.Contains(err.Error(), "crew k1") {
		t.Fatalf("err = %v, want it to name crew k2 only", err)
	}
	if res.Removed || len(res.Failed) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if _, ok := w.Project("shop"); !ok {
		t.Fatal("project was unregistered while crew k2 still runs")
	}
	if n := rt.LiveAgentCount(); n != 1 {
		t.Fatalf("%d agents running, want only the one that refused", n)
	}
}

func TestRemoveProjectMateStopFailureKeepsTheProject(t *testing.T) {
	w, rt, deps := runningProject(t)
	_, err := spawn.RemoveProject(context.Background(), w, deps, "shop", spawn.RemoveProjectOptions{
		Stow: func(context.Context, string) error { return errors.New("stow timed out") },
	})
	if err == nil || !strings.Contains(err.Error(), "mate: stow timed out") {
		t.Fatalf("err = %v, want it to name the Mate", err)
	}
	if _, ok := w.Project("shop"); !ok || rt.LiveAgentCount() != 1 {
		t.Fatal("the project must stay registered with its Mate")
	}
}

func TestRemoveProjectOnlyClearsAnAgentHerdrDoesNotList(t *testing.T) {
	w, rt, deps := runningProject(t)
	rt.DropPane(readCrewMeta(t, w, "k1")[spawn.MetaPane])
	stowed := false
	res, err := spawn.RemoveProject(context.Background(), w, deps, "shop", spawn.RemoveProjectOptions{
		Stow: func(context.Context, string) error { stowed = true; return nil },
	})
	if err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	if !res.Stopped[0].Cleared || res.Stopped[1].Cleared {
		t.Fatalf("stopped = %+v, want only k1 cleared", res.Stopped)
	}
	if !stowed {
		t.Fatal("the live Mate was not stowed")
	}
	if m := readCrewMeta(t, w, "k1"); m[spawn.MetaPane] != "" || m[spawn.MetaState] != "" {
		t.Fatalf("k1 meta = %v, want its run meta cleared and no closing state", m)
	}
}

func TestRemoveProjectSkipsClosedCrewsAndAgentsWithNoPane(t *testing.T) {
	w := crewWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	if err := w.WriteCrewMeta("shop", "old", map[string]string{spawn.MetaState: spawn.CrewStateFinished, spawn.MetaPane: "p9"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "never", map[string]string{spawn.MetaTask: "x"}); err != nil {
		t.Fatal(err)
	}
	res, err := spawn.RemoveProject(context.Background(), w, deps, "shop", spawn.RemoveProjectOptions{})
	if err != nil || !res.Removed || len(res.Stopped) != 0 {
		t.Fatalf("res = %+v, err = %v, want nothing to stop and the project removed", res, err)
	}
}

func TestRemoveThenAddKeepsMemoryAndCrewRecords(t *testing.T) {
	w, _, deps := runningProject(t)
	memory := filepath.Join(w.ProjectDir("shop"), "memory.md")
	if err := os.WriteFile(memory, []byte("the shop uses sqlite\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := spawn.RemoveProject(context.Background(), w, deps, "shop", spawn.RemoveProjectOptions{}); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	repo := w.RepoDir("shop/shop")
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject after remove: %v", err)
	}
	if data, err := os.ReadFile(memory); err != nil || string(data) != "the shop uses sqlite\n" {
		t.Fatalf("memory.md = %q, %v, want it kept", data, err)
	}
	crews, err := spawn.ListCrews(w, "shop")
	if err != nil || len(crews) != 2 {
		t.Fatalf("crews = %+v, %v, want both records back", crews, err)
	}
}

func itoa(n int) string { return string(rune('0' + n)) }

func readCrewMeta(t *testing.T, w *store.Workspace, crew string) map[string]string {
	t.Helper()
	m, err := w.ReadCrewMeta("shop", crew)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
