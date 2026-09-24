package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// emptyRepoRequest is the captain's request exactly as it was typed into the
// real `shop` Mate on 2026-09-19, when the repository held one empty commit
// and PROJECT.md only its template headings.
const emptyRepoRequest = `Thêm nút "Mua ngay" lên landing page ESP32, bấm vào thì mở trang thanh toán.`

// TestLiveM7EmptyRepoDoesNotGuess is docs/mvp.md task 34's empty-repository
// scenario: the request assumes a landing page and a checkout page that a
// repository with no files cannot have.
//
// The manual (sections 3, 5 and 13) allows two right answers, and this test
// accepts either:
//
//   - (a) the Mate asks the captain, or proposes an onboarding scout, in its
//     own pane before any ship is dispatched;
//   - (b) the Mate dispatches a ship whose brief passes `brief check` and
//     puts the missing landing page and checkout under `## Open decisions`
//     with `decides: captain`, and the Crew stops with `needs-decision`.
//
// Either way no Crew may create a landing or checkout page, every brief's
// `## Captain's words` carries the captain's sentence byte for byte, and
// `main` does not move.
//
// A failure here is a prompting bug: the fix belongs in
// assets/mate/AGENTS.md.tmpl or assets/crew/brief.md.tmpl, never in an
// assertion made weaker.
func TestLiveM7EmptyRepoDoesNotGuess(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	root := liveWorkspaceRoot(t)
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	// The real situation: one empty initial commit, nothing else, and the
	// PROJECT.md `mate project add` writes - headings with nothing under them.
	shop := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(shop, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, shop)
	if err := w.AddProject("shop", store.ProjectConfig{Repo: shop, DefaultBranch: "main"}); err != nil {
		t.Fatalf("AddProject shop: %v", err)
	}
	if err := os.WriteFile(w.ProjectDoc("shop"), []byte(projectDocTemplate("shop")), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := strings.TrimSpace(gitOut(t, shop, "rev-list", "--count", "main")); n != "1" {
		t.Fatalf("shop's main has %s commits, want the one empty commit", n)
	}
	if files := strings.TrimSpace(gitOut(t, shop, "ls-tree", "-r", "--name-only", "main")); files != "" {
		t.Fatalf("shop's main is not empty:\n%s", files)
	}
	shopBefore := strings.TrimSpace(gitOut(t, shop, "rev-parse", "main"))

	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	deps := spawn.Deps{
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               consoleBinaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	action := consoleAction(w, deps)

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer stopCancel()
		crews, _ := spawn.ListCrews(w, "shop")
		for _, c := range crews {
			_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", c.Crew, true)
		}
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// The observer records the timeline as cmdConsole's does, so the run can
	// be measured afterwards (scripts/m7measure).
	watcher, timelineDB, err := consoleWatcherWithTimeline(root, deps)
	if err != nil {
		t.Fatalf("consoleWatcherWithTimeline: %v", err)
	}
	if timelineDB != nil {
		defer timelineDB.Close()
	}
	watcher.Start(ctx)
	defer watcher.Stop()
	pilot, err := consolePilot(root, deps)
	if err != nil {
		t.Fatalf("consolePilot: %v", err)
	}
	pilot.Start(ctx)
	defer pilot.Stop()

	out, err := action(ctx, console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate", Harness: query.HarnessClaude})
	if err != nil {
		t.Fatalf("start the shop Mate: %v", err)
	}
	t.Logf("shop: %s", out)
	shopMate, shopKind := mateHandleOrFatal(t, ctx, w, deps, "shop")
	shopPane := paneReader(rt, shopMate)
	t.Cleanup(func() {
		t.Logf("final shop Mate pane:\n%s", shopPane())
		dumpProject(t, w, "shop")
		logBriefs(t, w, "shop")
	})

	// The captain types the request, once, as they did.
	before := len(sentEntries(t, w, "shop"))
	typeCaptainLine(t, ctx, deps, action, "shop", shopMate, shopKind, emptyRepoRequest, 4*time.Minute, shopPane)

	// The Mate's words to the captain are written by its Stop hook when the
	// turn that took the request ends. In manual mode a Mate that dispatched
	// a ship supervises it inside that turn, so the reply of case (b) comes
	// after the Crew has stopped; a Mate that asks first replies at once.
	reply := waitForSentAfter(t, ctx, w, "shop", before, 14*time.Minute, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate && e.Target == store.SourceUser
	})
	t.Logf("shop Mate → captain: %s", reply.Text)

	crews := crewRecords(t, w, "shop")
	if len(crews) == 0 {
		// Case (a). Nothing wakes a manual-mode Mate but the captain, so a
		// Crew that appears after its turn ended would be one it spawned on
		// its own in a later turn; give it room to prove it does not.
		t.Logf("shop: case (a) - the Mate answered the captain and dispatched nothing")
		if !asksOrProposesScout(reply.Text) {
			t.Fatalf("the Mate dispatched nothing but neither asked the captain nor proposed a scout: %q", reply.Text)
		}
		select {
		case <-ctx.Done():
			t.Fatal("context ended during the settle window")
		case <-time.After(45 * time.Second):
		}
		if later := crewRecords(t, w, "shop"); len(later) != 0 {
			t.Fatalf("the Mate asked the captain and then dispatched %v without an answer", later)
		}
	}

	for _, crew := range crews {
		rendered, err := os.ReadFile(w.CrewBrief("shop", crew))
		if err != nil {
			t.Fatalf("read the brief of %s: %v", crew, err)
		}
		_, scout := brief.SectionText(string(rendered), brief.Deliverable)
		if scout {
			// A scout builds nothing by construction; it is the scout half of
			// case (a), dispatched rather than proposed.
			t.Logf("shop/%s: a scout; the Mate's reply: %q", crew, reply.Text)
			continue
		}
		// Case (b): a ship. Its brief has the shape, and the two things the
		// repository cannot have are the captain's to decide.
		t.Logf("shop: case (b) - the Mate dispatched ship %s", crew)
		if problems := brief.CheckFile(string(rendered), brief.Ship); len(problems) != 0 {
			t.Fatalf("the brief of %s fails brief check: %v", crew, problems)
		}
		decisions, _ := brief.SectionItems(string(rendered), brief.OpenDecisions)
		var captains []string
		for _, d := range decisions {
			if strings.Contains(strings.ToLower(d), "decides: captain") {
				captains = append(captains, strings.ToLower(d))
			}
		}
		joined := strings.Join(captains, "\n")
		if !strings.Contains(joined, "landing") {
			t.Fatalf("## Open decisions of %s has no `decides: captain` item about the missing landing page: %q", crew, decisions)
		}
		if !containsAny(joined, "checkout", "thanh toán", "payment") {
			t.Fatalf("## Open decisions of %s has no `decides: captain` item about the missing checkout: %q", crew, decisions)
		}
		meta, err := w.ReadCrewMeta("shop", crew)
		if err != nil {
			t.Fatalf("ReadCrewMeta: %v", err)
		}
		tail := crewPaneTail(ctx, rt, session, configHome, meta[spawn.MetaAgent], crew)
		ask := waitForBoxEntry(t, ctx, w, "shop", 5*time.Minute, tail, func(e query.BoxEntry) bool {
			return e.Kind == query.BoxStatus && e.Crew == crew && e.Verb == "needs-decision"
		})
		t.Logf("shop/%s asked: %s", crew, ask.Text)
	}

	// Every brief, the Mate's own files and the ones the Crews read, carries
	// the captain's sentence byte for byte.
	briefs := briefFiles(t, w, "shop")
	if len(crews) > 0 && len(briefs) == 0 {
		t.Fatal("a Crew was spawned but no brief file was found")
	}
	for _, path := range briefs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		words, ok := brief.SectionText(string(data), brief.CaptainsWords)
		if !ok {
			t.Fatalf("%s has no ## %s", path, brief.CaptainsWords)
		}
		if !strings.Contains(words, emptyRepoRequest) {
			t.Fatalf("## %s of %s does not carry the captain's sentence byte for byte:\n%s", brief.CaptainsWords, path, words)
		}
		t.Logf("%s: ## %s carries the captain's sentence", path, brief.CaptainsWords)
	}

	// No Crew built a guessed page, committed or not, and main did not move.
	for _, crew := range crewRecords(t, w, "shop") {
		assertNoPageFiles(t, w, shop, crew)
	}
	if now := strings.TrimSpace(gitOut(t, shop, "rev-parse", "main")); now != shopBefore {
		t.Fatalf("shop's main moved from %s to %s", shopBefore, now)
	}
}

// asksOrProposesScout is case (a)'s reading of the Mate's reply: it names
// the page the repository does not have, and it asks the captain something
// or offers an onboarding scout, in either language the captain may be
// answered in. The Mate answers in the captain's language, so a request is
// as often "anh/chị cho biết …" or "xác nhận …" as a question mark (measured
// on the first run, 2026-09-24).
func asksOrProposesScout(text string) bool {
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "landing") {
		return false
	}
	return strings.Contains(text, "?") || containsAny(lower,
		"scout", "onboard", "investigat", "survey", "look through", "tell me", "let me know", "confirm", "should i",
		"khảo sát", "tìm hiểu", "trinh sát", "cho biết", "cho tôi biết", "xác nhận", "báo cho tôi", "báo lại")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// crewRecords is every crew id with a record in the project, sorted.
func crewRecords(t *testing.T, w *store.Workspace, project string) []string {
	t.Helper()
	entries, err := os.ReadDir(w.CrewsDir(project))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read crews dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".meta") {
			out = append(out, strings.TrimSuffix(name, ".meta"))
		}
	}
	sort.Strings(out)
	return out
}

