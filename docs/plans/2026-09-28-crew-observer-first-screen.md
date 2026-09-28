# Crew observer first screen: implementation plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (or the subagent-driven variant) to implement this plan task-by-task.
> Three tracks run in parallel on disjoint files; the integrator verifies the whole against the real `ios7` recording.

**Goal:** the Crew page answers the captain's four questions on its first screen: how many tokens the Crew consumed, how many tool calls went to which kind of work, how often it repeated itself, and which step cost the most, all from data the observer already records.

**Architecture:** the read-only projection in `internal/diagnostics` gains a crew-level work overview (same `Overview` type the prompt-level overview already uses), a `loops` summary and two rankings by output + thinking.
Segments switch to the work-kind vocabulary that `executionWork` already produces, so segments, prompt overviews, the crew overview and the UI filters share one taxonomy, and Codex's JavaScript wrappers around `apply_patch` and heredoc writes become "edit / write code" instead of "Tool wrapper".
The dashboard UI draws a four-card strip from those fields, a tool-mix table per prompt, kind colours on the timeline with poll brackets, and kind filters plus an output + thinking sort on the segment table.

**Tech stack:** Go 1.2x (`internal/diagnostics`, `internal/dashboard`), plain browser JavaScript and CSS with no build step (`internal/dashboard/ui`), `node --test` for UI behaviour, `go test` table-driven tests, `make check` (gofmt + vet + tests with declared skips).

**Design source:** `docs/evidence/crew-observer-first-screen-2026-09-28/crew-observer-design-20260928.html` (approved by the captain on 2026-09-28). Callouts ①–⑧ there map to the tasks below.

**Decisions taken from the design's open questions:** "most expensive step" shows both rankings and leads with output + thinking (Q1); aggregation lives in the Go projector with tests, never only in the browser (Q2); tracks P, C and U run now and D6/D7 (rollout, pricing row) stay with the captain (Q3); no multi-crew page in this batch (Q4).

---

## 0. Ground rules for every track

- Work in `/Users/anh/Workspace/mate` on the current working tree. Do **not** commit, stash, reset or reformat files you do not own. The tree already carries uncommitted work from another session; the captain commits.
- Do not create a git worktree: the code you build on is uncommitted, so a worktree would not contain it.
- Each track owns the files listed in its section and touches nothing else. If a compile error comes from a file another track owns, wait a minute and retry; do not fix it yourself. Report it if it persists.
- TDD: write the failing test, run it, implement, run it, refactor. Keep the package compiling after every save (add a type before using it).
- Run only your package's tests while working (`go test ./internal/diagnostics/`, `go test ./internal/dashboard/`, `node --test internal/dashboard/ui_observability_test.cjs`). The integrator runs `make check`.
- `gofmt -l` must print nothing for your files. Comments explain why, in the voice of the existing code.
- Never fabricate a number: a missing measurement is `null`/`nil`, never `0`. Findings' tokens are non-additive; segment and overview-category tokens are additive because each call belongs to exactly one of them. `turn` stays the only token ledger.
- A real recording is available for manual checks: `curl -s http://127.0.0.1:7779/api/projects/hellovietnam/tasks/ios7` (a scratch dashboard served from an isolated copy of the review database; restart it only if you own the integrator role: `pkill -f 'mate dashboard .*7779'`, then `go build -o /private/tmp/claude-501/-Users-anh-Workspace-mate/9c30deb9-f4ab-4745-b5aa-8af8823f1c22/scratchpad/mate ./cmd/mate && /private/tmp/claude-501/-Users-anh-Workspace-mate/9c30deb9-f4ab-4745-b5aa-8af8823f1c22/scratchpad/mate dashboard /private/tmp/claude-501/-Users-anh-Workspace-mate/9c30deb9-f4ab-4745-b5aa-8af8823f1c22/scratchpad/ws --addr 127.0.0.1:7779 &`). Never start anything on 7777 or 7778.
- Expected figures for `ios7` (Codex 0.157.1, gpt-6-sol): 363 model calls, 360 tool calls, 40,409,527 tokens (233,931 input · 40,106,880 cache read · 0 cache write · 68,716 output · 18,842 thinking), 3 prompts, 184 segments, 522 execution records (162 native commands of which 10 failed, 153 polls, 207 wrappers), 30 processes polled, 17 polling chains (≥3 polls), 123 polls with no new output, 30 with new output, 30 findings. Highest output + thinking call: `…#turn#1753` (ordinal 301, 1,949 output + 790 thinking). Highest total-token segment: `segment#…#turn#1971` ("process 85006", 2,222,317 tokens).

## 1. The contract the three tracks share

New or changed JSON on `GET /api/projects/{project}/tasks/{crew}` → `performance`:

```json
{
  "overview": {
    "summary": "Wait / poll (183 calls) · Unclassified activity (57 calls) · Edit code / files (42 calls) · …",
    "categories": [
      {"kind": "wait", "label": "Wait / poll", "model_calls": 183, "execution_count": 0, "tokens": {"input": 46473, "cache_read": 20563200, "cache_write": 0, "output": 6976, "thinking": 165, "total": 20616649},
       "elapsed_ms": null, "call_ids": ["…"], "segment_ids": ["…"], "execution_ids": ["…"], "evidence": [{"…": "same Evidence shape as prompt overviews"}]}
    ],
    "sequence": [],
    "rule": "prompt-work-v1/observed-operations",
    "coverage": "Types inferred from recorded operations … across 3 prompts."
  },
  "top_output_segment_ids": ["segment#…#turn#242", "…"],
  "top_output_call_ids": ["crew:hellovietnam:ios7#…#turn#1753", "…"],
  "loops": {
    "measured": true,
    "chains": 17, "polls": 153, "unchanged_polls": 123, "progress_polls": 30, "unknown_polls": 0, "processes_polled": 30,
    "poll_calls": 183, "poll_tokens": {"input": 46473, "cache_read": 20563200, "cache_write": 0, "output": 6976, "thinking": 165, "total": 20616649},
    "repeated_reads": 0, "repeated_errors": 0, "repair_loops": 0, "segment_repeats": 122,
    "coverage": "Polling chains need native process identities; this harness reports them."
  }
}
```

Rules:

- `overview` is the crew-level `Overview`: the same categories as `prompt_turns[].overview` summed over every prompt, produced by the same code path (one function, two scopes). Category `tokens` add up to `performance.tokens`; `model_calls` add up to `model_calls`. `sequence` is empty at crew level (the per-prompt sequences already carry order). `coverage` ends with "across N prompts".
- `top_output_segment_ids`: up to five segment ids ordered by `tokens.output + tokens.thinking` descending, ties by id. `top_output_call_ids`: up to five call ids (the `turns[].id` values) by the same key. `top_segment_ids` keeps its meaning (total tokens).
- `loops.measured` is true when native execution status was observed (`freshness.capabilities` contains "native execution status"); when false, every poll number is `0` and `coverage` says polling chains are not measurable for this harness while repeated reads and retries still are. `chains` = number of `process_polling` findings; `polls`/`unchanged_polls`/`progress_polls`/`unknown_polls`/`processes_polled` from `processes[]`; `poll_calls`/`poll_tokens` from segments of kind `wait` (`poll_tokens` is `null` when there is no such segment); `repeated_reads`/`repeated_errors`/`repair_loops` count findings of those kinds; `segment_repeats` = Σ `segments[].repeat_count`.
- **Segment kinds use the work vocabulary** of `work.go`'s `workLabels`: `research`, `instructions`, `write_code`, `edit_code`, `review`, `test`, `coordination`, `wait`, `response`, `mixed`, `unknown`. The old `read`/`poll`/`git`/`edit`/`wrapper`/`debug`/`browser`/`communication` values disappear. `segments[].label` = `workLabels[kind]` + " · " + concrete target (file list for patches, literal command for shell, `process <id>` for polls), still ≤ 100 runes after the label. Findings, processes and executions are unchanged.
- The UI groups nothing silently: `mixed` and `unknown` are shown as their own rows and counted as "unclassified" in the coverage line.

## 2. Track P: projector (crew overview, rankings, loops)

**Owns:** `internal/diagnostics/types.go`, `internal/diagnostics/project.go`, `internal/diagnostics/overview.go`, `internal/diagnostics/loops.go` (new), `internal/diagnostics/summary_test.go` (new), `docs/dashboard.md` (the "Crew performance projection" table only).
**Must not touch:** `executions.go`, `work.go`, `project_test.go`, `overview_test.go`, anything under `internal/dashboard`.

### Task P1: crew-level overview

**Step 1: failing test** in `internal/diagnostics/summary_test.go`:

```go
package diagnostics

import (
	"testing"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

// Two prompts, three calls: the crew overview must be the prompt overviews
// added together, produced by the same classifier, with no sequence.
func TestCrewOverviewSumsPromptCategoriesAndConservesTokens(t *testing.T) {
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p1", 0), promptFact("s", "p2", 100)}}
	for i, tool := range []string{"Bash", "Edit", "Bash"} {
		prompt := "p1"
		if i == 2 {
			prompt = "p2"
		}
		c := fixtureCall(i, prompt)
		in.Calls = append(in.Calls, c)
		command := "go test ./..."
		if tool == "Edit" {
			command = "src.go"
		}
		in.Actions = append(in.Actions, Action{ID: c.ID + "#action", SessionID: "s", CallID: c.ID, Tool: tool, Summary: command, StartedAt: c.StartedAt, EndedAt: c.EndedAt})
	}
	p := Project(in, Options{Now: at(200), Closed: true})
	o := p.Overview
	if len(o.Sequence) != 0 || o.Rule == "" || o.Coverage == "" {
		t.Fatalf("crew overview shape %+v", o)
	}
	var tokens Tokens
	calls := 0
	for _, c := range o.Categories {
		if c.Tokens != nil {
			tokens.Add(*c.Tokens)
		}
		calls += c.ModelCalls
	}
	if tokens != p.Tokens || calls != 3 {
		t.Fatalf("crew categories %+v/%d, ledger %+v", tokens, calls, p.Tokens)
	}
	if categoryByKind(t, o, "test").ModelCalls != 2 || categoryByKind(t, o, "edit_code").ModelCalls != 1 {
		t.Fatalf("crew categories %+v", o.Categories)
	}
	// Per-prompt overviews are untouched by the crew-level one.
	if categoryByKind(t, p.PromptTurns[0].Overview, "test").ModelCalls != 1 || categoryByKind(t, p.PromptTurns[1].Overview, "test").ModelCalls != 1 {
		t.Fatal("prompt overviews changed")
	}
}
```

