// Focused interaction checks for the dependency-free dashboard. Run with:
// node --test internal/dashboard/ui_observability_test.cjs
// This small DOM implements only browser primitives used by app.js; layout
// and accessibility still need the real-browser acceptance check.
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

class Node {
  constructor(tag, owner) {
    this.tagName = tag;
    this.owner = owner;
    this.children = [];
    this.attrs = {};
    this.listeners = {};
    this.parentNode = null;
    this._text = "";
    this._open = false;
  }
  set textContent(value) {
    this._text = String(value);
    for (const child of this.children) child.parentNode = null;
    this.children = [];
  }
  get textContent() { return this._text + this.children.map(child => child.textContent).join(""); }
  set className(value) { this.attrs.class = value; }
  get className() { return this.attrs.class || ""; }
  set open(value) {
    if (this._open === value) return;
    this._open = value;
    queueMicrotask(() => this.emit("toggle"));
  }
  get open() { return this._open; }
  get isConnected() { return this === this.owner.root || !!(this.parentNode && this.parentNode.isConnected); }
  setAttribute(key, value) { this.attrs[key] = String(value); if (key === "open") this.open = true; }
  getAttribute(key) { return this.attrs[key] == null ? null : this.attrs[key]; }
  appendChild(child) {
    if (child.parentNode) child.parentNode.children = child.parentNode.children.filter(node => node !== child);
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
  emit(name) { for (const fn of this.listeners[name] || []) fn({ target: this }); }
  click() { this.emit("click"); }
  focus() { this.owner.activeElement = this; }
  scrollIntoView() { this.owner.scrolledTo = this; }
  matches(selector) {
    if (selector[0] === ".") return this.className.split(" ").includes(selector.slice(1));
    const attr = /^\[([^=\]]+)(?:="([^"]*)")?\]$/.exec(selector);
    if (attr) return attr[2] == null ? this.getAttribute(attr[1]) != null : this.getAttribute(attr[1]) === attr[2];
    return this.tagName === selector;
  }
  querySelectorAll(selector) {
    const matches = [];
    for (const child of this.children) {
      if (child.matches(selector)) matches.push(child);
      matches.push(...child.querySelectorAll(selector));
    }
    return matches;
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
}

// Output and thinking default to small fixed values; a call that must rank
// first by output + thinking passes its own.
const tokens = (input, cache = 0, output = 10, thinking = 4) => ({ input, cache_read: cache, cache_write: 0, output, thinking, total: input + cache + output });
const at = seconds => new Date(Date.UTC(2026, 8, 28, 10, 0, seconds)).toISOString();
// One category per work kind, tokens additive because each call belongs to
// exactly one of them. `instructions` has an execution but no attributed
// call, so its tokens are null rather than 0.
const baseOverview = coverage => ({
  summary: "Run tests / build (1 call) · Wait / poll (1 call) · Research / inspect (1 call) · Read instructions (1 execution)",
  rule: "prompt-work-v1/observed-operations", coverage, sequence: [],
  categories: [
    { kind: "wait", label: "Wait / poll", model_calls: 1, execution_count: 1, tokens: tokens(50, 300, 40, 20), elapsed_ms: null,
      call_ids: ["call-3"], segment_ids: ["segment-poll"], execution_ids: ["exec-poll"], evidence: [] },
    { kind: "test", label: "Run tests / build", model_calls: 1, execution_count: 1, tokens: tokens(10, 400), elapsed_ms: 10000,
      call_ids: ["call-2"], segment_ids: ["segment-test"], execution_ids: ["exec-test"], evidence: [] },
    { kind: "research", label: "Research / inspect", model_calls: 1, execution_count: 0, tokens: tokens(100, 200), elapsed_ms: null,
      call_ids: ["call-1"], segment_ids: ["segment-read"], execution_ids: [], evidence: [] },
    { kind: "instructions", label: "Read instructions", model_calls: 0, execution_count: 1, tokens: null, elapsed_ms: 200,
      call_ids: [], segment_ids: [], execution_ids: [], evidence: [] }
  ]
});
// Segment kinds use the work vocabulary the projector shares with prompt
// overviews (docs/plans/2026-09-28-crew-observer-first-screen.md section 1):
// the old `read` and `poll` kinds are `research` and `wait` on the wire.
const fixture = () => ({
  project: "shop", crew: "test-build", generated_at: at(40), last_event_id: 1,
  ledger: { text: "Investigate build", state: "at_desk_working", turns: 3, tokens: tokens(160, 900), tool_count: 4, harness: "codex" },
  branch: { name: "mate/test-build", exists: true }, status_lines: [], questions: [],
  turns: [
    { id: "call-1", ordinal: 1, started_at: at(0), ended_at: at(10), duration_ms: 10000, tokens: tokens(100, 200), outcome: "tool", tool_count: 1, context_tokens_after: 300 },
    { id: "call-2", ordinal: 2, started_at: at(10), ended_at: at(20), duration_ms: 10000, tokens: tokens(10, 400), outcome: "tool", tool_count: 2, context_tokens_after: 410 },
    { id: "call-3", ordinal: 3, started_at: at(20), ended_at: at(30), duration_ms: 10000, tokens: tokens(50, 300, 40, 20), outcome: "tool", tool_count: 1, context_tokens_after: 390 }
  ],
  performance: {
    version: "v1", generated_at: at(40), tokens: tokens(160, 900), model_calls: 3,
    current_segment_id: "segment-poll", top_segment_ids: ["segment-test"], top_finding_ids: ["finding-poll"],
    top_output_segment_ids: ["segment-poll", "segment-read", "segment-test"], top_output_call_ids: ["call-3", "call-1", "call-2"],
    recent_window_ms: 300000, recent_tokens: tokens(160, 900),
    time: { prompt_elapsed_ms: 40000, tool_elapsed_ms: 20000, invocation_ms: 25000, unallocated_ms: 20000, decision_wait_ms: 0 },
    freshness: { last_observed_at: at(40), last_ingested_at: at(40), last_usage_at: at(30), stale: false,
      capabilities: ["native execution status"], missing: ["One child process has no response link"] },
    prompt_turns: [{ id: "prompt-1", prompt: "<img src=x onerror=alert(1)> Run the build", prompt_at: at(0), started_at: at(0),
      elapsed_ms: 40000, model_calls: 3, tokens: tokens(160, 900), segment_ids: ["segment-read", "segment-test", "segment-poll"], coverage: "native",
      overview: baseOverview("Types inferred from recorded operations.") }],
    segments: [
      { id: "segment-read", prompt_id: "prompt-1", label: "Read build config", kind: "research", started_at: at(0), ended_at: at(10), elapsed_ms: 10000,
        tokens: tokens(100, 200), model_calls: 1, call_ids: ["call-1"], execution_ids: [], repeat_count: 0, outcome: "completed" },
      { id: "segment-test", prompt_id: "prompt-1", label: "Run TripUITests", kind: "test", started_at: at(10), ended_at: at(20), elapsed_ms: 10000,
        tokens: tokens(10, 400), model_calls: 1, call_ids: ["call-2"], execution_ids: ["exec-wrapper", "exec-test"], repeat_count: 2, outcome: "failed" },
      { id: "segment-poll", prompt_id: "prompt-1", label: "Check TripUITests progress", kind: "wait", started_at: at(20), elapsed_ms: 20000,
        tokens: tokens(50, 300, 40, 20), model_calls: 1, call_ids: ["call-3"], execution_ids: ["exec-poll"], repeat_count: 3, outcome: "running" }
    ],
    // The crew-level overview is the prompt overviews summed by the projector
    // (one prompt here, so the same categories with a crew coverage line).
    overview: baseOverview("Types inferred from recorded operations across 1 prompts."),
    // One chain of three polls on process 77, two without new output; the
    // finding counters are zero because nothing repeated a read or an error.
    loops: { measured: true, chains: 1, polls: 3, unchanged_polls: 2, progress_polls: 1, unknown_polls: 0, processes_polled: 1,
      poll_calls: 1, poll_tokens: tokens(50, 300, 40, 20), repeated_reads: 0, repeated_errors: 0, repair_loops: 0, segment_repeats: 5,
      coverage: "Polling chains need native process identities; this harness reports them." },
    findings: [{ id: "finding-poll", kind: "process_polling", title: "TripUITests was polled 3 times", detail: "2 polls had no new output",
      severity: "info", confidence: "observed", ongoing: true, count: 3, tokens: null, segment_ids: ["segment-poll"], call_ids: ["call-3"], execution_ids: ["exec-poll"],
      evidence: [{ id: "source-1", kind: "execution", label: "Process 77", source_ref: { path: "rollout.jsonl", offset: 400 } }], review: "Review the wait interval" }],
    executions: [
      { id: "exec-wrapper", prompt_id: "prompt-1", tool: "exec", is_wrapper: true, status: "completed", output_bytes: null, new_output_bytes: null },
      { id: "exec-test", prompt_id: "prompt-1", tool: "CommandExecution", command: "xcodebuild -only-testing:TripUITests", process_id: "77",
        started_at: at(10), ended_at: at(20), duration_ms: 10000, status: "failed", exit_code: 65, output_bytes: 82, new_output_bytes: null,
        error_signature: "Simulator unavailable", output_excerpt: "<script>alert('unsafe')</script> simulator unavailable", source_ref: { path: "rollout.jsonl", offset: 200 } },
      { id: "exec-poll", prompt_id: "prompt-1", tool: "write_stdin", poll: true, process_id: "77", started_at: at(20), status: "running",
        output_bytes: 0, new_output_bytes: 0, duration_ms: null, source_ref: { path: "rollout.jsonl", offset: 400 } }
    ],
    processes: [{ id: "process-77", command: "xcodebuild -only-testing:TripUITests", execution_ids: ["exec-test"], poll_ids: ["exec-poll"],
      polls: 3, unchanged_polls: 2, progress_polls: 1, unknown_polls: 0, tokens: null }]
  }
});

async function boot(body = fixture(), hash = "#/p/shop/t/test-build") {
  const doc = { activeElement: null };
  doc.root = new Node("body", doc);
  const ids = {};
  for (const id of ["view", "crumbs", "live"]) { ids[id] = new Node("div", doc); doc.root.appendChild(ids[id]); }
  const liveText = new Node("span", doc); liveText.className = "text"; ids.live.appendChild(liveText);
  doc.getElementById = id => ids[id];
  doc.createElement = tag => new Node(tag, doc);
  doc.createElementNS = (_, tag) => new Node(tag, doc);
  doc.createTextNode = text => { const node = new Node("#text", doc); node.textContent = text; return node; };
  const requests = [], pendingEvents = [], intervals = [];
  let current = body, now = Date.parse(at(40));
  class Clock extends Date { static now() { return now; } }
  const window = { scrollY: 260, addEventListener() {}, scrollTo(_, y) { this.scrollY = y; } };
  const context = vm.createContext({ document: doc, window, location: { hash },
    Date: Clock, setTimeout, setInterval: fn => intervals.push(fn),
    fetch(url) {
      requests.push(url);
      if (url.startsWith("/api/events")) return new Promise(resolve => pendingEvents.push(resolve));
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(current) });
    }
  });
  for (const file of ["humanize.js", "app.js"]) vm.runInContext(fs.readFileSync(path.join(__dirname, "ui", file), "utf8"), context);
  const flush = () => new Promise(resolve => setImmediate(resolve));
  await flush();
  return { doc, view: ids.view, window, requests, intervals, flush,
    async refresh(next = current, changed = false) {
      current = next;
      const reply = pendingEvents.shift();
      assert.ok(reply, "one long poll is outstanding");
      reply({ ok: true, status: 200, json: () => Promise.resolve({ last_event_id: 1, events: changed ? [{}] : [], now: [] }) });
      await flush();
    },
    advance(ms) { now += ms; intervals.forEach(fn => fn()); }
  };
}

