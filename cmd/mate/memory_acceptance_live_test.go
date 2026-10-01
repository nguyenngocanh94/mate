package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// The captain's lines in the memory acceptance (docs/mvp.md task 38). Each
// is what a person types into the Mate's pane: no sentinel, no crew id, no
// command, and never a reminder of something said before the restart.
const (
	memoryProject = "kiosk"

	// Step 1: a scout, so the correction below has a Crew to be about.
	memoryScoutBuy = `Find out which pages of the site link to /buy and write me a short report. Use a crew.`
	// The standing correction, given once, after the first Crew handed back.
	// It is about how every future Crew works, not about this one task, so
	// its owner is memory.md ## Lessons and a CREW.md proposal (manual
	// section 2), and it must reach the next brief without being repeated.
	memoryCorrection = `One standing rule for every crew from now on: whenever a crew's report mentions a file, it must cite it as path:line with the line number, and a crew must never commit report or note files to the repository. No need to redo this report.`
	memoryCloseScout = `Thanks, close that scout.`
	// Step 2: a ship with a product choice only the captain can make: the
	// site has two checkout pages and the request names neither.
	memoryShip = `Add a "Buy now" button to the top of index.html that links to our checkout page. Use a crew.`
	// Step 3, after the restart.
	memoryWaitingOn  = `What are you waiting on?`
	memoryScoutStyle = `Separately: find out which pages include the stylesheet css/site.css and write me a short report. Use a crew.`
)

// memoryLessonRE is the substance of the correction as a brief or a lesson
// would restate it: files cited with their line numbers.
var memoryLessonRE = regexp.MustCompile(`(?i)line[ -]?numbers?|path:line|file:line|:<line>|:line\b`)

// TestLiveMemorySurvivesRestart is docs/mvp.md task 38: a Mate learns one
// standing correction for Crews and holds one question for the captain, is
// restarted from the console, and afterwards, with nobody repeating either,
// names the question it is waiting on and briefs the next Crew with the
// correction. Four variants: Claude and Codex, each resumed by the
// console's restart and each started fresh after it, where the files are
// the only continuity.
//
// Everything is driven the way the captain drives it: lines typed into the
// Mate's pane, console gestures through consoleAction, the observer and the
// outbox sender started as cmdConsole starts them, manual mode. A failure is
// a prompting bug - the manual, the stow skill or a nudge - never a reason
// to weaken an assertion.
func TestLiveMemorySurvivesRestart(t *testing.T) {
	for _, v := range []struct {
		name  string
		kind  harness.Kind
		fresh bool
	}{
		{"claude", harness.KindClaude, false},
		{"claude-fresh", harness.KindClaude, true},
		{"codex", harness.KindCodex, false},
		{"codex-fresh", harness.KindCodex, true},
	} {
		t.Run(v.name, func(t *testing.T) { memoryAcceptance(t, v.kind, v.fresh) })
	}
}

