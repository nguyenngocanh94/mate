/*
 * app.js - the three tiers of docs/mvp.md M6, client-side routed by hash
 * and kept current by one long poll on /api/events.
 *
 *   #/                       workspace: one card per project
 *   #/p/<project>            project: the Mate, the task table, the inbox
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

  // ------------------------------------------------------------ formatting

  function fmtTokens(n) { return humanizeTokens(n || 0); }

  // A null cost is unknown, not zero: docs/timeline.md's "a missing price is
  // not a price of zero", which `matev2 usage` prints as `?`.
  function fmtCost(v) { return v == null ? "?" : humanizeCost(v); }

  function fmtPct(v) { return v == null ? "?" : goFixed(v, 1) + "%"; }

  function fmtMs(ms) { return ms == null ? "?" : humanizeDuration(ms); }

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
          (tokensAfter ? "  " + fmtTokens(tokensAfter) : "") })
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
  var diffState = null;        // null | {loading} | {body} | {error}
  var pollToken = 0;

  function link(hash) { return "#" + hash; }

  function routeHash(r) {
    if (r.tier === "task") return "/p/" + encodeURIComponent(r.project) + "/t/" + encodeURIComponent(r.crew);
    if (r.tier === "project") return "/p/" + encodeURIComponent(r.project);
    return "/";
  }

  function parseHash() {
    var h = (location.hash || "#/").replace(/^#/, "");
    var m = /^\/p\/([^/]+)\/t\/([^/]+)\/?$/.exec(h);
    if (m) return { tier: "task", project: decodeURIComponent(m[1]), crew: decodeURIComponent(m[2]) };
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
      el("span", { text: "context " + fmtPct(mate.context_pct) })
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
    out.appendChild(matePanel(body.mate || {}));

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

  function matePanel(mate) {
    var last = mate.last_turn;
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
        el("span", { text: mate.turns + " turns" }),
        el("span", { class: "sep", "aria-hidden": "true", text: "·" }),
        el("span", { text: fmtTokens(mate.tokens_today) + " today" })
      ]),
      el("div", { class: "split", style: "margin-top:12px" }, [
        tokenMeter(mate.tokens, { label: "tokens spent", cost: mate.cost === undefined ? null : mate.cost }),
        contextMeter(mate.context_pct)
      ]),
      el("div", { class: "section" }, [
        el("h3", { text: "last turn" }),
        last ? el("div", { class: "facts" }, [
          withRef(el("span", { class: "mono", text: "#" + last.ordinal }), last.ref),
          el("span", { class: "muted", title: last.started_at, text: day(last.started_at) }),
          el("span", { text: fmtMs(last.duration_ms) }),
          el("span", { class: "muted", text: last.trigger_kind || "no trigger" }),
          el("span", { text: last.outcome || "" }),
          el("span", { class: "muted", text: last.model || "" }),
          el("span", { text: fmtTokens(last.tokens && last.tokens.total) })
        ]) : el("p", { class: "empty", text: "this mate has taken no turn yet" })
      ])
    ]);
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
      tr.appendChild(el("td", { "data-label": "task", class: "task-text", text: t.text || "" }));
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
    out.appendChild(el("div", { class: "card-head" }, [
      el("h1", { text: body.crew }),
      stateSpan(ledger.state)
    ]));
    out.appendChild(el("p", { class: "muted", style: "margin:0 0 12px", text: ledger.text || "" }));
    out.appendChild(ledgerHeader(ledger, body.branch || {}));
    out.appendChild(el("div", { class: "section" }, [
      el("h2", { text: "turns" }),
      turnTimeline(body)
    ]));
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
    return out;
  }

  function ledgerHeader(t, branch) {
    var spawnToClose = t.closed && t.spawned_at && t.closed_at
      ? new Date(t.closed_at) - new Date(t.spawned_at)
      : (t.age_ms == null ? null : t.age_ms);
    return el("div", { class: "card" }, [
      el("div", { class: "facts" }, [
        el("span", { text: t.turns + " turns" }),
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
    if (!turns.length) return el("p", { class: "empty", text: "this crew has taken no turn yet" });
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
    api(turnPath(body.project, body.crew, id)).then(function (detail) {
      turnCache[id] = detail;
      if (expanded[id]) draw();
    }).catch(function (err) {
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
    } else {
      next = renderTask(data);
    }
    view.textContent = "";
    view.appendChild(next);
    if (focusKey) {
      var again = view.querySelector('[data-focus="' + focusKey.replace(/"/g, '\\"') + '"]');
      if (again) again.focus({ preventScroll: true });
    }
    window.scrollTo(0, y);
  }

  function currentPath() {
    if (route.tier === "task") {
      return "/api/projects/" + encodeURIComponent(route.project) +
        "/tasks/" + encodeURIComponent(route.crew);
    }
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

  // poll is the one long-poll loop. It hands back the exact id the data on
  // screen was computed at, so the page never re-fetches for an event it has
  // already seen, and every endpoint's answer comes out of the cache.
  function poll() {
    var token = pollToken;
    var url = "/api/events?since=" + encodeURIComponent(lastEventID) + "&wait=20";
    if (route.project) url += "&project=" + encodeURIComponent(route.project);
    api(url).then(function (body) {
      if (token !== pollToken) return;
      var serverID = body.last_event_id || 0;
      if (serverID < lastEventID) {
        // The whole timeline was rebuilt under us. `matev2 reindex` deletes
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
    if (!sameTask) {
      expanded = Object.create(null);
      turnCache = Object.create(null);
      diffState = null;
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
  go();
})();
