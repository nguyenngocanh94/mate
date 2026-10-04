/*
 * app.js - the three tiers of docs/mvp.md M6, client-side routed by hash
 * and kept current by one long poll on /api/events.
 *
 *   #/                       workspace: one card per project
 *   #/p/<project>            project: the Mate, the task table, the inbox
 *   #/p/<project>/mate       Mate: one exchange per prompt and reply
 *   #/p/<project>/t/<crew>   task: the ledger, the turn timeline, the diff
 *
 * Read-only, by construction: nothing here issues anything but a GET. Every
 * action stays in the console TUI, which is where a reader who can act
 * already is.
 *
 * The DOM is built node by node rather than from strings. A workspace's
 * timeline quotes whatever the captain typed and whatever a harness printed,
 * and textContent is the only way to be sure a task called
 * `<img onerror=...>` renders as a task name.
 */

(function () {
  "use strict";

  // ------------------------------------------------------------ vocabulary

  // The scene states of internal/timeline/scene. Each carries a glyph as
  // well as a tone: colour alone must never be the only difference between
  // "working" and "blocked".
  var STATES = {
    "": { word: "unplaced", glyph: "○", tone: "muted" },
    arriving: { word: "arriving", glyph: "→", tone: "muted" },
    at_desk_working: { word: "at desk working", glyph: "▶", tone: "good" },
    walking_to_ceo: { word: "walking to mate", glyph: "↗", tone: "warning" },
    waiting_at_ceo: { word: "waiting at mate", glyph: "?", tone: "warning" },
    waiting_review: { word: "waiting review", glyph: "◆", tone: "serious" },
    leaving: { word: "leaving", glyph: "←", tone: "muted" },
    gone: { word: "gone", glyph: "×", tone: "muted" },
    blocked: { word: "blocked", glyph: "!", tone: "critical" },
    asleep: { word: "asleep", glyph: "·", tone: "muted" },
    idle: { word: "idle", glyph: "○", tone: "muted" },
    reading: { word: "reading", glyph: "▤", tone: "info" },
    deciding: { word: "deciding", glyph: "◈", tone: "info" },
    answering: { word: "answering", glyph: "»", tone: "info" },
    reviewing: { word: "reviewing", glyph: "◎", tone: "info" },
    merging: { word: "merging", glyph: "▲", tone: "good" },
    on_phone: { word: "on phone", glyph: "●", tone: "info" },
    receiving_digest: { word: "receiving digest", glyph: "▼", tone: "info" },
    unknown: { word: "unknown", glyph: "○", tone: "muted" }
  };

  function stateInfo(state) {
    var s = state == null ? "" : String(state);
    return STATES[s] || { word: s.replace(/_/g, " "), glyph: "○", tone: "muted" };
  }

  // The inbox's one phrase per row, the same words internal/ui/console's
  // boxNeedPhrase picks. The inbox shows the need, not the text.
  function needPhrase(item) {
    if (item.kind === "incident") {
      switch (item.verb) {
        case "stale": return "stuck, quiet too long";
        case "runtime_lost": return "agent gone";
        case "wedged": return "send wedged";
        case "budget": return "over budget";
        default: return "incident " + (item.verb || "");
      }
    }
    if (item.kind === "status") {
      if (item.verb === "needs-decision" || item.verb === "blocked") return "needs an answer";
    }
    return item.verb || "";
  }

  // The four billed buckets, in the stack's order. The slots are the
  // validated categorical order of the dataviz palette, assigned to the
  // entity and never to its rank, so a bucket keeps its colour on every
  // meter on every page.
  var BUCKETS = [
    { key: "input", label: "input", slot: 1 },
    { key: "cache_read", label: "cache read", slot: 2 },
    { key: "cache_write", label: "cache write", slot: 3 },
    { key: "output", label: "output", slot: 4 }
  ];

  // The work vocabulary the projector uses for segments and overview
  // categories alike (internal/diagnostics/work.go's workLabels), so the
  // segment table, the timeline, the tool-mix table and the filter chips all
  // name work with the same words. The order is the order legends and
  // filter chips list kinds in; the two the classifier could not name come
  // last and are always shown as their own rows, never folded into another.
  var KINDS = {
    research: "Research / inspect",
    instructions: "Read instructions",
    write_code: "Write code / files",
    edit_code: "Edit code / files",
    review: "Review",
    test: "Run tests / build",
    coordination: "Coordinate",
    wait: "Wait / poll",
    response: "Respond",
    mixed: "Mixed activity",
    unknown: "Unclassified activity"
  };

  function kindLabel(kind) {
    if (KINDS[kind]) return KINDS[kind];
    return kind ? String(kind).replace(/_/g, " ") : KINDS.unknown;
  }

  // A finding's kind in one word, shown beside its severity so a reader can
  // tell a failure from a repeat before reading the sentence.
  var FINDING_KINDS = {
    execution_failure: "failure",
    process_polling: "repeat",
    repeated_read: "repeat",
    repeated_error: "repeat",
    repair_test_loop: "repeat",
    long_execution: "slow",
    large_output: "output",
    context_reset: "context",
    decision_wait: "wait"
  };

  // ------------------------------------------------------------ formatting

  function fmtTokens(n) { return humanizeTokens(n || 0); }

  // A null cost is unknown, not zero: docs/timeline.md's "a missing price is
  // not a price of zero", which `mate usage` prints as `?`.
  function fmtCost(v) { return v == null ? "?" : humanizeCost(v); }

  function fmtPct(v) { return v == null ? "?" : goFixed(v, 1) + "%"; }

  function fmtMs(ms) { return ms == null ? "?" : humanizeDuration(ms); }

  // fmtTokens prints 0 for an absent number, which is right for a bucket the
  // harness reported as empty and wrong for a value it never reported at
  // all: an unrecorded context size is "?", never 0.
  function fmtKnown(v) { return v == null ? "?" : fmtTokens(v); }

  function numText(v) { return v == null ? "?" : String(v); }

  // count reads "1 call" and "3 calls". A number on the first screen is a
  // sentence fragment, and "1 chains" is the kind of typo a reader stops at.
  function count(n, word, plural) {
    if (n == null) return "? " + (plural || word + "s");
    return n + " " + (n === 1 ? word : (plural || word + "s"));
  }

  // A timestamp on the wire is the database's own RFC3339 string. It is
  // shown as a local clock time, with the full string on hover, so the page
  // stays comparable to the row it came from.
  function clock(ts) {
    var d = ts ? new Date(ts) : null;
    if (!d || isNaN(d.getTime())) return "";
    return pad(d.getHours()) + ":" + pad(d.getMinutes()) + ":" + pad(d.getSeconds());
  }

  function day(ts) {
    var d = ts ? new Date(ts) : null;
    if (!d || isNaN(d.getTime())) return "";
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) + " " + clock(ts);
  }

  function pad(n) { return n < 10 ? "0" + n : String(n); }

  function sinceNow(ts) {
    var d = ts ? new Date(ts) : null;
    if (!d || isNaN(d.getTime())) return "";
    return humanizeDuration(Date.now() - d.getTime());
  }

  // ------------------------------------------------------------- DOM tools

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        var v = attrs[k];
        if (v == null || v === false) return;
        if (k === "text") { node.textContent = String(v); return; }
        if (k === "class") { node.className = String(v); return; }
        if (k === "onclick") { node.addEventListener("click", v); return; }
        node.setAttribute(k, v === true ? "" : String(v));
      });
    }
    append(node, children);
    return node;
  }

  function append(node, children) {
    if (children == null) return node;
    if (!Array.isArray(children)) children = [children];
    children.forEach(function (c) {
      if (c == null || c === false) return;
      node.appendChild(typeof c === "string" || typeof c === "number"
        ? document.createTextNode(String(c)) : c);
    });
    return node;
  }

  function txt(s) { return document.createTextNode(s == null ? "" : String(s)); }

  function stateSpan(state) {
    var info = stateInfo(state);
    return el("span", { class: "state", "data-tone": info.tone, "data-state": state || "" }, [
      el("span", { class: "glyph", "aria-hidden": "true", text: info.glyph }),
      el("span", { text: info.word })
    ]);
  }

  // refTitle hangs `path:offset` on a node's tooltip: every number on the
  // page is traceable back to the harness's own transcript (docs/mvp.md M6).
  function withRef(node, ref) {
    if (ref && ref.path) {
      node.setAttribute("data-ref", "");
      node.setAttribute("title", ref.path + ":" + (ref.offset || 0));
    }
    return node;
  }

  // --------------------------------------------------------------- meters

  // tokenMeter is the four buckets as one stacked bar with a labelled
  // legend. The legend is not optional: three of the four light-mode series
  // steps sit below 3:1 on the light surface, so the relief rule applies -
  // the numbers are always visible in text beside the swatches.
  function tokenMeter(tokens, opts) {
    tokens = tokens || {};
    opts = opts || {};
    var total = Number(tokens.total || 0);
    var bar = el("div", { class: "bar", role: "img",
      "aria-label": "tokens by bucket, " + fmtTokens(total) + " total" });
    BUCKETS.forEach(function (b) {
      var v = Number(tokens[b.key] || 0);
      if (total <= 0 || v <= 0) return;
      bar.appendChild(el("span", {
        "data-slot": b.slot,
        style: "width:" + (100 * v / total).toFixed(3) + "%",
        title: b.label + " " + fmtTokens(v)
      }));
    });
    var legend = el("div", { class: "legend" }, BUCKETS.map(function (b) {
      return el("span", { class: "item" }, [
        el("span", { class: "swatch", "data-slot": b.slot, "aria-hidden": "true" }),
        txt(b.label + " "),
        el("span", { class: "n", text: fmtTokens(tokens[b.key] || 0) })
      ]);
    }));
    if (Number(tokens.thinking || 0) > 0) {
      legend.appendChild(el("span", { class: "item muted", title:
        "reported by the harness inside the output it already counted, so it is not in the total" }, [
        txt("thinking "), el("span", { class: "n", text: fmtTokens(tokens.thinking) })
      ]));
    }
    return el("div", { class: "meter" }, [
      el("div", { class: "meter-label" }, [
        el("span", { text: opts.label || "tokens" }),
        el("span", { class: "value", text: fmtTokens(total) + (opts.cost === undefined ? "" : "  ·  cost " + fmtCost(opts.cost)) })
      ]),
      total > 0 ? bar : el("p", { class: "empty", text: "no tokens recorded yet" }),
      total > 0 ? legend : null
    ]);
  }

  // contextMeter is one magnitude against a known window, so it is one hue
  // and not four. An unknown window is drawn as an empty track with "?",
  // never as 0%.
  function contextMeter(pct, tokensAfter) {
    var known = pct != null;
    var bar = el("div", { class: "bar", role: "img",
      "aria-label": "context used, " + fmtPct(pct) });
    if (known) {
      bar.appendChild(el("span", { "data-slot": "ctx",
        style: "width:" + Math.max(0, Math.min(100, pct)).toFixed(3) + "%" }));
    }
    return el("div", { class: "meter" }, [
      el("div", { class: "meter-label" }, [
        el("span", { text: "context" }),
        el("span", { class: "value", text: fmtPct(pct) +
          (tokensAfter != null ? "  " + fmtTokens(tokensAfter) : "") })
      ]),
      bar,
      known ? null : el("p", { class: "actions-note", text: "this model has no priced context window" })
    ]);
  }

  // --------------------------------------------------------------- the API

  function api(path) {
    return fetch(path, { headers: { accept: "application/json" } }).catch(function () {
      // A transport failure is the server being gone, not an API error, and
      // "Failed to fetch" is the browser's own words rather than an answer a
      // reader can act on.
      var e = new Error("the dashboard is not answering");
      e.reason = "nothing responded at this address; is the dashboard still running?";
      throw e;
    }).then(function (r) {
      return r.json().catch(function () {
        throw new Error("the dashboard answered " + r.status + " with something that is not JSON");
      }).then(function (body) {
        if (!r.ok) {
          var e = new Error(body.error || ("the dashboard answered " + r.status));
          e.reason = body.reason || "";
          e.status = r.status;
          throw e;
        }
        return body;
      });
    });
  }

  function turnPath(project, crew, turnID) {
    return "/api/projects/" + encodeURIComponent(project) +
      "/tasks/" + encodeURIComponent(crew) +
      // A turn id contains `#`. Unescaped, the browser would send only the
      // part before it and keep the rest as a fragment (docs/dashboard.md 5).
      "/turns/" + encodeURIComponent(turnID);
  }

  // ---------------------------------------------------------------- state

  var view = document.getElementById("view");
  var crumbs = document.getElementById("crumbs");
  var livePill = document.getElementById("live");

  var route = { tier: "workspace", project: "", crew: "" };
  var data = null;             // the current page's body
  var lastEventID = 0;
  var filter = "open";         // the task table's state filter
  var expanded = Object.create(null);   // turn id -> true
  var turnCache = Object.create(null);  // turn id -> body | {error}
  var mateVisibleExchanges = 20;
  var mateFilter = "all";
  var diffState = null;        // null | {loading} | {body} | {error}
  var pollToken = 0;
  var detailState = Object.create(null);
  var segmentSort = "time";
  var segmentKindFilter = "all";  // "all" or one work kind of KINDS
  var selectedFinding = "";
  var selectedSegment = "";

  function link(hash) { return "#" + hash; }

  function routeHash(r) {
    if (r.tier === "task") return "/p/" + encodeURIComponent(r.project) + "/t/" + encodeURIComponent(r.crew);
    if (r.tier === "mate") return "/p/" + encodeURIComponent(r.project) + "/mate";
    if (r.tier === "project") return "/p/" + encodeURIComponent(r.project);
    return "/";
  }

  function parseHash() {
    var h = (location.hash || "#/").replace(/^#/, "");
    var m = /^\/p\/([^/]+)\/t\/([^/]+)\/?$/.exec(h);
    if (m) return { tier: "task", project: decodeURIComponent(m[1]), crew: decodeURIComponent(m[2]) };
    m = /^\/p\/([^/]+)\/mate\/?$/.exec(h);
    if (m) return { tier: "mate", project: decodeURIComponent(m[1]), crew: "" };
    m = /^\/p\/([^/]+)\/?$/.exec(h);
    if (m) return { tier: "project", project: decodeURIComponent(m[1]), crew: "" };
    return { tier: "workspace", project: "", crew: "" };
  }

  // ------------------------------------------------------------ the header

  function renderCrumbs() {
    crumbs.textContent = "";
    crumbs.appendChild(el("a", { href: link("/"), text: "workspace" }));
    if (route.tier !== "workspace") {
      crumbs.appendChild(el("span", { class: "sep", text: "/" }));
      crumbs.appendChild(el("a", { href: link("/p/" + encodeURIComponent(route.project)), text: route.project }));
    }
    if (route.tier === "task") {
      crumbs.appendChild(el("span", { class: "sep", text: "/" }));
      crumbs.appendChild(el("a", { href: link(routeHash(route)), text: route.crew }));
    } else if (route.tier === "mate") {
      crumbs.appendChild(el("span", { class: "sep", text: "/" }));
      crumbs.appendChild(el("a", { href: link(routeHash(route)), text: "mate" }));
    }
  }

  function setLive(state, text) {
    livePill.setAttribute("data-state", state);
    livePill.querySelector(".text").textContent = text;
  }

  function markLive() {
    setLive("live", "live · " + clock(new Date().toISOString()));
  }

  function markStale(reason) {
    setLive("stale", "stale · " + reason);
  }

  // ----------------------------------------------------------- tier 1

  function renderWorkspace(body) {
    var out = el("div");
    out.appendChild(el("div", { class: "card-head" }, [
      el("h1", { text: "workspace" }),
      el("span", { class: "badge path mono", title: "the workspace this dashboard is reading", text: body.root || "" })
    ]));
    if (!body.projects || !body.projects.length) {
      out.appendChild(el("p", { class: "empty", text: "no project is registered in this workspace yet" }));
      return out;
    }
    var cards = el("div", { class: "cards" });
    body.projects.forEach(function (p) { cards.appendChild(projectCard(p)); });
    out.appendChild(cards);
    return out;
  }

  function projectCard(p) {
    var mate = p.mate || {};
    var card = el("a", { class: "card", href: link("/p/" + encodeURIComponent(p.name)) });
    card.appendChild(el("div", { class: "card-head" }, [
      el("span", { class: "name", text: p.name }),
      el("span", { class: "badge", "data-mode": p.mode, text: p.mode || "manual" })
    ]));
    if (p.error) {
      card.appendChild(el("div", { class: "notice" }, [
        el("div", { class: "what", text: "this project could not be read in full" }),
        el("div", { class: "why", text: p.error })
      ]));
    }
    card.appendChild(el("div", { class: "facts" }, [
      el("span", { text: "mate " + (mate.harness || "?") }),
      el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
      mate.running
        ? el("span", { class: "state", "data-tone": "good" }, [
            el("span", { class: "glyph", "aria-hidden": "true", text: "●" }), el("span", { text: "running" })])
        : el("span", { class: "state", "data-tone": "muted" }, [
            el("span", { class: "glyph", "aria-hidden": "true", text: "×" }), el("span", { text: "stopped" })]),
      el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
      stateSpan(mate.state),
      mate.since ? el("span", { class: "sep", "aria-hidden": "true", text: "·" }) : null,
      mate.since ? el("span", { class: "muted", title: mate.since, text: "since " + sinceNow(mate.since) }) : null
    ]));
    card.appendChild(el("div", { class: "facts" }, [
      el("span", { text: fmtTokens(mate.tokens_today) + " tokens today" }),
      el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
      el("span", { text: "context " + (mate.context_tokens == null ? "?" : fmtTokens(mate.context_tokens)) + " (" + fmtPct(mate.context_pct) + ")" })
    ]));

    var byState = p.crews_by_state || {};
    var keys = Object.keys(byState).sort();
    var counters = el("div", { class: "counters" });
    if (!keys.length) {
      counters.appendChild(el("span", { class: "muted", text: "no crew" }));
    } else {
      keys.forEach(function (k) {
        counters.appendChild(el("span", { class: "counters-item" }, [
          el("span", { class: "count", text: String(byState[k]) }), txt(" "), stateSpan(k)
        ]));
      });
    }
    card.appendChild(el("div", { class: "section" }, [counters]));

    var waiting = Number(p.inbox_waiting || 0);
    card.appendChild(el("div", { class: "facts", style: "margin-top:8px" }, [
      waiting > 0
        ? el("span", { class: "state", "data-tone": "warning" }, [
            el("span", { class: "glyph", "aria-hidden": "true", text: "◆" }),
            el("span", { text: waiting + " waiting" })])
        : el("span", { class: "muted", text: "0 waiting" })
    ]));
    return card;
  }

  // ----------------------------------------------------------- tier 2

  function renderProject(body) {
    var out = el("div");
    out.appendChild(el("div", { class: "card-head" }, [
      el("h1", { text: body.project }),
      el("span", { class: "badge", "data-mode": body.mode, text: body.mode || "manual" })
    ]));
    out.appendChild(matePanel(body.mate || {}, body.project));

    var tasks = body.tasks || [];
    var right = el("div", { class: "card" }, [
      el("h2", { text: "inbox" }),
      inboxList(body)
    ]);
    var left = el("div", {}, [
      el("h2", { text: "tasks" }),
      filterChips(tasks),
      taskTable(body.project, tasks)
    ]);
    out.appendChild(el("div", { class: "section split" }, [left, right]));
    return out;
  }

  function matePanel(mate, project) {
    return el("div", { class: "card section" }, [
      el("h2", { text: "mate" }),
      el("div", { class: "facts", style: "margin-top:8px" }, [
        el("span", { text: mate.harness || "?" }),
        el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
        mate.running
          ? el("span", { class: "state", "data-tone": "good" }, [
              el("span", { class: "glyph", "aria-hidden": "true", text: "●" }), el("span", { text: "running" })])
          : el("span", { class: "state", "data-tone": "muted" }, [
              el("span", { class: "glyph", "aria-hidden": "true", text: "×" }), el("span", { text: "stopped" })]),
        el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
        stateSpan(mate.state),
        mate.target ? el("span", { class: "muted", text: "→ " + mate.target }) : null,
        mate.detail ? el("span", { class: "muted", text: "(" + mate.detail + ")" }) : null,
        mate.since ? el("span", { class: "muted", title: mate.since, text: "since " + sinceNow(mate.since) }) : null,
        el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
        el("span", { text: mate.turns + " model calls" }),
        el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
        el("span", { text: fmtTokens(mate.tokens_today) + " today" })
      ]),
      el("div", { class: "split", style: "margin-top:12px" }, [
        tokenMeter(mate.tokens, { label: "tokens spent", cost: mate.cost === undefined ? null : mate.cost }),
        contextMeter(mate.context_pct, mate.context_tokens)
      ]),
      route.tier === "mate" ? null : el("div", { class: "section" }, [
        el("a", { href: link("/p/" + encodeURIComponent(project) + "/mate"),
          text: "read Mate conversations →" })
      ])
    ]);
  }

  function renderMate(body) {
    var out = el("div");
    out.appendChild(el("h1", { text: body.project + " · Mate conversations" }));
    out.appendChild(matePanel(body.mate || {}, body.project));
    out.appendChild(el("div", { class: "section" }, [
      el("h2", { text: "questions and replies" }),
      exchangeFilters(body.exchanges || []),
      exchangeList(body)
    ]));
    return out;
  }

  function exchangeFilters(exchanges) {
    var counts = { all: exchanges.length, captain: 0, crew: 0, system: 0 };
    exchanges.forEach(function (x) { counts[x.source] = (counts[x.source] || 0) + 1; });
    var wrap = el("div", { class: "chips", role: "group", "aria-label": "filter Mate conversations" });
    ["all", "captain", "crew", "system", "unknown"].forEach(function (source) {
      if (source !== "all" && !counts[source]) return;
      wrap.appendChild(el("button", { class: "chip", type: "button",
        "aria-pressed": mateFilter === source ? "true" : "false",
        text: source + " " + counts[source],
        onclick: function () { mateFilter = source; mateVisibleExchanges = 20; draw(); }
      }));
    });
    return wrap;
  }

  function exchangeList(body) {
    var all = (body.exchanges || []).filter(function (x) {
      return mateFilter === "all" || x.source === mateFilter;
    }).reverse();
    if (!all.length) return el("p", { class: "empty", text: "no conversation matches this filter" });
    var visible = all.slice(0, mateVisibleExchanges);
    var wrap = el("div");
    wrap.appendChild(el("p", { class: "muted", text: "newest first · showing " + visible.length + " of " + all.length }));
    var list = el("ol", { class: "exchanges" });
    visible.forEach(function (x) { list.appendChild(exchangeCard(body.project, x)); });
    wrap.appendChild(list);
    if (visible.length < all.length) {
      wrap.appendChild(el("button", { class: "chip", type: "button", text: "show 20 older conversations",
        onclick: function () { mateVisibleExchanges += 20; draw(); } }));
    }
    return wrap;
  }

  function exchangeCard(project, x) {
    var details = savedDetails("exchange:" + x.id, "", [], "exchange");
    var summary = details.querySelector("summary");
    summary.appendChild(el("div", { class: "exchange-meta" }, [
      el("span", { class: "badge", text: x.source || "unknown" }),
      el("time", { title: x.prompt_at || x.started_at, text: day(x.prompt_at || x.started_at) }),
      x.duration_ms == null ? el("span", { text: "no reply recorded" })
        : el("span", { text: "answered in " + fmtMs(x.duration_ms) }),
      x.model_calls ? el("span", { text: x.model_calls + " model calls" }) : null,
      x.tokens && x.tokens.total ? el("span", { text: fmtTokens(x.tokens.total) + " tokens" }) : null
    ]));
    summary.appendChild(el("div", { class: "exchange-prompt", text: x.prompt || "Prompt not recorded" }));
    summary.appendChild(overviewSummary(x.overview));
    summary.appendChild(el("div", { class: "exchange-preview", text: x.response
      ? "Mate: " + excerpt(x.response, 260) : "Mate's reply has not been recorded" }));
    if ((x.activities || []).length) {
      summary.appendChild(el("div", { class: "exchange-outcome", text: x.activities.map(function (a) {
        if (a.kind === "crew.answer") return "Answered " + a.crew;
        if (a.kind === "crew.spawned") return "Started " + a.crew;
        if (a.kind === "merge.done") return "Merged " + a.crew;
        if (a.kind === "crew.finished" || a.kind === "crew.failed") return "Closed " + a.crew;
        return a.text;
      }).join(" · ") }));
    }
    details.appendChild(el("div", { class: "exchange-detail" }, [
      el("div", { class: "exchange-label", text: "Prompt" }),
      withRef(el("p", { class: "exchange-full", text: x.prompt || "Prompt not recorded" }), x.prompt_ref),
      el("div", { class: "exchange-label", text: "Mate's reply" }),
      withRef(el("p", { class: "exchange-full", text: x.response || "No reply recorded yet." }), x.response_ref),
      workOverview(x.overview, { prefix: "mate-overview:" + x.id }),
      (x.activities || []).length ? el("div", { class: "exchange-activity" }, [
        el("div", { class: "exchange-label", text: "What Mate did" }),
        el("ul", {}, x.activities.map(function (a) {
          return el("li", {}, [
            a.crew ? el("a", { href: link("/p/" + encodeURIComponent(project) + "/t/" + encodeURIComponent(a.crew)),
              text: a.crew }) : null,
            el("span", { text: (a.crew ? " · " : "") + a.text })
          ]);
        }))
      ]) : null,
      el("div", { class: "facts", style: "margin-top:12px" }, [
        el("span", { text: (x.model_calls || 0) + " model calls" }),
        el("span", { text: (x.tool_calls || 0) + " tool calls" }),
        x.model ? el("span", { text: x.model }) : null,
        x.context_tokens_after ? el("span", { text: "context " + fmtTokens(x.context_tokens_after) }) : null
      ])
    ]));
    return el("li", {}, [details]);
  }

  function excerpt(s, n) {
    var chars = Array.from(s || "");
    return chars.length <= n ? s : chars.slice(0, n).join("").trimEnd() + "…";
  }

  function taskIsOpen(t) { return !t.closed; }

  function filterChips(tasks) {
    var counts = { open: 0, closed: 0 };
    var byState = Object.create(null);
    tasks.forEach(function (t) {
      if (taskIsOpen(t)) counts.open++; else counts.closed++;
      var s = t.state || "";
      byState[s] = (byState[s] || 0) + 1;
    });
    var chips = [
      { key: "open", label: "open", n: counts.open },
      { key: "closed", label: "closed", n: counts.closed },
      { key: "all", label: "all", n: tasks.length }
    ];
    Object.keys(byState).sort().forEach(function (s) {
      chips.push({ key: "state:" + s, label: stateInfo(s).word, n: byState[s], state: s });
    });
    var wrap = el("div", { class: "chips", role: "group", "aria-label": "filter tasks by state" });
    chips.forEach(function (c) {
      var pressed = filter === c.key;
      var b = el("button", { class: "chip", type: "button", "aria-pressed": pressed ? "true" : "false",
        onclick: function () { filter = c.key; draw(); } });
      if (c.state !== undefined) {
        var info = stateInfo(c.state);
        b.appendChild(el("span", { class: "glyph", "aria-hidden": "true",
          style: "font-family:var(--mono)", text: info.glyph + " " }));
      }
      b.appendChild(txt(c.label + " "));
      b.appendChild(el("span", { class: "n", text: String(c.n) }));
      wrap.appendChild(b);
    });
    return wrap;
  }

  function filteredTasks(tasks) {
    if (filter === "all") return tasks;
    if (filter === "open") return tasks.filter(taskIsOpen);
    if (filter === "closed") return tasks.filter(function (t) { return !taskIsOpen(t); });
    if (filter.indexOf("state:") === 0) {
      var want = filter.slice(6);
      return tasks.filter(function (t) { return (t.state || "") === want; });
    }
    return tasks;
  }

  var TASK_COLUMNS = ["id", "task", "state", "age", "tokens", "cost", "asked", "waited", "branch"];

  function taskTable(project, tasks) {
    var rows = filteredTasks(tasks);
    if (!rows.length) {
      return el("p", { class: "empty", text: "no task matches this filter" });
    }
    var thead = el("thead", {}, [el("tr", {}, TASK_COLUMNS.map(function (c) {
      var numeric = c === "age" || c === "tokens" || c === "cost" || c === "asked" || c === "waited";
      return el("th", { class: numeric ? "n" : null, text: c });
    }))]);
    var tbody = el("tbody");
    rows.forEach(function (t) {
      var href = link("/p/" + encodeURIComponent(project) + "/t/" + encodeURIComponent(t.crew));
      var tr = el("tr", { class: "clickable", onclick: function () { location.hash = href.slice(1); } });
      tr.appendChild(el("td", { "data-label": "id" }, [
        el("a", { href: href, class: "mono", text: t.crew })
      ]));
      tr.appendChild(el("td", { "data-label": "task", class: "task-text" }, [el("div", {}, [
        el("span", { text: t.text || "" }), el("a", { class: "inspect-work", href: href, text: "Inspect work →",
          "aria-label": "Inspect work, tokens and findings for " + t.crew })
      ])]));
      tr.appendChild(el("td", { "data-label": "state" }, [
        stateSpan(t.state),
        t.closed ? el("span", { class: "muted", text: " · " + (t.close_state || "closed") }) : null
      ]));
      tr.appendChild(el("td", { "data-label": "age", class: "n", title: t.spawned_at || "", text: fmtMs(t.age_ms) }));
      tr.appendChild(el("td", { "data-label": "tokens", class: "n", text: fmtTokens(t.tokens && t.tokens.total) }));
      tr.appendChild(el("td", { "data-label": "cost", class: "n", text: fmtCost(t.cost) }));
      tr.appendChild(el("td", { "data-label": "asked", class: "n", text: String(t.question_count || 0) }));
      tr.appendChild(el("td", { "data-label": "waited", class: "n", text: t.waited_ms ? fmtMs(t.waited_ms) : "–" }));
      tr.appendChild(el("td", { "data-label": "branch", class: "mono branch", text: t.branch || "–" }));
      tbody.appendChild(tr);
    });
    return el("div", { class: "table-wrap" }, [el("table", {}, [thead, tbody])]);
  }

  function inboxList(body) {
    var out = el("div");
    if (body.inbox_error) {
      out.appendChild(el("div", { class: "notice" }, [
        el("div", { class: "what", text: "the inbox could not be read" }),
        el("div", { class: "why", text: body.inbox_error })
      ]));
    }
    var items = body.inbox || [];
    out.appendChild(el("p", { class: "facts", style: "margin:0 0 8px" }, [
      items.length
        ? el("span", { class: "state", "data-tone": "warning" }, [
            el("span", { class: "glyph", "aria-hidden": "true", text: "◆" }),
            el("span", { text: items.length + " waiting" })])
        : el("span", { class: "muted", text: "0 waiting" })
    ]));
    if (!items.length) {
      out.appendChild(el("p", { class: "empty", text: "nothing is waiting on a decision" }));
      return out;
    }
    var ul = el("ul", { class: "inbox" });
    items.forEach(function (it) {
      var who = it.kind === "message"
        ? (it.target ? it.source + "->" + it.target : it.source)
        : (it.crew || it.source || "");
      ul.appendChild(el("li", { "data-attention": it.attention ? "true" : "false" }, [
        el("span", { class: "at", title: it.at || "", text: clock(it.at).slice(0, 5) }),
        el("span", { class: "who", text: who }),
        el("span", {}, [
          el("span", { class: "need", text: needPhrase(it) }),
          it.text ? el("span", { class: "said", text: it.text }) : null
        ])
      ]));
    });
    out.appendChild(ul);
    return out;
  }

  // ----------------------------------------------------------- tier 3

  function renderTask(body) {
    var out = el("div");
    var ledger = body.ledger || {};
    out.appendChild(el("div", { class: "card-head crew-head" }, [
      el("h1", { text: body.crew }),
      stateSpan(ledger.state),
      runtimeChips(body)
    ]));
    out.appendChild(el("p", { class: "muted", style: "margin:0 0 6px", text: ledger.text || "" }));
    out.appendChild(crewFacts(body));
    out.appendChild(crewPerformance(body));
    out.appendChild(savedDetails("ledger", "Crew totals and branch", [ledgerHeader(ledger, body.branch || {})], "section diagnostics"));
    if ((body.status_lines || []).length) {
      out.appendChild(el("div", { class: "section" }, [
        el("h2", { text: "status lines" }),
        statusList(body.status_lines)
      ]));
    }
    out.appendChild(el("div", { class: "section" }, [
      el("h2", { text: "questions" }),
      questionList(body.questions || [])
    ]));
    out.appendChild(el("div", { class: "section" }, [
      el("h2", { text: "diff" }),
      diffPanel(body)
    ]));
    out.appendChild(savedDetails("raw-calls", "Raw model calls and events (" + (body.turns || []).length + ")",
      [turnTimeline(body)], "section diagnostics"));
    return out;
  }

  // The harness and model the recording actually observed, as chips beside
  // the crew name. They come from the observed runtime, never from the
  // launch profile: a configured model is not proof of the model that
  // answered, which is why recordingDetails keeps the two apart.
  function runtimeChips(body) {
    var p = body.performance;
    var runtime = (p && p.runtime) || {};
    var chips = [];
    // The harness name comes from the launch profile when one was recorded
    // and otherwise from the ledger's actor row; the version is only ever
    // the one the recording itself reported.
    var harness = (p && p.profile && p.profile.harness) || (body.ledger && body.ledger.harness) || "";
    if (harness || runtime.harness_version) chips.push(el("span", { class: "kchip", title: "harness of this crew and the version observed in the recording",
      text: [harness, runtime.harness_version].filter(Boolean).join(" ") }));
    if (runtime.model) chips.push(el("span", { class: "kchip", title: "model and effort observed in the recording",
      text: runtime.model + (runtime.effort ? " · " + runtime.effort : "") }));
    return chips.length ? el("span", { class: "head-chips" }, chips) : null;
  }

  // One line of facts about the run: when it was spawned and closed, how
  // many prompts, model calls and tool calls it made, whether its branch
  // still exists, and how long it waited on decisions. Every value is the
  // ledger's or the branch check's own; an unrecorded time says so.
  // spawnSpanMs is how long the task has been alive: a closed task measures
  // spawn to close, an open one uses the server's age, and a task with no
  // spawn time has no span rather than a zero one.
  function spawnSpanMs(t) {
    if (t.closed && t.spawned_at && t.closed_at) return new Date(t.closed_at) - new Date(t.spawned_at);
    return t.age_ms == null ? null : t.age_ms;
  }

  function crewFacts(body) {
    var t = body.ledger || {};
    var branch = body.branch || {};
    var p = body.performance;
    var span = spawnSpanMs(t);
    var when = "spawned " + (clock(t.spawned_at) || "time unknown");
    if (t.closed) when += " → closed " + (clock(t.closed_at) || "time unknown");
    when += " · " + (t.closed ? "" : "open for ") + fmtMs(span);
    return el("div", { class: "facts crew-facts" }, [
      el("span", { title: (t.spawned_at || "") + (t.closed_at ? " → " + t.closed_at : ""), text: when }),
      p ? el("span", { text: count((p.prompt_turns || []).length, "prompt") }) : null,
      el("span", { text: count(t.turns, "model call") }),
      el("span", { text: count(t.tool_count, "tool call") }),
      el("span", { class: "mono", title: branch.reason || "", text: branch.name
        ? "branch " + branch.name + " · " + (branch.exists ? "exists" : "gone") : "no branch" }),
      el("span", { text: count(t.question_count, "question") + (t.waited_ms ? " · waited " + fmtMs(t.waited_ms) : "") })
    ]);
  }

  // Crew diagnostics are projected from recorded facts. The UI never assigns
  // tokens to tools or treats an absent native result as a successful command.
  function savedDetails(key, label, children, className, defaultOpen) {
    var open = Object.prototype.hasOwnProperty.call(detailState, key) ? detailState[key] : !!defaultOpen;
    var details = el("details", { class: className, "data-detail": key, open: open });
    details.appendChild(el("summary", { "data-focus": "detail:" + key, text: label }));
    append(details, children);
    details.addEventListener("toggle", function () { if (details.isConnected) detailState[key] = details.open; });
    return details;
  }

  function byID(items, id) { return (items || []).find(function (item) { return item.id === id; }); }

  function processForExecution(p, e) {
    return (p.processes || []).find(function (item) {
      return (item.execution_ids || []).indexOf(e.id) !== -1 || (item.poll_ids || []).indexOf(e.id) !== -1;
    });
  }

  function segmentLabel(s, p) {
    var label = s.label || s.kind || "Unclassified work";
    if (/\bturn\.gap\b/.test(label)) return "Activity not recorded";
    if (!/\b(?:const|let|var)\s+\w+\s*=|\bawait\s+(?:tools|functions)\.|\bPromise\.(?:all|allSettled)\s*\(/.test(label)) return label;
    var native = (s.execution_ids || []).map(function (id) { return byID(p.executions, id); }).find(function (e) { return e && !e.is_wrapper && e.command; });
    if (native) return excerpt(native.command, 120);
    return kindLabel(s.kind) + " · native command not linked";
  }

  // kindChip names a kind with a swatch and the word. The swatch colour is
  // assigned to the kind in app.css through data-kind; the word is always
  // beside it, so colour is never the only signal.
  function kindChip(kind) {
    return el("span", { class: "kchip", "data-kind": kind || "unknown" }, [
      el("i", { "aria-hidden": "true" }), txt(kindLabel(kind))
    ]);
  }

  function findingKindChip(f) {
    var word = FINDING_KINDS[f.kind] || (f.kind ? String(f.kind).replace(/_/g, " ") : "finding");
    return el("span", { class: "kchip finding-kind", "data-finding": word, "data-severity": f.severity || "" }, [
      el("i", { "aria-hidden": "true" }), txt(word + (f.severity ? " · " + f.severity : ""))
    ]);
  }

  // promptTag is "P2 21:57": the prompt's 1-based position in the crew and
  // the clock of the moment in question, the shorthand every table uses so
  // a segment can be placed without reading the prompt text.
  function promptTag(p, promptID, at, seconds) {
    var index = (p.prompt_turns || []).findIndex(function (prompt) { return prompt.id === promptID; });
    var time = clock(at);
    return (index < 0 ? "P?" : "P" + (index + 1)) + (time ? " " + (seconds ? time : time.slice(0, 5)) : "");
  }

  function isFailedNative(e) {
    return !e.is_wrapper && !e.poll && (e.status === "failed" || (e.exit_code != null && e.exit_code !== 0));
  }

  function segmentTokens(s, bucket) {
    return s.model_calls === 0 ? "?" : fmtTokens(s.tokens && s.tokens[bucket]);
  }

  function timestamp(value) {
    var n = value ? Date.parse(value) : NaN;
    return Number.isFinite(n) ? n : null;
  }

  function fmtBytes(n) {
    if (n == null) return "unknown";
    return n < 1024 ? n + " B" : n < 1048576 ? (n / 1024).toFixed(1) + " KB" : (n / 1048576).toFixed(1) + " MB";
  }

  function elapsedNode(start, end, measured, running) {
    var startMs = timestamp(start);
    return el("span", { class: "num", "data-elapsed-start": running && startMs != null ? start : null,
      title: start ? start + (end ? " → " + end : " · last observed in progress") : "timing not recorded",
      text: running && startMs != null ? fmtMs(Math.max(0, Date.now() - startMs)) : fmtMs(measured) });
  }

  function ageNode(at) {
    return el("span", { "data-age-at": timestamp(at) == null ? null : at, title: at || "timestamp not recorded",
      text: timestamp(at) == null ? "unknown" : sinceNow(at) + " ago" });
  }

  function usageText(tokens) {
    if (!tokens) return "usage not linked to model calls";
    return fmtTokens(tokens.input) + " fresh input · " + fmtTokens(tokens.cache_read) + " cache read · " +
      fmtTokens(tokens.cache_write) + " cache write · " + fmtTokens(tokens.output) + " output";
  }

  function sourceNote(ref) {
    return ref && ref.path ? el("span", { class: "source-ref mono", title: ref.path + ":" + (ref.offset || 0),
      text: ref.path + ":" + (ref.offset || 0) }) : el("span", { class: "muted", text: "source reference unavailable" });
  }

  function crewPerformance(body) {
    var p = body.performance;
    if (!p) return el("div", { class: "notice", "data-tone": "quiet" }, [
      el("div", { class: "what", text: "Work diagnostics are not available yet" }),
      el("div", { class: "why", text: "The observer has not recorded segment and execution evidence for this Crew." })
    ]);
    var out = el("div", { class: "crew-performance" });
    out.appendChild(currentWork(body, p));
    out.appendChild(answersStrip(body, p));
    out.appendChild(stepsCard(body, p));
    out.appendChild(findingsPanel(body, p));
    out.appendChild(el("section", { class: "section", "aria-label": "Prompt turn timelines" }, [
      el("h2", { text: "Work over time" }),
      el("p", { class: "actions-note", text: "Each prompt keeps its own sequence of work. Token steps are recorded model responses; commands and decision waits share the time axis." }),
      promptTimelines(body, p)
    ]));
    out.appendChild(toolMix(body, p));
    out.appendChild(segmentTable(body, p));
    var unplaced = (p.executions || []).filter(function (e) { return !e.prompt_id && !e.call_id; });
    if (unplaced.length) out.appendChild(savedDetails("unplaced-executions", "Executions without a confirmed prompt (" + unplaced.length + ")", [
      el("p", { class: "coverage-note", text: "These records are not assigned to a nearby prompt or segment. Their token cost is unknown." }), executionList(p, unplaced)
    ], "section diagnostics"));
    out.appendChild(recordingDetails(p));
    return out;
  }

  function currentWork(body, p) {
    var current = byID(p.segments, p.current_segment_id);
    var latest = current || (p.segments || [])[Math.max(0, (p.segments || []).length - 1)];
    var freshness = p.freshness || {};
    var closed = body.ledger && body.ledger.closed;
    var stale = !closed && (freshness.stale || (timestamp(freshness.last_observed_at) != null && Date.now() - timestamp(freshness.last_observed_at) > 30000));
    var heading = current ? "Current work" : "Last recorded work";
    var recent = p.recent_tokens;
    var top = byID(p.segments, (p.top_segment_ids || [])[0]);
    var card = el("section", { class: "card work-current", "aria-label": "Current Crew work" });
    card.appendChild(el("div", { class: "card-head" }, [
      el("h2", { text: heading }),
      el("span", { class: "recording-state", "data-observed-at": closed ? null : freshness.last_observed_at || "",
        "data-recording-error": freshness.stale ? "true" : "false", "data-stale": stale ? "true" : "false",
        text: closed ? "Historical recording" : !freshness.last_observed_at ? "Recording time unknown" : stale ? "Recording is stale" : "Recorded recently" })
    ]));
    card.appendChild(el("div", { class: "work-current-title" }, [
      latest ? el("button", { class: "text-button", type: "button", "data-focus": "current-work", text: segmentLabel(latest, p),
        onclick: function () { revealSegments([latest.id]); } }) : txt("No work segment recorded")
    ]));
    if (latest) card.appendChild(el("div", { class: "facts" }, [
      el("span", { title: latest.started_at || "", text: "since " + (clock(latest.started_at) || "unknown") }),
      el("span", {}, [txt(current ? "elapsed " : "recorded span "), elapsedNode(latest.started_at, latest.ended_at, latest.elapsed_ms, !!current)]),
      el("span", { text: latest.model_calls === 0 ? "Usage not reported for this activity yet" : latest.model_calls + " model calls · " + fmtTokens(latest.tokens && latest.tokens.total) + " tokens" })
    ]));
    var running = closed ? [] : (p.executions || []).filter(function (e) { return !e.is_wrapper && (e.status === "running" || e.status === "in_progress"); });
    running.slice(-2).forEach(function (e) {
      var process = processForExecution(p, e);
      card.appendChild(el("div", { class: "running-execution" }, [el("span", { text: "Last seen running: " }),
        el("code", { text: excerpt((e.poll && process && process.command) || e.command || e.target || e.tool || "Unknown command", 150) }),
        txt(" · "), elapsedNode(e.started_at, e.ended_at, e.duration_ms, true),
        e.process_id ? el("span", { text: " · process " + e.process_id }) : null]));
    });
    card.appendChild(el("div", { class: "work-metrics" }, [
      workMetric("Recorded tokens", p.model_calls === 0 ? "Not reported" : fmtTokens(p.tokens && p.tokens.total),
        p.model_calls === 0 ? "Usage is reported when model responses arrive" : usageText(p.tokens)),
      workMetric("Last " + fmtMs(p.recent_window_ms || 300000), recent ? "+" + fmtTokens(recent.total) : "unknown", recent ? usageText(recent) : "No usage observation"),
      workMetric("Prompt elapsed", fmtMs(p.time && p.time.prompt_elapsed_ms), "Time in recorded prompt turns"),
      workMetric("Tool elapsed", fmtMs(p.time && p.time.tool_elapsed_ms), "Overlapping executions counted once")
    ]));
    card.appendChild(el("div", { class: "freshness-line" }, [
      el("span", {}, [txt("Observer "), ageNode(freshness.last_observed_at)]),
      el("span", {}, [txt("Ingest "), ageNode(freshness.last_ingested_at)]),
      el("span", {}, [txt("Usage "), ageNode(freshness.last_usage_at)])
    ]));
    if (top) card.appendChild(el("div", { class: "work-hotspot" }, [
      el("span", { text: "Most context re-sent: " }),
      el("button", { class: "text-button", type: "button", "data-focus": "top-segment", text: segmentLabel(top, p) + " · " + (top.model_calls === 0 ? "usage pending" : fmtTokens(top.tokens && top.tokens.total)),
        onclick: function () { revealSegments([top.id]); } })
    ]));
    var missing = freshness.missing || [];
    if (missing.length) card.appendChild(el("p", { class: "coverage-note", text: "Coverage: " + missing.slice(0, 2).join(" · ") + (missing.length > 2 ? " · " + (missing.length - 2) + " more in recording details" : "") }));
    return card;
  }

  function workMetric(label, value, note) {
    return el("div", { class: "work-metric" }, [el("span", { class: "metric-label", text: label }),
      el("strong", { class: "num", text: value }), el("span", { class: "metric-note", text: note })]);
  }

  // answersStrip is the first screen's four answers (design callouts ①–④):
  // how many tokens, how many tool calls for which kind of work, how often
  // the Crew repeated itself, and which step cost the most. Each card reads
  // one projector field and says "not recorded yet" when that field is
  // absent, so an older projection degrades to words rather than to zeros.
  function answersStrip(body, p) {
    return el("div", { class: "answers" }, [
      tokensAnswer(body, p), workAnswer(body, p), repeatsAnswer(body, p), expensiveAnswer(body, p)
    ]);
  }

  function answerCard(key, title, children) {
    return el("section", { class: "card answer", "data-answer": key, "aria-label": title },
      [el("h2", { text: title })].concat(children));
  }

  function tokensAnswer(body, p) {
    var ledger = body.ledger || {};
    var tokens = p.tokens || {};
    var recent = p.recent_tokens;
    var prompts = p.prompt_turns || [];
    var children = [
      el("div", { class: "big num", text: p.model_calls === 0 ? "not reported" : fmtTokens(tokens.total) }),
      tokenMeter(tokens, { label: "buckets" }),
      el("div", { class: "answer-lines" }, [
        el("span", { text: "thinking " + fmtTokens(tokens.thinking) + " · inside output, not added" }),
        el("span", { text: "context after last call " + fmtKnown(ledger.context_tokens_last) + " · window " + fmtPct(ledger.context_pct) + " · cost " + fmtCost(ledger.cost) }),
        el("span", { text: ledger.closed ? "historical recording · no live window"
          : "last " + fmtMs(p.recent_window_ms || 300000) + " " + (recent ? "+" + fmtTokens(recent.total) : "unknown") })
      ])
    ];
    if (prompts.length) children.push(el("div", { class: "answer-lines answer-prompts" }, prompts.map(function (prompt, i) {
      return el("button", { class: "text-button", type: "button", "data-focus": "answer-prompt:" + prompt.id,
        title: "Open prompt " + (i + 1) + " on the timeline",
        text: promptTag(p, prompt.id, prompt.prompt_at || prompt.started_at) + " · " +
          (prompt.model_calls === 0 ? "usage pending" : fmtTokens(prompt.tokens && prompt.tokens.total)) + " · " +
          count(prompt.model_calls || 0, "call") + " · " + fmtMs(prompt.elapsed_ms),
        onclick: function () { revealPrompt(prompt.id); } });
    })));
    return answerCard("tokens", "Tokens", children);
  }

  // The steps card answers "what did the agent do to finish this task", in
  // five bands: the task and its totals; the sequence of stages, drawn as a
  // strip sized by tokens and listed as a table; the same stages summed by
  // work type; the skills loaded and the stage each load happened in; and a
  // legend. A stage is a run of consecutive model calls doing one type of
  // work (the projector's `steps`). Stages partition the calls, so their
  // numbers add up, and the work-type table is summed from them rather than
  // read from another field, so the two tables agree by construction.
  //
  // Unknown is `?` and a true zero is `–`, everywhere in the card. A
  // projection older than `steps` or `skills` says so in words.
  var STAGE_ROWS = 12;

  // The work-type table's fixed order: real types first, then the two the
  // classifier could not name.
  var EFFORT_ORDER = ["research", "write_code", "edit_code", "test", "review", "coordination",
    "wait", "instructions", "response", "mixed", "unknown"];

  function stepTokens(s) { return Number(s.tokens && s.tokens.total) || 0; }

  // A count the recording always has: zero is a dash, never "0".
  function dashed(n) { return n ? String(n) : "–"; }

  function dashedTokens(n) { return n ? fmtTokens(n) : "–"; }

  // An unknown value is a `?` in the warning colour; the legend says so.
  function unknownable(text) {
    return text === "?" ? el("span", { class: "sc-unknown", title: "not recorded", text: "?" }) : txt(text);
  }

  function stageTime(ms) { return ms == null ? "?" : ms === 0 ? "–" : fmtMs(ms); }

  // A skill pin is the skill's 1-based place in the Skills band, so the same
  // skill loaded in two stages shows the same number twice.
  function skillPin(index, name) {
    return el("span", { class: "sc-pin", "data-pin": String(index), title: "skill " + index + ": " + name + " loaded here",
      "aria-label": "skill " + index + " " + name, text: String(index) });
  }

  function kindSwatch(kind) {
    return el("i", { class: "sc-swatch", "data-kind": kind || "unknown", "aria-hidden": "true" });
  }

  function stepsCard(body, p) {
    var prompts = p.prompt_turns || [];
    var recorded = prompts.filter(function (prompt) { return prompt.overview && Array.isArray(prompt.overview.steps); });
    var skills = Array.isArray(p.skills) ? p.skills : null;
    // Stages are numbered across the whole task, in prompt order.
    var stages = [];
    recorded.forEach(function (prompt) {
      prompt.overview.steps.forEach(function (s, i) {
        stages.push({ step: s, n: stages.length + 1, prompt: prompt, promptStart: i === 0 && stages.length > 0, pins: [] });
      });
    });
    var stageOfCall = {};
    stages.forEach(function (st) { (st.step.call_ids || []).forEach(function (id) { stageOfCall[id] = st; }); });
    (skills || []).forEach(function (skill, i) {
      (skill.loads || []).forEach(function (load) {
        var st = load.call_id ? stageOfCall[load.call_id] : null;
        if (st && !st.pins.some(function (pin) { return pin.index === i + 1; })) st.pins.push({ index: i + 1, name: skill.name });
      });
    });

    var card = el("section", { class: "card steps-card", "aria-label": "Stages of this task" });
    card.appendChild(stepsHeadBand(body, p, stages, recorded.length > 0));
    if (!recorded.length) {
      card.appendChild(el("div", { class: "sc-band", "data-band": "sequence" }, [
        el("p", { class: "coverage-note", text: prompts.length ? "Steps not recorded yet" : "No prompt has been recorded yet." })
      ]));
    } else {
      card.appendChild(sequenceBand(body, p, stages));
      card.appendChild(effortBand(p, stages));
    }
    card.appendChild(skillsBand(skills, stageOfCall, recorded.length > 0));
    card.appendChild(el("div", { class: "sc-band sc-legend", "aria-label": "How to read this card" }, [
      el("div", {}, [el("span", { class: "sc-unknown", text: "?" }), txt(" unknown"), el("span", { class: "sc-legend-gap", "aria-hidden": "true" }),
        txt("– zero"), el("span", { class: "sc-legend-gap", "aria-hidden": "true" }), txt("Mdl = model (API) calls · Tool = tool calls")]),
      el("div", { text: "strip width = tokens · numbered pins = skill loads" })
    ]));
    return card;
  }

  // The pill is the page's own state wording with its glyph and tone; a
  // closed task reads done or failed, never a colour alone.
  function taskPill(ledger) {
    var info;
    if (ledger.closed) {
      var close = ledger.close_state || "";
      info = close === "finished" ? { word: "done", glyph: "✓", tone: "good" }
        : close === "failed" ? { word: "failed", glyph: "×", tone: "critical" }
        : { word: close ? close.replace(/[_-]/g, " ") : "closed", glyph: "■", tone: "muted" };
    } else info = stateInfo(ledger.state);
    return el("span", { class: "state sc-pill", "data-tone": info.tone, "data-state": ledger.closed ? "closed:" + (ledger.close_state || "") : ledger.state || "" }, [
      el("span", { class: "glyph", "aria-hidden": "true", text: info.glyph }), el("span", { text: info.word })
    ]);
  }

  function scStat(label, value) {
    return el("div", { class: "sc-stat" }, [el("span", { class: "sc-label", text: label }), el("strong", { class: "sc-big" }, [unknownable(value)])]);
  }

  function stepsHeadBand(body, p, stages, recorded) {
    var ledger = body.ledger || {};
    var calls = p.model_calls;
    return el("div", { class: "sc-band sc-head", "data-band": "head" }, [
      el("div", { class: "sc-title" }, [taskPill(ledger), el("h2", { class: "sc-task", text: ledger.text || body.crew || "Task" })]),
      el("div", { class: "sc-sub" }, [
        el("span", { class: "sc-label", text: "Agent" }), txt(" " + (body.crew || "?")), el("span", { class: "sc-bar-sep", "aria-hidden": "true", text: "|" }),
        txt(recorded ? count(stages.length, "stage") : "stages not recorded")
      ]),
      el("div", { class: "sc-stats" }, [
        scStat("Tokens", calls === 0 ? "–" : calls == null ? "?" : fmtTokens(p.tokens && p.tokens.total)),
        scStat("Model calls", calls == null ? "?" : dashed(calls)),
        scStat("Elapsed", stageTime(p.time && p.time.prompt_elapsed_ms))
      ])
    ]);
  }

  function bandHead(label, aside) {
    return el("div", { class: "sc-band-head" }, [el("h3", { class: "sc-label", text: label }), aside ? el("span", { class: "sc-aside", text: aside }) : null]);
  }

  function sequenceBand(body, p, stages) {
    var open = stages.filter(function (st) { return st.step.open; });
    var band = el("div", { class: "sc-band", "data-band": "sequence" }, [
      bandHead("Sequence", count(stages.length, "stage") + (open.length ? " · #" + open[open.length - 1].n + " in progress" : ""))
    ]);
    if (!stages.length) {
      band.appendChild(el("p", { class: "coverage-note", text: "No model call recorded for this task yet." }));
      return band;
    }
    var total = 0, max = 0;
    stages.forEach(function (st) { var t = stepTokens(st.step); total += t; if (t > max) max = t; });
    // The strip is the whole task at a glance and is never cut: one piece
    // per stage, as wide as its tokens, with a wider gap between prompts.
    var pinned = stages.some(function (st) { return st.pins.length; });
    band.appendChild(el("div", { class: "sc-strip", role: "img", "data-pins": pinned ? null : "none", "aria-label": count(stages.length, "stage") + " in order, sized by tokens: " +
      stages.map(function (st) { return st.n + " " + kindLabel(st.step.kind) + (st.step.open ? " (in progress)" : ""); }).join(", ") },
      stages.map(function (st) {
        var s = st.step, t = stepTokens(s);
        return el("div", { class: "sc-piece", "data-kind": s.kind || "unknown", "data-stage": String(st.n),
          "data-prompt-start": st.promptStart ? "true" : null, "data-open": s.open ? "true" : null,
          style: "flex-grow:" + (total > 0 ? t : 1),
          title: "#" + st.n + " " + kindLabel(s.kind) + " · " + dashedTokens(t) + " tokens" + (s.open ? " · in progress" : "") },
        [el("div", { class: "sc-pins" }, st.pins.map(function (pin) { return skillPin(pin.index, pin.name); })), el("div", { class: "sc-fill" })]);
      })));
    var rows = [];
    stages.forEach(function (st) {
      if (st.promptStart) rows.push(promptSeparator(p, st.prompt));
      rows.push(stageRow(st, max));
    });
    // Rows up to the twelfth stage are shown; a separator stays with the
    // stage it introduces.
    var cut = rows.findIndex(function (row) { return Number(row.getAttribute("data-stage")) > STAGE_ROWS; });
    if (cut > 0 && rows[cut - 1].getAttribute("data-prompt")) cut--;
    var table = el("div", { class: "sc-table sc-stages", role: "table", "aria-label": "Stages in order" }, [
      el("div", { class: "sc-row sc-th", role: "row" }, ["#", "Stage", "Mdl", "Tool", "Tokens", "Time"].map(function (h, i) {
        return el("span", { role: "columnheader", class: i > 1 ? "sc-num" : null, title: h === "Mdl" ? "model (API) calls" : h === "Tool" ? "tool calls" : null, text: h });
      }))
    ].concat(cut < 0 ? rows : rows.slice(0, cut)));
    if (cut >= 0) table.appendChild(savedDetails("steps-later:" + (body.crew || ""),
      "Continue through " + count(stages.length - STAGE_ROWS, "later stage"), rows.slice(cut), "sc-later"));
    band.appendChild(table);
    return band;
  }

  function promptSeparator(p, prompt) {
    return el("div", { class: "sc-row sc-prompt", role: "row", "data-prompt": prompt.id }, [
      el("span", { role: "cell", title: prompt.prompt || "",
        text: promptTag(p, prompt.id, prompt.prompt_at || prompt.started_at) + " · " + excerpt(prompt.prompt || "Prompt text unavailable", 80) })
    ]);
  }

  function stageRow(st, max) {
    var s = st.step, t = stepTokens(s), kind = s.kind || "unknown";
    var ids = s.segment_ids || [];
    var name = ids.length
      ? el("button", { class: "text-button sc-stage-name", type: "button", "data-focus": "step:" + ids[0],
        title: "Open the work segments of this stage", onclick: function () { revealSegments(ids); }, text: kindLabel(kind) })
      : el("span", { class: "sc-stage-name", text: kindLabel(kind) });
    // What a mixed stage mixed, by name; no token is split among the parts.
    var parts = (s.parts || []).filter(function (k) { return k !== kind; });
    return el("div", { class: "sc-row sc-stage", role: "row", "data-kind": kind, "data-stage": String(st.n), "data-open": s.open ? "true" : null }, [
      el("span", { class: "sc-n", role: "cell", text: String(st.n) }),
      el("span", { class: "sc-what", role: "cell" }, [kindSwatch(kind), name]
        .concat(st.pins.map(function (pin) { return skillPin(pin.index, pin.name); }))
        .concat([parts.length ? el("span", { class: "sc-parts", text: parts.map(kindLabel).join(" + ") }) : null,
          s.open ? el("span", { class: "sc-open", text: "in progress" }) : null])),
      el("span", { class: "sc-num", role: "cell", text: dashed(s.model_calls) }),
      el("span", { class: "sc-num", role: "cell", text: dashed(s.executions) }),
      el("span", { class: "sc-num sc-tok", role: "cell" }, [txt(dashedTokens(t)),
        t > 0 && max > 0 ? el("i", { class: "sc-underline", "data-kind": kind, "aria-hidden": "true", style: "width:" + (100 * t / max).toFixed(1) + "%" }) : null]),
      el("span", { class: "sc-num", role: "cell", title: s.started_at ? "Started " + day(s.started_at) : "Time not recorded" }, [unknownable(stageTime(s.elapsed_ms))])
    ]);
  }

  function effortBand(p, stages) {
    var sums = {}, extra = [];
    var total = { calls: 0, tools: 0, tokens: 0 };
    stages.forEach(function (st) {
      var kind = st.step.kind || "unknown";
      if (!sums[kind]) {
        sums[kind] = { calls: 0, tools: 0, tokens: 0 };
        if (EFFORT_ORDER.indexOf(kind) === -1) extra.push(kind);
      }
      var t = stepTokens(st.step);
      sums[kind].calls += st.step.model_calls || 0; sums[kind].tools += st.step.executions || 0; sums[kind].tokens += t;
      total.calls += st.step.model_calls || 0; total.tools += st.step.executions || 0; total.tokens += t;
    });
    function share(tokens) { return total.tokens > 0 && tokens > 0 ? fmtPct(100 * tokens / total.tokens) : "–"; }
    function bar(tokens, kind) {
      return el("span", { class: "sc-track", role: "cell", "aria-hidden": "true" },
        [total.tokens > 0 && tokens > 0 ? el("i", { "data-kind": kind, style: "width:" + (100 * tokens / total.tokens).toFixed(1) + "%" }) : null]);
    }
    // Kinds the vocabulary does not know go just before the two the
    // classifier could not name.
    var order = EFFORT_ORDER.slice(0, -2).concat(extra, EFFORT_ORDER.slice(-2));
    var rows = order.map(function (kind) {
      var row = sums[kind];
      var empty = !row;
      row = row || { calls: 0, tools: 0, tokens: 0 };
      return el("div", { class: "sc-row sc-effort", role: "row", "data-effort": kind, "data-empty": empty ? "true" : null }, [
        el("span", { class: "sc-what", role: "cell" }, [kindSwatch(kind), el("span", { text: kindLabel(kind) })]),
        el("span", { class: "sc-num", role: "cell", text: dashed(row.calls) }),
        el("span", { class: "sc-num", role: "cell", text: dashed(row.tools) }),
        el("span", { class: "sc-num", role: "cell", text: dashedTokens(row.tokens) }),
        bar(row.tokens, kind),
        el("span", { class: "sc-num", role: "cell", text: share(row.tokens) })
      ]);
    });
    rows.push(el("div", { class: "sc-row sc-effort sc-total", role: "row", "data-effort": "total" }, [
      el("span", { role: "cell", text: "Total" }),
      el("span", { class: "sc-num", role: "cell", text: dashed(total.calls) }),
      el("span", { class: "sc-num", role: "cell", text: dashed(total.tools) }),
      el("span", { class: "sc-num", role: "cell", text: dashedTokens(total.tokens) }),
      el("span", { role: "cell", "aria-hidden": "true" }),
      el("span", { class: "sc-num", role: "cell", text: total.tokens > 0 ? "100%" : "–" })
    ]));
    var band = el("div", { class: "sc-band", "data-band": "effort" }, [
      bandHead("Where the effort went", "by work type"),
      el("div", { class: "sc-table sc-efforts", role: "table", "aria-label": "Effort by work type" }, [
        el("div", { class: "sc-row sc-th", role: "row" }, ["Type", "Mdl", "Tool", "Tokens", "", "Share"].map(function (h, i) {
          return el("span", { role: "columnheader", class: i > 0 && i !== 4 ? "sc-num" : null, text: h });
        }))
      ].concat(rows))
    ]);
    // A prompt recorded before steps existed leaves calls outside any stage;
    // say how many rather than let the totals disagree silently.
    var missingCalls = (p.model_calls || 0) - total.calls;
    var missingTokens = (Number(p.tokens && p.tokens.total) || 0) - total.tokens;
    if (missingCalls > 0 || missingTokens > 0) band.appendChild(el("p", { class: "coverage-note",
      text: count(Math.max(missingCalls, 0), "model call") + " and " + fmtTokens(Math.max(missingTokens, 0)) + " tokens are not in a recorded stage." }));
    return band;
  }

  // skillsBand lists every skill the recording shows being loaded, how, and
  // the stage each load happened in. A load is all a transcript can show.
  function skillsBand(skills, stageOfCall, recorded) {
    var loads = 0;
    (skills || []).forEach(function (skill) { loads += (skill.loads || []).length; });
    var band = el("div", { class: "sc-band", "data-band": "skills", "data-skills": "true" }, [
      bandHead("Skills", skills && skills.length ? count(skills.length, "skill") + " · " + count(loads, "load") : null)
    ]);
    if (!skills) {
      band.appendChild(el("p", { class: "coverage-note", text: "Skills not recorded yet" }));
      return band;
    }
    if (!skills.length) {
      band.appendChild(el("p", { class: "coverage-note", text: "No skill was loaded in this recording." }));
      return band;
    }
    band.appendChild(el("ul", { class: "sc-skills" }, skills.map(function (skill, i) {
      var via = [];
      var chips = (skill.loads || []).map(function (load) {
        var how = load.via === "tool" ? "skill tool" : "read SKILL.md";
        if (via.indexOf(how) === -1) via.push(how);
        var st = load.call_id ? stageOfCall[load.call_id] : null;
        if (!st) return el("span", { class: "sc-load", "data-linked": "false", title: load.at ? "Loaded " + day(load.at) : null,
          text: recorded ? "stage not linked" : "stage not recorded" });
        var kind = st.step.kind || "unknown";
        return el("span", { class: "sc-load", "data-stage": String(st.n), title: load.at ? "Loaded " + day(load.at) : null }, [
          el("span", { class: "sc-load-n", text: "#" + st.n }), kindSwatch(kind), txt(kindLabel(kind))
        ]);
      });
      return el("li", { "data-skill": skill.name }, [
        el("div", { class: "sc-skill-head" }, [skillPin(i + 1, skill.name), el("strong", { class: "sc-skill-name", text: skill.name }),
          el("span", { class: "sc-skill-count", text: "×" + (skill.count || (skill.loads || []).length) })]),
        el("div", { class: "sc-skill-where" }, [el("span", { text: (via.join(" and ") || "load not recorded") + " · loaded in" })].concat(chips))
      ]);
    })));
    band.appendChild(el("p", { class: "sc-note", text: "A load is the harness's skill tool being called or a SKILL.md being read. It does not show that the skill was followed." }));
    return band;
  }

  function revealPrompt(id) {
    detailState["prompt:" + id] = true;
    draw();
    var target = matchingNode("data-detail", "prompt:" + id);
    if (target) target.scrollIntoView({ block: "start", behavior: "auto" });
  }

  // executionCounts splits the recorded executions the way the ledger's tool
  // count is read: native commands (and how many failed), process polls, and
  // the wrappers the harness ran around them.
  function executionCounts(p) {
    var counts = { native: 0, failed: 0, polls: 0, wrappers: 0 };
    (p.executions || []).forEach(function (e) {
      if (e.poll) counts.polls++;
      else if (e.is_wrapper) counts.wrappers++;
      else { counts.native++; if (isFailedNative(e)) counts.failed++; }
    });
    return counts;
  }

  function byTokensDesc(a, b) {
    var av = a.tokens ? Number(a.tokens.total) || 0 : -1;
    var bv = b.tokens ? Number(b.tokens.total) || 0 : -1;
    return bv - av;
  }

  function workAnswer(body, p) {
    var ledger = body.ledger || {};
    var counts = executionCounts(p);
    var changes = Array.isArray(p.progress) ? count(p.progress.length, "file change") : "file changes not recorded";
    var children = [
      el("div", { class: "big num", text: numText(ledger.tool_count) }),
      el("p", { class: "answer-sub", text: count(counts.native, "native command") + " (" + counts.failed + " failed) · " +
        count(counts.polls, "process poll") + " · " + count(counts.wrappers, "wrapper") + " · " + changes })
    ];
    var overview = p.overview;
    if (!overview) {
      children.push(el("p", { class: "coverage-note", text: "Work types not recorded yet" }));
      return answerCard("work", "Tool calls by work", children);
    }
    var total = Number(p.tokens && p.tokens.total) || 0;
    // A category with no call, no execution and no usage was observed in no
    // prompt at all; listing it would name work that never happened.
    var categories = (overview.categories || []).filter(function (c) {
      return c.model_calls || c.execution_count || c.tokens;
    }).sort(byTokensDesc);
    if (!categories.length) {
      children.push(el("p", { class: "coverage-note", text: "No work observed yet" }));
      return answerCard("work", "Tool calls by work", children);
    }
    children.push(el("div", { class: "answer-bars", "aria-label": "Model calls and tokens per work type" }, categories.map(function (category) {
      var share = category.tokens && total > 0 ? 100 * (Number(category.tokens.total) || 0) / total : null;
      // apply_patch edits and polls are calls without a native command, so a
      // zero command count is left out rather than shown as a measurement.
      var numbers = count(category.model_calls || 0, "call") +
        (category.execution_count ? " · " + count(category.execution_count, "cmd") : "") + " · " +
        (category.tokens ? fmtTokens(category.tokens.total) + " · " + fmtPct(share) : "usage not attributed");
      return el("div", { class: "answer-bar", "data-kind": category.kind }, [
        el("div", { class: "row" }, [kindChip(category.kind), el("span", { class: "n", text: numbers })]),
        share == null ? null : el("span", { class: "track", role: "img", "aria-label": kindLabel(category.kind) + " " + fmtPct(share) + " of tokens" }, [
          el("i", { style: "width:" + Math.max(0, Math.min(100, share)).toFixed(3) + "%" })])
      ]);
    })));
    children.push(el("p", { class: "actions-note", text: "Counts are model calls per work type; tokens belong to those calls." }));
    return answerCard("work", "Tool calls by work", children);
  }

  function processForFinding(p, f) {
    var ids = f.execution_ids || [];
    return (p.processes || []).find(function (item) {
      return ids.some(function (id) {
        return (item.poll_ids || []).indexOf(id) !== -1 || (item.execution_ids || []).indexOf(id) !== -1;
      });
    });
  }

  function repeatsAnswer(body, p) {
    var loops = p.loops;
    if (!loops) return answerCard("repeats", "Repeats", [el("p", { class: "coverage-note", text: "Repeats not recorded yet" })]);
    var counters = el("span", { text: "Same content re-read: " + numText(loops.repeated_reads) + " · same-error retries: " + numText(loops.repeated_errors) +
      " · repair loops: " + numText(loops.repair_loops) + " · repeats inside segments: " + numText(loops.segment_repeats) });
    if (!loops.measured) {
      // A harness without native process identities cannot have its polling
      // counted; printing 0 chains would claim it never polled.
      return answerCard("repeats", "Repeats", [
        el("div", { class: "big", text: "not measurable" }),
        el("p", { class: "answer-sub", text: loops.coverage || "Polling chains need native process identities, which this harness does not report; repeated reads and retries are still detected." }),
        el("div", { class: "answer-lines" }, [counters])
      ]);
    }
    var polls = Number(loops.polls) || 0;
    var total = Number(p.tokens && p.tokens.total) || 0;
    var lines = [el("span", { text: count(loops.unchanged_polls, "poll") + " saw no new output (" +
      fmtPct(polls > 0 ? 100 * (Number(loops.unchanged_polls) || 0) / polls : null) + ") · " +
      count(loops.processes_polled, "process was polled", "processes were polled") })];
    if (loops.poll_tokens) lines.push(el("span", { text: "Poll calls: " + numText(loops.poll_calls) + " · " + fmtTokens(loops.poll_tokens.total) + " tokens · " +
      fmtPct(total > 0 ? 100 * (Number(loops.poll_tokens.total) || 0) / total : null) + " of the Crew" }));
    var longest = null;
    (p.findings || []).forEach(function (f) {
      if (f.kind === "process_polling" && (!longest || (f.count || 0) > (longest.count || 0))) longest = f;
    });
    if (longest) {
      var process = processForFinding(p, longest);
      lines.push(el("span", {}, [txt("Longest: "),
        el("button", { class: "text-button", type: "button", "data-focus": "answer-longest-chain", text: excerpt(longest.title || longest.kind, 72),
          onclick: function () { selectedFinding = longest.id; revealSegments(longest.segment_ids || [], true); } }),
        txt(" · " + (process
          ? count(process.polls, "poll") + " · " + numText(process.unchanged_polls) + " unchanged · " + fmtMs(process.elapsed_ms) +
            (process.tokens ? " · " + fmtTokens(process.tokens.total) + " tokens" : "")
          : "process record not linked"))]));
    }
    lines.push(counters);
    return answerCard("repeats", "Repeats", [
      el("div", { class: "big num", text: count(loops.chains, "chain") + " · " + count(loops.polls, "poll") }),
      el("div", { class: "answer-lines" }, lines)
    ]);
  }

  function segmentButton(p, s, focusKey) {
    return el("button", { class: "text-button", type: "button", "data-focus": focusKey, text: segmentLabel(s, p),
      onclick: function () { revealSegments([s.id]); } });
  }

  // The most expensive step is answered twice, because the two rankings
  // answer different questions: output + thinking is what the model wrote,
  // total tokens is the context it was sent again and again.
  function expensiveAnswer(body, p) {
    var lines = [];
    var callIDs = p.top_output_call_ids;
    var callID = (callIDs || [])[0];
    if (!callIDs) lines.push(el("span", { class: "coverage-note", text: "calls not ranked yet" }));
    else if (!callID) lines.push(el("span", { text: "no model call recorded" }));
    else {
      var call = byID(body.turns, callID);
      var segment = (p.segments || []).find(function (s) { return (s.call_ids || []).indexOf(callID) !== -1; });
      lines.push(el("span", {}, [
        el("strong", { text: call ? "call #" + call.ordinal : "call not in this page's ledger" }),
        call ? txt(" · " + fmtTokens(call.tokens && call.tokens.output) + " output + " + fmtTokens(call.tokens && call.tokens.thinking) + " thinking · " +
          fmtMs(call.duration_ms) + " · context after " + fmtKnown(call.context_tokens_after) +
          (segment ? " · " + promptTag(p, segment.prompt_id, call.started_at) : "")) : null
      ]));
      lines.push(el("span", {}, [txt("segment: "), segment ? segmentButton(p, segment, "answer-top-call") : txt("no segment holds this call")]));
    }
    var segmentIDs = p.top_output_segment_ids;
    var top = byID(p.segments, (segmentIDs || [])[0]);
    if (!segmentIDs) lines.push(el("span", { class: "coverage-note", text: "segments not ranked yet" }));
    else if (top) lines.push(el("span", {}, [txt("segment with most: "), segmentButton(p, top, "answer-top-output-segment"),
      txt(" · " + (top.model_calls === 0 ? "usage pending" : fmtTokens((Number(top.tokens && top.tokens.output) || 0) + (Number(top.tokens && top.tokens.thinking) || 0)) + " output + thinking") +
        " · " + count(top.model_calls || 0, "call") + " · " + fmtMs(top.elapsed_ms))]));
    var contextIDs = p.top_segment_ids;
    var context = byID(p.segments, (contextIDs || [])[0]);
    return answerCard("expensive", "Most expensive step", [
      el("h3", { text: "By output + thinking" }),
      el("div", { class: "answer-lines" }, lines),
      el("h3", { text: "By context re-sent" }),
      el("div", { class: "answer-lines" }, [context
        ? el("span", {}, [segmentButton(p, context, "answer-top-context-segment"),
          txt(" · " + (context.model_calls === 0 ? "usage pending" : fmtTokens(context.tokens && context.tokens.total) + " tokens") + " · " +
            count(context.model_calls || 0, "call") + " · " + count(context.repeat_count, "repeat") + " · " + fmtMs(context.elapsed_ms))])
        : el("span", { class: "coverage-note", text: contextIDs ? "no segment recorded" : "not ranked yet" })])
    ]);
  }

  function findingsPanel(body, p) {
    var findings = p.findings || [];
    var top = (p.top_finding_ids || []).map(function (id) { return byID(findings, id); }).filter(Boolean);
    if (!top.length) top = findings.slice(0, 3);
    top = top.slice(0, 3);
    var section = el("section", { class: "section", "aria-label": "Findings to inspect" }, [el("h2", { text: "Worth a look" })]);
    if (!findings.length) {
      section.appendChild(el("p", { class: "empty", text: "No pattern crossed a detector threshold in the available evidence." }));
      return section;
    }
    var list = el("div", { class: "finding-list" });
    top.forEach(function (f) { list.appendChild(findingCard(body, p, f)); });
    section.appendChild(list);
    var rest = findings.filter(function (f) { return !top.some(function (t) { return t.id === f.id; }); });
    if (rest.length) section.appendChild(savedDetails("more-findings", "Show " + rest.length + " more findings",
      rest.map(function (f) { return findingCard(body, p, f); }), "more-findings"));
    section.appendChild(el("p", { class: "actions-note", text: "Findings can share the same calls; their token totals are not additive. Repetition alone does not establish waste." }));
    var selected = byID(findings, selectedFinding);
    if (selected) section.appendChild(findingEvidence(body, p, selected));
    return section;
  }

  function findingCard(body, p, f) {
    return el("button", { class: "finding-card", type: "button", "aria-pressed": selectedFinding === f.id ? "true" : "false",
      "data-focus": "finding:" + f.id, onclick: function () {
        selectedFinding = f.id;
        revealSegments(f.segment_ids || [], true);
      } }, [
      findingKindChip(f),
      el("span", { class: "finding-title", text: f.title || f.kind }),
      el("span", { class: "finding-detail", text: f.detail || "" }),
      el("span", { class: "finding-usage", text: usageText(f.tokens) }),
      el("span", { class: "finding-footer", text: (f.ongoing ? "Ongoing" : "Recorded") + " · " + (f.confidence || "unknown") + " confidence · Inspect evidence →" })
    ]);
  }

  function findingEvidence(body, p, f) {
    var box = el("section", { class: "card finding-evidence", "data-evidence-panel": "", tabindex: "-1" }, [
      el("div", { class: "card-head" }, [el("h3", { text: f.title }), el("button", { class: "chip", type: "button", text: "Close evidence",
        "data-focus": "close-evidence", onclick: function () { selectedFinding = ""; draw(); } })]),
      el("p", { text: f.review || "Inspect the commands and source records below." }),
      el("div", { class: "facts" }, [el("span", { text: (f.count || 0) + " observations" }),
        el("span", { text: (clock(f.started_at) || "unknown start") + " → " + (clock(f.ended_at) || "last observed ongoing") }),
        el("span", { text: "Rule " + (f.rule || "unknown") })])
    ]);
    var segments = (f.segment_ids || []).map(function (id) { return byID(p.segments, id); }).filter(Boolean);
    if (segments.length) box.appendChild(el("div", { class: "chips evidence-links" }, segments.map(function (s) {
      return el("button", { class: "chip", type: "button", "data-focus": "evidence-segment:" + s.id, text: "Open " + segmentLabel(s, p),
        onclick: function () { revealSegments([s.id]); } });
    })));
    var executions = (f.execution_ids || []).map(function (id) { return byID(p.executions, id); }).filter(Boolean);
    if (executions.length) box.appendChild(executionList(p, executions, f.execution_ids));
    var evidence = f.evidence || [];
    if (evidence.length) box.appendChild(savedDetails("finding-sources:" + f.id, "Source evidence (" + evidence.length + ")",
      [el("ul", { class: "evidence-sources" }, evidence.map(function (e) { return el("li", {}, [
        el("span", { text: e.label || e.kind || e.id }), sourceNote(e.source_ref)
      ]); }))], "evidence-sources-wrap"));
    if (!segments.length && !executions.length) box.appendChild(el("p", { class: "coverage-note", text: "This finding has no confirmed segment or execution link. Its source evidence is shown above." }));
    return box;
  }

  function revealSegments(ids, finding) {
    ids.forEach(function (id) { detailState["segment:" + id] = true; });
    selectedSegment = ids[0] || "";
    draw();
    var target = finding ? view.querySelector("[data-evidence-panel]") : matchingNode("data-segment", selectedSegment);
    if (target) { target.scrollIntoView({ block: "start", behavior: "auto" }); target.focus({ preventScroll: true }); }
  }

  function matchingNode(attribute, value) {
    return Array.from(view.querySelectorAll("[" + attribute + "]")).find(function (node) { return node.getAttribute(attribute) === value; });
  }

  // The overview is shared by Mate exchanges and Crew prompts. Categories
  // partition model-call usage; the ordered stages retain returns to a type.
  // Collapsed summaries deliberately contain no nested interactive controls.
  function overviewSummary(overview) {
    if (!overview) return el("div", { class: "overview-summary overview-unavailable", text: "Work types not recorded yet" });
    return el("div", { class: "overview-summary" }, [
      el("div", { class: "overview-sentence", text: overview.summary || "Work types are not classified in the recorded evidence" }),
      el("div", { class: "overview-types", "aria-label": "Types of work in this prompt" }, (overview.categories || []).map(function (category) {
        return el("span", { class: "work-kind", "data-kind": category.kind, text: category.label || category.kind || "Unknown" });
      }))
    ]);
  }

  function workOverview(overview, context) {
    if (!overview) return null;
    var section = el("section", { class: "work-overview", "aria-label": "Prompt work overview", "data-overview": context.prefix }, [
      el("h3", { text: "Work in this prompt" }),
      overviewStages(overview.sequence || [], context),
      el("p", { class: "actions-note", text: "Each model call is counted in one type. Calls with several types stay in Mixed activity; token usage is not split among commands or files. Observed execution times can overlap across types." })
    ]);
    var categories = overview.categories || [];
    if (!categories.length) section.appendChild(el("p", { class: "coverage-note", text: "There is not enough recorded evidence to classify the work." }));
    categories.forEach(function (category) { section.appendChild(overviewCategory(category, context)); });
    section.appendChild(el("p", { class: "coverage-note", text: overview.coverage || "Classification coverage has not been reported." }));
    if (overview.rule) section.appendChild(el("p", { class: "actions-note", text: "Classification rule: " + overview.rule }));
    return section;
  }

  function overviewStages(sequence, context) {
    if (!sequence.length) return el("p", { class: "coverage-note", text: "The order of work stages is not recorded." });
    function stageList(stages, offset) {
      return el("ol", { class: "overview-stages", start: offset + 1, "aria-label": "Work stages in time order" }, stages.map(function (stage, index) {
        var label = (index + offset + 1) + ". " + (stage.label || stage.kind || "Unknown");
        var ids = stage.segment_ids || [];
        var item = context.performance && ids.length ? el("button", { class: "overview-stage", type: "button",
          "data-focus": context.prefix + ":stage:" + (index + offset), text: label,
          title: stage.started_at ? "Started " + day(stage.started_at) + " · open segment evidence" : "Open segment evidence",
          onclick: function () { revealSegments(ids); } }) : el("span", { class: "overview-stage", text: label,
            title: stage.started_at ? "Started " + day(stage.started_at) : "Time not recorded" });
        return el("li", { "data-kind": stage.kind }, [item]);
      }));
    }
    var first = stageList(sequence.slice(0, 12), 0);
    if (sequence.length <= 12) return first;
    return el("div", {}, [first, savedDetails(context.prefix + ":later-stages", "Continue through " + (sequence.length - 12) + " later work stages",
      [stageList(sequence.slice(12), 12)], "overview-later-stages")]);
  }

  function overviewCategory(category, context) {
    var key = context.prefix + ":category:" + category.kind;
    var details = savedDetails(key, "", [], "overview-category");
    var summary = details.querySelector("summary");
    append(summary, [
      el("span", { class: "overview-category-name", text: category.label || category.kind || "Unknown" }),
      el("span", { class: "overview-category-meta", text: (category.model_calls || 0) + " attributed calls" +
        (category.execution_count > 0 ? " · " + category.execution_count + " recorded executions" : "") }),
      el("span", { class: "overview-category-meta", text: category.tokens ? fmtTokens(category.tokens.total) + " tokens" : "usage not attributed" }),
      el("span", { class: "overview-category-meta", text: category.elapsed_ms == null ? "time unknown" : fmtMs(category.elapsed_ms) + " observed execution time" })
    ]);
    var body = el("div", { class: "overview-category-body" });
    if (category.tokens) body.appendChild(tokenMeter(category.tokens, { label: "Tokens of model calls assigned to this type" }));
    else body.appendChild(el("p", { class: "coverage-note", text: "No model-call usage is attributed exclusively to this type. Its recorded actions may belong to a mixed call or have no confirmed call link." }));
    if (context.performance && (category.segment_ids || []).length) body.appendChild(el("button", { class: "chip overview-evidence-button", type: "button",
      "data-focus": key + ":segments", text: "Open related work segments →", onclick: function () { revealSegments(category.segment_ids); } }));
    var executions = context.performance ? (category.execution_ids || []).map(function (id) { return byID(context.performance.executions, id); }).filter(Boolean) : [];
    var evidence = category.evidence || [];
    if (executions.length) body.appendChild(executionList(context.performance, executions, category.execution_ids));
    if (evidence.length) {
      var evidenceBox = overviewEvidence(evidence, key);
      body.appendChild(executions.length ? savedDetails(key + ":source-evidence", "Source and progress evidence (" + evidence.length + ")", [evidenceBox], "evidence-sources-wrap") : evidenceBox);
    }
    if (!executions.length && !evidence.length) body.appendChild(el("p", { class: "coverage-note", text: "Detailed action evidence is unavailable for this category." }));
    details.appendChild(body);
    return details;
  }

  function overviewEvidence(evidence, prefix) {
    function record(item) {
      return el("article", { class: "overview-evidence-record", "data-overview-evidence": item.id }, [
        el("div", { class: "facts" }, [el("strong", { text: item.label || item.kind || "Recorded action" }),
          item.at ? el("time", { title: item.at, text: clock(item.at) }) : null,
          item.status || item.exit_code != null ? el("span", { class: item.exit_code != null && item.exit_code !== 0 ? "execution-error" : null, text: executionStatus(item) }) : null]),
        item.command ? el("pre", { class: "execution-command", text: item.command }) : null,
        item.output_excerpt ? savedDetails(prefix + ":output:" + item.id, "Recorded output", [el("pre", { class: "execution-output", text: item.output_excerpt })], "execution-output-wrap") : null,
        sourceNote(item.source_ref)
      ]);
    }
    var box = el("div", { class: "overview-evidence" });
    evidence.slice(0, 8).forEach(function (item) { box.appendChild(record(item)); });
    if (evidence.length > 8) box.appendChild(savedDetails(prefix + ":more-evidence", "Show " + (evidence.length - 8) + " more evidence records",
      evidence.slice(8).map(record), "more-executions"));
    return box;
  }

  function promptTimelines(body, p) {
    var prompts = p.prompt_turns || [];
    if (!prompts.length) return el("p", { class: "empty", text: "Prompt boundaries have not been recorded." });
    var wrap = el("div", { class: "prompt-timelines" });
    prompts.slice().reverse().forEach(function (prompt, i) {
      var segments = (p.segments || []).filter(function (s) { return s.prompt_id === prompt.id; });
      var children = [el("div", { class: "prompt-body" }, [
        el("p", { class: "prompt-text", text: prompt.prompt || "Prompt text unavailable" }),
        el("div", { class: "facts" }, [el("span", { text: (prompt.model_calls || 0) + " model calls" }),
          el("span", { text: fmtTokens(prompt.tokens && prompt.tokens.total) + " tokens" }),
          el("span", {}, [txt("prompt elapsed "), elapsedNode(prompt.prompt_at || prompt.started_at, prompt.ended_at, prompt.elapsed_ms,
            !!p.current_segment_id && segments.some(function (s) { return s.id === p.current_segment_id; }))]),
          prompt.coverage ? el("span", { class: "coverage-note", text: prompt.coverage }) : null]),
        workOverview(prompt.overview, { prefix: "crew-overview:" + prompt.id, performance: p }),
        activityTimeline(body, p, prompt, segments),
        savedDetails("prompt-segment-sequence:" + prompt.id, "Detailed segment sequence (" + segments.length + ")", [
          el("div", { class: "segment-sequence", "aria-label": "Work segments in order" }, segments.map(function (s, index) {
          return el("button", { type: "button", class: "segment-step", "data-kind": s.kind,
            "data-focus": "timeline:" + s.id, text: (index + 1) + ". " + segmentLabel(s, p) + " · " + (s.model_calls === 0 ? "usage pending" : fmtTokens(s.tokens && s.tokens.total)),
            onclick: function () { revealSegments([s.id]); } });
        }))], "more-executions", !prompt.overview)
      ])];
      var unlinked = (p.executions || []).filter(function (e) { return e.prompt_id === prompt.id && !e.call_id; });
      if (unlinked.length) children[0].appendChild(savedDetails("prompt-native:" + prompt.id,
        "Native commands without model-call attribution (" + unlinked.length + ")", [
          el("p", { class: "coverage-note", text: "The harness identifies this prompt, but does not link these executions to a model response. No token cost is assigned." }),
          executionList(p, unlinked)
        ], "more-executions"));
      var label = "Prompt " + (prompts.length - i) + " · " + (day(prompt.prompt_at || prompt.started_at) || "time unknown") +
        " · " + excerpt(prompt.prompt || "Prompt text unavailable", 110);
      var card = savedDetails("prompt:" + prompt.id, label, children, "prompt-timeline", i === 0);
      card.querySelector("summary").appendChild(overviewSummary(prompt.overview));
      wrap.appendChild(card);
    });
    return wrap;
  }

  function svgNode(tag, attrs, text) {
    var node = document.createElementNS("http://www.w3.org/2000/svg", tag);
    Object.keys(attrs || {}).forEach(function (key) { node.setAttribute(key, String(attrs[key])); });
    if (text != null) node.textContent = String(text);
    return node;
  }

  // The lanes of a prompt timeline, top to bottom: the cumulative token
  // line, one output + thinking bar per call, the segments by kind, native
  // commands, polls with their chains, and decision waits. Every y is a
  // viewBox unit; the SVG scales to the card's width.
  var LANES = {
    tokens: { label: 22, top: 26, bottom: 96 },
    output: { label: 118, top: 102, bottom: 126 },
    segments: { label: 150, top: 132, height: 26 },
    commands: { label: 174, top: 164, height: 11 },
    polls: { label: 196, top: 188, height: 8, bracket: 199, count: 214 },
    decisions: { label: 236, top: 226, height: 12 },
    axis: 258,
    height: 266
  };

  function activityTimeline(body, p, prompt, segments) {
    var start = timestamp(prompt.prompt_at || prompt.started_at);
    var end = timestamp(prompt.ended_at);
    if (segments.some(function (s) { return s.id === p.current_segment_id; })) end = timestamp(p.freshness && p.freshness.last_observed_at);
    if (end == null) end = timestamp(p.freshness && p.freshness.last_observed_at);
    if (start == null || end == null || end <= start) return el("p", { class: "empty", text: "A shared time axis needs recorded start and end observations." });
    var width = 920, left = 92, right = 12, plotWidth = width - left - right;
    var x = function (at) { return left + Math.max(0, Math.min(1, (at - start) / (end - start))) * plotWidth; };
    var svg = svgNode("svg", { viewBox: "0 0 " + width + " " + LANES.height, role: "img",
      "aria-label": "Cumulative tokens, output per call, work segments by kind, command spans, poll chains and decision waits on one time axis" });
    [["Tokens", LANES.tokens.label], ["Output", LANES.output.label], ["Segments", LANES.segments.label],
      ["Commands", LANES.commands.label], ["Polls", LANES.polls.label], ["Decisions", LANES.decisions.label]].forEach(function (lane) {
      svg.appendChild(svgNode("text", { x: 0, y: lane[1], class: "timeline-label" }, lane[0]));
    });
    for (var tick = 0; tick <= 4; tick++) {
      var at = start + (end - start) * tick / 4;
      svg.appendChild(svgNode("line", { x1: x(at), x2: x(at), y1: LANES.tokens.top, y2: LANES.decisions.top + LANES.decisions.height + 2, class: "timeline-grid" }));
      svg.appendChild(svgNode("text", { x: x(at), y: LANES.axis, "text-anchor": tick === 0 ? "start" : tick === 4 ? "end" : "middle", class: "timeline-label" }, clock(new Date(at).toISOString())));
    }
    segments.forEach(function (s) {
      var from = timestamp(s.started_at), to = timestamp(s.ended_at);
      if (from == null) return;
      if (to == null) to = end;
      var selected = byID(p.findings, selectedFinding);
      var related = selectedSegment === s.id || (selected && (selected.segment_ids || []).indexOf(s.id) !== -1);
      var rect = svgNode("rect", { x: x(from), y: LANES.segments.top, width: Math.max(1, x(to) - x(from)), height: LANES.segments.height,
        class: "timeline-segment", "data-kind": s.kind || "unknown", "data-highlight": related ? "true" : "false" });
      rect.appendChild(svgNode("title", {}, kindLabel(s.kind) + " · " + segmentLabel(s, p) + " · " + usageText(s.tokens)));
      svg.appendChild(rect);
    });
    var callIDs = new Set();
    segments.forEach(function (s) { (s.call_ids || []).forEach(function (id) { callIDs.add(id); }); });
    var calls = (body.turns || []).filter(function (t) { return callIDs.has(t.id); }).slice().sort(function (a, b) {
      return (timestamp(a.ended_at || a.started_at) || 0) - (timestamp(b.ended_at || b.started_at) || 0);
    });
    var total = Number(prompt.tokens && prompt.tokens.total) || 1;
    var floor = LANES.tokens.bottom, rise = LANES.tokens.bottom - LANES.tokens.top;
    var cumulative = 0, path = "M " + left + " " + floor;
    calls.forEach(function (call) {
      var at = timestamp(call.ended_at || call.started_at);
      if (at == null) return;
      cumulative += Number(call.tokens && call.tokens.total) || 0;
      path += " H " + x(at).toFixed(2) + " V " + (floor - rise * Math.min(1, cumulative / total)).toFixed(2);
    });
    path += " H " + x(end);
    var line = svgNode("path", { d: path, class: "timeline-token-line" });
    line.appendChild(svgNode("title", {}, "cumulative recorded tokens · " + fmtTokens(prompt.tokens && prompt.tokens.total) + " by the end of the prompt"));
    svg.appendChild(line);
    svg.appendChild(svgNode("text", { x: width - right, y: LANES.tokens.label, "text-anchor": "end", class: "timeline-label" }, fmtTokens(prompt.tokens && prompt.tokens.total)));
    // Output + thinking per call: what the model wrote, scaled to the
    // prompt's heaviest call, so the reasoning steps stand out from the
    // cache reads the token line is dominated by.
    var outputOf = function (call) { return (Number(call.tokens && call.tokens.output) || 0) + (Number(call.tokens && call.tokens.thinking) || 0); };
    var maxOutput = calls.reduce(function (m, call) { return Math.max(m, outputOf(call)); }, 0);
    calls.forEach(function (call) {
      var at = timestamp(call.ended_at || call.started_at);
      if (at == null) return;
      var value = outputOf(call);
      var h = maxOutput > 0 ? Math.max(1, (LANES.output.bottom - LANES.output.top) * value / maxOutput) : 1;
      var bar = svgNode("rect", { x: (x(at) - 1.5).toFixed(2), y: (LANES.output.bottom - h).toFixed(2), width: 3, height: h.toFixed(2), class: "timeline-output" });
      bar.appendChild(svgNode("title", {}, "call #" + call.ordinal + " · " + fmtTokens(value) + " output + thinking"));
      svg.appendChild(bar);
    });
    if (maxOutput > 0) svg.appendChild(svgNode("text", { x: width - right, y: LANES.output.label, "text-anchor": "end", class: "timeline-label" }, "max " + fmtTokens(maxOutput)));
    var executions = (p.executions || []).filter(function (e) { return e.prompt_id === prompt.id; });
    executions.filter(function (e) { return !e.is_wrapper && !e.poll; }).forEach(function (e) {
      var from = timestamp(e.started_at), to = timestamp(e.ended_at);
      if (from == null) return;
      if (to == null && e.status !== "running" && e.status !== "in_progress") return;
      var rect = svgNode("rect", { x: x(from), y: LANES.commands.top, width: Math.max(2, x(to == null ? end : to) - x(from)), height: LANES.commands.height,
        class: "timeline-execution", "data-failed": isFailedNative(e) ? "true" : "false" });
      rect.appendChild(svgNode("title", {}, (e.command || e.tool || "execution") + " · " + executionStatus(e)));
      svg.appendChild(rect);
    });
    // One tick per poll; three or more polls of the same process are a chain
    // and get a bracket with their count, so repetition is legible without
    // opening a finding.
    var chains = Object.create(null);
    executions.filter(function (e) { return e.poll; }).forEach(function (e) {
      var from = timestamp(e.started_at);
      if (from == null) return;
      var change = e.new_output_bytes == null ? "new output unknown" : e.new_output_bytes === 0 ? "no new output" : fmtBytes(e.new_output_bytes) + " new output";
      var tickRect = svgNode("rect", { x: (x(from) - 1).toFixed(2), y: LANES.polls.top, width: 2.5, height: LANES.polls.height, class: "timeline-poll",
        "data-failed": e.status === "failed" || (e.exit_code != null && e.exit_code !== 0) ? "true" : "false" });
      tickRect.appendChild(svgNode("title", {}, "poll of " + (e.process_id ? "process " + e.process_id : "an unlinked process") + " · " + (e.status || "status unknown") + " · " + change));
      svg.appendChild(tickRect);
      if (e.process_id) (chains[e.process_id] = chains[e.process_id] || []).push({ at: from, execution: e });
    });
    Object.keys(chains).forEach(function (pid) {
      var polls = chains[pid];
      if (polls.length < 3) return;
      var ats = polls.map(function (poll) { return poll.at; });
      var a = x(Math.min.apply(null, ats)) - 3, b = x(Math.max.apply(null, ats)) + 3;
      var process = processForExecution(p, polls[0].execution);
      var bracket = svgNode("path", { d: "M " + a.toFixed(2) + " " + LANES.polls.bracket + " v 4 H " + b.toFixed(2) + " v -4", class: "timeline-bracket" });
      bracket.appendChild(svgNode("title", {}, "process " + pid + " polled " + polls.length + " times" +
        (process ? " · " + numText(process.unchanged_polls) + " without new output" : "")));
      svg.appendChild(bracket);
      svg.appendChild(svgNode("text", { x: ((a + b) / 2).toFixed(2), y: LANES.polls.count, "text-anchor": "middle", class: "timeline-label timeline-bracket-label" },
        "×" + polls.length + (process && process.tokens ? " · " + fmtTokens(process.tokens.total) : "")));
    });
    (body.questions || []).forEach(function (q) {
      var from = timestamp(q.asked_at), to = timestamp(q.answered_at);
      if (from == null || from > end || (to != null && to < start)) return;
      var rect = svgNode("rect", { x: x(from), y: LANES.decisions.top, width: Math.max(2, x(to == null ? end : to) - x(from)), height: LANES.decisions.height, class: "timeline-wait" });
      rect.appendChild(svgNode("title", {}, q.text || "Waiting for a decision"));
      svg.appendChild(rect);
    });
    var present = Object.keys(KINDS).filter(function (kind) { return segments.some(function (s) { return (s.kind || "unknown") === kind; }); });
    segments.forEach(function (s) { if (s.kind && !KINDS[s.kind] && present.indexOf(s.kind) === -1) present.push(s.kind); });
    return el("div", { class: "activity-timeline" }, [svg, kindLegend(present),
      el("p", { class: "actions-note", text: "Line: cumulative recorded tokens · bars: output + thinking of each call · blocks: work segments by kind · Commands: native executions · Polls: one tick per poll, a bracket marks a chain of three or more with its count. Select a segment below to see exact evidence." })]);
  }

  function kindLegend(kinds) {
    var items = kinds.map(function (kind) {
      return el("span", { class: "item" }, [el("span", { class: "swatch", "data-kind": kind, "aria-hidden": "true" }), txt(kindLabel(kind))]);
    });
    items.push(el("span", { class: "item" }, [el("span", { class: "swatch", "data-failed": "true", "aria-hidden": "true" }), txt("failed command")]));
    return el("div", { class: "legend timeline-legend", "aria-label": "Kinds of work on this timeline" }, items);
  }

  // toolMix is design callout ⑦: the overview's categories per prompt. Each
  // model call belongs to exactly one work type, so a row adds up to its
  // prompt and the Crew row adds up to the ledger.
  function toolMix(body, p) {
    var section = el("section", { class: "section", "aria-label": "Tool mix by prompt" }, [el("h2", { text: "Tool mix by prompt" })]);
    var prompts = p.prompt_turns || [];
    if (!prompts.length) {
      section.appendChild(el("p", { class: "empty", text: "Prompt boundaries have not been recorded." }));
      return section;
    }
    var overview = p.overview;
    if (!overview) section.appendChild(el("p", { class: "coverage-note", text: "Work types not recorded yet; calls, tokens, repeats and failed commands are still listed per prompt." }));
    // A kind no model call was ever attributed to would be a column of
    // dashes; the work card already names it with its execution count.
    var kinds = ((overview && overview.categories) || []).filter(function (c) { return c.model_calls || c.tokens; })
      .sort(byTokensDesc).map(function (c) { return c.kind; });
    var head = [el("th", { text: "Prompt" }), el("th", { class: "n", text: "Calls" }), el("th", { class: "n", text: "Tokens" })]
      .concat(kinds.map(function (kind) { return el("th", { class: "n" }, [kindChip(kind)]); }))
      .concat([el("th", { class: "n", text: "Repeats" }), el("th", { class: "n", text: "Failed cmds" })]);
    function row(label, calls, tokensText, categories, repeats, failed, className) {
      var cells = [el("td", { "data-label": "Prompt", class: "tool-mix-prompt", text: label }),
        el("td", { "data-label": "Calls", class: "n", text: calls }), el("td", { "data-label": "Tokens", class: "n", text: tokensText })];
      kinds.forEach(function (kind) {
        var category = categories ? categories.find(function (c) { return c.kind === kind; }) : null;
        // No overview for the prompt is unknown ("?"); a kind the prompt
        // never attributed a call to is nothing ("–"), not zero tokens.
        var text = !categories ? "?" : !category || (!category.model_calls && !category.tokens) ? "–"
          : (category.model_calls || 0) + " · " + (category.tokens ? fmtTokens(category.tokens.total) : "?");
        cells.push(el("td", { "data-label": kindLabel(kind), class: "n", text: text }));
      });
      cells.push(el("td", { "data-label": "Repeats", class: "n", text: repeats }));
      cells.push(el("td", { "data-label": "Failed cmds", class: "n", text: failed }));
      return el("tr", { class: className }, cells);
    }
    var repeatsOf = function (segments) { return segments.reduce(function (sum, s) { return sum + (Number(s.repeat_count) || 0); }, 0); };
    var tbody = el("tbody");
    prompts.forEach(function (prompt, i) {
      var segments = (p.segments || []).filter(function (s) { return s.prompt_id === prompt.id; });
      var failed = (p.executions || []).filter(function (e) { return e.prompt_id === prompt.id && isFailedNative(e); }).length;
      tbody.appendChild(row("P" + (i + 1) + " · " + excerpt(prompt.prompt || "Prompt text unavailable", 24), String(prompt.model_calls || 0),
        prompt.model_calls === 0 ? "?" : fmtTokens(prompt.tokens && prompt.tokens.total),
        prompt.overview ? prompt.overview.categories || [] : null, String(repeatsOf(segments)), String(failed), null));
    });
    tbody.appendChild(row("Crew", String(p.model_calls || 0), p.model_calls === 0 ? "?" : fmtTokens(p.tokens && p.tokens.total),
      overview ? overview.categories || [] : null, String(repeatsOf(p.segments || [])),
      String((p.executions || []).filter(isFailedNative).length), "tool-mix-crew"));
    section.appendChild(el("div", { class: "table-wrap" }, [el("table", { class: "tool-mix" }, [el("thead", {}, [el("tr", {}, head)]), tbody])]));
    section.appendChild(el("p", { class: "actions-note", text: "Each model call belongs to exactly one work type, so rows and columns add up to the ledger. Failed commands are native exit codes; repeats are the segments' own repeat counts." }));
    return section;
  }

  // unclassifiedCoverage says how much of the recording the classifier could
  // not name: mixed and unknown segments are listed as their own rows and
  // counted here, never folded into another kind.
  function unclassifiedCoverage(p) {
    var segments = p.segments || [];
    var unnamed = segments.filter(function (s) { return !s.kind || s.kind === "mixed" || s.kind === "unknown"; });
    var sum = function (key) { return unnamed.reduce(function (acc, s) { return acc + (Number(s.tokens && s.tokens[key]) || 0); }, 0); };
    var total = Number(p.tokens && p.tokens.total) || 0, output = Number(p.tokens && p.tokens.output) || 0;
    return "Unclassified segments: " + unnamed.length + " of " + segments.length + " · " +
      fmtPct(total > 0 ? 100 * sum("total") / total : null) + " of tokens · " + fmtPct(output > 0 ? 100 * sum("output") / output : null) + " of output";
  }

  function segmentTable(body, p) {
    var section = el("section", { class: "section", "aria-label": "Work segments" });
    var select = el("select", { "aria-label": "Sort work segments", "data-focus": "segment-sort" }, [
      el("option", { value: "time", text: "Timeline order" }), el("option", { value: "output", text: "Output + thinking, highest first" }),
      el("option", { value: "input", text: "Fresh input, highest first" }),
      el("option", { value: "total", text: "Total tokens, highest first" }), el("option", { value: "elapsed", text: "Elapsed, longest first" })
    ]);
    select.value = segmentSort;
    select.addEventListener("change", function () { segmentSort = select.value; draw(); });
    section.appendChild(el("div", { class: "card-head segment-controls" }, [el("h2", { text: "Work segments" }), el("label", {}, [txt("Sort by "), select])]));
    section.appendChild(el("p", { class: "actions-note", text: "Tokens belong to model calls in each segment. Repeated visits remain separate; no token cost is assigned to an individual command." }));
    var all = p.segments || [];
    section.appendChild(kindFilterChips(all));
    var rows = all.filter(function (s) { return segmentKindFilter === "all" || (s.kind || "unknown") === segmentKindFilter; });
    function sortValue(s) {
      if (segmentSort === "elapsed") return s.elapsed_ms == null ? -1 : s.elapsed_ms;
      if (!s.tokens) return -1;
      if (segmentSort === "output") return (Number(s.tokens.output) || 0) + (Number(s.tokens.thinking) || 0);
      return s.tokens[segmentSort] == null ? -1 : s.tokens[segmentSort];
    }
    if (segmentSort !== "time") rows.sort(function (a, b) { return sortValue(b) - sortValue(a); });
    if (!rows.length) section.appendChild(el("p", { class: "empty", text: all.length ? "No segment matches this kind filter." : "No model calls have been assigned to a work segment." }));
    rows.forEach(function (s) { section.appendChild(segmentRow(body, p, s)); });
    section.appendChild(el("p", { class: "coverage-note", text: unclassifiedCoverage(p) }));
    return section;
  }

  // Filter chips name every kind present with its count; "All" is the
  // default. The pressed chip is marked by aria-pressed, not by colour alone.
  function kindFilterChips(segments) {
    var counts = Object.create(null);
    segments.forEach(function (s) { var kind = s.kind || "unknown"; counts[kind] = (counts[kind] || 0) + 1; });
    var wrap = el("div", { class: "chips segment-filters", role: "group", "aria-label": "Filter work segments by kind" });
    function chip(key, label, n, kind) {
      var b = el("button", { class: "chip", type: "button", "data-focus": "segment-kind:" + key,
        "aria-pressed": segmentKindFilter === key ? "true" : "false",
        onclick: function () { segmentKindFilter = key; draw(); } });
      if (kind) b.appendChild(el("span", { class: "swatch", "data-kind": kind, "aria-hidden": "true" }));
      b.appendChild(txt(label + " "));
      b.appendChild(el("span", { class: "n", text: String(n) }));
      return b;
    }
    wrap.appendChild(chip("all", "All", segments.length));
    var kinds = Object.keys(KINDS).concat(Object.keys(counts).filter(function (kind) { return !KINDS[kind]; }));
    kinds.forEach(function (kind) { if (counts[kind]) wrap.appendChild(chip(kind, kindLabel(kind), counts[kind], kind)); });
    return wrap;
  }

  // segmentToolCalls sums the ledger's tool count over the segment's calls.
  // A call the page does not hold makes the sum unknown, not smaller.
  function segmentToolCalls(body, s) {
    if (s.model_calls === 0) return "?";
    var ids = s.call_ids || [];
    var sum = 0;
    for (var i = 0; i < ids.length; i++) {
      var call = byID(body.turns, ids[i]);
      if (!call || call.tool_count == null) return "?";
      sum += Number(call.tool_count) || 0;
    }
    return String(sum);
  }

  function segmentRow(body, p, s) {
    var f = byID(p.findings, selectedFinding);
    var related = f && (f.segment_ids || []).indexOf(s.id) !== -1;
    var open = !!detailState["segment:" + s.id];
    var details = savedDetails("segment:" + s.id, "", [], "work-segment", false);
    details.setAttribute("data-segment", s.id);
    details.setAttribute("data-selected", selectedSegment === s.id || related ? "true" : "false");
    details.setAttribute("tabindex", "-1");
    var summary = details.querySelector("summary");
    append(summary, [el("div", { class: "segment-heading" }, [
      el("span", { class: "segment-label", text: segmentLabel(s, p) }),
      kindChip(s.kind),
      el("span", { class: "segment-clock mono", title: s.started_at || "time unknown", text: promptTag(p, s.prompt_id, s.started_at, true) }),
      el("span", { class: "badge", text: s.outcome || "unknown" })]),
      el("div", { class: "segment-numbers" }, [
        segmentNumber("Fresh input", segmentTokens(s, "input")), segmentNumber("Cache read", segmentTokens(s, "cache_read")),
        segmentNumber("Cache write", segmentTokens(s, "cache_write")), segmentNumber("Output", segmentTokens(s, "output")),
        segmentNumber("Thinking", segmentTokens(s, "thinking")), segmentNumber("Total", segmentTokens(s, "total")),
        el("span", { class: "segment-number" }, [el("span", { text: "Elapsed" }), elapsedNode(s.started_at, s.ended_at, s.elapsed_ms, s.id === p.current_segment_id)]),
        segmentNumber("Calls", String(s.model_calls || 0)), segmentNumber("Tool calls", segmentToolCalls(body, s)),
        segmentNumber("Repeats", s.repeat_count == null ? "?" : String(s.repeat_count))
      ])]);
    // Build expensive execution and call detail only while it is open.
    if (open) details.appendChild(segmentEvidence(body, p, s, f));
    details.addEventListener("toggle", function () {
      if (details.isConnected && details.open && !details.querySelector(".segment-evidence")) details.appendChild(segmentEvidence(body, p, s, f));
    });
    return details;
  }

  function segmentNumber(label, value) {
    return el("span", { class: "segment-number" }, [el("span", { text: label }), el("strong", { class: "num", text: value })]);
  }

  function segmentEvidence(body, p, s, f) {
    var box = el("div", { class: "segment-evidence" });
    if (s.model_calls === 0) box.appendChild(el("p", { class: "coverage-note", text: "Usage not reported for this activity yet. The native execution is visible before a model response reports tokens." }));
    var prompt = byID(p.prompt_turns, s.prompt_id);
    box.appendChild(el("div", { class: "facts" }, [
      el("span", { text: "Tool elapsed " + fmtMs(s.tool_elapsed_ms) }), el("span", { text: "Summed invocation time " + fmtMs(s.invocation_ms) }),
      el("span", { text: "Classification: " + (s.rule || "unknown") })
    ]));
    if (prompt) box.appendChild(savedDetails("segment-prompt:" + s.id, "Prompt that started this work", [
      el("p", { class: "prompt-text", text: prompt.prompt || "Prompt text unavailable" }), sourceNote(prompt.source_ref)
    ], "evidence-prompt"));
    var executions = (s.execution_ids || []).map(function (id) { return byID(p.executions, id); }).filter(Boolean);
    box.appendChild(el("h3", { text: "Commands and tool evidence" }));
    box.appendChild(executions.length ? executionList(p, executions, f && f.execution_ids) :
      el("p", { class: "empty", text: "No execution is linked to these model calls. Inspect the call records for available tool evidence." }));
    var calls = (s.call_ids || []).map(function (id) { return byID(body.turns, id); }).filter(Boolean);
    var callList = el("ol", { class: "turns" });
    calls.forEach(function (call) {
      var row = turnRow(body, call);
      if (f && (f.call_ids || []).indexOf(call.id) !== -1) row.setAttribute("data-highlight", "true");
      callList.appendChild(row);
    });
    box.appendChild(savedDetails("segment-calls:" + s.id, "Model calls in this segment (" + calls.length + ")", [callList], "segment-calls"));
    return box;
  }

  function executionStatus(e) {
    if (e.exit_code != null) return "exit " + e.exit_code + (e.exit_code === 0 ? " · completed" : " · failed");
    if (e.is_wrapper) return "wrapper result · child exit unknown";
    if (e.status === "running" || e.status === "in_progress") return "still running at last observation";
    return e.status ? e.status + " · exit unknown" : "result unknown";
  }

  function executionList(p, executions, highlighted) {
    var wrap = el("div", { class: "execution-list" });
    var show = executions.slice(0, 12);
    show.forEach(function (e) { wrap.appendChild(executionCard(p, e, (highlighted || []).indexOf(e.id) !== -1)); });
    if (executions.length > show.length) wrap.appendChild(savedDetails("executions:" + executions[0].id + ":" + executions.length,
      "Show " + (executions.length - show.length) + " more execution records", executions.slice(show.length).map(function (e) {
        return executionCard(p, e, (highlighted || []).indexOf(e.id) !== -1);
      }), "more-executions"));
    return wrap;
  }

  function executionCard(p, e, highlight) {
    var process = processForExecution(p, e);
    var running = e.status === "running" || e.status === "in_progress";
    var card = el("article", { class: "execution-card", "data-highlight": highlight ? "true" : "false", "data-failed": e.exit_code != null && e.exit_code !== 0 ? "true" : "false" });
    card.appendChild(el("div", { class: "execution-head" }, [
      el("span", { class: "badge", text: e.poll ? "Process poll" : e.is_wrapper ? "Wrapper" : e.tool || "Execution" }),
      el("span", { class: "execution-status", text: executionStatus(e) }),
      elapsedNode(e.started_at, e.ended_at, e.duration_ms, running)
    ]));
    card.appendChild(el("pre", { class: "execution-command", text: e.command || e.target || (e.poll && process && process.command) || "Command not recorded" }));
    if (e.poll && process && process.command && process.command !== e.command) card.appendChild(el("p", { class: "process-evidence mono", text: "Job: " + process.command }));
    var output = e.new_output_bytes == null ? "new output unknown" : e.new_output_bytes === 0 ? "no new output observed" : fmtBytes(e.new_output_bytes) + " new output";
    card.appendChild(el("div", { class: "facts" }, [
      e.cwd ? el("span", { class: "mono", text: "cwd " + e.cwd }) : null,
      e.process_id ? el("span", { class: "mono", text: "process " + e.process_id }) : e.poll ? el("span", { text: "process link unavailable" }) : null,
      el("span", { text: "output " + fmtBytes(e.output_bytes) + " · " + output + (e.truncated ? " · truncated" : "") })
    ]));
    if (process) card.appendChild(el("p", { class: "process-evidence", text: "Process history: " + (process.polls || 0) + " polls · " +
      (process.progress_polls || 0) + " with new output · " + (process.unchanged_polls || 0) + " unchanged · " + (process.unknown_polls || 0) + " output unknown" }));
    if (e.error_signature || e.output_excerpt) card.appendChild(savedDetails("output:" + e.id, e.error_signature ? "Error and recorded output" : "Recorded output preview",
      [e.error_signature ? el("p", { class: "execution-error", text: e.error_signature }) : null,
        e.output_excerpt ? el("pre", { class: "execution-output", text: e.output_excerpt }) : null], "execution-output-wrap", e.exit_code != null && e.exit_code !== 0));
    card.appendChild(sourceNote(e.source_ref));
    return card;
  }

  function recordingDetails(p) {
    var freshness = p.freshness || {};
    var time = p.time || {};
    var children = [el("div", { class: "recording-body" }, [
      el("p", { text: "Capabilities: " + ((freshness.capabilities || []).join(" · ") || "not reported") }),
      (freshness.missing || []).length ? el("ul", {}, freshness.missing.map(function (item) { return el("li", { text: item }); })) : null,
      el("div", { class: "facts" }, [el("span", { text: "Decision wait " + fmtMs(time.decision_wait_ms) }),
        el("span", { text: "Unallocated prompt time " + fmtMs(time.unallocated_ms) }),
        el("span", { text: "Summed invocation time " + fmtMs(time.invocation_ms) })]),
      el("p", { class: "actions-note", text: "Concurrent tools and decision waits may overlap. Unallocated time is not a measurement of model latency. Usage changes only when the harness reports it." }),
      el("p", { class: "coverage-note", text: unclassifiedCoverage(p) })
    ])];
    if (p.runtime) children.push(el("div", { class: "recording-body" }, [
      el("h3", { text: "Observed runtime" }),
      el("p", { text: "Harness version " + (p.runtime.harness_version || "unknown") + " · model " + (p.runtime.model || "unknown") + " · effort " + (p.runtime.effort || "unknown") })
    ]));
    var inputs = p.observed_inputs || [];
    if (inputs.length) children.push(savedDetails("observed-inputs", "Observed instruction inputs (" + inputs.length + ")", [
      el("p", { class: "coverage-note", text: "These inputs were observed in harness records. Byte sizes describe content, not measured token cost." }),
      el("ul", { class: "evidence-sources" }, inputs.map(function (input) { return el("li", {}, [
        el("span", { class: "mono", text: input.path || input.kind || "Unnamed instruction" }),
        el("span", { text: " · " + fmtBytes(input.bytes) + " · " + (day(input.at) || "time unknown") }),
        input.sha256 ? el("span", { class: "source-ref mono", text: "sha256 " + input.sha256 }) : null,
        sourceNote(input.source_ref)
      ]); }))
    ], "recording-body"));
    if (p.profile) children.push(runProfile(p.profile));
    return savedDetails("recording-details", "Recording details and coverage", children, "section diagnostics");
  }

  function runProfile(profile) {
    var details = [el("p", { class: "coverage-note", text: "Configured launch inputs were captured " + (day(profile.captured_at) || "at an unknown time") +
      ". A configured file is not proof that the harness loaded it." }),
      el("div", { class: "facts" }, [
        el("span", { text: "Repo " + (profile.repo || "unknown") }),
        el("span", { text: "Harness " + (profile.harness || "unknown") }),
        el("span", { text: "Requested model " + (profile.requested_model || "harness default") }),
        el("span", { text: "Requested effort " + (profile.requested_effort || "harness default") + (profile.effort_omitted ? " · omitted from launch" : "") }),
        el("span", { class: "mono", text: "Commit " + (profile.repo_commit || "unknown") }),
        el("span", { text: profile.repo_dirty == null ? "Working tree state unknown" : profile.repo_dirty ? "Working tree dirty at launch" : "Working tree clean at launch" })
      ]),
      profile.git_error ? el("p", { class: "coverage-note", text: profile.git_error }) : null,
      profile.fingerprint ? el("p", { class: "source-ref mono", text: "Profile fingerprint " + profile.fingerprint }) : null,
      el("ul", { class: "evidence-sources" }, (profile.documents || []).map(function (doc) { return el("li", {}, [
        el("span", { class: "mono", text: doc.path || doc.role || "Unnamed configured file" }),
        el("span", { text: " · " + (doc.state || "unknown state") + " · " + (doc.state === "configured" ? fmtBytes(doc.bytes) : "size unknown") }),
        doc.sha256 ? el("span", { class: "source-ref mono", text: "sha256 " + doc.sha256 }) : null
      ]); }))];
    return savedDetails("run-profile", "Configured launch profile", details, "recording-body");
  }

  function updateLiveClocks() {
    view.querySelectorAll("[data-elapsed-start]").forEach(function (node) {
      var start = timestamp(node.getAttribute("data-elapsed-start"));
      if (start != null) node.textContent = fmtMs(Math.max(0, Date.now() - start));
    });
    view.querySelectorAll("[data-age-at]").forEach(function (node) { node.textContent = sinceNow(node.getAttribute("data-age-at")) + " ago"; });
    view.querySelectorAll("[data-observed-at]").forEach(function (node) {
      var at = timestamp(node.getAttribute("data-observed-at"));
      if (at == null) return;
      var stale = node.getAttribute("data-recording-error") === "true" || Date.now() - at > 30000;
      node.setAttribute("data-stale", stale ? "true" : "false");
      node.textContent = stale ? "Recording is stale" : "Recorded recently";
    });
  }

  function ledgerHeader(t, branch) {
    var spawnToClose = spawnSpanMs(t);
    return el("div", { class: "card" }, [
      el("div", { class: "facts" }, [
        el("span", { text: t.turns + " model calls" }),
        el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
        el("span", { text: (t.tool_count || 0) + " tool calls" }),
        el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
        el("span", { title: (t.spawned_at || "") + (t.closed_at ? " → " + t.closed_at : ""),
          text: (t.closed ? "spawn→close " : "open for ") + fmtMs(spawnToClose) }),
        el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
        el("span", { text: "cost " + fmtCost(t.cost) }),
        t.last_model ? el("span", { class: "sep", "aria-hidden": "true", text: "·" }) : null,
        t.last_model ? el("span", { class: "muted", text: t.last_model }) : null
      ]),
      el("div", { class: "split", style: "margin-top:12px" }, [
        tokenMeter(t.tokens, { label: "tokens", cost: t.cost }),
        contextMeter(t.context_pct, t.context_tokens_last)
      ]),
      el("div", { class: "facts", style: "margin-top:12px" }, [
        el("span", { class: "mono", text: branch.name || "no branch" }),
        branch.name
          ? (branch.exists
              ? el("span", { class: "state", "data-tone": "good" }, [
                  el("span", { class: "glyph", "aria-hidden": "true", text: "●" }),
                  el("span", { text: "git still has it" })])
              : el("span", { class: "state", "data-tone": "muted" }, [
                  el("span", { class: "glyph", "aria-hidden": "true", text: "×" }),
                  el("span", { text: branch.reason || "gone" })]))
          : null
      ])
    ]);
  }

  function turnTimeline(body) {
    var turns = body.turns || [];
    if (!turns.length) return el("p", { class: "empty", text: "this crew has made no model call yet" });
    var ol = el("ol", { class: "turns" });
    turns.forEach(function (t) { ol.appendChild(turnRow(body, t)); });
    return ol;
  }

  function turnRow(body, t) {
    var open = !!expanded[t.id];
    var li = el("li", { class: "turn" });
    var head = el("button", { class: "turn-head", type: "button",
      "aria-expanded": open ? "true" : "false", "data-focus": "turn:" + t.id,
      onclick: function () { toggleTurn(body, t.id); } });
    head.appendChild(el("span", { class: "ord mono", text: (open ? "▾ " : "▸ ") + "#" + t.ordinal }));
    head.appendChild(el("span", { class: "when", title: t.started_at || "", text: day(t.started_at).slice(11) || "–" }));
    head.appendChild(el("span", { class: "num", text: fmtMs(t.duration_ms) }));
    head.appendChild(el("span", { class: "what" }, [
      el("span", { text: t.outcome || "–" }),
      el("span", { class: "muted", text: t.trigger_kind ? "  ← " + t.trigger_kind : "" }),
      el("span", { class: "muted", text: t.model ? "  " + t.model : "" })
    ]));
    head.appendChild(el("span", { class: "num", text: fmtTokens(t.tokens && t.tokens.total) }));
    head.appendChild(el("span", { class: "num", text: fmtPct(t.context_pct) }));
    withRef(head, t.ref);
    li.appendChild(head);
    if (open) li.appendChild(turnBody(body, t));
    return li;
  }

  function toggleTurn(body, id) {
    if (expanded[id]) {
      delete expanded[id];
      draw();
      return;
    }
    expanded[id] = true;
    draw();
    if (turnCache[id]) return;
    var token = pollToken;
    api(turnPath(body.project, body.crew, id)).then(function (detail) {
      if (token !== pollToken) return;
      turnCache[id] = detail;
      if (expanded[id]) draw();
    }).catch(function (err) {
      if (token !== pollToken) return;
      turnCache[id] = { error: err.message, reason: err.reason };
      if (expanded[id]) draw();
    });
  }

  function turnBody(body, t) {
    var wrap = el("div", { class: "body" });
    wrap.appendChild(el("div", { class: "section" }, [
      el("div", { class: "facts" }, [
        el("span", { title: t.started_at || "", text: "started " + day(t.started_at) }),
        el("span", { text: "took " + fmtMs(t.duration_ms) }),
        el("span", { text: "trigger " + (t.trigger_kind || "–") +
          (t.trigger_event_id ? " #" + t.trigger_event_id : "") }),
        el("span", { text: "context after " + fmtTokens(t.context_tokens_after) + " (" + fmtPct(t.context_pct) + ")" })
      ]),
      tokenMeter(t.tokens, { label: "tokens this turn" })
    ]));

    var detail = turnCache[t.id];
    if (!detail) {
      wrap.appendChild(el("p", { class: "empty", text: "loading the tool calls…" }));
      return wrap;
    }
    if (detail.error) {
      wrap.appendChild(el("div", { class: "notice" }, [
        el("div", { class: "what", text: detail.error }),
        detail.reason ? el("div", { class: "why", text: detail.reason }) : null
      ]));
      return wrap;
    }
    wrap.appendChild(el("div", { class: "section" }, [
      el("h3", { text: "actions" }), actionsTable(detail.actions || [])
    ]));
    wrap.appendChild(el("div", { class: "section" }, [
      el("h3", { text: "events" }), eventsList(detail.events || [])
    ]));
    return wrap;
  }

  var ACTION_COLUMNS = ["tool", "target", "summary", "duration", "ok"];

  function actionsTable(actions) {
    if (!actions.length) return el("p", { class: "empty", text: "no tool call was recorded in this turn" });
    var thead = el("thead", {}, [el("tr", {}, ACTION_COLUMNS.map(function (c) {
      return el("th", { class: c === "duration" ? "n" : null, text: c });
    }))]);
    var tbody = el("tbody");
    var synthesised = 0;
    actions.forEach(function (a) {
      if (a.tool === "thinking") synthesised++;
      var tr = el("tr");
      tr.appendChild(withRef(el("td", { "data-label": "tool", class: "mono", text: a.tool || "" }), a.ref));
      tr.appendChild(el("td", { "data-label": "target", class: "mono", text: a.target || "" }));
      tr.appendChild(el("td", { "data-label": "summary", text: a.summary || "" }));
      tr.appendChild(el("td", { "data-label": "duration", class: "n",
        text: a.duration_ms == null ? "?" : fmtMs(a.duration_ms) }));
      tr.appendChild(el("td", { "data-label": "ok" }, [
        a.ok == null
          ? el("span", { class: "muted", text: "?" })
          : el("span", { class: "state", "data-tone": a.ok ? "good" : "critical" }, [
              el("span", { class: "glyph", "aria-hidden": "true", text: a.ok ? "●" : "!" }),
              el("span", { text: a.ok ? "ok" : "failed" })])
      ]));
      tbody.appendChild(tr);
    });
    var out = el("div", { class: "table-wrap" }, [el("table", {}, [thead, tbody])]);
    if (synthesised) {
      // The count on the turn is the calls the harness made; the ingest also
      // writes a `thinking` row for a busy stretch no call explains
      // (docs/timeline.md 4), so the two numbers legitimately differ.
      return el("div", {}, [out, el("p", { class: "actions-note",
        text: synthesised + " of these are synthesised `thinking` rows, not calls the harness made" })]);
    }
    return out;
  }

  function eventsList(events) {
    if (!events.length) return el("p", { class: "empty", text: "no event was recorded inside this turn" });
    var thead = el("thead", {}, [el("tr", {}, ["at", "kind", "actor", "payload"].map(function (c) {
      return el("th", { text: c });
    }))]);
    var tbody = el("tbody");
    events.forEach(function (e) {
      var tr = el("tr");
      tr.appendChild(el("td", { "data-label": "at", class: "mono", title: e.at || "", text: clock(e.at) }));
      tr.appendChild(el("td", { "data-label": "kind", class: "mono", text: e.kind || "" }));
      tr.appendChild(el("td", { "data-label": "actor", text: (e.actor || "") + (e.actor_kind ? " (" + e.actor_kind + ")" : "") }));
      var cell = el("td", { "data-label": "payload", class: "mono payload",
        text: e.payload == null ? "" : JSON.stringify(e.payload) });
      if (e.ref) { cell.setAttribute("data-ref", ""); cell.setAttribute("title", e.ref + ":" + (e.ref_offset || 0)); }
      tr.appendChild(cell);
      tbody.appendChild(tr);
    });
    return el("div", { class: "table-wrap" }, [el("table", {}, [thead, tbody])]);
  }

  function statusList(lines) {
    var ul = el("ul", { class: "inbox" });
    lines.forEach(function (s) {
      ul.appendChild(el("li", {}, [
        el("span", { class: "at", title: s.at || "", text: clock(s.at).slice(0, 5) }),
        el("span", { class: "who", text: s.verb || "" }),
        withRef(el("span", { text: s.text || s.line || "" }), s.ref)
      ]));
    });
    return ul;
  }

  function questionList(questions) {
    if (!questions.length) return el("p", { class: "empty", text: "this crew asked nothing" });
    var out = el("div");
    questions.forEach(function (q) {
      out.appendChild(el("div", { class: "card", style: "margin-bottom:8px" }, [
        el("div", { class: "facts" }, [
          q.answered_at
            ? el("span", { class: "state", "data-tone": "good" }, [
                el("span", { class: "glyph", "aria-hidden": "true", text: "●" }),
                el("span", { text: "answered by " + (q.answered_by || "?") })])
            : el("span", { class: "state", "data-tone": "warning" }, [
                el("span", { class: "glyph", "aria-hidden": "true", text: "?" }),
                el("span", { text: "still waiting" })]),
          el("span", { class: "muted", title: q.asked_at || "", text: "asked " + day(q.asked_at) }),
          // null is "still waiting", which is not the same as waiting zero,
          // so an unanswered question says how long it has been waiting
          // rather than printing a duration it does not have.
          el("span", { text: q.waited_ms == null
            ? "waiting " + sinceNow(q.asked_at) + " so far"
            : "waited " + fmtMs(q.waited_ms) })
        ]),
        el("p", { style: "margin:8px 0 0", text: q.text || "" }),
        q.answer ? el("p", { class: "muted", style: "margin:4px 0 0", text: "» " + q.answer }) : null
      ]));
    });
    return out;
  }

  function diffPanel(body) {
    var wrap = el("div");
    if (diffState && diffState.body) {
      var d = diffState.body;
      wrap.appendChild(el("p", { class: "facts" }, [
        el("span", { class: "mono", text: d.branch || "no branch" }),
        d.exists ? null : el("span", { class: "muted", text: d.reason || "the branch is gone" })
      ]));
      if (d.text) wrap.appendChild(diffPre(d.text));
      else wrap.appendChild(el("p", { class: "empty", text: d.reason || "there is nothing to show" }));
      return wrap;
    }
    if (diffState && diffState.error) {
      wrap.appendChild(el("div", { class: "notice" }, [
        el("div", { class: "what", text: diffState.error }),
        diffState.reason ? el("div", { class: "why", text: diffState.reason }) : null
      ]));
      return wrap;
    }
    if (diffState && diffState.loading) {
      wrap.appendChild(el("p", { class: "empty", text: "reading the branch…" }));
      return wrap;
    }
    // Lazily: a diff shells out to git, and a reader who never scrolls this
    // far should not pay for it on every poll.
    wrap.appendChild(el("button", { class: "chip", type: "button", text: "show the branch diff",
      onclick: function () { loadDiff(body); } }));
    return wrap;
  }

  function loadDiff(body) {
    diffState = { loading: true };
    draw();
    api("/api/projects/" + encodeURIComponent(body.project) +
        "/tasks/" + encodeURIComponent(body.crew) + "/diff")
      .then(function (d) { diffState = { body: d }; draw(); })
      .catch(function (err) { diffState = { error: err.message, reason: err.reason }; draw(); });
  }

  function diffPre(text) {
    var pre = el("pre", { class: "diff" });
    text.split("\n").forEach(function (line, i, all) {
      var cls = null;
      if (/^\+\+\+|^---/.test(line)) cls = "meta";
      else if (line[0] === "+") cls = "add";
      else if (line[0] === "-") cls = "del";
      else if (line[0] === "@") cls = "hunk";
      else if (/^diff --git|^index |^new file|^deleted file/.test(line)) cls = "meta";
      pre.appendChild(cls ? el("span", { class: cls, text: line }) : txt(line));
      if (i < all.length - 1) pre.appendChild(txt("\n"));
    });
    return pre;
  }

  // ------------------------------------------------------------- the shell

  function errorPanel(err) {
    return el("div", { class: "notice" }, [
      el("div", { class: "what", text: err.message || "the dashboard could not be read" }),
      err.reason ? el("div", { class: "why", text: err.reason }) : null,
      el("div", { class: "why", text: "the page keeps polling; it will fill in as soon as the answer changes" })
    ]);
  }

  // draw re-renders the current page from what is already in hand, then puts
  // the scroll position back. Nothing here fetches: a poll that found no new
  // event must not move the page under a reader's hands.
  function draw() {
    var y = window.scrollY;
    // A re-render replaces the node the reader is standing on, so the focus
    // key travels with it: a keyboard reader who opened a turn must not be
    // put back at the top of the document by a poll.
    var active = document.activeElement;
    var focusKey = active && active.getAttribute ? active.getAttribute("data-focus") : null;
    var next;
    if (data && data.error instanceof Error) {
      next = errorPanel(data.error);
    } else if (!data) {
      next = el("p", { class: "empty", text: "loading…" });
    } else if (route.tier === "workspace") {
      next = renderWorkspace(data);
    } else if (route.tier === "project") {
      next = renderProject(data);
    } else if (route.tier === "mate") {
      next = renderMate(data);
    } else {
      next = renderTask(data);
    }
    view.textContent = "";
    view.appendChild(next);
    if (focusKey) {
      var again = matchingNode("data-focus", focusKey);
      if (again) again.focus({ preventScroll: true });
    }
    window.scrollTo(0, y);
  }

  function currentPath() {
    if (route.tier === "task") {
      return "/api/projects/" + encodeURIComponent(route.project) +
        "/tasks/" + encodeURIComponent(route.crew);
    }
    if (route.tier === "mate") return "/api/projects/" + encodeURIComponent(route.project) + "/mate";
    if (route.tier === "project") return "/api/projects/" + encodeURIComponent(route.project);
    return "/api/workspace";
  }

  function refresh() {
    var token = pollToken;
    return api(currentPath()).then(function (body) {
      if (token !== pollToken) return;
      data = body;
      lastEventID = body.last_event_id || 0;
      markLive();
      draw();
    }).catch(function (err) {
      if (token !== pollToken) return;
      data = { error: err };
      markStale(err.reason || err.message);
      draw();
    });
  }

  // One long-poll loop follows the event ID behind the displayed snapshot.
  // Mate and Crew also refresh after a quiet poll, because execution timing
  // and recording health can advance before a new usage event is recorded.
  function poll() {
    var token = pollToken;
    var observingWork = route.tier === "task" || route.tier === "mate";
    var url = "/api/events?since=" + encodeURIComponent(lastEventID) + "&wait=" + (observingWork ? "5" : "20");
    if (route.project) url += "&project=" + encodeURIComponent(route.project);
    api(url).then(function (body) {
      if (token !== pollToken) return;
      var serverID = body.last_event_id || 0;
      if (serverID < lastEventID) {
        // The whole timeline was rebuilt under us. `mate reindex` deletes
        // and re-inserts every row, so `MAX(event.id)` goes backwards and a
        // cursor from before the rebuild names an id that will never come
        // round again. Taking the server's number is the only way back:
        // without this the page polls a dead cursor for ever and quietly
        // shows yesterday's workspace while saying "live".
        lastEventID = serverID;
        refresh().then(function () { if (token === pollToken) poll(); });
        return;
      }
      var moved = (body.events && body.events.length) || (body.now && body.now.length);
      if (moved) {
        lastEventID = Math.max(lastEventID, serverID);
        refresh().then(function () { if (token === pollToken) poll(); });
        return;
      }
      markLive();
      // Native executions and observer health can change before a model
      // response creates a usage event. One GET per completed long poll
      // refreshes those facts; local clocks need no additional request.
      if (observingWork) {
        refresh().then(function () { if (token === pollToken) poll(); });
        return;
      }
      poll();
    }).catch(function (err) {
      if (token !== pollToken) return;
      markStale(err.reason || err.message);
      // A failed poll is retried on a slow beat rather than in a tight loop:
      // a dashboard whose server went away must not spin.
      setTimeout(function () { if (token === pollToken) poll(); }, 3000);
    });
  }

  function go() {
    pollToken++;
    var next = parseHash();
    var sameTask = next.tier === "task" && route.tier === "task" &&
      next.project === route.project && next.crew === route.crew;
    var sameMate = next.tier === "mate" && route.tier === "mate" && next.project === route.project;
    if (!sameTask && !sameMate) {
      expanded = Object.create(null);
      turnCache = Object.create(null);
      diffState = null;
      mateVisibleExchanges = 20;
      mateFilter = "all";
      detailState = Object.create(null);
      segmentSort = "time";
      segmentKindFilter = "all";
      selectedFinding = "";
      selectedSegment = "";
    }
    if (next.tier !== route.tier || next.project !== route.project) filter = "open";
    route = next;
    data = null;
    renderCrumbs();
    draw();
    var token = pollToken;
    refresh().then(function () { if (token === pollToken) poll(); });
  }

  window.addEventListener("hashchange", go);
  setInterval(updateLiveClocks, 1000);
  go();
})();