const keyed = (view, attribute, value) => view.querySelectorAll(`[${attribute}]`).find(node => node.getAttribute(attribute) === value);

test("finding opens exact evidence and related segment without inventing call usage", async () => {
  const app = await boot();
  assert.match(app.view.textContent, /Check TripUITests progress/);
  assert.match(app.view.textContent, /usage not linked to model calls/);
  keyed(app.view, "data-focus", "finding:finding-poll").click();
  const segment = keyed(app.view, "data-segment", "segment-poll");
  assert.equal(segment.open, true);
  assert.equal(segment.getAttribute("data-selected"), "true");
  assert.equal(keyed(app.view, "data-segment", "segment-test").open, false);
  assert.match(app.view.querySelector("[data-evidence-panel]").textContent, /3 polls · 1 with new output · 2 unchanged/);
  assert.equal(app.doc.scrolledTo, app.view.querySelector("[data-evidence-panel]"));
  assert.equal(app.view.querySelectorAll("img").length, 0, "prompt markup remains text");
});

test("native child exit overrides wrapper success and unknown output stays unknown", async () => {
  const app = await boot();
  keyed(app.view, "data-focus", "top-segment").click();
  await app.flush();
  const segment = keyed(app.view, "data-segment", "segment-test");
  assert.match(segment.textContent, /exit 65 · failed/);
  assert.match(segment.textContent, /xcodebuild -only-testing:TripUITests/);
  assert.match(segment.textContent, /wrapper result · child exit unknown/);
  assert.match(segment.textContent, /output unknown · new output unknown/);
  assert.match(segment.textContent, /Simulator unavailable/);
  assert.equal(segment.querySelectorAll("script").length, 0, "tool output is inert text");
});