**Step 2:** `go test ./internal/diagnostics/ -run TestCrewOverview -v` → FAIL (`p.Overview` undefined).

**Step 3: implement.** In `types.go` add `Overview Overview \`json:"overview"\`` to `Performance` (after `Progress`; fields are appended, never inserted, docs/dashboard.md convention). In `overview.go`, refactor `promptOverview(promptID, observations, callKinds)` so the call loop takes a predicate instead of comparing `p.callPrompt[call.ID] != promptID`: extract `func (p *projection) buildOverview(observations []workObservation, callKinds map[string]map[string]bool, include func(callID string) bool) Overview`; `promptOverview` becomes `buildOverview(obs, callKinds, func(id string) bool { return p.callPrompt[id] == promptID })`. In `makeOverviews`, after the per-prompt loop, gather every observation of every known prompt (skip prompts not in `promptIndex`, exactly as `workObservations` does), call `buildOverview(all, byCall, func(string) bool { return true })`, set `Sequence = []WorkStep{}` and append `fmt.Sprintf(" across %d prompts.", len(p.out.PromptTurns))` to `Coverage`, assign to `p.out.Overview`. When there are no calls and no observations, `p.out.Overview = EmptyOverview()`.

**Step 4:** `go test ./internal/diagnostics/` → PASS (all existing overview tests too).

### Task P2: rankings by output + thinking

**Step 1: failing test** (append to `summary_test.go`):

```go
func TestOutputRankingsPreferWhatTheModelWroteNotCacheReads(t *testing.T) {
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p", 0)}}
	heavyCache, heavyOutput, small := fixtureCall(0, "p"), fixtureCall(1, "p"), fixtureCall(2, "p")
	heavyCache.Tokens = Tokens{Input: 10, CacheRead: 900000, Output: 5, Thinking: 1}
	heavyOutput.Tokens = Tokens{Input: 10, CacheRead: 100, Output: 3000, Thinking: 700}
	small.Tokens = Tokens{Input: 1, CacheRead: 1, Output: 1}
	in.Calls = []Call{heavyCache, heavyOutput, small}
	for i, c := range in.Calls {
		tool := []string{"Bash", "Edit", "Read"}[i]
		in.Actions = append(in.Actions, Action{ID: c.ID + "#a", SessionID: "s", CallID: c.ID, Tool: tool, Summary: "x", StartedAt: c.StartedAt, EndedAt: c.EndedAt})
	}
	p := Project(in, Options{Now: at(100), Closed: true})
	if len(p.TopSegmentIDs) == 0 || p.TopSegmentIDs[0] != "segment#"+heavyCache.ID {
		t.Fatalf("total ranking %v", p.TopSegmentIDs)
	}
	if len(p.TopOutputSegmentIDs) != 3 || p.TopOutputSegmentIDs[0] != "segment#"+heavyOutput.ID {
		t.Fatalf("output ranking %v", p.TopOutputSegmentIDs)
	}
	if len(p.TopOutputCallIDs) != 3 || p.TopOutputCallIDs[0] != heavyOutput.ID || p.TopOutputCallIDs[2] != small.ID {
		t.Fatalf("call ranking %v", p.TopOutputCallIDs)
	}
}
```

**Step 2:** run → FAIL. **Step 3:** add `TopOutputSegmentIDs []string \`json:"top_output_segment_ids"\`` and `TopOutputCallIDs []string \`json:"top_output_call_ids"\`` to `Performance` (initialised to `[]string{}` in `Project`), compute in `finish()` next to `TopSegmentIDs`: stable sort copies by `Tokens.Output + Tokens.Thinking` descending, then id ascending, keep five. **Step 4:** PASS.

### Task P3: loops summary

**Step 1: failing tests** (append to `summary_test.go`). Reuse the fixtures of `TestPollingLinksOneJobAndDistinguishesNewOutput`:

```go
func TestLoopsSummarizePollingOnceAndStayUnmeasuredWithoutNativeStatus(t *testing.T) {
	job := execution("job", 0, 60)
	job.ProcessID = "42"
	in := Input{Facts: []telemetry.Fact{job}}
	for i := 0; i < 3; i++ {
		c := fixtureCall(i, "p")
		in.Calls = append(in.Calls, c)
		ref := fmt.Sprintf("poll%d", i)
		in.Actions = append(in.Actions, Action{ID: "s#tool#" + ref, CallID: c.ID, SessionID: "s", Tool: "functions.exec"})
		f := telemetry.Fact{ID: ref, Kind: "tool_call", SessionID: "s", SourceRef: ref, WrapperRef: ref, Tool: "functions.exec", Poll: true, ProcessID: "42", OccurredAt: at(i * 10), SourceOffset: int64(i), Output: &telemetry.Output{SHA256: "result", Bytes: 100}}
		if i == 0 {
			f.Output.NewBytes = n64(0)
		}
		if i == 1 {
			f.Output.NewBytes = n64(12)
		}
		in.Facts = append(in.Facts, f)
	}
	p := Project(in, Options{Now: at(100)})
	l := p.Loops
	if !l.Measured || l.Chains != 1 || l.Polls != 3 || l.UnchangedPolls != 1 || l.ProgressPolls != 1 || l.UnknownPolls != 1 || l.ProcessesPolled != 1 {
		t.Fatalf("loops %+v", l)
	}
	if l.PollCalls != 3 || l.PollTokens == nil || *l.PollTokens != p.Tokens || l.SegmentRepeats != 2 {
		t.Fatalf("poll usage %+v vs ledger %+v", l, p.Tokens)
	}
	claude := Project(Input{Calls: []Call{fixtureCall(0, "p")}}, Options{Now: at(100)})
	if claude.Loops.Measured || claude.Loops.Polls != 0 || claude.Loops.PollTokens != nil || !strings.Contains(claude.Loops.Coverage, "not measurable") {
		t.Fatalf("unmeasured harness invented polling: %+v", claude.Loops)
	}
}
```

