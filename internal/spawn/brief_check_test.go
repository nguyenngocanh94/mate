package spawn_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// TestSpawnCrewRefusesAMalformedBriefBeforeCreatingAnything: `crew spawn`
// runs the same check as `mate brief check` and a refusal leaves no
// worktree, no branch, no crews/<id>/, no meta and no pane - there is
// nothing to clean up and nothing reads as a crew that tried to start.
func TestSpawnCrewRefusesAMalformedBriefBeforeCreatingAnything(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	old, err := os.ReadFile("../brief/testdata/buyesp32-old-task.md")
	if err != nil {
		t.Fatal(err)
	}

	_, err = spawn.SpawnCrew(context.Background(), w, fakeDeps(t, rt), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: string(old),
	})
	var shape *brief.Error
	if !errors.As(err, &shape) {
		t.Fatalf("err = %v, want a *brief.Error", err)
	}
	if len(shape.Problems) != 6 || !strings.Contains(err.Error(), "## Acceptance: missing") {
		t.Fatalf("refusal does not list every problem:\n%v", err)
	}
	for _, path := range []string{w.WorktreeDir("shop", "k3"), w.CrewDir("shop", "k3"), w.CrewMeta("shop", "k3"), w.CrewStatus("shop", "k3")} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Errorf("%s exists after a refused brief (%v)", path, statErr)
		}
	}
	if exists, _ := gitx.New().BranchExists(context.Background(), w.RepoDir("shop/shop"), "mate/k3"); exists {
		t.Error("branch mate/k3 exists after a refused brief")
	}
	if len(rt.Tabs) != 0 {
		t.Errorf("a pane was opened for a refused brief: %v", rt.Tabs)
	}
}

// TestSpawnCrewScoutShape: --scout requires ## Deliverable and renders the
// scout's definition of done with the report path the app knows; without it
// the same brief is refused, because a ship has no Deliverable.
func TestSpawnCrewScoutShape(t *testing.T) {
	w := crewWorkspace(t, "shop")
	text := brieftest.Scout("Find out why the cart empties.", "What empties it?")

	_, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, runtime.NewFake()), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: text,
	})
	if err == nil || !strings.Contains(err.Error(), "## Deliverable: only a scout brief has one") {
		t.Fatalf("a Deliverable without --scout: err = %v", err)
	}
	_, err = spawn.SpawnCrew(context.Background(), w, fakeDeps(t, runtime.NewFake()), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("Find out."), Scout: true,
	})
	if err == nil || !strings.Contains(err.Error(), "## Deliverable: missing") {
		t.Fatalf("--scout without a Deliverable: err = %v", err)
	}

	res, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, runtime.NewFake()), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: text, Scout: true,
	})
	if err != nil {
		t.Fatalf("SpawnCrew --scout: %v", err)
	}
	rendered, err := os.ReadFile(res.BriefPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "wait-mate: report ready at "+w.CrewReport("shop", "k3")) {
		t.Errorf("scout brief does not name its report path:\n%s", rendered)
	}
	if strings.Contains(string(rendered), "# Before you hand back") {
		t.Error("scout brief carries the ship hand-back")
	}
	if ps := brief.CheckFile(string(rendered), brief.Scout); len(ps) != 0 {
		t.Errorf("the rendered scout brief fails brief check: %v", ps)
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaTask] != "Find out why the cart empties." {
		t.Errorf("task = %q, want the first line of the captain's words", meta[spawn.MetaTask])
	}
}

// TestSpawnCrewAppendsCrewRules: the captain's CREW.md files land at the end
// of the brief, workspace first, and a leading `# Task` the Mate wrote is
// not doubled.
func TestSpawnCrewAppendsCrewRules(t *testing.T) {
	w := crewWorkspace(t, "shop")
	if err := os.WriteFile(w.WorkspaceCrewDoc(), []byte("<!-- seed -->\n- Reproduce first.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectCrewDoc("shop"), []byte("- Run make check.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, runtime.NewFake()), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: "# Task\n\n" + brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	rendered, err := os.ReadFile(res.BriefPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered)
	if n := strings.Count(text, "\n# Task\n"); n != 1 {
		t.Errorf("# Task appears %d times", n)
	}
	rules := text[strings.Index(text, "# Captain's standing crew rules"):]
	ws, proj := strings.Index(rules, "- Reproduce first."), strings.Index(rules, "- Run make check.")
	if ws < 0 || proj < 0 || ws > proj {
		t.Errorf("crew rules missing or out of order:\n%s", rules)
	}
	if strings.Contains(text, "seed") {
		t.Error("the CREW.md comment reached the brief")
	}
}

// A crew's meta records its task's shape, and CrewIsScout answers from it;
// a crew spawned before `kind=` existed is read off its brief instead.
func TestCrewKindIsRecordedAndRead(t *testing.T) {
	w := crewWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	if _, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "sc", BriefText: brieftest.Scout("Find out why the cart empties.", "What empties it?"), Scout: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "sh", BriefText: brieftest.Ship("Fix the cart."),
	}); err != nil {
		t.Fatal(err)
	}
	for crew, want := range map[string]string{"sc": "scout", "sh": "ship"} {
		meta, err := w.ReadCrewMeta("shop", crew)
		if err != nil {
			t.Fatal(err)
		}
		if meta[spawn.MetaKind] != want || spawn.CrewIsScout(w, "shop", crew, meta) != (want == "scout") {
			t.Fatalf("%s: kind=%q scout=%v, want %s", crew, meta[spawn.MetaKind], spawn.CrewIsScout(w, "shop", crew, meta), want)
		}
		// An older record: no kind, only the brief.
		delete(meta, spawn.MetaKind)
		if got := spawn.CrewIsScout(w, "shop", crew, meta); got != (want == "scout") {
			t.Fatalf("%s without kind: scout=%v, want %s", crew, got, want)
		}
	}
}