test("a top finding opens an unlinked native error without searching the segment list", async () => {
  const body = fixture();
  const p = body.performance;
  const failed = p.executions.find(e => e.id === "exec-test");
  failed.call_id = "";
  p.segments[1].execution_ids = ["exec-wrapper"];
  p.findings = [{ id: "native-error", title: "TripUITests failed twice with exit 65", detail: "Simulator unavailable", count: 2,
    confidence: "native exit", tokens: null, segment_ids: [], call_ids: [], execution_ids: [failed.id], evidence: [] }];
  p.top_finding_ids = ["native-error"];
  const app = await boot(body);
  keyed(app.view, "data-focus", "finding:native-error").click();
  const panel = app.view.querySelector("[data-evidence-panel]");
  assert.ok(panel);
  assert.match(panel.textContent, /xcodebuild -only-testing:TripUITests/);
  assert.match(panel.textContent, /exit 65 · failed/);
  assert.equal(app.doc.scrolledTo, panel);
  assert.equal(app.view.querySelectorAll("[data-segment]").filter(node => node.open).length, 0);
});

test("primary work labels keep wrapper JavaScript in evidence instead of the first screen", async () => {
  const body = fixture();
  const p = body.performance;
  p.segments[2].label = 'Build / test · const r = await tools.exec_command({cmd:"make check"});';
  p.segments[2].kind = "test";
  p.segments[2].execution_ids = ["exec-wrapper"];
  const app = await boot(body);
  const label = keyed(app.view, "data-focus", "current-work");
  // The fallback label comes from the shared KINDS table, so the `test` kind
  // reads "Run tests / build" (the projector's workLabels), no longer the
  // old "Build / test" wording of the retired segment vocabulary.
  assert.equal(label.textContent, "Run tests / build · native command not linked");
  assert.doesNotMatch(label.textContent, /const r/);
});

test("running native execution before usage is labelled pending, and synthetic gaps stay out of primary labels", async () => {
  const body = fixture();
  const current = body.performance.segments[2];
  current.model_calls = 0;
  current.call_ids = [];
  current.tokens = { input: 0, cache_read: 0, cache_write: 0, output: 0, total: 0 };
  current.label = "Activity not classified · turn.gap";
  const app = await boot(body);
  assert.equal(keyed(app.view, "data-focus", "current-work").textContent, "Activity not recorded");
  assert.match(app.view.querySelector(".work-current").textContent, /Usage not reported for this activity yet/);
  assert.doesNotMatch(app.view.querySelector(".work-current").textContent, /0 model calls · 0 tokens/);
  assert.match(keyed(app.view, "data-segment", current.id).textContent, /Total\?/);
});

test("sort keeps repeated segments and live refresh retains evidence, focus and scroll", async () => {
  const app = await boot();
  keyed(app.view, "data-focus", "top-segment").click();
  await app.flush();
  const select = keyed(app.view, "data-focus", "segment-sort");
  select.value = "input";
  select.focus();
  select.emit("change");
  assert.deepEqual(app.view.querySelectorAll("[data-segment]").map(node => node.getAttribute("data-segment")), ["segment-read", "segment-poll", "segment-test"]);
  await app.refresh();
  assert.equal(keyed(app.view, "data-segment", "segment-test").open, true);
  assert.match(keyed(app.view, "data-segment", "segment-test").textContent, /exit 65/);
  assert.equal(app.doc.activeElement.getAttribute("data-focus"), "segment-sort");
  assert.equal(app.window.scrollY, 260);
  assert.ok(app.requests.some(url => url.includes("wait=5")));
});

test("elapsed and recording age advance locally without interpolating usage or fetching", async () => {
  const app = await boot();
  const requests = app.requests.length;
  const elapsed = app.view.querySelector("[data-elapsed-start]");
  const before = elapsed.textContent;
  const metrics = app.view.querySelector(".work-metrics").textContent;
  app.advance(35000);
  assert.notEqual(elapsed.textContent, before);
  assert.equal(app.view.querySelector(".work-metrics").textContent, metrics);
  assert.equal(app.requests.length, requests);
  assert.equal(app.view.querySelector("[data-observed-at]").textContent, "Recording is stale");
});

test("missing diagnostics gives an explicit coverage message and leaves totals reachable", async () => {
  const body = fixture();
  delete body.performance;
  const app = await boot(body);
  assert.match(app.view.textContent, /Work diagnostics are not available yet/);
  assert.ok(keyed(app.view, "data-detail", "ledger"));
  assert.ok(keyed(app.view, "data-detail", "raw-calls"));
});