func memoryAcceptance(t *testing.T, kind harness.Kind, fresh bool) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)
	codexHome := os.Getenv("CODEX_HOME")

	root := liveWorkspaceRoot(t)
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	repo := filepath.Join(w.Root(), memoryProject)
	writeRepo(t, repo, memorySite)
	if err := run([]string{"project", "add", memoryProject, repo, "--workspace", root}, os.Stdout, os.Stderr); err != nil {
		t.Fatalf("project add: %v", err)
	}
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open after project add: %v", err)
	}
	mainBefore := strings.TrimSpace(gitOut(t, repo, "rev-parse", "main"))

	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	deps := spawn.Deps{
		Harnesses:            harnesses,
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

	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Minute)
	defer cancel()
	action := consoleAction(w, deps)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer stopCancel()
		crews, _ := spawn.ListCrews(w, memoryProject)
		for _, c := range crews {
			_, _ = spawn.StopCrew(stopCtx, w, deps, memoryProject, c.Crew, true)
		}
		_, _ = spawn.StopMate(stopCtx, w, deps, memoryProject)
	})

	watcher, timelineDB, err := consoleWatcherWithTimeline(root, deps)
	if err != nil {
		t.Fatalf("consoleWatcherWithTimeline: %v", err)
	}
	if timelineDB == nil {
		t.Fatal("the observer opened no timeline database")
	}
	defer timelineDB.Close()
	watcher.Start(ctx)
	defer watcher.Stop()
	pilot, err := consolePilot(root, deps)
	if err != nil {
		t.Fatalf("consolePilot: %v", err)
	}
	pilot.Start(ctx)
	defer pilot.Stop()

	harnessChoice := query.HarnessKind("claude")
	if kind == harness.KindCodex {
		harnessChoice = query.HarnessKind("codex")
	}
	out, err := action(ctx, console.ActionRequest{Action: console.ActionStart, Target: memoryProject, TargetKind: "mate", Harness: harnessChoice})
	if err != nil {
		t.Fatalf("start the Mate: %v", err)
	}
	t.Logf("start: %s", out)
	mate, mateKind := mateHandleOrFatal(t, ctx, w, deps, memoryProject)
	if mateKind != kind {
		t.Fatalf("the Mate runs %s, want %s", mateKind, kind)
	}
	pane := func() string { return paneReader(rt, mate)() }
	voice := mateVoice{t: t, w: w, project: memoryProject, kind: kind, rollouts: &[]string{}}
	t.Cleanup(func() {
		t.Logf("final Mate pane:\n%s", pane())
		dumpProject(t, w, memoryProject)
		for _, f := range []string{w.MemoryFile(memoryProject), w.BacklogFile(memoryProject), w.ProjectDoc(memoryProject)} {
			data, _ := os.ReadFile(f)
			t.Logf("==== %s ====\n%s", filepath.Base(f), data)
		}
	})
	captain := func(text string) time.Time {
		t.Helper()
		at := time.Now()
		typeCaptainLine(t, ctx, deps, action, memoryProject, mate, kind, text, 6*time.Minute, pane)
		return at
	}

	// ---------- 1. a scout, and a correction the Mate must route ----------

	captain(memoryScoutBuy)
	scout1 := waitForCrewRecord(t, ctx, w, memoryProject, 5*time.Minute, pane)
	t.Logf("scout 1: %s", scout1)
	scout1Pane := crewPaneTail(ctx, rt, session, configHome, agentOf(t, w, memoryProject, scout1), scout1)
	summary := awaitHandbackRelay(t, ctx, w, action, voice, memoryProject, scout1, 10*time.Minute, scout1Pane, pane,
		func(s string) bool { return containsAny(strings.ToLower(s), "about", "index", "/buy") })
	t.Logf("EVIDENCE scout 1 relayed: %s", summary)

	corrected := captain(memoryCorrection)
	routedReply := voice.waitReply(ctx, corrected, 6*time.Minute, func(string) bool { return true }, pane)
	t.Logf("EVIDENCE Mate's reply to the correction: %s", routedReply.text)
	lesson := routedLesson(t, w, memoryProject)
	proposed := strings.Contains(routedReply.text, "CREW.md") && memoryLessonRE.MatchString(routedReply.text)
	if lesson == "" && !proposed {
		t.Fatalf("the Mate neither filed the correction under memory.md ## Lessons nor proposed it for CREW.md.\nreply: %s\nmemory.md:\n%s",
			routedReply.text, readFileOr(w.MemoryFile(memoryProject)))
	}
	t.Logf("EVIDENCE lesson entry: %s", lesson)
	t.Logf("EVIDENCE CREW.md proposal in the reply: %v", proposed)
	assertNoHarnessMemory(t, w, memoryProject, codexHome)

	closed := captain(memoryCloseScout)
	waitForCrewState(t, ctx, w, memoryProject, scout1, spawn.CrewStateFinished, 6*time.Minute, pane)
	voice.waitReply(ctx, closed, 4*time.Minute, func(string) bool { return true }, pane)

	// ---------- 2. a question only the captain can answer ----------

	shipAsked := captain(memoryShip)
	escalation := awaitEscalation(t, ctx, w, action, voice, memoryProject, shipAsked, 12*time.Minute, pane)
	t.Logf("EVIDENCE the Mate asked the captain: %s", escalation.text)
	// A line the app typed into the Mate (an [assign]) starts a turn of its
	// own; the captain restarts after reading its answer, not during it.
	if last, ok := lastAppLineToMate(t, w, memoryProject); ok && last.After(escalation.at) {
		voice.waitReply(ctx, last, 6*time.Minute, func(string) bool { return true }, pane)
	}
	t.Logf("backlog.md before the restart:\n%s", readFileOr(w.BacklogFile(memoryProject)))

	restartAt := time.Now()
	out, err = action(ctx, console.ActionRequest{Action: console.ActionRestartMate, Target: memoryProject, TargetKind: "project"})
	if err != nil {
		t.Fatalf("restart: %v\n%s", err, pane())
	}
	t.Logf("EVIDENCE restart (%s): %s", time.Since(restartAt).Round(time.Second), out)
	if !strings.HasPrefix(out, "stowed; ") || !strings.Contains(out, "is running on "+string(kind)) {
		t.Fatalf("restart outcome = %q, want it to open with stowed and bring the Mate back on %s", out, kind)
	}
	if receipt, ok := voice.firstSince(restartAt); ok {
		t.Logf("EVIDENCE stow receipt: %s", receipt.text)
	} else {
		t.Logf("EVIDENCE stow receipt: (no reply recorded)")
	}
	if fresh {
		// `mate mate stop --no-stow`, then `mate mate start --fresh`: the
		// conversation is gone and only the files carry anything over.
		if _, err := spawn.StopMate(ctx, w, deps, memoryProject); err != nil {
			t.Fatalf("stop after the restart: %v", err)
		}
		started, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: memoryProject, Harness: kind, Resume: true, Fresh: true})
		if err != nil {
			t.Fatalf("mate start --fresh: %v", err)
		}
		if started.Resumed {
			t.Fatal("the fresh start resumed a session")
		}
		t.Logf("EVIDENCE fresh start: agent %s, session %q", started.Agent, started.SessionID)
	}
	meta, _ := w.ReadMateMeta(memoryProject)
	t.Logf("EVIDENCE mate.meta after the restart: resumed=%q resumed_from=%q session_id=%q", meta[spawn.MetaResumed], meta[spawn.MetaResumedFrom], meta[spawn.MetaSessionID])
	if !fresh && kind == harness.KindClaude && meta[spawn.MetaResumed] != "true" {
		t.Fatalf("the console's restart of a Claude Mate did not resume its session: %v", meta)
	}
	mate, _ = mateHandleOrFatal(t, ctx, w, deps, memoryProject)

	// The question as sent: the Mate may put it to the captain more than once
	// (after the ship asks, and again when the captain hands that question
	// over), so the held line must quote one of those messages.
	var askedCaptain []string
	for _, r := range voice.since(shipAsked) {
		if r.at.Before(restartAt) {
			askedCaptain = append(askedCaptain, r.text)
		}
	}
	held := heldLine(t, w, memoryProject, askedCaptain)
	t.Logf("EVIDENCE held line: %s", held)

	// ---------- 3. after the restart, nobody repeats anything ----------

	asked := captain(memoryWaitingOn)
	waiting := voice.waitReply(ctx, asked, 6*time.Minute, func(string) bool { return true }, pane)
	t.Logf("EVIDENCE what are you waiting on: %s", waiting.text)
	if low := strings.ToLower(waiting.text); !strings.Contains(low, "checkout") || !containsAny(low, "classic", "express") {
		t.Fatalf("asked what it is waiting on, the Mate did not name the held checkout question:\n%s", waiting.text)
	}

	known := map[string]bool{scout1: true}
	for _, c := range crewRecords(t, w, memoryProject) {
		known[c] = true
	}
	captain(memoryScoutStyle)
	scout2 := waitForNewCrew(t, ctx, w, memoryProject, known, 6*time.Minute, pane)
	t.Logf("scout 2: %s", scout2)
	briefText := waitForFile(t, ctx, w.CrewBrief(memoryProject, scout2), time.Minute)
	build, _ := brief.SectionText(briefText, brief.Build)
	t.Logf("EVIDENCE scout 2 ## Build:\n%s", build)
	if !memoryLessonRE.MatchString(build) {
		t.Fatalf("the second scout's ## Build does not carry the correction (files cited with line numbers):\n%s", briefText)
	}
	scout2Pane := crewPaneTail(ctx, rt, session, configHome, agentOf(t, w, memoryProject, scout2), scout2)
	relay2 := awaitHandbackRelay(t, ctx, w, action, voice, memoryProject, scout2, 10*time.Minute, scout2Pane, pane,
		func(s string) bool { return containsAny(strings.ToLower(s), "site.css", "stylesheet", "css") })
	t.Logf("EVIDENCE scout 2 relayed: %s", relay2)
	if report, err := os.ReadFile(filepath.Join(w.CrewsDir(memoryProject), scout2, "report.md")); err == nil {
		t.Logf("scout 2 report.md:\n%s", report)
	}

	before := correctionLines(t, w, memoryProject, scout1)
	after := correctionLines(t, w, memoryProject, scout2)
	tlBefore := timelineCorrections(t, ctx, timelineDB, w, memoryProject, scout1)
	tlAfter := timelineCorrections(t, ctx, timelineDB, w, memoryProject, scout2)
	t.Logf("EVIDENCE repeated corrections after spawn: sent.log scout 1 %d, scout 2 %d; timeline scout 1 %d, scout 2 %d",
		len(before), len(after), len(tlBefore), len(tlAfter))
	for _, l := range append(after, tlAfter...) {
		t.Logf("  repeated to %s: %s", scout2, l)
	}
	if len(after) != 0 || len(tlAfter) != 0 {
		t.Fatalf("the Mate repeated the correction to the second crew after its spawn (%d in sent.log, %d in the timeline)", len(after), len(tlAfter))
	}

	// ---------- 4. the files ----------

	var check, checkErr bytes.Buffer
	if err := run([]string{"memory", "check", memoryProject, "--workspace", root}, &check, &checkErr); err != nil {
		t.Fatalf("memory check failed: %v\n%s%s", err, check.String(), checkErr.String())
	}
	t.Logf("EVIDENCE memory check: %s%s", check.String(), checkErr.String())
	var recall bytes.Buffer
	if err := run([]string{"recall", memoryProject, "--workspace", root}, &recall, os.Stderr); err != nil {
		t.Fatalf("recall: %v", err)
	}
	digest := recall.String()
	// The lesson as it stands now: a stow may rewrite it in place, but it
	// must still be a ## Lessons entry, and the digest must carry it.
	lessonNow := routedLesson(t, w, memoryProject)
	t.Logf("EVIDENCE lesson at the end: %s", lessonNow)
	if lessonNow == "" {
		t.Fatalf("memory.md ## Lessons no longer carries the correction at the end:\n%s", readFileOr(w.MemoryFile(memoryProject)))
	}
	if !strings.Contains(digest, strings.TrimSpace(lessonNow)) {
		t.Fatalf("recall does not carry the lesson %q:\n%s", lessonNow, digest)
	}
	if !memoryLessonRE.MatchString(digest) {
		t.Fatalf("recall carries no trace of the correction:\n%s", digest)
	}
	if nowHeld := heldLine(t, w, memoryProject, askedCaptain); !strings.Contains(digest, nowHeld) {
		t.Fatalf("recall does not carry the held question %q:\n%s", nowHeld, digest)
	}
	if now := strings.TrimSpace(gitOut(t, repo, "rev-parse", "main")); now != mainBefore {
		t.Fatalf("main moved to %s; nothing in this scenario is the captain's word to merge", now)
	}
	assertNoHarnessMemory(t, w, memoryProject, codexHome)
}

