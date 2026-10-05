// Behaviour checks for Mate Office (ui/office/), written from the design
// boards and the dashboard's rules rather than from the code: the three
// drawers open from what they belong to, a stuck crew reads as stuck, an
// unknown value reads as unknown, workspace text stays text, and the page
// asks for nothing but GETs on its own origin.
//
// Run with: node --test internal/dashboard/ui_office_test.cjs
// TestOfficeBehaviour in ui_office_test.go runs it as part of `go test`
// whenever node is on PATH.
//
// The DOM below implements only the primitives office.js uses. Layout,
// colour and the drawings are checked in a real browser (docs/dashboard.md
// section 11), not here.
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// ------------------------------------------------------------ a small DOM

class Node {
  constructor(tag, doc) {
    this.tagName = tag;
    this.doc = doc;
    this.children = [];
    this.attrs = {};
    this.listeners = {};
    this.parentNode = null;
    this._text = "";
    this.scrollTop = 0;
  }
  set textContent(v) { this._text = String(v); this.children.forEach(c => { c.parentNode = null; }); this.children = []; }
  get textContent() { return this._text + this.children.map(c => c.textContent).join(""); }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k); }
  get className() { return this.attrs.class || ""; }
  get classes() { return this.className.split(/\s+/).filter(Boolean); }
  appendChild(c) { if (c.parentNode) c.parentNode.removeChild(c); c.parentNode = this; this.children.push(c); return c; }
  insertBefore(c, ref) {
    if (c.parentNode) c.parentNode.removeChild(c);
    const i = ref ? this.children.indexOf(ref) : -1;
    c.parentNode = this;
    if (i < 0) this.children.push(c); else this.children.splice(i, 0, c);
    return c;
  }
  removeChild(c) { this.children = this.children.filter(x => x !== c); c.parentNode = null; return c; }
  replaceChild(n, old) { const i = this.children.indexOf(old); if (n.parentNode) n.parentNode.removeChild(n); n.parentNode = this; this.children[i] = n; old.parentNode = null; return old; }
  get firstChild() { return this.children[0] || null; }
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
  dispatch(name, extra) {
    const ev = Object.assign({ type: name, target: this, stopped: false, stopPropagation() { this.stopped = true; } }, extra);
    for (let n = this; n && !ev.stopped; n = n.parentNode) for (const fn of n.listeners[name] || []) fn(ev);
    return ev;
  }
  click() { return this.dispatch("click"); }
  focus() { this.doc.activeElement = this; }
  matchesCompound(sel) {
    const re = /([#.]?[\w-]+|\[[^\]]+\])/g;
    let m;
    while ((m = re.exec(sel))) {
      const t = m[1];
      if (t[0] === "#") { if (this.attrs.id !== t.slice(1)) return false; }
      else if (t[0] === ".") { if (!this.classes.includes(t.slice(1))) return false; }
      else if (t[0] === "[") {
        const a = /^\[([^=\]]+)(?:="([^"]*)")?\]$/.exec(t);
        if (a[2] == null ? !this.hasAttribute(a[1]) : this.getAttribute(a[1]) !== a[2]) return false;
      } else if (this.tagName !== t) return false;
    }
    return true;
  }
  matches(sel) {
    const parts = sel.trim().split(/\s+(?![^\[]*\])/);
    if (!this.matchesCompound(parts[parts.length - 1])) return false;
    let n = this.parentNode;
    for (let i = parts.length - 2; i >= 0; i--) {
      while (n && !n.matchesCompound(parts[i])) n = n.parentNode;
      if (!n) return false;
      n = n.parentNode;
    }
    return true;
  }
  querySelectorAll(sel) {
    const out = [];
    const walk = n => { for (const c of n.children) { if (c.tagName !== "#text" && c.matches(sel)) out.push(c); walk(c); } };
    walk(this);
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  get text() { return this.textContent.replace(/\s+/g, " ").trim(); }
}

// ------------------------------------------------------------ the fixture

const FIXTURE = path.join(__dirname, "testdata", "office", "busy.json");
const fixture = () => JSON.parse(fs.readFileSync(FIXTURE, "utf8"));

// boot loads humanize.js and office.js into a fresh context the way the page
// does, against a fake fetch that serves the fixture.
async function boot(opts = {}) {
  const fx = opts.fixture || fixture();
  const doc = { activeElement: null };
  doc.body = new Node("body", doc);
  doc.documentElement = new Node("html", doc);
  const root = new Node("div", doc);
  root.setAttribute("id", "office");
  doc.body.appendChild(root);
  doc.getElementById = id => (id === "office" ? root : null);
  doc.createElement = tag => new Node(tag, doc);
  doc.createElementNS = (_, tag) => new Node(tag, doc);
  doc.createTextNode = text => { const n = new Node("#text", doc); n._text = String(text); return n; };

  const requests = [];
  const pendingEvents = [];
  const winListeners = {};
  let hash = opts.hash || "";
  const window = {
    scrollY: 0,
    scrollTo() {},
    addEventListener(name, fn) { (winListeners[name] ||= []).push(fn); },
    matchMedia: q => ({ matches: q.includes("max-width") ? !!opts.phone : false, addEventListener() {} }),
    localStorage: { getItem() { return null; }, setItem() {} },
    onhashchange: null
  };
  window.location = {
    get hash() { return hash; },
    set hash(v) {
      const next = v === "#" ? "" : v;
      if (next === hash) return;
      hash = next;
      setImmediate(() => (winListeners.hashchange || []).forEach(fn => fn({})));
    }
  };
  const fail = opts.fail || {};
  const context = vm.createContext({
    // Timers never fire: a retry after a failed poll must not keep node up.
    document: doc, window, setTimeout() { return 0; }, setInterval() { return 0; }, console,
    fetch(url, init) {
      requests.push({ url, method: init && init.method });
      if (url.startsWith("/api/events")) {
        if (opts.eventsFail) return Promise.reject(new TypeError("Failed to fetch"));
        return new Promise(resolve => pendingEvents.push(resolve));
      }
      const u = new URL(url, "http://office.test");
      const p = decodeURIComponent(u.pathname);
      let body = null;
      if (fail[p]) return Promise.resolve({ ok: false, status: 500, json: () => Promise.resolve({ error: "the timeline could not be read", reason: fail[p] }) });
      let m;
      if (p === "/api/workspace") body = fx.workspace;
      else if (p === "/api/now") body = fx.now;
      else if ((m = /^\/api\/projects\/([^/]+)\/tasks\/([^/]+)$/.exec(p))) body = fx.tasks[m[1] + "/" + m[2]] || taskBody(fx, m[1], m[2]);
      else if ((m = /^\/api\/projects\/([^/]+)\/mate$/.exec(p))) body = fx.mates[m[1]] || { project: m[1], exchanges: [] };
      else if ((m = /^\/api\/projects\/([^/]+)$/.exec(p))) body = fx.projects[m[1]];
      if (!body) return Promise.resolve({ ok: false, status: 404, json: () => Promise.resolve({ error: "not found " + p }) });
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(JSON.parse(JSON.stringify(body))) });
    }
  });
  // The fixture's own clock: ages read "23m" because it is 14:32:07 there.
  vm.runInContext("Date.now = function () { return " + fx.now_ms + "; };", context);
  for (const file of ["humanize.js", path.join("office", "office.js")]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "ui", file), "utf8"), context, { filename: file });
  }
  const flush = async () => { for (let i = 0; i < 8; i++) await new Promise(r => setImmediate(r)); };
  await flush();
  return {
    doc, root, requests, flush, fx,
    drawer: () => root.querySelector("#drawer"),
    key(name) { (winListeners.keydown || []).forEach(fn => fn({ key: name })); },
    hash: () => hash,
    async click(node) { assert.ok(node, "the thing to click exists"); node.click(); await flush(); }
  };
}