test("launch configuration and observed runtime remain distinct with missing document sizes unknown", async () => {
  const body = fixture();
  body.performance.runtime = { model: "actual-model", effort: "high", harness_version: "0.157" };
  body.performance.observed_inputs = [{ id: "instructions-1", kind: "instruction", path: "AGENTS.md", bytes: 42, sha256: "observed-hash", at: at(2), source_ref: { path: "rollout.jsonl", offset: 80 } }];
  body.performance.profile = { repo: "shop", harness: "codex", requested_model: "requested-model", requested_effort: "xhigh", repo_commit: "abc123",
    captured_at: at(0), repo_dirty: false, documents: [{ path: "CLAUDE.md", state: "missing", bytes: 0 }] };
  const app = await boot(body);
  const recording = keyed(app.view, "data-detail", "recording-details");
  assert.match(recording.textContent, /Observed runtime.*actual-model/);
  assert.match(recording.textContent, /Requested model requested-model/);
  assert.match(recording.textContent, /AGENTS.md · 42 B/);
  assert.match(recording.textContent, /CLAUDE.md · missing · size unknown/);
  assert.match(recording.textContent, /not proof that the harness loaded it/);
});

function workOverviewFixture() {
  return {
    summary: "Researched the setup, edited code, and ran tests twice.", rule: "recorded-work-types-v1", coverage: "Mixed calls are charged once; one execution has no call link.",
    categories: [
      { kind: "research", label: "Research", model_calls: 1, execution_count: 1, tokens: tokens(100, 200), elapsed_ms: 4000,
        call_ids: ["call-1"], segment_ids: ["segment-read"], execution_ids: [], evidence: [{ id: "research-1", kind: "command", label: "Read build settings", at: at(2), command: "cat Makefile", status: "completed", exit_code: 0, source_ref: { path: "rollout.jsonl", offset: 10 } }] },
      { kind: "edit_code", label: "Edit code", model_calls: 0, execution_count: 0, tokens: null, elapsed_ms: null,
        call_ids: [], segment_ids: [], execution_ids: [], evidence: [{ id: "edit-1", kind: "file_change", label: "Changed Trip.swift", at: at(5), source_ref: { path: "rollout.jsonl", offset: 20 } }] },
      { kind: "test", label: "Run tests", model_calls: 0, execution_count: 2, tokens: null, elapsed_ms: 10000,
        call_ids: [], segment_ids: ["segment-test"], execution_ids: ["exec-test"], evidence: [{ id: "test-1", kind: "command", label: "TripUITests failed", at: at(10), command: "xcodebuild -only-testing:TripUITests", status: "failed", exit_code: 65,
          output_excerpt: "<script>unsafe()</script> simulator unavailable", source_ref: { path: "rollout.jsonl", offset: 30 } }] },
      { kind: "mixed", label: "Mixed activity", model_calls: 1, execution_count: 0, tokens: tokens(10, 400), elapsed_ms: null,
        call_ids: ["call-2"], segment_ids: ["segment-test"], execution_ids: [], evidence: [] },
      { kind: "unknown", label: "Unknown", model_calls: 1, execution_count: 0, tokens: tokens(50, 300), elapsed_ms: null,
        call_ids: ["call-3"], segment_ids: ["segment-poll"], execution_ids: [], evidence: [] }
    ],
    sequence: [
      { kind: "research", label: "Research", started_at: at(0), segment_ids: ["segment-read"] },
      { kind: "edit_code", label: "Edit code", started_at: at(5), segment_ids: [] },
      { kind: "test", label: "Run tests", started_at: at(10), segment_ids: ["segment-test"] },
      { kind: "edit_code", label: "Edit code", started_at: at(15), segment_ids: [] },
      { kind: "test", label: "Run tests", started_at: at(20), segment_ids: ["segment-poll"] }
    ]
  };
}

test("Crew prompt summary shows work types and ordered repeated stages link exact evidence", async () => {
  const body = fixture();
  body.performance.prompt_turns[0].overview = workOverviewFixture();
  const app = await boot(body);
  const prompt = keyed(app.view, "data-detail", "prompt:prompt-1");
  prompt.open = false;
  const summary = prompt.querySelector("summary");
  assert.match(summary.textContent, /Researched the setup, edited code, and ran tests twice/);
  assert.deepEqual(summary.querySelectorAll(".work-kind").map(node => node.textContent), ["Research", "Edit code", "Run tests", "Mixed activity", "Unknown"]);
  assert.equal(summary.querySelectorAll("button").length, 0, "collapsed summary contains no nested buttons");
  assert.equal(summary.querySelectorAll("a").length, 0, "collapsed summary contains no nested links");
  const panel = keyed(app.view, "data-overview", "crew-overview:prompt-1");
  assert.deepEqual(panel.querySelectorAll(".overview-stage").map(node => node.textContent), ["1. Research", "2. Edit code", "3. Run tests", "4. Edit code", "5. Run tests"]);
  keyed(app.view, "data-focus", "crew-overview:prompt-1:stage:2").click();
  assert.equal(keyed(app.view, "data-segment", "segment-test").open, true);
  assert.match(keyed(app.view, "data-segment", "segment-test").textContent, /exit 65/);
});

test("overview keeps mixed call tokens intact and unknown attribution out of zero-cost categories", async () => {
  const body = fixture();
  body.performance.prompt_turns[0].overview = workOverviewFixture();
  const app = await boot(body);
  const mixed = keyed(app.view, "data-detail", "crew-overview:prompt-1:category:mixed");
  const tests = keyed(app.view, "data-detail", "crew-overview:prompt-1:category:test");
  const edits = keyed(app.view, "data-detail", "crew-overview:prompt-1:category:edit_code");
  assert.match(mixed.querySelector("summary").textContent, /420 tokens/);
  assert.match(tests.querySelector("summary").textContent, /usage not attributed/);
  assert.match(tests.querySelector("summary").textContent, /0 attributed calls · 2 recorded executions/);
  assert.doesNotMatch(tests.querySelector("summary").textContent, /0 tokens/);
  assert.match(edits.querySelector("summary").textContent, /time unknown/);
  assert.doesNotMatch(edits.querySelector("summary").textContent, /0 executions|0 recorded executions/);
  assert.equal(tests.querySelectorAll(".meter").length, 0, "unattributed categories do not draw a zero-token meter");
  assert.equal(mixed.querySelectorAll(".meter").length, 1, "mixed usage is displayed once in its category");
});

