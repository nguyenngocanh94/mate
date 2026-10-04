// Builds busy.json beside it - run `node gen.cjs busy.json` from this
// directory after changing it. busy.json is a busy workspace in
// the API's own shapes, modelled on the Mate Office design boards (shop,
// docs-site, infra), so the office's busy and stuck states can be tested and
// looked at although the real scratch workspace is quiet.
const fs = require("fs");
const NOW = Date.parse("2026-10-03T14:32:07Z");
const at = (msAgo) => new Date(NOW - msAgo).toISOString().replace(/\.(\d{3})Z$/, ".$1000000Z");
const m = 60000, h = 3600000, d = 86400000;
const tokens = (input, cr, cw, out, think = 0) => ({ input, cache_read: cr, cache_write: cw, output: out, thinking: think, total: input + cr + cw + out });

function task(o) {
  return Object.assign({
    crew: "", text: "", branch: "mate/" + o.crew, state: "", since: "", target: "", detail: "",
    closed: false, spawned_at: at(2 * h), age_ms: 2 * h, turns: 10, tokens: tokens(0, 0, 0, 0), cost: null,
    last_model: "", context_tokens_last: 0, context_pct: null, question_count: 0, handback_count: 0,
    waited_ms: 0, tool_count: 0, harness: "claude"
  }, o);
}

const shopTasks = [
  task({ crew: "rd1", text: "Cart summary redesign", state: "at_desk_working", since: at(6 * m), turns: 31, harness: "claude",
    tokens: tokens(17300, 110800, 11500, 4300, 900), cost: 0.52, context_tokens_last: 82000, context_pct: 41 }),
  task({ crew: "api-fix", text: "Retry order create on 409", state: "waiting_review", since: at(11 * m), target: "mate", detail: "handback",
    turns: 24, tokens: tokens(10600, 67900, 7100, 2600), cost: 0.32, context_tokens_last: 74000, context_pct: 37 }),
  task({ crew: "ui-polish", text: "Button and spacing polish", state: "waiting_at_ceo", since: at(3 * m), target: "mate", detail: "question",
    turns: 12, question_count: 1, tokens: tokens(6200, 40100, 4200, 1500), cost: 0.19, context_tokens_last: 44000, context_pct: 22 }),
  task({ crew: "db-migrate", text: "Order indexes and backfill", state: "blocked", since: at(23 * m), detail: "stale",
    turns: 57, tokens: tokens(41200, 228600, 30100, 10500), cost: 1.12, context_tokens_last: 176000, context_pct: 88 }),
  task({ crew: "login-flow", text: "Login flow", state: "gone", since: at(2 * d), detail: "merged", closed: true, close_state: "finished",
    closed_at: at(2 * d), merged_at: at(2 * d), tokens: tokens(40000, 340000, 15000, 7000), cost: 1.4 }),
  task({ crew: "search-facets", text: "Search facets", state: "gone", since: at(4 * d), detail: "failed", closed: true, close_state: "failed",
    closed_at: at(4 * d), tokens: tokens(13000, 110000, 7000, 3000), cost: null }),
  task({ crew: "order-email", text: "Order email", state: "gone", since: at(9 * d), closed: true, close_state: "finished",
    closed_at: at(9 * d), merged_at: at(9 * d), tokens: tokens(15000, 130000, 8000, 2200), cost: 0.6 }),
  task({ crew: "healthcheck", text: "Healthcheck", state: "gone", since: at(23 * d), closed: true, close_state: "finished",
    closed_at: at(23 * d), merged_at: at(23 * d), tokens: tokens(2000, 15000, 1200, 500), cost: 0.07 })
];