// memorySite is the tiny static site the crews work on: three pages that
// link to /buy or not, two checkout pages and no rule saying which is "our
// checkout page", and one stylesheet two pages include.
var memorySite = map[string]string{
	"README.md": "# kiosk\n\nA tiny static storefront. Open index.html in a browser.\n",
	"index.html": `<!doctype html>
<html>
<head>
  <title>Kiosk</title>
  <link rel="stylesheet" href="css/site.css">
</head>
<body>
  <h1>Kiosk</h1>
  <p>Maker boards, shipped fast.</p>
  <a href="/buy">Shop the boards</a>
</body>
</html>
`,
	"about.html": `<!doctype html>
<html>
<head>
  <title>About Kiosk</title>
  <link rel="stylesheet" href="css/site.css">
</head>
<body>
  <h1>About</h1>
  <p>We are two people and a soldering iron.</p>
  <p>Ready? <a href="/buy">Buy a board</a>.</p>
</body>
</html>
`,
	"contact.html": `<!doctype html>
<html>
<head><title>Contact</title></head>
<body>
  <h1>Contact</h1>
  <p>Write to hello@kiosk.example.</p>
</body>
</html>
`,
	"checkout-classic.html": "<!doctype html>\n<html><body><h1>Classic checkout</h1><p>Card form, three steps.</p></body></html>\n",
	"checkout-express.html": "<!doctype html>\n<html><body><h1>Express checkout</h1><p>One click with a saved wallet.</p></body></html>\n",
	"css/site.css":          "body { font-family: sans-serif; margin: 2rem; }\nh1 { color: #1d3557; }\n",
}