function taskBody(fx, project, crew) {
  const t = fx.projects[project].tasks.find(x => x.crew === crew);
  return { project, crew, ledger: t, turns: [], status_lines: [], questions: [], branch: { name: t.branch, exists: true },
    performance: { skills: [], segments: [], prompt_turns: [] } };
}

const byKey = (root, key) => root.querySelector(`[data-key="${key}"]`);
const deskOf = (root, crew) => root.querySelectorAll(".desk").find(n => n.querySelector(".id").text === crew);
const roomOf = (root, name) => root.querySelector(`section.room[data-room="${name}"]`);

// ------------------------------------------------------------ the office

test("every project is a room: its Mate at the big desk, open crews at desks, finished ones in the cabinet", async () => {
  const app = await boot();
  const rooms = app.root.querySelectorAll("section.room");
  assert.deepEqual(rooms.map(r => r.getAttribute("data-room")), ["shop", "docs-site", "infra"]);
  const shop = roomOf(app.root, "shop");
  assert.deepEqual(shop.querySelectorAll(".desk").map(d => d.querySelector(".id").text), ["rd1", "api-fix", "ui-polish", "db-migrate"]);
  assert.match(shop.querySelector(".mate").text, /Mate · shop.*deciding.*Claude.*1\.8M today/);
  assert.equal(shop.querySelector(".cab .cnt").text, "4", "four finished tasks are filed, not seated");
  assert.match(shop.querySelector(".plate").text, /shop.*running.*2 waiting/);
  assert.equal(roomOf(app.root, "docs-site").querySelector(".light").text, "running",
    "an idle Mate with crews at work is a project at work");
  assert.equal(shop.querySelector(".plate .ptok").text, "3.7M tokens", "the Mate plus every task, open and closed");
  assert.equal(roomOf(app.root, "docs-site").querySelector(".plate .ptok").text, "719k tokens");
  assert.equal(roomOf(app.root, "infra").querySelector(".plate .ptok").text, "725.3k tokens");
  const infra = roomOf(app.root, "infra");
  assert.ok(infra.classes.includes("r-stopped"), "a room whose Mate is not running has its lights off");
  assert.match(infra.text, /stopped/);
  assert.match(infra.text, /Lights off · project stopped/);
  assert.match(app.root.querySelector(".top").text, /Today 2\.6M tokens/);
});