test("Mate overview works without a Crew performance blob and retains expanded category on refresh", async () => {
  const overview = workOverviewFixture();
  const body = { project: "shop", last_event_id: 1, mate: { harness: "claude", turns: 3, tokens: tokens(160, 900) },
    exchanges: [{ id: "exchange-1", source: "captain", prompt: "Fix the build", response: "Investigated and handed back", model_calls: 3, tool_calls: 4,
      started_at: at(0), prompt_at: at(0), duration_ms: 30000, tokens: tokens(160, 900), activities: [], overview }] };
  const app = await boot(body, "#/p/shop/mate");
  const exchange = keyed(app.view, "data-detail", "exchange:exchange-1");
  assert.equal(exchange.open, false);
  assert.match(exchange.querySelector("summary").textContent, /Research.*Edit code.*Run tests/);
  assert.equal(exchange.querySelector("summary").querySelectorAll("button").length, 0);
  exchange.open = true;
  const category = keyed(app.view, "data-detail", "mate-overview:exchange-1:category:test");
  category.open = true;
  category.querySelector("summary").focus();
  await app.flush();
  assert.match(category.textContent, /xcodebuild -only-testing:TripUITests/);
  assert.match(category.textContent, /exit 65 · failed/);
  assert.match(category.textContent, /rollout.jsonl:30/);
  assert.equal(category.querySelectorAll("script").length, 0);
  await app.refresh(body, true);
  assert.equal(keyed(app.view, "data-detail", "exchange:exchange-1").open, true);
  assert.equal(keyed(app.view, "data-detail", "mate-overview:exchange-1:category:test").open, true);
  assert.equal(app.doc.activeElement.getAttribute("data-focus"), "detail:mate-overview:exchange-1:category:test");
});

test("header names the harness from the ledger and adds the observed version only when recorded", async () => {
  const app = await boot();
  const chips = app.view.querySelector(".head-chips");
  assert.ok(chips, "harness chip rendered from ledger.harness");
  assert.match(chips.textContent, /codex/);
  assert.doesNotMatch(chips.textContent, /undefined|null/);
  const body = fixture();
  body.performance.runtime = { model: "m", effort: "high", harness_version: "0.157.1" };
  const versioned = await boot(body);
  assert.match(versioned.view.querySelector(".head-chips").textContent, /codex 0\.157\.1/);
  assert.match(versioned.view.querySelector(".head-chips").textContent, /m · high/);
});