// mateVoice reads what the Mate said to the captain, turn by turn. A Claude
// Mate's Stop hook writes each turn's last message to sent.log as `mate →
// user`; a Codex Mate has no Stop hook, so its words are the rollout's
// task_complete messages, found through the transcript its SessionStart hook
// records in mate.meta. A start rewrites mate.meta without the transcript
// until the hook runs again at the next prompt, and a fresh start opens a new
// rollout, so every rollout seen is kept and read.
type mateVoice struct {
	t        *testing.T
	w        *store.Workspace
	project  string
	kind     harness.Kind
	rollouts *[]string
}

type mateReply struct {
	at   time.Time
	text string
}

func (v mateVoice) since(at time.Time) []mateReply {
	v.t.Helper()
	var out []mateReply
	if v.kind == harness.KindClaude {
		for _, e := range sentEntries(v.t, v.w, v.project) {
			if e.Source == store.SourceMate && e.Target == store.SourceUser && e.Time.After(at) {
				out = append(out, mateReply{at: e.Time, text: e.Text})
			}
		}
		return out
	}
	meta, _ := v.w.ReadMateMeta(v.project)
	if p := meta[spawn.MetaTranscript]; p != "" && !slices.Contains(*v.rollouts, p) {
		*v.rollouts = append(*v.rollouts, p)
	}
	for _, path := range *v.rollouts {
		for _, r := range codexRolloutReplies(v.t, path) {
			if r.at.After(at) {
				out = append(out, r)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out
}

func (v mateVoice) firstSince(at time.Time) (mateReply, bool) {
	replies := v.since(at)
	if len(replies) == 0 {
		return mateReply{}, false
	}
	return replies[0], true
}

// waitReply waits for a reply after at that match accepts, and returns it.
func (v mateVoice) waitReply(ctx context.Context, at time.Time, within time.Duration, match func(string) bool, evidence func() string) mateReply {
	v.t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, r := range v.since(at) {
			if match(r.text) {
				return r
			}
		}
		if time.Now().After(deadline) {
			var seen []string
			for _, r := range v.since(at) {
				seen = append(seen, r.text)
			}
			v.t.Fatalf("no matching Mate reply within %s; replies since %s: %q\npane:\n%s", within, at.Format("15:04:05"), seen, evidence())
		}
		select {
		case <-ctx.Done():
			v.t.Fatalf("context ended waiting for a Mate reply\npane:\n%s", evidence())
		case <-time.After(3 * time.Second):
		}
	}
}

// codexRolloutReplies is every finished turn's last message in a Codex
// rollout, with its time.
func codexRolloutReplies(t *testing.T, path string) []mateReply {
	t.Helper()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []mateReply
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 16<<20), 16<<20)
	for sc.Scan() {
		var rec struct {
			Timestamp string `json:"timestamp"`
			Type      string `json:"type"`
			Payload   struct {
				Type    string `json:"type"`
				Message string `json:"last_agent_message"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "event_msg" || rec.Payload.Type != "task_complete" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
		if err != nil || strings.TrimSpace(rec.Payload.Message) == "" {
			continue
		}
		out = append(out, mateReply{at: at, text: rec.Payload.Message})
	}
	return out
}

// awaitHandbackRelay waits for a crew's wait-mate and for the Mate to relay
// its findings to the captain. The project is in manual mode, so the Mate
// sees nothing it has not been handed: when the hand-back sits in the
// captain's inbox and the Mate has said nothing about it, the captain
// presses [assign] on it once, as a captain would.
func awaitHandbackRelay(t *testing.T, ctx context.Context, w *store.Workspace, action console.ActionFunc, voice mateVoice,
	project, crew string, within time.Duration, crewPane, matePane func() string, relays func(string) bool) string {
	t.Helper()
	handback := waitForBoxEntry(t, ctx, w, project, within, crewPane, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Crew == crew && e.Verb == "wait-mate"
	})
	t.Logf("%s handed back: %s", crew, handback.Text)
	since := handback.At.Add(-time.Second)
	deadline := time.Now().Add(within)
	assigned := false
	for {
		for _, r := range voice.since(since) {
			if relays(r.text) {
				return r.text
			}
		}
		if !assigned && time.Since(handback.At) > 45*time.Second {
			if item, ok := inboxItemFor(w, project, crew, "wait-mate"); ok {
				t.Logf("captain [assign]s %s's hand-back: %s", crew, assignAndAwaitDelivery(t, ctx, w, action, project, item, 6*time.Minute, matePane))
				assigned = true
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Mate never relayed %s's findings within %s\npane:\n%s", crew, within, matePane())
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for the Mate to relay %s", crew)
		case <-time.After(3 * time.Second):
		}
	}
}

// awaitEscalation waits for the Mate to put the checkout choice to the
// captain, whichever way it gets there: asking before it spawns, or
// spawning a ship that stops with needs-decision, which the captain hands
// to the Mate with [assign].
func awaitEscalation(t *testing.T, ctx context.Context, w *store.Workspace, action console.ActionFunc, voice mateVoice,
	project string, since time.Time, within time.Duration, pane func() string) mateReply {
	t.Helper()
	deadline := time.Now().Add(within)
	assigned := map[string]bool{}
	for {
		for _, r := range voice.since(since) {
			if low := strings.ToLower(r.text); strings.Contains(low, "classic") && strings.Contains(low, "express") {
				return r
			}
		}
		box := query.LoadBox(w, project)
		if box.IsKnown() {
			for _, e := range box.Value.Inbox {
				if e.Verb == "needs-decision" && !assigned[e.AssignKey] && e.Resolvable() {
					assigned[e.AssignKey] = true
					t.Logf("the ship %s asked: %s; captain [assign]s it: %s", e.Crew, e.Text,
						assignAndAwaitDelivery(t, ctx, w, action, project, e, 6*time.Minute, pane))
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Mate never put the checkout choice to the captain within %s\npane:\n%s", within, pane())
		}
		select {
		case <-ctx.Done():
			t.Fatal("context ended waiting for the escalation")
		case <-time.After(3 * time.Second):
		}
	}
}

// lastAppLineToMate is when the app last typed a line into the Mate.
func lastAppLineToMate(t *testing.T, w *store.Workspace, project string) (time.Time, bool) {
	t.Helper()
	var at time.Time
	for _, e := range sentEntries(t, w, project) {
		if e.Source == store.SourceApp && e.Target == store.TargetMate {
			at = e.Time
		}
	}
	return at, !at.IsZero()
}

func inboxItemFor(w *store.Workspace, project, crew, verb string) (query.BoxEntry, bool) {
	box := query.LoadBox(w, project)
	if !box.IsKnown() {
		return query.BoxEntry{}, false
	}
	for _, e := range box.Value.Inbox {
		if e.Crew == crew && e.Verb == verb && e.Resolvable() {
			return e, true
		}
	}
	return query.BoxEntry{}, false
}

// routedLesson is the memory.md ## Lessons entry that carries the
// correction, provided `memory check`'s own parser accepts its shape; "" when
// there is none.
func routedLesson(t *testing.T, w *store.Workspace, project string) string {
	t.Helper()
	text := readFileOr(w.MemoryFile(project))
	entries, problems := memory.Check(text, time.Now(), w.Root(), w.ProjectDir(project))
	bad := map[int]string{}
	for _, p := range problems {
		bad[p.Line] = p.String()
	}
	lines := strings.Split(text, "\n")
	for _, e := range entries {
		if e.Section != memory.LessonsSection || !memoryLessonRE.MatchString(e.Text) {
			continue
		}
		if msg, ok := bad[e.Line]; ok {
			t.Fatalf("the lesson the Mate filed has a bad shape: %s", msg)
		}
		return lines[e.Line-1]
	}
	return ""
}

// heldLine is backlog.md's `## Held for the captain` line for the checkout
// question: dated today, naming both pages, and carrying at least 40
// characters of the question exactly as the Mate sent it in one of its
// messages to the captain.
func heldLine(t *testing.T, w *store.Workspace, project string, asked []string) string {
	t.Helper()
	text := readFileOr(w.BacklogFile(project))
	today := time.Now().Format(memory.DateLayout)
	var section []string
	in := false
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "## ") {
			in = strings.TrimSpace(strings.TrimPrefix(l, "## ")) == memory.BacklogHeld
			continue
		}
		if in && strings.TrimSpace(l) != "" {
			section = append(section, l)
		}
	}
	for _, l := range section {
		low := strings.ToLower(l)
		if !strings.Contains(low, "classic") || !strings.Contains(low, "express") {
			continue
		}
		if !strings.Contains(l, today) {
			t.Fatalf("the held line for the checkout question carries no date %s: %s", today, l)
		}
		best := ""
		for _, a := range asked {
			if run := commonRun(normaliseQuote(l), normaliseQuote(a)); len([]rune(run)) > len([]rune(best)) {
				best = run
			}
		}
		// Verbatim is a long shared run, or a whole short question: "Which
		// page should "Buy now" open?" copied exactly is 34 characters.
		whole := strings.TrimSpace(best)
		if len([]rune(best)) < 40 && !(len([]rune(whole)) >= 15 && strings.HasSuffix(whole, "?")) {
			t.Fatalf("the held line does not carry the question verbatim (longest shared run %q):\nheld: %s\nasked:\n%s", best, l, strings.Join(asked, "\n---\n"))
		}
		return l
	}
	t.Fatalf("backlog.md ## %s holds no line for the checkout question:\n%s", memory.BacklogHeld, text)
	return ""
}

var quoteFolds = strings.NewReplacer("“", `"`, "”", `"`, "‘", "'", "’", "'", "`", "", "**", "")

func normaliseQuote(s string) string {
	return strings.Join(strings.Fields(quoteFolds.Replace(s)), " ")
}

// commonRun is the longest substring a and b share.
func commonRun(a, b string) string {
	ra, rb := []rune(a), []rune(b)
	best, end := 0, 0
	prev := make([]int, len(rb)+1)
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		for j := 1; j <= len(rb); j++ {
			if ra[i-1] == rb[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best, end = cur[j], i
				}
			}
		}
		prev = cur
	}
	return string(ra[end-best : end])
}