test("a room where nobody is working says how long since anyone moved", async () => {
  const fx = fixture();
  // docs-site's Mate is idle; stop its busy crews. The last to move is
  // search, 20s ago, so that is how long the room has been quiet.
  const states = { nav: "waiting_review", i18n: "waiting_at_ceo", search: "idle" };
  fx.projects["docs-site"].tasks.forEach(t => { if (states[t.crew]) t.state = states[t.crew]; });
  const app = await boot({ fixture: fx });
  const light = roomOf(app.root, "docs-site").querySelector(".light");
  assert.equal(light.text, "idle 20s");
  assert.ok(light.classes.includes("idle"));
  assert.equal(roomOf(app.root, "shop").querySelector(".light").text, "running");
});

test("a crew with a question stands in the hallway, and its empty desk shows it went", async () => {
  const app = await boot();
  const figs = app.root.querySelectorAll(".hall .fig");
  assert.deepEqual(figs.map(f => f.querySelector(".id").text), ["ui-polish", "i18n"]);
  assert.match(figs[0].querySelector(".bubble").text, /needs an answer/);
  assert.ok(figs[1].classes.includes("walk"));
  assert.match(figs[1].text, /to the Mate/);
  const desk = deskOf(app.root, "ui-polish");
  assert.ok(desk.classes.includes("away"));
  assert.equal(desk.querySelector(".a-head"), null, "nobody sits at the desk of a crew at the door");
  assert.match(app.root.querySelector(".cap").text, /1 question waiting/);
});