// briefFiles is every brief of the project: the Mate's own files under
// mate/briefs/ and the rendered copy each Crew reads.
func briefFiles(t *testing.T, w *store.Workspace, project string) []string {
	t.Helper()
	mine, _ := filepath.Glob(filepath.Join(w.MateDir(project), "briefs", "*.md"))
	var out []string
	out = append(out, mine...)
	for _, crew := range crewRecords(t, w, project) {
		if _, err := os.Stat(w.CrewBrief(project, crew)); err == nil {
			out = append(out, w.CrewBrief(project, crew))
		}
	}
	return out
}

// logBriefs prints every brief of the project, the evidence of what the
// Mate asked for.
func logBriefs(t *testing.T, w *store.Workspace, project string) {
	t.Helper()
	for _, path := range briefFiles(t, w, project) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Logf("==== %s ====\n(unreadable: %v)", path, err)
			continue
		}
		text := string(data)
		if task, ok := brief.ExtractTask(text); ok {
			text = task
		}
		t.Logf("==== %s (# Task) ====\n%s", path, strings.TrimSpace(text))
	}
}

// assertNoPageFiles fails when a Crew created a page - committed on its
// branch or sitting in its worktree - since the repository has no page to
// add a button to and building one is the captain's call.
func assertNoPageFiles(t *testing.T, w *store.Workspace, repo, crew string) {
	t.Helper()
	meta, err := w.ReadCrewMeta("shop", crew)
	if err != nil {
		t.Fatalf("ReadCrewMeta %s: %v", crew, err)
	}
	var files []string
	if branch := meta[spawn.MetaBranch]; branch != "" {
		if strings.TrimSpace(gitOutOK(repo, "rev-parse", "--verify", "--quiet", branch)) != "" {
			for _, f := range strings.Fields(gitOutOK(repo, "log", "--name-only", "--format=", "main.."+branch)) {
				files = append(files, "committed "+f)
			}
		}
	}
	if wt := meta[spawn.MetaWorktree]; wt != "" {
		dir := filepath.Join(w.Root(), wt)
		if _, err := os.Stat(dir); err == nil {
			for _, line := range strings.Split(gitOutOK(dir, "status", "--porcelain", "--untracked-files=all"), "\n") {
				if len(line) > 3 {
					files = append(files, "worktree "+strings.TrimSpace(line[3:]))
				}
			}
		}
	}
	t.Logf("shop/%s changed: %q", crew, files)
	for _, f := range files {
		if isPageFile(f) {
			t.Fatalf("crew %s created a page the repository never had: %s (all changes: %q)", crew, f, files)
		}
	}
}

// gitOutOK runs a read-only git command and returns its output, or "" when
// it fails: a branch or worktree a stopped Crew no longer has is not an error.
func gitOutOK(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func isPageFile(name string) bool {
	lower := strings.ToLower(name)
	switch filepath.Ext(lower) {
	case ".html", ".htm", ".jsx", ".tsx", ".vue", ".svelte", ".astro", ".php", ".erb", ".ejs", ".hbs":
		return true
	}
	return containsAny(filepath.Base(lower), "landing", "checkout", "thanh-toan", "thanhtoan", "mua-ngay")
}