// correctionLines are the lines the Mate sent a crew after its spawn that
// restate the correction, from sent.log.
func correctionLines(t *testing.T, w *store.Workspace, project, crew string) []string {
	t.Helper()
	meta, _ := w.ReadCrewMeta(project, crew)
	spawned, _ := time.Parse(time.RFC3339, meta[spawn.MetaStartedAt])
	var out []string
	for _, e := range sentEntries(t, w, project) {
		if e.Source == store.SourceMate && e.Target == store.CrewTarget(crew) && e.Time.After(spawned) && memoryLessonRE.MatchString(e.Text) {
			out = append(out, e.Text)
		}
	}
	return out
}

// timelineCorrections is the same count read the way research section 12.8
// defines it: the timeline's messages from the Mate to that crew after the
// task's spawn. It waits a little for the observer to have indexed sent.log.
func timelineCorrections(t *testing.T, ctx context.Context, handle *db.DB, w *store.Workspace, project, crew string) []string {
	t.Helper()
	var out []string
	deadline := time.Now().Add(time.Minute)
	for {
		out = out[:0]
		rows, err := handle.SQL().QueryContext(ctx, `
			SELECT m.text FROM message m
			  JOIN event e ON e.id = m.event_id
			  LEFT JOIN task k ON k.crew_actor_id = m.to_actor_id
			 WHERE m.from_actor_id = ? AND m.to_actor_id = ?
			   AND (k.spawned_at IS NULL OR k.spawned_at = '' OR e.at > k.spawned_at)`,
			timeline.MateActorID(project), timeline.CrewActorID(project, crew))
		if err != nil {
			t.Fatalf("timeline query: %v", err)
		}
		total := 0
		for rows.Next() {
			var text string
			if err := rows.Scan(&text); err != nil {
				t.Fatal(err)
			}
			total++
			if memoryLessonRE.MatchString(text) {
				out = append(out, text)
			}
		}
		rows.Close()
		if total >= len(sentToCrew(t, sentEntries(t, w, project), crew)) || time.Now().After(deadline) {
			t.Logf("timeline: %d line(s) from the Mate to %s after its spawn", total, crew)
			return out
		}
		time.Sleep(3 * time.Second)
	}
}