test("a stuck crew looks stuck: alert, outlined desk, the word and why", async () => {
  const app = await boot();
  const desk = deskOf(app.root, "db-migrate");
  assert.ok(desk.classes.includes("s-blocked"));
  assert.ok(desk.querySelector(".a-alert"), "the red alert badge");
  const chip = desk.querySelector(".chip");
  assert.ok(chip.classes.includes("t-bad"));
  assert.equal(chip.text, "stuck");
  assert.equal(desk.querySelector(".quiet").text, "quiet too long · 23m");
  assert.match(desk.getAttribute("aria-label"), /stuck, quiet too long/);
});

test("clicking the Mate opens the Mate drawer for that room", async () => {
  const app = await boot();
  assert.ok(app.drawer().hasAttribute("hidden"), "no drawer before anything is picked");
  await app.click(roomOf(app.root, "shop").querySelector(".mate"));
  assert.equal(app.hash(), "#mate/shop");
  const d = app.drawer();
  assert.ok(!d.hasAttribute("hidden"));
  assert.equal(d.querySelector("h2").text, "shop");
  assert.match(d.querySelector(".role").text, /Mate · team lead.*Claude/);
  assert.match(d.querySelector(".dstate").text, /deciding.*since 14m/);
  assert.equal(d.querySelector(".big .n").text, "2.4M");
  assert.equal(d.querySelector(".big .c b").text, "$6.31");
  assert.deepEqual(d.querySelectorAll(".bl b").map(b => b.text), ["283.2k", "1.9M", "194.6k", "42.2k"]);
  assert.match(d.querySelector(".meter").text, /context 62% · 124k of 200k/);
  assert.match(d.querySelector(".tv").text, /1\.8M/);
  assert.match(d.text, /Thinking it over, facing api-fix/);
  assert.deepEqual(d.querySelectorAll(".cr .id").map(n => n.text), ["rd1", "api-fix", "ui-polish", "db-migrate"]);
  assert.ok(app.requests.some(r => r.url === "/api/projects/shop/mate"), "the drawer reads the Mate's own endpoint");
  assert.ok(roomOf(app.root, "shop").querySelector(".mate").classes.includes("sel"), "the picked Mate is ringed");
});

test("clicking a crew opens its drawer: state, usage, current activity and the task", async () => {
  const app = await boot();
  await app.click(deskOf(app.root, "db-migrate"));
  assert.equal(app.hash(), "#crew/shop/db-migrate");
  const d = app.drawer();
  assert.equal(d.querySelector("h2").text, "db-migrate");
  assert.match(d.querySelector(".role").text, /Crew · shop/);
  const chip = d.querySelector(".dstate .chip");
  assert.ok(chip.classes.includes("t-bad"));
  assert.equal(chip.text, "stuck, quiet too long");
  assert.equal(d.querySelector(".big .n").text, "310.4k");
  assert.match(d.querySelector(".meter").text, /context 88% · 176k of 200k/);
  assert.match(d.querySelector(".tv").text, /196k/);
  assert.equal(d.querySelectorAll(".spark i").length, 12, "twelve half hours");
  const activity = d.querySelector("[aria-label=\"Current activity\"]");
  assert.match(activity.text, /Running 0042_add_order_index/);
  assert.match(activity.text, /Run tests \/ build · running/);
  assert.match(activity.text, /db\/migrations\/0042_add_order_index\.sql/);
  const task = d.querySelector("[aria-label=\"Task\"]");
  assert.match(task.text, /Order indexes and backfill.*mate\/db-migrate.*57.*db-migrations ×2/);
  assert.equal(task.querySelector("a.link").getAttribute("href"), "../#/p/shop/t/db-migrate");
  assert.ok(app.requests.some(r => r.url === "/api/projects/shop/tasks/db-migrate"));
  const admin = app.root.querySelectorAll(".seg a").find(a => a.text === "Admin");
  assert.equal(admin.getAttribute("href"), "../#/p/shop/t/db-migrate", "Admin goes to the same crew's page");
});