(`SegmentRepeats` is 2 because the polling finding marks its segment with `count-1` repeats; adjust the expected value to whatever `markRepeats` produces for this fixture and say so in the test comment. Add `"fmt"` and `"strings"` imports.)

**Step 2:** FAIL. **Step 3:** new file `loops.go`:

```go
package diagnostics

// LoopSummary answers "how often did the Crew repeat itself" in one row.
// Polling numbers come from native process identities, so they are only
// claimed when the harness reported native execution status; the finding
// counters do not depend on that and stay valid for every harness.
type LoopSummary struct {
	Measured        bool    `json:"measured"`
	Chains          int     `json:"chains"`
	Polls           int     `json:"polls"`
	UnchangedPolls  int     `json:"unchanged_polls"`
	ProgressPolls   int     `json:"progress_polls"`
	UnknownPolls    int     `json:"unknown_polls"`
	ProcessesPolled int     `json:"processes_polled"`
	PollCalls       int     `json:"poll_calls"`
	PollTokens      *Tokens `json:"poll_tokens"`
	RepeatedReads   int     `json:"repeated_reads"`
	RepeatedErrors  int     `json:"repeated_errors"`
	RepairLoops     int     `json:"repair_loops"`
	SegmentRepeats  int     `json:"segment_repeats"`
	Coverage        string  `json:"coverage"`
}

func (p *projection) summarizeLoops() { … }
```

Put the type in `loops.go` (not `types.go`) to keep the diff local; add `Loops LoopSummary \`json:"loops"\`` to `Performance`. Call `p.summarizeLoops()` from `finish()` after findings are built and sorted. `Measured = p.capabilities["native execution status"]`. Coverage strings: measured → "Polling chains need native process identities; this harness reports them."; unmeasured → "Polling chains are not measurable for this harness (no native process identities); repeated reads and retries are still detected." **Step 4:** PASS.

### Task P4: contract

Add rows to the table in `docs/dashboard.md` "Crew performance projection" for `overview`, `top_output_segment_ids`/`top_output_call_ids`, `loops`, and change the `segments` row to say kinds use the work vocabulary (`research`, `instructions`, `write_code`, `edit_code`, `review`, `test`, `coordination`, `wait`, `response`, `mixed`, `unknown`). One line each, same voice as the existing rows. Then `gofmt -l internal/diagnostics` (nothing), `go vet ./internal/diagnostics/`, `go test ./internal/diagnostics/`.

**Report:** files changed, test names added, the three `go test` results, and the exact `jq` lines you ran against port 7779 if you restarted it (you normally do not).

## 3. Track C: classifier (one vocabulary, wrappers decoded)

**Owns:** `internal/diagnostics/executions.go`, `internal/diagnostics/work.go`, `internal/diagnostics/project_test.go`, `internal/diagnostics/overview_test.go`, `internal/diagnostics/classify_test.go` (new), and one call site in `internal/diagnostics/findings.go`: `detectReads` currently calls `classify(e, …)` and compares against `"read"`/`"instructions"`; it must use the new vocabulary (`research`/`instructions`) through whatever per-execution helper you keep. Nothing else in `findings.go` changes.
**Must not touch:** `types.go`, `project.go`, `overview.go`, `loops.go`, anything under `internal/dashboard`.

### Task C1: segments speak the work vocabulary

**Step 1: failing tests** in `classify_test.go`:

```go
package diagnostics

import (
	"strings"
	"testing"
)

func TestSegmentKindsUseTheWorkVocabulary(t *testing.T) {
	cases := []struct {
		name         string
		e            Execution
		kind, target string
	}{
		{"poll", Execution{Tool: "functions.exec", IsWrapper: true, Poll: true, ProcessID: "77", Command: `const r = await tools.write_stdin({session_id: 77})`}, "wait", "process 77"},
		{"read", Execution{Tool: "exec_command", Command: "sed -n '20,58p' README.md"}, "research", "sed -n '20,58p' README.md"},
		{"test", Execution{Tool: "exec_command", Command: "set -o pipefail; xcodebuild -scheme HelloVietnam test | tail -40"}, "test", "set -o pipefail; xcodebuild -scheme HelloVietnam test | tail -40"},
		{"git", Execution{Tool: "exec_command", Command: "git add -A && git commit -m x"}, "coordination", "git add -A && git commit -m x"},
		{"instructions", Execution{Tool: "Read", Target: "AGENTS.md"}, "instructions", "AGENTS.md"},
		{"claude edit", Execution{Tool: "Edit", Target: "src/main.go"}, "edit_code", "src/main.go"},
	}
	for _, c := range cases {
		kind, target, label := classifyCall([]Execution{c.e}, "")
		if kind != c.kind || target != c.target {
			t.Errorf("%s: kind=%s target=%q label=%q", c.name, kind, target, label)
		}
		if !strings.HasPrefix(label, workLabels[c.kind]) {
			t.Errorf("%s: label %q does not start with %q", c.name, label, workLabels[c.kind])
		}
	}
}

func TestCodexApplyPatchWrapperIsAnEditWithFileTargets(t *testing.T) {
	patch := `text(await tools.apply_patch("*** Begin Patch\n*** Update File: /w/HelloVietnam/TripViews.swift\n@@\n-a\n+b\n*** Add File: /w/HelloVietnam/New.swift\n+x\n*** End Patch\n"))`
	kind, target, label := classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: patch}}, "/w")
	if kind != "mixed" || !strings.Contains(target, "HelloVietnam/TripViews.swift") || !strings.Contains(target, "HelloVietnam/New.swift") {
		t.Fatalf("kind=%s target=%q label=%q", kind, target, label)
	}
	update := `text(await tools.apply_patch("*** Begin Patch\n*** Update File: /w/HelloVietnam/TripViews.swift\n@@\n-a\n+b\n*** End Patch\n"))`
	kind, target, label = classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: update}}, "/w")
	if kind != "edit_code" || target != "HelloVietnam/TripViews.swift" || !strings.HasPrefix(label, "Edit code / files · HelloVietnam/TripViews.swift") {
		t.Fatalf("kind=%s target=%q label=%q", kind, target, label)
	}
	if strings.Contains(label, "tools.") || strings.Contains(label, "Begin Patch") {
		t.Fatalf("wrapper JavaScript leaked into the label: %q", label)
	}
}

func TestHeredocWriteIsWriteCodeNotResearch(t *testing.T) {
	cmd := "cat >> HelloVietnam/TripService.swift <<'EOF'\nimport MapKit\nEOF"
	kind, target, _ := classifyCall([]Execution{{Tool: "exec_command", Command: cmd}}, "")
	if kind != "write_code" || target != cmd {
		t.Fatalf("kind=%s target=%q", kind, target)
	}
	wrapped := `const r=await tools.exec_command({cmd:"cat >> HelloVietnam/TripService.swift <<'EOF'\nimport MapKit\nEOF"}); text(r)`
	kind, _, label := classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: wrapped}}, "")
	if kind != "write_code" || strings.Contains(label, "tools.") {
		t.Fatalf("kind=%s label=%q", kind, label)
	}
}

func TestStatusEchoIsCoordinationAndScriptsStayUnknownButNamed(t *testing.T) {
	kind, _, label := classifyCall([]Execution{{Tool: "exec_command", Command: `echo "working: polishing layout" >> "$MATE_STATUS"`}}, "")
	if kind != "coordination" || !strings.Contains(label, "working: polishing layout") {
		t.Fatalf("status line kind=%s label=%q", kind, label)
	}
	kind, _, label = classifyCall([]Execution{{Tool: "exec_command", Command: "python3 - <<'PY'\nprint(1)\nPY"}}, "")
	if kind != "unknown" || !strings.HasPrefix(label, "Unclassified activity · python3 - <<'PY'") {
		t.Fatalf("script kind=%s label=%q", kind, label)
	}
	kind, _, _ = classifyCall([]Execution{{Tool: "exec_command", Command: "xcrun xcresulttool get test-results tests --path /tmp/x.xcresult"}}, "")
	if kind != "research" {
		t.Fatalf("xcresulttool kind=%s", kind)
	}
}
```

**Step 2:** `go test ./internal/diagnostics/ -run 'TestSegmentKinds|TestCodexApplyPatch|TestHeredoc|TestStatusEcho' -v` → FAIL.

**Step 3: implement** in `executions.go` and `work.go`:

- `classifyCall` keeps its shape `(executions []Execution, worktree string) (kind, target, label string)` but derives kinds through `executionWork(e)` (work.go) so both classifiers agree; keep the per-execution target derivation (`classifyTarget(e, worktree)`), keep "mixed" when the union of kinds has more than one member, keep `"%d targets"` when several targets exist, and use `workLabels[kind]` for the label prefix. Delete the old `classify` kind switch and its `readCommand`/`buildCommand` regexes if nothing else uses them (`grep -rn buildCommand internal/`).
- Targets: for a wrapper that invokes `tools.apply_patch(`, the target is the comma-joined list of `*** Update File:` / `*** Add File:` / `*** Delete File:` paths normalised against the worktree (a new `patchTargets(command, worktree string) []string` in `work.go`; unescape `\n` inside the JavaScript string literal before scanning lines, `strconv.Unquote` on the literal when it is a plain `"…"`). For a wrapper with `tools.exec_command({cmd:"…"})` the target is the literal command (already `literalCommands`). Polls keep `process <id>` / `unlinked process`.
- `work.go` `commandWork`: `echo`/`printf` with a redirect whose file argument contains `MATE_STATUS` → `coordination` (the Crew talking to the Mate); `xcrun` with `xcresulttool` / `simctl … list|get|diagnose` → `research`; `cp`/`mv`/`touch`/`mkdir -p` → `""` (housekeeping, not a work type); `rm` → `""`; `open`, `plutil -p`, `defaults read` → `research`. Anything else stays `unknown`. `cat`/`tee` with `>`/`>>` already map to `write_code`; make `hasRedirect` accept `>>` (two consecutive `>` words) explicitly with a comment.
- `python3 -`, `perl -e`, `ruby -e`, `node -e` stay `unknown` on purpose (a script is not interpreted); the label still carries the first line of the command so the segment is named.