const docsTasks = [
  task({ crew: "copy-edit", text: "Tone pass on getting started", state: "asleep", since: at(47 * m), detail: "stale", harness: "codex",
    turns: 5, tokens: tokens(3500, 7600, 0, 1000), context_tokens_last: 9000, context_pct: 4.5 }),
  task({ crew: "nav", text: "Sidebar navigation rewrite", state: "at_desk_working", since: at(2 * m), harness: "codex",
    turns: 19, tokens: tokens(18800, 40800, 0, 5100), context_tokens_last: 74000, context_pct: 29 }),
  task({ crew: "i18n", text: "Locale scaffolding", state: "walking_to_ceo", since: at(1 * m), target: "mate", detail: "question", harness: "codex",
    turns: 9, tokens: tokens(6300, 13700, 0, 1800), context_tokens_last: 36000, context_pct: 14 }),
  task({ crew: "seo", text: "Meta tags and sitemap", state: "idle", since: at(5 * m), harness: "pi",
    turns: 6, tokens: tokens(1000, 6500, 600, 300), context_tokens_last: 8000, context_pct: 4 }),
  task({ crew: "search", text: "Site search index", state: "arriving", since: at(20 * 1000), harness: "pi",
    turns: 0, tokens: tokens(0, 0, 0, 0), context_tokens_last: 0, context_pct: null })
];

const infraTasks = [
  task({ crew: "ci-cache", text: "Cache Go modules in CI", state: "gone", since: at(3 * h), detail: "runtime_lost", turns: 33,
    tokens: tokens(11700, 75000, 7800, 2800), cost: 0.35, context_tokens_last: 0, context_pct: null }),
  task({ crew: "tf-upgrade", text: "Terraform upgrade", state: "gone", since: at(9 * d), closed: true, close_state: "finished",
    closed_at: at(9 * d), merged_at: at(9 * d), tokens: tokens(25000, 170000, 11000, 4000), cost: 0.8 })
];

function mate(o) {
  return Object.assign({ harness: "claude", running: true, state: "idle", since: at(9 * m), target: "", detail: "",
    tokens_today: 0, context_pct: null, context_tokens: null, turns: 0, tokens: tokens(0, 0, 0, 0), cost: null, last_turn: null }, o);
}

const mates = {
  shop: mate({ state: "deciding", since: at(14 * m), target: "api-fix", tokens_today: 1800000, context_pct: 62, context_tokens: 124000,
    turns: 140, tokens: tokens(283200, 1880000, 194600, 42200, 8000), cost: 6.31,
    last_turn: { id: "mate:shop#t140", ordinal: 140, started_at: at(70 * 1000), ended_at: at(38 * 1000), duration_ms: 32000, tokens: tokens(1200, 90000, 800, 400), context_tokens_after: 124000, context_pct: 62, tool_count: 2, ref: {} } }),
  "docs-site": mate({ harness: "codex", state: "idle", since: at(9 * m), tokens_today: 240000, context_pct: 18, context_tokens: 72000,
    turns: 60, tokens: tokens(177000, 390000, 0, 45000), cost: null,
    last_turn: { id: "mate:docs-site#t60", ordinal: 60, started_at: at(10 * m), ended_at: at(9 * m), duration_ms: 60000, tokens: tokens(1000, 9000, 0, 300), context_tokens_after: 72000, context_pct: 18, tool_count: 1, ref: {} } }),
  infra: mate({ running: false, state: "gone", since: at(3 * h), tokens_today: 0, turns: 80, tokens: tokens(50000, 340000, 20000, 8000), cost: 1.5 })
};

const env = { generated_at: at(0), last_event_id: 900 };
const inbox = {
  shop: [{ seq: 4, at: at(3 * m), kind: "status", source: "crew", target: "crew:ui-polish", crew: "ui-polish", verb: "needs-decision", text: "keep the 6px button radius or move to 8px?", attention: true }],
  "docs-site": [], infra: []
};
const tasks = { shop: shopTasks, "docs-site": docsTasks, infra: infraTasks };