test("picking a crew from the Mate drawer moves the drawer to that crew", async () => {
  const app = await boot({ hash: "#mate/shop" });
  const row = app.drawer().querySelectorAll(".cr").find(r => r.querySelector(".id").text === "rd1");
  await app.click(row);
  assert.equal(app.hash(), "#crew/shop/rd1");
  assert.equal(app.drawer().querySelector("h2").text, "rd1");
});

test("the filing cabinet lists finished tasks by drawer, with their final state", async () => {
  const app = await boot();
  await app.click(roomOf(app.root, "shop").querySelector(".cab"));
  assert.equal(app.hash(), "#cabinet/shop");
  const d = app.drawer();
  assert.equal(d.querySelector("h2").text, "Filing cabinet");
  assert.match(d.querySelector(".role").text, /shop · finished tasks/);
  assert.match(d.querySelector(".dstate").text, /4 finished.*oldest Sep 10/);
  assert.match(d.querySelector(".csum").text, /3 merged.*1 failed.*708\.9k tokens/);
  const groups = d.querySelectorAll(".cg").map(g => [g.querySelector(".cgh b").text, g.querySelectorAll(".tn").map(n => n.text)]);
  assert.deepEqual(groups, [
    ["Top drawer", ["login-flow", "search-facets"]],
    ["Middle drawer", ["order-email"]],
    ["Bottom drawer", ["healthcheck"]]
  ]);
  const failed = d.querySelectorAll(".tr").find(r => r.querySelector(".tn").text === "search-facets");
  assert.ok(failed.querySelector(".chip").classes.includes("t-bad"));
  assert.match(failed.text, /failed.*4d ago.*Sep 29.*133k/);
  assert.ok(roomOf(app.root, "shop").querySelector(".cab").classes.includes("open"), "its top drawer is pulled out");
});

test("Escape, the close button and a click on the floor close the drawer", async () => {
  const app = await boot({ hash: "#mate/shop" });
  assert.ok(!app.drawer().hasAttribute("hidden"));
  app.key("Escape");
  await app.flush();
  assert.equal(app.hash(), "");
  assert.ok(app.drawer().hasAttribute("hidden"));

  await app.click(deskOf(app.root, "rd1"));
  await app.click(app.drawer().querySelector("[aria-label=\"Close details\"]"));
  assert.ok(app.drawer().hasAttribute("hidden"));

  await app.click(deskOf(app.root, "rd1"));
  assert.ok(!app.drawer().hasAttribute("hidden"));
  await app.click(app.root.querySelector("#floor"));
  assert.ok(app.drawer().hasAttribute("hidden"), "clicking the floor closes it");
});

// ------------------------------------------------------------ unknowns

test("an unknown value reads as unknown, never as zero", async () => {
  // search has no scene row yet and no recorded turn; docs-site prices
  // nothing.
  const app = await boot({ hash: "#crew/docs-site/search" });
  const d = app.drawer();
  assert.equal(d.querySelector(".tv b").text, "?", "tokens today without a scene row");
  assert.match(d.querySelector(".meter").text, /context \? · window unknown/);
  assert.ok(d.querySelector(".meter .bar").classes.includes("unk"), "the hatched track, not an empty one");
  assert.equal(d.querySelector(".big .c b").text, "?");
  assert.match(d.querySelector(".big .c").text, /cost not reported/);

  const mate = await boot({ hash: "#mate/docs-site" });
  assert.equal(mate.drawer().querySelector(".big .c b").text, "?", "a Mate on an unpriced model has no cost");
});

test("when the scene cannot be read, tokens today is ? rather than 0", async () => {
  const app = await boot({ fail: { "/api/now": "no such table: v_now" } });
  assert.match(app.root.querySelector(".top .kpi").text, /Today \? tokens/);
  assert.equal(roomOf(app.root, "shop").querySelectorAll(".desk").length, 4, "the rest of the office still draws");
});