**Step 4:** run the four tests → PASS. Then run the whole package: `TestWrapperLabelsShowLiteralCommandAndBuildIntent` must still pass (kind `test`); `TestLedgerBucketsBelongToOneContiguousSegmentAndPrompt` must still see 4 segments; update any assertion in `project_test.go`/`overview_test.go` that names an old kind (`read`, `poll`, `git`, `edit`, `wrapper`) and explain the rename in the test's comment. Do not weaken a test to make it pass.

### Task C2: measure the gain on the real recording

Run: `go build -o /private/tmp/claude-501/-Users-anh-Workspace-mate/9c30deb9-f4ab-4745-b5aa-8af8823f1c22/scratchpad/mate-c ./cmd/mate && /private/tmp/claude-501/-Users-anh-Workspace-mate/9c30deb9-f4ab-4745-b5aa-8af8823f1c22/scratchpad/mate-c dashboard /private/tmp/claude-501/-Users-anh-Workspace-mate/9c30deb9-f4ab-4745-b5aa-8af8823f1c22/scratchpad/ws --addr 127.0.0.1:7781 &` then

```bash
curl -s http://127.0.0.1:7781/api/projects/hellovietnam/tasks/ios7 > /tmp/ios7-c.json
jq '.performance.segments | group_by(.kind) | map({kind: .[0].kind, n: length, calls: (map(.model_calls)|add), tokens: (map(.tokens.total)|add)}) | sort_by(-.tokens)' /tmp/ios7-c.json
jq '[.performance.segments[] | select(.kind=="mixed" or .kind=="unknown")] | length' /tmp/ios7-c.json
jq -r '.performance.segments[] | select(.label|test("tools\\.|Begin Patch")) | .label' /tmp/ios7-c.json | head
```

Acceptance: total calls across kinds still 363 and tokens 40,409,527; `mixed`+`unknown` segments ≤ 40 (from 71); no label contains `tools.` or `Begin Patch`; the segment that starts at `…#turn#242` is `edit_code` (or `mixed` with an `edit_code` component) with `TripViews.swift`/`TripService.swift` in its target. Kill your server afterwards (`pkill -f 'addr 127.0.0.1:7781'`). Put the before/after kind table in your report.

## 4. Track U: the first screen (app.js, app.css, node tests)

**Owns:** `internal/dashboard/ui/app.js`, `internal/dashboard/ui/app.css`, `internal/dashboard/ui_observability_test.cjs`, `internal/dashboard/ui_assets_test.go` only if a new asset rule is needed.
**Must not touch:** any Go file outside `ui_assets_test.go`, `humanize.js`, `index.html` unless a new container is unavoidable.

Read first: `renderTask`, `crewPerformance`, `currentWork`, `findingsPanel`, `promptTimelines`, `activityTimeline`, `segmentTable`, `segmentRow`, `segmentEvidence`, `executionList`, `recordingDetails`, `workOverview`, `overviewCategory` in `app.js`; the "Crew observability" and "Prompt overviews" blocks of `app.css`; the whole node test file (its fake DOM supports `querySelectorAll` on `.class`, `[attr]`, `[attr="v"]` and tag names; `open` on details; `click`, `emit("change")`, `focus`). Build the DOM with `el(...)`, `textContent` only, no innerHTML, no external asset.