test("four answers use overview, loops and rankings without inventing numbers", async () => {
  const app = await boot();
  const strip = app.view.querySelector(".answers");
  assert.ok(strip, "the four-answer strip is rendered");
  assert.equal(strip.querySelectorAll("[data-answer]").length, 4);
  const work = keyed(strip, "data-answer", "work");
  assert.equal(work.querySelector(".big").textContent, "4", "the big number is the ledger's tool count");
  assert.match(work.textContent, /1 native command \(1 failed\) · 1 process poll · 1 wrapper · file changes not recorded/);
  const bars = work.querySelectorAll(".answer-bar");
  assert.deepEqual(bars.map(bar => bar.querySelector(".kchip").textContent), ["Run tests / build", "Wait / poll", "Research / inspect", "Read instructions"],
    "categories are ordered by tokens (420, 390, 310) with the unattributed one last");
  assert.match(bars[0].textContent, /1 call · 1 cmd · 420 · 39\.3%/);
  assert.match(bars[3].textContent, /usage not attributed/);
  assert.doesNotMatch(bars[3].textContent, /\b0 tokens|0%/);
  assert.equal(bars[3].querySelectorAll(".track").length, 0, "no bar is drawn for a category without attributed usage");
  assert.equal(bars[0].querySelectorAll(".track").length, 1);
  const repeats = keyed(strip, "data-answer", "repeats");
  assert.match(repeats.textContent, /1 chain · 3 polls/);
  assert.match(repeats.textContent, /2 polls saw no new output \(66\.7%\) · 1 process was polled/);
  assert.match(repeats.textContent, /Poll calls: 1 · 390 tokens/);
  assert.match(repeats.textContent, /Longest: .*TripUITests was polled 3 times.*3 polls · 2 unchanged/);
  assert.match(repeats.textContent, /Same content re-read: 0 · same-error retries: 0 · repair loops: 0 · repeats inside segments: 5/);
  const expensive = keyed(strip, "data-answer", "expensive");
  assert.match(expensive.textContent, /By output \+ thinking/);
  assert.match(expensive.textContent, /call #3 · 40 output \+ 20 thinking · 10s · context after 390/);
  assert.match(expensive.textContent, /By context re-sent/);
  assert.match(expensive.textContent, /Run TripUITests · 420 tokens · 1 call · 2 repeats · 10s/);
  const tokensCard = keyed(strip, "data-answer", "tokens");
  assert.match(tokensCard.textContent, /thinking 4 · inside output, not added/);
  assert.match(tokensCard.textContent, /context after last call \? · window \? · cost \?/, "an unknown ledger value is a question mark, never a zero");
  assert.match(tokensCard.textContent, /P1 .* · 1.1k · 3 calls · 40s/);
  keyed(app.view, "data-focus", "answer-top-call").click();
  await app.flush();
  assert.equal(keyed(app.view, "data-segment", "segment-poll").open, true, "the top output call opens its own segment");
  assert.equal(keyed(app.view, "data-segment", "segment-test").open, false);
  assert.match(app.view.querySelector(".work-hotspot").textContent, /Most context re-sent:/);
});

test("unmeasured polling says so", async () => {
  const body = fixture();
  body.performance.loops = { measured: false, chains: 0, polls: 0, unchanged_polls: 0, progress_polls: 0, unknown_polls: 0, processes_polled: 0,
    poll_calls: 0, poll_tokens: null, repeated_reads: 2, repeated_errors: 1, repair_loops: 0, segment_repeats: 5,
    coverage: "Polling chains are not measurable for this harness (no native process identities); repeated reads and retries are still detected." };
  const app = await boot(body);
  const repeats = keyed(app.view.querySelector(".answers"), "data-answer", "repeats");
  assert.match(repeats.textContent, /not measurable/);
  assert.match(repeats.textContent, /native process identities/);
  assert.doesNotMatch(repeats.textContent, /\d+ polls?\b/, "an unmeasured harness prints no poll count, not even 0");
  assert.match(repeats.textContent, /Same content re-read: 2 · same-error retries: 1 · repair loops: 0/);
  delete body.performance.loops;
  delete body.performance.overview;
  delete body.performance.top_output_call_ids;
  delete body.performance.top_output_segment_ids;
  const older = await boot(body);
  const strip = older.view.querySelector(".answers");
  assert.match(keyed(strip, "data-answer", "repeats").textContent, /Repeats not recorded yet/);
  assert.match(keyed(strip, "data-answer", "work").textContent, /Work types not recorded yet/);
  assert.match(keyed(strip, "data-answer", "expensive").textContent, /not ranked yet/);
  assert.match(older.view.textContent, /Work types not recorded yet/);
});

test("kind filter and output sort", async () => {
  const app = await boot();
  const chip = keyed(app.view, "data-focus", "segment-kind:test");
  assert.equal(chip.getAttribute("aria-pressed"), "false");
  assert.match(chip.textContent, /Run tests \/ build/);
  assert.match(keyed(app.view, "data-focus", "segment-kind:all").textContent, /All 3/);
  chip.click();
  assert.deepEqual(app.view.querySelectorAll("[data-segment]").map(node => node.getAttribute("data-segment")), ["segment-test"]);
  assert.equal(keyed(app.view, "data-focus", "segment-kind:test").getAttribute("aria-pressed"), "true");
  keyed(app.view, "data-focus", "segment-kind:all").click();
  const select = keyed(app.view, "data-focus", "segment-sort");
  select.value = "output";
  select.emit("change");
  assert.deepEqual(app.view.querySelectorAll("[data-segment]").map(node => node.getAttribute("data-segment")), ["segment-poll", "segment-read", "segment-test"],
    "output + thinking ranks the poll segment first and keeps ties in time order");
  const row = keyed(app.view, "data-segment", "segment-test");
  assert.match(row.textContent, /Run tests \/ build/);
  // The clock is local time, so only the prompt tag's shape is asserted.
  assert.match(row.textContent, /P1 \d\d:\d\d/);
  assert.match(row.textContent, /Tool calls2/);
  assert.match(row.textContent, /Thinking4/);
  assert.match(app.view.textContent, /Unclassified segments: 0 of 3/);
});

test("tool mix rows add up to the crew row", async () => {
  const app = await boot();
  const table = app.view.querySelector(".tool-mix");
  assert.ok(table, "the tool mix table is rendered");
  assert.ok(table.parentNode.className.includes("table-wrap"));
  // The instructions category has executions but no attributed call, so it
  // gets no column: it would be dashes in every row.
  assert.deepEqual(table.querySelectorAll("th").map(node => node.textContent),
    ["Prompt", "Calls", "Tokens", "Run tests / build", "Wait / poll", "Research / inspect", "Repeats", "Failed cmds"]);
  const rows = table.querySelectorAll("tbody")[0].querySelectorAll("tr");
  assert.equal(rows.length, 2, "one prompt row and the crew row");
  assert.match(rows[0].textContent, /P1 · <img src=x onerror=alert…/, "the prompt is quoted as text, cut at 24 characters");
  assert.match(rows[0].textContent, /1 · 390/);
  assert.equal(rows[0].querySelectorAll("img").length, 0, "prompt markup remains text");
  const crew = rows[rows.length - 1];
  assert.deepEqual(crew.querySelectorAll("td").map(node => node.textContent), ["Crew", "3", "1.1k", "1 · 420", "1 · 390", "1 · 310", "5", "1"],
    "the crew row is the ledger: 3 calls, 1.1k tokens, 5 repeats, 1 failed native command");
  assert.match(app.view.textContent, /Each model call belongs to exactly one work type/);
});

test("timeline draws poll brackets with counts", async () => {
  const body = fixture();
  const p = body.performance;
  p.executions.push(
    { id: "exec-poll-2", prompt_id: "prompt-1", tool: "write_stdin", poll: true, process_id: "77", started_at: at(25), ended_at: at(26), status: "completed", output_bytes: 0, new_output_bytes: 0 },
    { id: "exec-poll-3", prompt_id: "prompt-1", tool: "write_stdin", poll: true, process_id: "77", started_at: at(30), ended_at: at(31), status: "completed", output_bytes: 4, new_output_bytes: 4 });
  p.processes[0].poll_ids = ["exec-poll", "exec-poll-2", "exec-poll-3"];
  const app = await boot(body);
  const svg = app.view.querySelector(".activity-timeline").querySelector("svg");
  assert.ok(svg.querySelectorAll("text").some(node => /×3/.test(node.textContent)), "a chain of three polls is labelled with its count");
  assert.equal(svg.querySelectorAll(".timeline-poll").length, 3);
  assert.equal(svg.querySelectorAll(".timeline-bracket").length, 1);
  assert.equal(svg.querySelectorAll(".timeline-output").length, 3, "one output bar per model call of the prompt");
  assert.ok(svg.querySelectorAll("title").some(node => /call #3 · 60 output \+ thinking/.test(node.textContent)));
  const legend = app.view.querySelector(".timeline-legend");
  assert.deepEqual(legend.querySelectorAll(".item").map(node => node.textContent), ["Research / inspect", "Run tests / build", "Wait / poll", "failed command"]);
  const finding = keyed(app.view, "data-focus", "finding:finding-poll");
  assert.match(finding.querySelector(".finding-kind").textContent, /repeat · info/);
});

test("Mate refreshes observed execution time after a quiet five-second poll without inventing usage", async () => {
  const body = { project: "shop", last_event_id: 1, mate: { harness: "claude", turns: 3, tokens: tokens(160, 900) },
    exchanges: [{ id: "active-exchange", source: "captain", prompt: "Run tests", model_calls: 3,
      started_at: at(0), prompt_at: at(0), tokens: tokens(160, 900), activities: [], overview: workOverviewFixture() }] };
  const app = await boot(body, "#/p/shop/mate");
  const key = "mate-overview:active-exchange:category:test";
  assert.match(keyed(app.view, "data-detail", key).querySelector("summary").textContent, /10s observed execution time/);
  const pageRequests = app.requests.filter(url => url === "/api/projects/shop/mate").length;
  const next = structuredClone(body);
  next.exchanges[0].overview.categories.find(category => category.kind === "test").elapsed_ms = 15000;
  await app.refresh(next);
  assert.equal(app.requests.filter(url => url === "/api/projects/shop/mate").length, pageRequests + 1);
  assert.ok(app.requests.filter(url => url.startsWith("/api/events")).every(url => url.includes("wait=5")));
  assert.match(keyed(app.view, "data-detail", key).querySelector("summary").textContent, /15s observed execution time/);
  assert.match(keyed(app.view, "data-detail", key).querySelector("summary").textContent, /usage not attributed/);
  assert.match(keyed(app.view, "data-detail", "mate-overview:active-exchange:category:mixed").querySelector("summary").textContent, /420 tokens/);
});

// The stages the projector folds a prompt's calls into, with the skill two
// loads of which the card places. Stage tokens are the calls' own, so they
// add up to the prompt and to the task, which the header says.
const step = (kind, label, calls, executions, usage, extra = {}) => ({ kind, label, started_at: at(0), ended_at: at(10), elapsed_ms: 10000,
  model_calls: calls.length, executions, tokens: usage, call_ids: calls, segment_ids: [], skills: [], parts: [kind], open: false, ...extra });
const stepsFixture = () => {
  const body = fixture();
  const prompt = body.performance.prompt_turns[0];
  prompt.overview.steps = [
    step("instructions", "Read instructions", ["call-1"], 1, tokens(100, 200), { skills: ["token-review"], segment_ids: ["segment-read"] }),
    step("mixed", "Mixed activity", ["call-2"], 2, tokens(10, 400), { parts: ["research", "test"], segment_ids: ["segment-test"] }),
    step("response", "Respond", ["call-3"], 0, tokens(50, 300, 40, 20), { elapsed_ms: null, open: true })
  ];
  // The three calls' own usage: 310 + 420 + 390.
  prompt.tokens = body.performance.tokens = tokens(160, 900, 60, 24);
  body.performance.skills = [
    { name: "token-review", count: 2, loads: [
      { at: at(1), prompt_id: "prompt-1", call_id: "call-1", execution_id: "exec-skill", via: "tool" },
      { at: at(2), prompt_id: "prompt-1", call_id: "", execution_id: "exec-unlinked", via: "read", path: ".claude/skills/token-review/SKILL.md" }] }
  ];
  return body;
};
const cells = row => row.querySelectorAll("[role]").filter(node => node.getAttribute("role") === "cell").map(node => node.textContent);
const pinsOf = node => node.querySelectorAll(".sc-pin").map(pin => pin.textContent);

test("steps card lists each stage in order with its type, counts, and an open stage in words", async () => {
  const app = await boot(stepsFixture());
  const card = app.view.querySelector(".steps-card");
  assert.ok(card, "the steps card is rendered");
  assert.match(card.querySelector(".sc-sub").textContent, /Agent test-build\|3 stages/);
  const rows = card.querySelectorAll(".sc-stage");
  assert.deepEqual(rows.map(row => row.getAttribute("data-kind")), ["instructions", "mixed", "response"]);
  assert.deepEqual(cells(rows[0]), ["1", "Read instructions1", "1", "1", "310", "10s"]);
  assert.deepEqual(cells(rows[1]), ["2", "Mixed activityResearch / inspect + Run tests / build", "1", "2", "420", "10s"],
    "a mixed stage names what it mixed");
  assert.deepEqual(cells(rows[2]), ["3", "Respondin progress", "1", "–", "390", "?"],
    "an open stage says so in words, no tool call is a dash, and unknown time is ? rather than 0");
  assert.equal(rows[2].getAttribute("data-open"), "true");
  assert.match(card.querySelector("[data-band=\"sequence\"]").querySelector(".sc-aside").textContent, /3 stages · #3 in progress/);
  const pieces = card.querySelector(".sc-strip").querySelectorAll(".sc-piece");
  assert.deepEqual(pieces.map(piece => piece.getAttribute("data-kind")), ["instructions", "mixed", "response"], "one strip piece per stage, in order");
  assert.equal(pieces[2].getAttribute("data-open"), "true");
  assert.match(pieces[2].getAttribute("title"), /in progress/);
  assert.doesNotMatch(card.textContent, /xcodebuild|rollout\.jsonl/, "the card names work types, never commands or files");
});

test("steps card places each skill load on its stage and says how it was loaded", async () => {
  const app = await boot(stepsFixture());
  const skills = keyed(app.view, "data-skills", "true");
  assert.equal(skills.querySelector(".sc-aside").textContent, "1 skill · 2 loads");
  const row = keyed(skills, "data-skill", "token-review");
  assert.deepEqual(pinsOf(row), ["1"]);
  assert.match(row.textContent, /token-review×2/);
  assert.match(row.querySelector(".sc-skill-where").textContent, /^skill tool and read SKILL\.md · loaded in/);
  assert.deepEqual(row.querySelectorAll(".sc-load").map(chip => chip.textContent), ["#1Read instructions", "stage not linked"],
    "a load without a call link is listed and not given a stage");
  assert.match(skills.textContent, /does not show that the skill was followed/);
});

test("a stage opens the work segments behind it", async () => {
  const app = await boot(stepsFixture());
  keyed(app.view, "data-focus", "step:segment-test").click();
  assert.equal(keyed(app.view, "data-segment", "segment-test").open, true);
});

test("steps card says what is not recorded instead of drawing zeros", async () => {
  const older = await boot();
  const card = older.view.querySelector(".steps-card");
  assert.match(card.textContent, /Steps not recorded yet/);
  assert.match(card.textContent, /Skills not recorded yet/);
  assert.match(card.querySelector(".sc-sub").textContent, /stages not recorded/);
  assert.equal(card.querySelectorAll(".sc-stage").length, 0);
  assert.equal(card.querySelectorAll(".sc-effort").length, 0, "no effort table of zeros");
  assert.equal(card.querySelectorAll(".sc-strip").length, 0);

  const body = stepsFixture();
  body.performance.skills = [];
  const none = await boot(body);
  assert.match(keyed(none.view, "data-skills", "true").textContent, /No skill was loaded in this recording/);
});

test("a long task shows its first twelve stages and keeps the rest one click away", async () => {
  const body = stepsFixture();
  body.performance.prompt_turns[0].overview.steps = Array.from({ length: 30 }, (_, i) =>
    step(i % 2 ? "test" : "edit_code", i % 2 ? "Run tests / build" : "Edit code / files", ["call-" + i], 1, tokens(10, 20)));
  const app = await boot(body);
  const card = app.view.querySelector(".steps-card");
  const later = keyed(card, "data-detail", "steps-later:test-build");
  assert.match(later.querySelector("summary").textContent, /Continue through 18 later stages/);
  assert.equal(card.querySelectorAll(".sc-stage").length, 30, "every stage is in the page");
  assert.equal(later.querySelectorAll(".sc-stage").length, 18);
  assert.equal(later.querySelectorAll(".sc-stage")[0].getAttribute("data-stage"), "13");
  assert.equal(card.querySelector(".sc-strip").querySelectorAll(".sc-piece").length, 30, "the strip still shows the whole task");
});

test("the effort table is summed from the stages and its total matches the header", async () => {
  const app = await boot(stepsFixture());
  const card = app.view.querySelector(".steps-card");
  const header = card.querySelectorAll(".sc-big").map(node => node.textContent);
  const total = cells(keyed(card, "data-effort", "total"));
  assert.deepEqual(total, ["Total", "3", "3", header[0], "", "100%"]);
  assert.equal(total[1], header[1], "model calls agree with the header");
  assert.deepEqual(cells(keyed(card, "data-effort", "instructions")), ["Read instructions", "1", "1", "310", "", "27.7%"]);
  assert.deepEqual(cells(keyed(card, "data-effort", "response")), ["Respond", "1", "–", "390", "", "34.8%"]);
  assert.equal(card.querySelectorAll(".coverage-note").filter(note => /not in a recorded stage/.test(note.textContent)).length, 0);
});

test("every work type is listed in a fixed order, a type with no call with dashes", async () => {
  const app = await boot(stepsFixture());
  const rows = app.view.querySelector(".steps-card").querySelectorAll(".sc-effort").filter(row => row.getAttribute("data-effort") !== "total");
  assert.deepEqual(rows.map(row => row.getAttribute("data-effort")),
    ["research", "write_code", "edit_code", "test", "review", "coordination", "wait", "instructions", "response", "mixed", "unknown"]);
  const write = rows[1];
  assert.equal(write.getAttribute("data-empty"), "true");
  assert.deepEqual(cells(write), ["Write code / files", "–", "–", "–", "", "–"]);
  assert.equal(write.querySelector(".sc-track").children.length, 0, "an empty track");
  assert.equal(rows[9].getAttribute("data-empty"), null, "mixed has a call here");
});

test("stages are numbered continuously across prompts with a separator per later prompt", async () => {
  const body = stepsFixture();
  body.performance.prompt_turns.push({ id: "prompt-2", prompt: "Fix the build and rerun it", prompt_at: at(50), started_at: at(50),
    elapsed_ms: 5000, model_calls: 2, tokens: tokens(20, 40), segment_ids: [], overview: { categories: [], sequence: [], steps: [
      step("edit_code", "Edit code / files", ["call-4"], 1, tokens(10, 20)),
      step("test", "Run tests / build", ["call-5"], 1, tokens(10, 20))] } });
  const app = await boot(body);
  const card = app.view.querySelector(".steps-card");
  const stagesTable = card.querySelector(".sc-stages");
  const rows = stagesTable.querySelectorAll(".sc-row").filter(row => !/sc-th/.test(row.className));
  assert.deepEqual(rows.map(row => row.getAttribute("data-stage") || row.getAttribute("data-prompt")), ["1", "2", "3", "prompt-2", "4", "5"]);
  assert.match(rows[3].textContent, /^P2 \d\d:\d\d · Fix the build and rerun it$/);
  const pieces = card.querySelector(".sc-strip").querySelectorAll(".sc-piece");
  assert.deepEqual(pieces.map(piece => piece.getAttribute("data-prompt-start")), [null, null, null, "true", null]);
  assert.match(card.querySelector(".sc-sub").textContent, /5 stages/);
});

test("a skill pin carries the skill's place in the skills list, on every stage that loaded it", async () => {
  const body = stepsFixture();
  body.performance.skills = [
    { name: "alpha", count: 2, loads: [{ call_id: "call-1", via: "tool" }, { call_id: "call-3", via: "tool" }] },
    { name: "beta", count: 1, loads: [{ call_id: "call-2", via: "read" }] }
  ];
  const app = await boot(body);
  const card = app.view.querySelector(".steps-card");
  assert.deepEqual(card.querySelector(".sc-strip").querySelectorAll(".sc-piece").map(pinsOf), [["1"], ["2"], ["1"]]);
  assert.deepEqual(card.querySelectorAll(".sc-stage").map(pinsOf), [["1"], ["2"], ["1"]]);
  assert.deepEqual(keyed(card, "data-skills", "true").querySelectorAll("li").map(pinsOf), [["1"], ["2"]]);
  assert.deepEqual(keyed(card, "data-skill", "alpha").querySelectorAll(".sc-load").map(chip => chip.textContent), ["#1Read instructions", "#3Respond"]);
  assert.match(keyed(card, "data-skill", "beta").textContent, /read SKILL\.md · loaded in#2Mixed activity/);
});

test("a /usage suffix opens the task tier and scrolls to the token card once, keeping the plain crumb", async () => {
  const app = await boot(fixture(), "#/p/shop/t/test-build/usage");
  assert.match(app.view.textContent, /Check TripUITests progress/, "the task tier renders as without the suffix");
  assert.equal(app.doc.scrolledTo, keyed(app.view, "data-answer", "tokens"));
  const crumb = app.doc.root.children.find(node => node.children.length && node.querySelectorAll("a").some(a => a.textContent === "test-build"));
  assert.equal(crumb.querySelectorAll("a").pop().getAttribute("href"), "#/p/shop/t/test-build", "breadcrumbs stay plain");
  app.doc.scrolledTo = null;
  await app.refresh(fixture(), true);
  assert.equal(app.doc.scrolledTo, null, "later polls do not scroll again");

  const plain = await boot();
  assert.equal(plain.doc.scrolledTo, undefined, "old URLs do not scroll");
});