test("a room whose project cannot be read says so and the others still draw", async () => {
  const app = await boot({ fail: { "/api/projects/docs-site": "sent.log is unreadable" } });
  assert.match(roomOf(app.root, "docs-site").text, /could not be read: sent\.log is unreadable/);
  assert.equal(roomOf(app.root, "docs-site").querySelector(".plate .ptok b").text, "?", "an unread room is not 0 tokens");
  assert.equal(roomOf(app.root, "shop").querySelector(".plate .ptok b").text, "3.7M");
  assert.equal(roomOf(app.root, "shop").querySelectorAll(".desk").length, 4);
});

test("a project token label is ? when any part of the sum is missing", async () => {
  const fx = fixture();
  delete fx.projects.shop.mate.tokens.total;
  const app = await boot({ fixture: fx });
  assert.equal(roomOf(app.root, "shop").querySelector(".plate .ptok b").text, "?", "a partial sum is not the project's total");
  assert.equal(roomOf(app.root, "docs-site").querySelector(".plate .ptok").text, "719k tokens");
});

test("a failed poll turns the live pill stale with the reason", async () => {
  const app = await boot({ eventsFail: true });
  const pill = app.root.querySelector("#live");
  assert.ok(pill.classes.includes("stale"));
  assert.match(pill.text, /stale · the dashboard is not answering/);
});

// ------------------------------------------------------------ safety

test("workspace text stays text", async () => {
  const fx = fixture();
  const evil = "<img src=x onerror=alert(1)>";
  fx.projects.shop.tasks[0].crew = evil;
  fx.projects.shop.tasks[0].text = "<script>alert('task')</script>";
  fx.projects.shop.inbox[0].text = "<b>bold</b>";
  const app = await boot({ fixture: fx, hash: "#crew/shop/" + encodeURIComponent(evil) });
  assert.equal(app.root.querySelectorAll("img").length, 0);
  assert.equal(app.root.querySelectorAll("script").length, 0);
  assert.equal(app.drawer().querySelector("h2").text, evil);
  assert.match(app.drawer().text, /<script>alert\('task'\)<\/script>/);
  const src = fs.readFileSync(path.join(__dirname, "ui", "office", "office.js"), "utf8");
  for (const sink of ["innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval("]) {
    assert.ok(!src.includes(sink), "office.js uses " + sink);
  }
});

test("the page only ever GETs its own origin", async () => {
  const app = await boot({ hash: "#crew/shop/db-migrate" });
  await app.click(roomOf(app.root, "shop").querySelector(".mate"));
  await app.click(roomOf(app.root, "shop").querySelector(".cab"));
  assert.ok(app.requests.length >= 8);
  for (const r of app.requests) {
    assert.ok(r.url.startsWith("/api/"), r.url + " is not this origin's API");
    assert.equal(r.method || "GET", "GET", r.url + " is not a GET");
  }
});

// ------------------------------------------------------------ phone

test("on a phone the rooms are cards and the drawer is a bottom sheet over a scrim", async () => {
  const app = await boot({ phone: true });
  assert.equal(app.root.querySelectorAll("section.room").length, 0);
  assert.equal(app.root.querySelectorAll("section.mroom").length, 3);
  const shop = app.root.querySelector('section.mroom[aria-label="shop room"]');
  assert.equal(shop.querySelector(".ptok").text, "3.7M tokens");
  const scrim = app.root.querySelector(".scrim");
  assert.ok(scrim.hasAttribute("hidden"));
  const rd1 = app.root.querySelectorAll(".mc").find(n => n.querySelector(".id").text === "rd1");
  await app.click(rd1);
  assert.equal(app.drawer().querySelector("h2").text, "rd1");
  assert.ok(app.drawer().querySelector(".grab"), "the sheet's handle");
  assert.ok(!app.root.querySelector(".scrim").hasAttribute("hidden"));
  await app.click(app.root.querySelector(".scrim"));
  assert.ok(app.drawer().hasAttribute("hidden"), "tapping the scrim closes the sheet");
  const stuck = app.root.querySelectorAll(".mc").find(n => n.querySelector(".id").text === "db-migrate");
  assert.ok(stuck.classes.includes("p-blocked"));
  assert.equal(stuck.querySelector(".mw").text, "stuck");
});