func sentToCrew(_ *testing.T, entries []store.SentEntry, crew string) []store.SentEntry {
	var out []store.SentEntry
	for _, e := range entries {
		if e.Source == store.SourceMate && e.Target == store.CrewTarget(crew) {
			out = append(out, e)
		}
	}
	return out
}

// assertNoHarnessMemory: the Mate's memory is its files, never the
// harness's own (docs/mvp.md M8, B6). Claude's auto-memory directory for the
// Mate's cwd and the lab CODEX_HOME's memories must hold nothing.
func assertNoHarnessMemory(t *testing.T, w *store.Workspace, project, codexHome string) {
	t.Helper()
	projects, err := harness.ClaudeProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		filepath.Join(projects, harness.ClaudeProjectSlug(w.MateDir(project)), "memory"),
		filepath.Join(codexHome, "memories"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read %s: %v", dir, err)
		}
		if len(entries) > 0 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("the harness kept a memory of its own in %s: %v", dir, names)
		}
	}
}

func waitForNewCrew(t *testing.T, ctx context.Context, w *store.Workspace, project string, known map[string]bool,
	within time.Duration, evidence func() string) string {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, c := range crewRecords(t, w, project) {
			if !known[c] {
				return c
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Mate spawned no new crew within %s\n%s", within, evidence())
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for a new crew\n%s", evidence())
		case <-time.After(3 * time.Second):
		}
	}
}

func waitForFile(t *testing.T, ctx context.Context, path string, within time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for %s", path)
		case <-time.After(2 * time.Second):
		}
	}
}

func readFileOr(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("(unreadable: %v)", err)
	}
	return string(data)
}