const projects = {};
const now = [];
for (const name of ["shop", "docs-site", "infra"]) {
  projects[name] = Object.assign({}, env, { project: name, mode: name === "shop" ? "manual" : "auto", mate: mates[name], tasks: tasks[name], inbox: inbox[name] });
  const mm = mates[name];
  now.push({ actor_id: "mate:" + name, actor: "mate", actor_kind: "mate", project: name, state: mm.state, since: mm.since, target: mm.target, detail: mm.detail,
    tokens_today: mm.tokens_today, context_pct: mm.context_pct, context_tokens: mm.context_tokens });
  now.push({ actor_id: "user:" + name, actor: "captain", actor_kind: "user", project: name, state: "", tokens_today: 0, context_pct: null, context_tokens: null });
  for (const t of tasks[name]) {
    if (t.closed) continue;
    const today = { rd1: 98200, "api-fix": 88200, "ui-polish": 52000, "db-migrate": 196000, "copy-edit": 4300, nav: 41500, i18n: 21800, seo: 8400 }[t.crew];
    // `search` has no scene row yet: its tokens today are unknown, not 0.
    if (t.crew === "search") continue;
    now.push({ actor_id: "crew:" + name + ":" + t.crew, actor: t.crew, actor_kind: "crew", project: name, state: t.state, since: t.since, target: t.target, detail: t.detail,
      tokens_today: today || 0, context_pct: t.context_pct, context_tokens: t.context_tokens_last || null });
  }
}

const workspace = Object.assign({}, env, { root: "/Users/captain/studio", projects: ["shop", "docs-site", "infra"].map(name => ({
  name, mode: projects[name].mode,
  mate: { harness: mates[name].harness, running: mates[name].running, state: mates[name].state, since: mates[name].since, tokens_today: mates[name].tokens_today, context_pct: mates[name].context_pct, context_tokens: mates[name].context_tokens },
  crews_by_state: tasks[name].reduce((a, t) => (a[t.state] = (a[t.state] || 0) + 1, a), {}),
  inbox_waiting: inbox[name].length
})) });

// One crew's task page: db-migrate, stuck, with turns spread over the last
// six hours for the spark.
const dbTurns = [];
for (let i = 0; i < 12; i++) {
  dbTurns.push({ id: "crew:shop:db-migrate#t" + i, ordinal: i, started_at: at((330 - i * 25) * m), ended_at: at((329 - i * 25) * m), duration_ms: 60000,
    outcome: "tool_use", model: "claude-sonnet", tokens: tokens(1000 * (i + 1), 8000 * (i + 1), 500, 300), context_tokens_after: 100000 + i * 6000, context_pct: 50 + i * 3, tool_count: 1, ref: {} });
}
const dbTask = Object.assign({}, env, {
  project: "shop", crew: "db-migrate", ledger: shopTasks[3], turns: dbTurns,
  status_lines: [
    { event_id: 700, at: at(40 * m), verb: "working", text: "writing 0042_add_order_index", line: "working: writing 0042_add_order_index", ref: {} },
    { event_id: 720, at: at(23 * m), verb: "working", text: "Running 0042_add_order_index", line: "working: Running 0042_add_order_index", ref: {} }
  ],
  questions: [], branch: { name: "mate/db-migrate", exists: true },
  performance: {
    current_segment_id: "seg-run",
    segments: [{ id: "seg-run", kind: "test", label: "Run tests / build · migrate up", target: "db/migrations/0042_add_order_index.sql", started_at: at(23 * m) }],
    prompt_turns: [{ id: "p1", prompt: "Read the brief", overview: { steps: [
      { kind: "research", label: "Research / inspect", started_at: at(5 * h), model_calls: 4, tokens: tokens(4000, 30000, 1000, 800), open: false },
      { kind: "test", label: "Run tests / build", started_at: at(23 * m), model_calls: 2, tokens: tokens(2000, 20000, 500, 300), open: true }
    ] } }],
    skills: [{ name: "db-migrations", count: 2, loads: [] }]
  }
});

const shopMate = Object.assign({}, env, { project: "shop", mate: mates.shop, exchanges: [
  { id: "prompt#1", source: "crew", prompt: "api-fix is ready", started_at: at(50 * m), overview: { steps: [
    { kind: "review", label: "Review", started_at: at(50 * m), model_calls: 3, tokens: tokens(5000, 300000, 2000, 900) },
    { kind: "coordination", label: "Coordinate", started_at: at(14 * m), model_calls: 2, tokens: tokens(3000, 200000, 1000, 600), open: true }
  ] } }
] });

const out = { now_ms: NOW, workspace, now: Object.assign({}, env, { now }), projects, tasks: { "shop/db-migrate": dbTask }, mates: { shop: shopMate } };
fs.writeFileSync(process.argv[2], JSON.stringify(out, null, 1) + "\n");
console.log("ok", process.argv[2]);