The fixture in the node test must be updated to the shared contract: segment kinds `research`/`test`/`wait` instead of `read`/`test`/`poll`; add `performance.overview` (crew-level categories summing the prompt's), `performance.loops`, `top_output_segment_ids`, `top_output_call_ids`; keep every existing test passing after adjusting the vocabulary.

### Task U1: the four-answer strip

New `answersStrip(body, p)` rendered by `crewPerformance` right after `currentWork` (before findings), class `answers`, four `section.card.answer` with `data-answer="tokens|work|repeats|expensive"` and an `h2` each. Content:

1. **Tokens**: `fmtTokens(p.tokens.total)` big; `tokenMeter(p.tokens, {label: "buckets"})`; line "thinking N · inside output, not added"; "context after last call " + `fmtTokens(body.ledger.context_tokens_last)` + " · window " + `fmtPct(body.ledger.context_pct)` + " · cost " + `fmtCost(body.ledger.cost)`; "last 5 min +N" from `recent_tokens` (or "historical recording" when `body.ledger.closed`); one row per prompt (oldest first): "P1 21:10 · 3.2M · 61 calls · 9m56s", each a `button.text-button` that opens that prompt's details (`detailState["prompt:"+id] = true; draw()`).
2. **Tool calls by work**: big `body.ledger.tool_count + ""`; sub-line "N native commands (M failed) · K process polls · W wrappers · F file changes" computed from `p.executions` (`!is_wrapper && !poll` = native; `status === "failed"` or non-zero exit = failed; `poll`; `is_wrapper && !poll` = wrappers) and `p.progress.length`; then one bar per `p.overview.categories` sorted by `tokens.total` desc (categories with `tokens == null` go last with "usage not attributed" and no bar), each row: kind chip (`kindChip(kind)`), `model_calls + " calls"`, `execution_count + " cmds"`, `fmtTokens(tokens.total)`, share of `p.tokens.total` as percent (one decimal, `goFixed`), and a track whose width is that share. Footer note: "Counts are model calls per work type; tokens belong to those calls." When `p.overview` is missing → "Work types not recorded yet".
3. **Repeats**: from `p.loops`; when `!loops.measured` the big text is "not measurable" and the body says polling needs native process identities, then still prints repeated reads / errors / repair loops counts. When measured: big `chains + " chains · " + polls + " polls"`; "N polls saw no new output (P%) · M processes were polled"; "Poll calls: N · tokens · S% of the Crew" from `poll_calls`/`poll_tokens` (omit the line when `poll_tokens == null`); "Longest: …" = the `process_polling` finding with the highest `count` (title excerpt, count, unchanged polls parsed from `detail` is not allowed: use the linked `processes[]` entry via `execution_ids`/`poll_ids` to read `unchanged_polls`), with a `button.text-button` that selects that finding (`selectedFinding = id; revealSegments(f.segment_ids, true)`); "Same content re-read: N · same-error retries: N · repair loops: N · repeats inside segments: N".
4. **Most expensive step**: heading "By output + thinking" then the first `top_output_call_ids` entry joined to `body.turns` (`#ordinal`, output, thinking, `fmtMs(duration_ms)`, context after) and to its segment (`segments.find(s => s.call_ids.includes(id))`) with a `button.text-button` that reveals that segment; a second line for `top_output_segment_ids[0]` (label, output + thinking, calls, elapsed). Then heading "By context re-sent" with `top_segment_ids[0]` (label, total, calls, repeat_count, elapsed) and a reveal button. Missing lists → "not ranked yet".

Also add to the `card-head` of the page (after the crew name): chips for `p.runtime.harness_version`, `p.runtime.model` + `effort` when present; and a facts line "spawned HH:MM:SS → closed HH:MM:SS · age · N prompts · N model calls · N tool calls · branch (exists/gone) · N questions · waited …" from `body.ledger`, `body.branch`, `p.prompt_turns.length`. Keep `currentWork` as is; change its "Most tokens:" line to read "Most context re-sent:".

### Task U2: kinds everywhere

- `KINDS` table in app.js: `{research: "Research / inspect", instructions: "Read instructions", write_code: "Write code / files", edit_code: "Edit code / files", review: "Review", test: "Run tests / build", coordination: "Coordinate", wait: "Wait / poll", response: "Respond", mixed: "Mixed activity", unknown: "Unclassified activity"}`; `kindChip(kind)` returns `span.kchip[data-kind]` with a swatch `i` and the label; `segmentLabel` falls back to this table.
- CSS: `[data-kind]` colours by token, assigned to the entity: `research` and `instructions` → `--series-1`; `write_code`/`edit_code` → `--series-3`; `test` → `--series-4`; `wait` → `--series-2`; `review`/`coordination` → `--ink-2`; `response` → `--muted`; `mixed`/`unknown` → hatched `repeating-linear-gradient(135deg, var(--muted) 0 3px, transparent 3px 6px)`. Apply the same mapping to `.timeline-segment[data-kind=…]` (fill, opacity 0.2) and `.segment-step[data-kind]` (left border). Remove the dead `edit`/`editing`/`build_test` selectors.
- Findings: a `span.kchip.finding-kind` before the title: `execution_failure` → "failure · warning", `process_polling`/`repeated_read`/`repeated_error`/`repair_test_loop` → "repeat · info" (severity from the finding), `long_execution` → "slow", `large_output` → "output", `context_reset` → "context", `decision_wait` → "wait".

### Task U3: timeline lanes

In `activityTimeline`: (a) segment rects get the kind colours through `data-kind` (already set); (b) under the token line, a lane "Output" with one 3px bar per call whose height is `(output + thinking) / max` of the prompt's calls (data from `body.turns` joined by `call_ids`), title "call #N · X output + thinking"; (c) the "Polls" lane: group the prompt's `executions` with `poll === true` by `process_id`; for groups of ≥ 3 draw a bracket path under the ticks and a `text` label "×N" plus the process's `tokens.total` from `p.processes` when present; (d) a legend row under the SVG built from `KINDS` for the kinds present in this prompt plus "failed command". Increase the viewBox height as needed; labels use class `timeline-label`; every `rect`/`path` keeps a `<title>`.

### Task U4: tool mix by prompt

New section after the prompt timelines: `h2 "Tool mix by prompt"`, a `table.tool-mix` (wrapped in `.table-wrap`) with one row per prompt (oldest first, "P1 · " + first 24 chars of the prompt) and columns: calls, tokens, then one column per kind present in `p.overview.categories` ordered by crew tokens desc, then "repeats" (Σ `repeat_count` of the prompt's segments) and "failed cmds" (native failed executions of that prompt). Cells read "calls · tokens" (`fmtTokens`), a dash when the prompt has no such category. Last row "Crew" from `p.overview`. Below: the note "Each model call belongs to exactly one work type, so rows and columns add up to the ledger."

### Task U5: segment table

- Header row (`.segment-numbers` grows to 10 columns; phone → 5): add "Kind" (chip), "Prompt" ("P" + 1-based index of `prompt_id` in `p.prompt_turns` + clock of `started_at`), "Tool calls" (Σ `tool_count` of `body.turns` in `call_ids`; "?" when a call is missing) and "Thinking" (`tokens.thinking`).
- Filter chips above the table: "All N" plus one per kind present with its count; state `segmentKindFilter` (default "all"); a chip click sets it and `draw()`; chips carry `data-focus="segment-kind:<kind>"` and `aria-pressed`.
- Sort select gains `{value: "output", text: "Output + thinking, highest first"}` sorting by `tokens.output + tokens.thinking`.
- Coverage line at the end of `recordingDetails` (and mirrored as one `p.coverage-note` under the table): "Unclassified segments: N of M · S% of tokens · O% of output" from kinds `mixed` + `unknown`.

### Task U6: tests

Add to `ui_observability_test.cjs` (each a separate `test(...)`):

1. "four answers use overview, loops and rankings without inventing numbers": strip renders; work card lists categories with "calls" and tokens, a category with `tokens: null` shows "usage not attributed" and no `.track`; repeats card shows "3 chains" wording from the fixture's loops; expensive card shows "#3" (the fixture's highest output call) and clicking its button opens the right segment (`open === true`).
2. "unmeasured polling says so": `loops.measured = false` → text "not measurable", no "polls" count.
3. "kind filter and output sort": clicking chip `segment-kind:test` leaves only `segment-test` in `[data-segment]`; sort "output" orders by output + thinking.
4. "tool mix rows add up to the crew row": table exists, last row text contains the crew calls and tokens.
5. "timeline draws poll brackets with counts": the fixture gets three poll executions for process 77 → an SVG `text` with "×3".
6. Update existing assertions that named old kinds.

Run `node --test internal/dashboard/ui_observability_test.cjs` → all pass; `go test ./internal/dashboard/ -run 'TestUI|TestAssets' ` for the asset rules. Then load the real page for a visual check: `chrome-devtools-axi newpage http://127.0.0.1:7779/#/p/hellovietnam/t/ios7` (the 7779 server is the integrator's; if its JSON lacks `overview`/`loops`, your UI must degrade to "not recorded yet" without errors), `resize 1440 1000`, `screenshot`, `resize 500 900`, `screenshot`, and an overflow probe: `eval "() => document.documentElement.scrollWidth + ' ' + document.documentElement.clientWidth"` must print equal numbers at both sizes. Close the page you opened.

**Report:** functions added/changed, CSS classes added, test names, node and go results, the two screenshot paths and the overflow probe output.

## 5. Integration (integrator, after all three tracks report)

1. `gofmt -l . ; go vet ./... ; go build -o /private/tmp/claude-501/-Users-anh-Workspace-mate/9c30deb9-f4ab-4745-b5aa-8af8823f1c22/scratchpad/mate ./cmd/mate`.
2. Restart 7779 with the new binary and check the contract on `ios7`:

```bash
J=/private/tmp/claude-501/-Users-anh-Workspace-mate/9c30deb9-f4ab-4745-b5aa-8af8823f1c22/scratchpad/ios7-final.json
curl -s http://127.0.0.1:7779/api/projects/hellovietnam/tasks/ios7 > $J
jq '.performance | {calls: .model_calls, total: .tokens.total, ov_calls: ([.overview.categories[].model_calls]|add), ov_tokens: ([.overview.categories[].tokens.total // 0]|add)}' $J      # 363 · 40409527 · 363 · 40409527
jq '.performance.loops' $J                                                                            # measured true, chains 17, polls 153, unchanged 123, progress 30, processes_polled 30, poll_calls 183, poll_tokens.total 20616649
jq -r '.performance.top_output_call_ids[0]' $J                                                        # …#turn#1753
jq -r '.performance.top_segment_ids[0]' $J                                                            # …#turn#1971
jq '[.performance.segments[] | select(.kind=="mixed" or .kind=="unknown")] | length' $J              # ≤ 40
jq -r '.performance.segments[].kind' $J | sort | uniq -c                                              # only work-vocabulary kinds
jq -r '.performance.segments[] | select(.label|test("tools\\.|Begin Patch")) | .label' $J | wc -l    # 0
```

3. `make check` (gofmt, vet, every test; skips must be the declared live ones).
4. Browser acceptance at 1440×1000 and 500×900 in the tool's Chrome: first screen shows the four cards with the ios7 numbers above, no horizontal overflow, console has no errors, light and dark themes readable; screenshots saved beside the design artifact as `docs/evidence/crew-observer-first-screen-2026-09-28/crew-observer-after-*.png`.
5. Code review: `go-reviewer` on the diagnostics diff (`diff -ru <baseline>/internal/diagnostics internal/diagnostics`), `code-reviewer` on the UI diff; fix Critical/Important findings; re-run 3–4.
6. Evidence: `docs/evidence/crew-observer-first-screen-2026-09-28.md` with the measured before/after (unnamed segments 71 → N, kinds table, loops numbers, screenshots), the commands above, and the rollout note (7777 stays the old build until the captain runs D6).

Not in this batch: D6 (close console, migrate schema, restart), D7 (`pricing.yaml` row for gpt-6-sol), Claude-specific polling detection, the multi-crew page.
