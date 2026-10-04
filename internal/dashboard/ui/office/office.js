/*
 * office.js - Mate Office: the scene of docs/mvp.md M5 drawn as an office,
 * from the same read-only API the admin UI at / reads (docs/dashboard.md
 * section 11).
 *
 * Every project is a room. Its Mate sits at the big desk by the door, its
 * open crews at the desks below, and its finished tasks are in the filing
 * cabinet. The hallway holds the captain's desk and any crew that has gone
 * to the Mate's door with a question. Clicking anyone opens a drawer with
 * what the API knows about them; nothing on this page writes anything.
 *
 * Where each actor stands is `v_now`'s scene state (internal/timeline/scene,
 * docs/timeline.md section 9). A value the API does not have is drawn as
 * unknown (`?`, a hatched track), never as zero.
 *
 * The DOM is built node by node: a workspace quotes whatever the captain
 * typed and whatever a harness printed, so no string from the API ever
 * reaches anything but textContent or an attribute value.
 */

(function () {
  "use strict";

  var SVG_NS = "http://www.w3.org/2000/svg";

  // ------------------------------------------------------------ drawing

  // Icon paths are the design's own (24x24, stroked).
  var IC = {
    office: "M4 21V5a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v16M2 21h20M9 7h1M14 7h1M9 11h1M14 11h1M10 21v-4h4v4",
    arriving: "M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4M10 17l5-5-5-5M15 12H3",
    leaving: "M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9",
    working: "M3 6h18a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1zM6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 14h10",
    walking: "M5 12h14M13 6l6 6-6 6",
    ask: "M21 11.5a8.4 8.4 0 0 1-12.2 7.5L3 21l2-5.3A8.5 8.5 0 1 1 21 11.5zM9.6 9a2.5 2.5 0 0 1 4.8.8c0 1.7-2.4 2.2-2.4 3.7M12 16.5h.01",
    review: "M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8zM14 3v5h5M9 13h6M9 17h4",
    idle: "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM10 9v6M14 9v6",
    blocked: "M10.3 3.9L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0zM12 9v4M12 17h.01",
    bang: "M12 6v8M12 18h.01",
    asleep: "M4 6h6l-6 7h6M13 11h7l-7 8h7",
    gone: "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM5.6 5.6l12.8 12.8",
    unknown: "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM9.6 9a2.5 2.5 0 0 1 4.8.8c0 1.7-2.4 2.2-2.4 3.7M12 16.5h.01",
    think: "M9 18h6M10 21h4M12 3a6 6 0 0 0-3.6 10.8c.7.6 1.1 1.3 1.1 2.2h5c0-.9.4-1.6 1.1-2.2A6 6 0 0 0 12 3z",
    read: "M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zM12 9a3 3 0 1 0 0 6a3 3 0 1 0 0-6z",
    answer: "M9 17l-5-5 5-5M4 12h11a5 5 0 0 1 5 5v2",
    merge: "M6 3v12M18 9a3 3 0 1 0 0-6a3 3 0 0 0 0 6zM6 21a3 3 0 1 0 0-6a3 3 0 0 0 0 6zM18 9a9 9 0 0 1-9 9",
    phone: "M22 16.9v3a2 2 0 0 1-2.2 2 19.8 19.8 0 0 1-8.6-3.1 19.5 19.5 0 0 1-6-6A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1.9.4 1.8.7 2.7a2 2 0 0 1-.5 2.1L8 9.8a16 16 0 0 0 6 6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.7.7a2 2 0 0 1 1.7 2z",
    mail: "M4 4h16a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2zM22 6l-10 7L2 6",
    merged: "M20 6L9 17l-5-5",
    failed: "M18 6L6 18M6 6l12 12",
    close: "M18 6L6 18M6 6l12 12",
    sun: "M12 8a4 4 0 1 0 0 8a4 4 0 1 0 0-8zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4",
    moon: "M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z",
    arrowR: "M5 12h14M13 6l6 6-6 6",
    cabinet: "M5 3h14a1 1 0 0 1 1 1v16a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zM4 9h16M4 15h16M10 6h4M10 12h4M10 18h4"
  };

  // The harness is the head, drawn with its own mark (the design's "harness
  // is the face"). A harness this page has no mark for keeps a plain head
  // with its initial, and an unrecorded one a "?".
  var FACE = {
    claude: "m4.7144 15.9555 4.7174-2.6471.079-.2307-.079-.1275h-.2307l-.7893-.0486-2.6956-.0729-2.3375-.0971-2.2646-.1214-.5707-.1215-.5343-.7042.0546-.3522.4797-.3218.686.0608 1.5179.1032 2.2767.1578 1.6514.0972 2.4468.255h.3886l.0546-.1579-.1336-.0971-.1032-.0972L6.973 9.8356l-2.55-1.6879-1.3356-.9714-.7225-.4918-.3643-.4614-.1578-1.0078.6557-.7225.8803.0607.2246.0607.8925.686 1.9064 1.4754 2.4893 1.8336.3643.3035.1457-.1032.0182-.0728-.164-.2733-1.3539-2.4467-1.445-2.4893-.6435-1.032-.17-.6194c-.0607-.255-.1032-.4674-.1032-.7285L6.287.1335 6.6997 0l.9957.1336.419.3642.6192 1.4147 1.0018 2.2282 1.5543 3.0296.4553.8985.2429.8318.091.255h.1579v-.1457l.1275-1.706.2368-2.0947.2307-2.6957.0789-.7589.3764-.9107.7468-.4918.5828.2793.4797.686-.0668.4433-.2853 1.8517-.5586 2.9021-.3643 1.9429h.2125l.2429-.2429.9835-1.3053 1.6514-2.0643.7286-.8196.85-.9046.5464-.4311h1.0321l.759 1.1293-.34 1.1657-1.0625 1.3478-.8804 1.1414-1.2628 1.7-.7893 1.36.0729.1093.1882-.0183 2.8535-.607 1.5421-.2794 1.8396-.3157.8318.3886.091.3946-.3278.8075-1.967.4857-2.3072.4614-3.4364.8136-.0425.0304.0486.0607 1.5482.1457.6618.0364h1.621l3.0175.2247.7892.522.4736.6376-.079.4857-1.2142.6193-1.6393-.3886-3.825-.9107-1.3113-.3279h-.1822v.1093l1.0929 1.0686 2.0035 1.8092 2.5075 2.3314.1275.5768-.3218.4554-.34-.0486-2.2039-1.6575-.85-.7468-1.9246-1.621h-.1275v.17l.4432.6496 2.3436 3.5214.1214 1.0807-.17.3521-.6071.2125-.6679-.1214-1.3721-1.9246L14.38 17.959l-1.1414-1.9428-.1397.079-.674 7.2552-.3156.3703-.7286.2793-.6071-.4614-.3218-.7468.3218-1.4753.3886-1.9246.3157-1.53.2853-1.9004.17-.6314-.0121-.0425-.1397.0182-1.4328 1.9672-2.1796 2.9446-1.7243 1.8456-.4128.164-.7164-.3704.0667-.6618.4008-.5889 2.386-3.0357 1.4389-1.882.929-1.0868-.0062-.1579h-.0546l-6.3385 4.1164-1.1293.1457-.4857-.4554.0608-.7467.2307-.2429 1.9064-1.3114Z",
    codex: "M8.086.457a6.105 6.105 0 013.046-.415c1.333.153 2.521.72 3.564 1.7a.117.117 0 00.107.029c1.408-.346 2.762-.224 4.061.366l.063.03.154.076c1.357.703 2.33 1.77 2.918 3.198.278.679.418 1.388.421 2.126a5.655 5.655 0 01-.18 1.631.167.167 0 00.04.155 5.982 5.982 0 011.578 2.891c.385 1.901-.01 3.615-1.183 5.14l-.182.22a6.063 6.063 0 01-2.934 1.851.162.162 0 00-.108.102c-.255.736-.511 1.364-.987 1.992-1.199 1.582-2.962 2.462-4.948 2.451-1.583-.008-2.986-.587-4.21-1.736a.145.145 0 00-.14-.032c-.518.167-1.04.191-1.604.185a5.924 5.924 0 01-2.595-.622 6.058 6.058 0 01-2.146-1.781c-.203-.269-.404-.522-.551-.821a7.74 7.74 0 01-.495-1.283 6.11 6.11 0 01-.017-3.064.166.166 0 00.008-.074.115.115 0 00-.037-.064 5.958 5.958 0 01-1.38-2.202 5.196 5.196 0 01-.333-1.589 6.915 6.915 0 01.188-2.132c.45-1.484 1.309-2.648 2.577-3.493.282-.188.55-.334.802-.438.286-.12.573-.22.861-.304a.129.129 0 00.087-.087A6.016 6.016 0 015.635 2.31C6.315 1.464 7.132.846 8.086.457zm-.804 7.85a.848.848 0 00-1.473.842l1.694 2.965-1.688 2.848a.849.849 0 001.46.864l1.94-3.272a.849.849 0 00.007-.854l-1.94-3.393zm5.446 6.24a.849.849 0 000 1.695h4.848a.849.849 0 000-1.696h-4.848z",
    pi: "M0 0v24h6v-6h6v-6H6V6h6v6h6V0Zm18 12v12h6V12Z"
  };

  var HARNESS = {
    claude: { name: "Claude", tag: "tg-c" },
    codex: { name: "Codex", tag: "tg-x" },
    pi: { name: "Pi", tag: "tg-p" }
  };

  function harnessInfo(h) {
    var key = h ? String(h).toLowerCase() : "";
    if (HARNESS[key]) return { key: key, name: HARNESS[key].name, tag: HARNESS[key].tag, mark: true };
    if (!key) return { key: "", name: "harness ?", tag: "tg-u", mark: false, letter: "?" };
    return { key: key, name: String(h), tag: "tg-u", mark: false, letter: key.charAt(0).toUpperCase() };
  }

  // Shirts are decoration, not data: a crew keeps the same colour on every
  // load because the colour is picked by its name. The palette is the
  // design's own set of shirts.
  var SHIRTS = ["#8EA6BF", "#C4A07A", "#B48DA0", "#9BB096", "#C7A46A", "#89A9A3", "#B9917F", "#A9B57E", "#9FA6C7", "#8E9AA6", "#5F8378", "#7E86B4"];

  function shirt(name) {
    var s = String(name || "");
    var h = 0;
    for (var i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
    return SHIRTS[h % SHIRTS.length];
  }

  // ------------------------------------------------------------ vocabulary

  // Crew scene states (docs/timeline.md 9.1), each with a glyph as well as a
  // tone so colour is never the only signal. The words are the design's,
  // except walking_to_ceo: the scene sends a crew to its Mate (the CEO of
  // the room), not to the captain, so it says so.
  var CREW_STATES = {
    arriving: { word: "arriving", tone: "t-info", icon: "arriving" },
    at_desk_working: { word: "working", tone: "t-ok", icon: "working" },
    walking_to_ceo: { word: "to the Mate", tone: "t-info", icon: "walking" },
    waiting_at_ceo: { word: "needs an answer", tone: "t-wait", icon: "ask" },
    waiting_review: { word: "waiting for review", tone: "t-wait", icon: "review" },
    idle: { word: "idle", tone: "t-idle", icon: "idle" },
    blocked: { word: "stuck", tone: "t-bad", icon: "blocked" },
    asleep: { word: "asleep", tone: "t-slp", icon: "asleep" },
    leaving: { word: "leaving", tone: "t-gone", icon: "leaving" },
    gone: { word: "agent gone", tone: "t-gone", icon: "gone" }
  };
  var LEGEND = ["arriving", "at_desk_working", "walking_to_ceo", "waiting_at_ceo", "waiting_review", "idle", "blocked", "asleep", "gone"];

  // The Mate's states (docs/timeline.md 9.1 and 9.4).
  var MATE_STATES = {
    idle: { word: "idle", tone: "t-idle", icon: "idle" },
    deciding: { word: "deciding", tone: "t-ok", icon: "think" },
    reading: { word: "reading", tone: "t-info", icon: "read" },
    answering: { word: "answering", tone: "t-info", icon: "answer" },
    reviewing: { word: "reviewing", tone: "t-wait", icon: "review" },
    merging: { word: "merging", tone: "t-ok", icon: "merge" },
    on_phone: { word: "on the phone", tone: "t-info", icon: "phone" },
    receiving_digest: { word: "note on the desk", tone: "t-wait", icon: "mail" },
    asleep: { word: "asleep", tone: "t-slp", icon: "asleep" },
    blocked: { word: "stuck", tone: "t-bad", icon: "blocked" },
    gone: { word: "agent gone", tone: "t-gone", icon: "gone" }
  };

  var UNKNOWN_STATE = { word: "unknown", tone: "t-gone", icon: "unknown" };

  function crewState(s) { return CREW_STATES[s] || (s ? { word: String(s).replace(/_/g, " "), tone: "t-gone", icon: "unknown" } : UNKNOWN_STATE); }
  function mateState(s) { return MATE_STATES[s] || (s ? { word: String(s).replace(/_/g, " "), tone: "t-gone", icon: "unknown" } : UNKNOWN_STATE); }

  // The scene's `detail` in words: why a crew is asleep or stuck, what a
  // note on the Mate's desk is. The incident kinds are the console's own
  // phrases (internal/ui/console's inbox).
  var DETAILS = {
    stale: "quiet too long",
    runtime_lost: "agent unreachable",
    wedged: "send wedged",
    question: "with a question",
    handback: "with finished work",
    digest: "a digest",
    assign: "the captain's note"
  };
  function detailWord(d) { return d ? (DETAILS[d] || String(d).replace(/_/g, " ")) : ""; }

  // The present tense of docs/timeline.md 9.5's scene phrases, for the
  // Mate drawer's "Current activity".
  function mateSentence(m) {
    var who = m.target || "a crew";
    switch (m.state) {
      case "idle": return "Alone in its office";
      case "deciding": return m.target ? "Thinking it over, facing " + m.target : "Thinking it over";
      case "reading": return "Reading " + who + "'s note";
      case "answering": return "Answering " + who;
      case "reviewing": return "Reviewing " + who + "'s work";
      case "merging": return "Landing " + who + "'s work";
      case "on_phone": return "On the phone with the captain";
      case "receiving_digest":
        return (m.detail === "assign" ? "The captain's note" : "A digest") + " about " + who + " is on the desk, unread";
      case "asleep": return "Asleep: nothing has moved" + (m.detail ? " (" + detailWord(m.detail) + ")" : "");
      case "blocked": return "Cannot be reached" + (m.detail ? " (" + detailWord(m.detail) + ")" : "");
      case "gone": return "Out of the building";
      case "": case undefined: case null: return "Not placed in the scene yet";
      default: return String(m.state).replace(/_/g, " ");
    }
  }

  // ------------------------------------------------------------ numbers

  // Tokens and cost are humanize.js's, the admin UI's own copy of
  // internal/query's formatters, so no number here can read differently
  // from `mate usage`. A null is unknown and prints `?`.
  function tok(n) { return n == null ? "?" : humanizeTokens(n); }
  function cost(v) { return v == null ? "?" : humanizeCost(v); }

  // formatAge is cmd/mate's formatAge (the AGE column): the largest whole
  // unit, rounded down - a crew in a state for 3 days 23 hours has been in
  // it for 3 days.
  function formatAge(ms) {
    if (ms == null || !isFinite(ms)) return "?";
    if (ms < 0) ms = 0;
    if (ms < 60000) return Math.floor(ms / 1000) + "s";
    if (ms < 3600000) return Math.floor(ms / 60000) + "m";
    if (ms < 86400000) return Math.floor(ms / 3600000) + "h";
    return Math.floor(ms / 86400000) + "d";
  }

  function parseAt(ts) {
    if (!ts) return null;
    var t = Date.parse(ts);
    return isNaN(t) ? null : t;
  }

  function age(ts) {
    var t = parseAt(ts);
    return t == null ? "?" : formatAge(Date.now() - t);
  }

  function pad(n) { return n < 10 ? "0" + n : String(n); }

  function clock(ts) {
    var t = typeof ts === "number" ? ts : parseAt(ts);
    if (t == null) return "?";
    var d = new Date(t);
    return pad(d.getHours()) + ":" + pad(d.getMinutes()) + ":" + pad(d.getSeconds());
  }

  var MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  function monthDay(ts) {
    var t = parseAt(ts);
    if (t == null) return "?";
    var d = new Date(t);
    return MONTHS[d.getMonth()] + " " + d.getDate();
  }

  function plural(n, one, many) { return n + " " + (n === 1 ? one : many); }

  // ------------------------------------------------------------ DOM tools

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    setAttrs(node, attrs);
    append(node, children);
    return node;
  }

  function setAttrs(node, attrs) {
    if (!attrs) return;
    Object.keys(attrs).forEach(function (k) {
      var v = attrs[k];
      if (v == null || v === false) return;
      if (k === "text") { node.textContent = String(v); return; }
      if (k === "class") { node.setAttribute("class", String(v)); return; }
      if (k === "onclick") { node.addEventListener("click", v); return; }
      node.setAttribute(k, v === true ? "" : String(v));
    });
  }

  function append(node, children) {
    if (children == null) return node;
    if (!Array.isArray(children)) children = [children];
    children.forEach(function (c) {
      if (c == null || c === false) return;
      node.appendChild(typeof c === "string" || typeof c === "number" ? document.createTextNode(String(c)) : c);
    });
    return node;
  }

  function svgPath(cls, d, viewBox) {
    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", cls);
    svg.setAttribute("viewBox", viewBox || "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    var p = document.createElementNS(SVG_NS, "path");
    p.setAttribute("d", d);
    svg.appendChild(p);
    return svg;
  }

  function icon(name) { return svgPath("i", IC[name] || IC.unknown); }

  function chip(info, text, extra) {
    return el("span", { class: "chip " + info.tone + (extra ? " " + extra : "") }, [icon(info.icon), text == null ? info.word : text]);
  }

  function icoChip(info) {
    return el("span", { class: "chip ico " + info.tone, title: info.word }, icon(info.icon));
  }

  function htag(h) {
    return el("span", { class: "htag " + h.tag, text: h.name });
  }

  // face draws a head: the harness mark on its own colour, or a plain head.
  function face(cls, h, extra) {
    if (h.mark) {
      return el("span", { class: cls + " fh-" + h.key + (extra ? " " + extra : "") }, svgPath("fc", FACE[h.key]));
    }
    return el("span", { class: cls + " fh-unknown" + (extra ? " " + extra : ""), text: h.letter });
  }

  // pick wires a selectable figure: clicking it opens its drawer and does
  // not fall through to the floor, whose click closes the drawer.
  function pickable(node, key) {
    node.setAttribute("data-key", key);
    node.setAttribute("data-focus", "pick:" + key);
    node.addEventListener("click", function (e) {
      if (e && e.stopPropagation) e.stopPropagation();
      select(key);
    });
    return node;
  }

  // ------------------------------------------------------------ the API

  function api(path) {
    // Read-only by construction: the only request this page ever makes is
    // a GET to its own origin.
    return fetch(path, { method: "GET", headers: { accept: "application/json" } }).catch(function () {
      var e = new Error("the dashboard is not answering");
      e.reason = "the dashboard is not answering";
      throw e;
    }).then(function (r) {
      return r.json().catch(function () {
        throw new Error("the dashboard answered " + r.status + " with something that is not JSON");
      }).then(function (body) {
        if (!r.ok) {
          var e = new Error(body.error || ("the dashboard answered " + r.status));
          e.reason = body.reason || body.error || "";
          throw e;
        }
        return body;
      });
    });
  }

  function enc(s) { return encodeURIComponent(s); }
  function projectPath(p) { return "/api/projects/" + enc(p); }
  function taskPath(p, c) { return "/api/projects/" + enc(p) + "/tasks/" + enc(c); }
  function matePath(p) { return "/api/projects/" + enc(p) + "/mate"; }

  // ------------------------------------------------------------ state

  var root = document.getElementById("office");
  var ws = null;              // /api/workspace
  var now = null;             // actor id -> SceneRow, or null when /api/now failed
  var projects = {};          // name -> /api/projects/{p} body, or {__error}
  var fatal = null;           // the workspace could not be read at all
  var lastEventID = 0;
  var pollToken = 0;
  var detailToken = 0;
  var detail = null;          // {key, body} | {key, error} | {key, loading}
  var live = { state: "loading", at: null, reason: "" };
  var sel = parseHash();
  var lastOpener = null;

  function parseHash() {
    var h = (window.location.hash || "").replace(/^#\/?/, "");
    if (!h) return null;
    var parts = h.split("/").map(function (x) {
      try { return decodeURIComponent(x); } catch (e) { return x; }
    });
    if (parts[0] === "mate" && parts[1]) return { kind: "mate", project: parts[1] };
    if (parts[0] === "cabinet" && parts[1]) return { kind: "cab", project: parts[1] };
    if (parts[0] === "crew" && parts[1] && parts[2]) return { kind: "crew", project: parts[1], crew: parts[2] };
    return null;
  }

  function selKey(s) {
    if (!s) return "";
    if (s.kind === "crew") return "crew/" + enc(s.project) + "/" + enc(s.crew);
    if (s.kind === "cab") return "cabinet/" + enc(s.project);
    return "mate/" + enc(s.project);
  }

  function select(key) {
    var cur = selKey(sel);
    if (key === cur) return;
    var active = document.activeElement;
    lastOpener = active && active.getAttribute ? active.getAttribute("data-focus") : null;
    window.location.hash = key ? "#" + key : "#";
    // hashchange does the rest, so the back button and a pasted link take
    // the same path as a click.
    if (!("onhashchange" in window)) onHash();
  }

  function onHash() {
    var next = parseHash();
    var changed = selKey(next) !== selKey(sel);
    sel = next;
    if (changed) {
      detail = null;
      loadDetail().then(draw);
      draw();
      var close = root.querySelector("[data-focus=\"drawer-close\"]");
      if (sel && close && close.focus) close.focus({ preventScroll: true });
      if (!sel && lastOpener) {
        var back = root.querySelector("[data-focus=\"" + lastOpener + "\"]");
        if (back && back.focus) back.focus({ preventScroll: true });
      }
    }
  }

  // ------------------------------------------------------------ loading

  function loadAll() {
    var token = pollToken;
    return Promise.all([
      api("/api/workspace"),
      api("/api/now").catch(function (e) { return { __error: e }; })
    ]).then(function (res) {
      if (token !== pollToken) return null;
      var wsBody = res[0];
      var nowBody = res[1];
      return Promise.all((wsBody.projects || []).map(function (p) {
        return api(projectPath(p.name)).then(function (b) { return [p.name, b]; }, function (e) { return [p.name, { __error: e }]; });
      })).then(function (list) {
        if (token !== pollToken) return;
        ws = wsBody;
        fatal = null;
        if (nowBody.__error) {
          now = null;
        } else {
          now = {};
          (nowBody.now || []).forEach(function (r) { now[r.actor_id] = r; });
        }
        var next = {};
        list.forEach(function (pair) { next[pair[0]] = pair[1]; });
        projects = next;
        var ids = [wsBody.last_event_id || 0];
        if (!nowBody.__error) ids.push(nowBody.last_event_id || 0);
        // The smallest id any of the bodies was built at: polling from it
        // can only fetch an event twice, never miss one.
        lastEventID = Math.min.apply(null, ids);
        return loadDetail();
      });
    }).then(function () {
      if (token !== pollToken) return;
      markLive();
      draw();
    }, function (err) {
      if (token !== pollToken) return;
      if (!ws) fatal = err;
      markStale(err.reason || err.message);
      draw();
    });
  }

  function loadDetail() {
    var s = sel;
    var key = selKey(s);
    var token = ++detailToken;
    if (!s || s.kind === "cab") return Promise.resolve();
    if (!detail || detail.key !== key) detail = { key: key, loading: true };
    var path = s.kind === "crew" ? taskPath(s.project, s.crew) : matePath(s.project);
    return api(path).then(function (body) {
      if (token !== detailToken) return;
      detail = { key: key, body: body };
    }, function (err) {
      if (token !== detailToken) return;
      detail = { key: key, error: err };
    });
  }

  function markLive() {
    live = { state: "live", at: Date.now(), reason: "" };
  }

  function markStale(reason) {
    live = { state: "stale", at: live.at, reason: reason || "the dashboard is not answering" };
    drawLive();
  }

  // One long poll on /api/events, as the admin UI keeps itself live. A
  // moved scene or a new event reloads the scene; a quiet poll with a Crew
  // or Mate drawer open reloads only that drawer, because a running
  // command's clock advances before any new event is written.
  function poll() {
    var token = pollToken;
    var watching = sel && sel.kind !== "cab";
    api("/api/events?since=" + enc(lastEventID) + "&wait=" + (watching ? "5" : "20")).then(function (body) {
      if (token !== pollToken) return;
      var serverID = body.last_event_id || 0;
      var moved = (body.events && body.events.length) || (body.now && body.now.length);
      if (serverID < lastEventID || moved) {
        // A smaller id is a `mate reindex` under us (docs/dashboard.md 10):
        // the old cursor will never come round again.
        lastEventID = serverID < lastEventID ? serverID : Math.max(lastEventID, serverID);
        loadAll().then(function () { if (token === pollToken) poll(); });
        return;
      }
      markLive();
      if (watching) {
        loadDetail().then(function () { if (token === pollToken) { draw(); poll(); } });
        return;
      }
      drawLive();
      poll();
    }, function (err) {
      if (token !== pollToken) return;
      markStale(err.reason || err.message);
      setTimeout(function () { if (token === pollToken) poll(); }, 3000);
    });
  }

  // ------------------------------------------------------------ the model

  function projectNames() { return ws ? (ws.projects || []).map(function (p) { return p.name; }) : []; }

  function card(name) {
    var list = ws ? ws.projects || [] : [];
    for (var i = 0; i < list.length; i++) if (list[i].name === name) return list[i];
    return null;
  }

  function body(name) {
    var b = projects[name];
    return b && !b.__error ? b : null;
  }

  function sceneRow(actorID) { return now ? now[actorID] || null : null; }

  function tasksOf(name) { var b = body(name); return b ? b.tasks || [] : []; }
  function openTasks(name) { return tasksOf(name).filter(function (t) { return !t.closed; }); }
  function closedTasks(name) {
    return tasksOf(name).filter(function (t) { return t.closed; }).sort(function (a, b) {
      return (parseAt(closedAt(b)) || 0) - (parseAt(closedAt(a)) || 0);
    });
  }
  function closedAt(t) { return t.closed_at || t.merged_at || ""; }

  function findTask(name, crew) {
    var list = tasksOf(name);
    for (var i = 0; i < list.length; i++) if (list[i].crew === crew) return list[i];
    return null;
  }

  // The Mate as the room needs it: the project page's block when it loaded,
  // else the workspace card's thinner one.
  function mateOf(name) {
    var b = body(name);
    var c = card(name) || {};
    var m = b ? b.mate : c.mate || {};
    return m || {};
  }

  function crewTokensToday(name, crew) {
    var row = sceneRow("crew:" + name + ":" + crew);
    return row ? row.tokens_today : null;
  }

  // A cabinet row's final state: merged when the timeline saw the merge,
  // otherwise the crew's own closing state (finished, failed).
  function finalState(t) {
    if (t.merged_at) return { word: "merged", tone: "t-ok", icon: "merged" };
    if (t.close_state === "failed") return { word: "failed", tone: "t-bad", icon: "failed" };
    if (t.close_state) return { word: String(t.close_state).replace(/_/g, " "), tone: "t-gone", icon: "merged" };
    return { word: "closed", tone: "t-gone", icon: "merged" };
  }

  function isAway(state) { return state === "walking_to_ceo" || state === "waiting_at_ceo"; }

  function waitingCount(name) {
    return openTasks(name).filter(function (t) { return t.state === "waiting_at_ceo" || t.state === "waiting_review"; }).length;
  }

  function mobile() {
    try { return !!(window.matchMedia && window.matchMedia("(max-width: 759px)").matches); } catch (e) { return false; }
  }

  // ------------------------------------------------------------ theme

  function storedTheme() {
    try { return window.localStorage.getItem("mate-office-theme") || ""; } catch (e) { return ""; }
  }

  function applyTheme(t) {
    var html = document.documentElement;
    if (t === "dark" || t === "light") html.setAttribute("data-theme", t);
    else html.removeAttribute("data-theme");
  }

  function isDark() {
    var t = document.documentElement.getAttribute("data-theme");
    if (t) return t === "dark";
    try { return !!(window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches); } catch (e) { return false; }
  }

  function toggleTheme() {
    var next = isDark() ? "light" : "dark";
    applyTheme(next);
    try { window.localStorage.setItem("mate-office-theme", next); } catch (e) { /* a private window keeps the choice for this page only */ }
    draw();
  }

  // ------------------------------------------------------------ the top bar

  function liveNode() {
    if (live.state === "stale") {
      return el("div", { class: "live stale", id: "live", role: "status", "aria-live": "polite", title: live.reason }, [
        icon("blocked"), "stale · ", el("span", { class: "rs", text: live.reason })
      ]);
    }
    if (live.state === "loading") {
      return el("div", { class: "live loading", id: "live", role: "status" }, [el("i", { class: "dot" }), "loading"]);
    }
    return el("div", { class: "live", id: "live", role: "status", "aria-live": "polite" }, [el("i", { class: "dot" }), "live · " + clock(live.at)]);
  }

  function drawLive() {
    var old = root.querySelector("#live");
    if (old && old.parentNode) old.parentNode.replaceChild(liveNode(), old);
  }

  // Tokens today across the workspace: every Mate's and crew's
  // `v_now.tokens_today`. Unknown when the scene could not be read.
  function tokensToday() {
    if (!now) return null;
    var sum = 0;
    Object.keys(now).forEach(function (id) {
      var r = now[id];
      if (r.actor_kind === "crew" || r.actor_kind === "mate") sum += r.tokens_today || 0;
    });
    return sum;
  }

  function adminHref() {
    if (!sel) return "../";
    if (sel.kind === "crew") return "../#/p/" + enc(sel.project) + "/t/" + enc(sel.crew);
    if (sel.kind === "mate") return "../#/p/" + enc(sel.project) + "/mate";
    return "../#/p/" + enc(sel.project);
  }

  function segNav() {
    return el("nav", { class: "seg", "aria-label": "View" }, [
      el("a", { class: "on", href: "./", "aria-current": "page", text: "Office" }),
      el("a", { href: adminHref(), text: "Admin", title: "the admin dashboard" })
    ]);
  }

  function themeButton() {
    var dark = isDark();
    var b = el("button", { class: "ib", type: "button", "aria-label": dark ? "Switch to light theme" : "Switch to dark theme", "data-focus": "theme" },
      icon(dark ? "sun" : "moon"));
    b.addEventListener("click", toggleTheme);
    return b;
  }

  function wsName() {
    if (!ws || !ws.root) return "?";
    var parts = String(ws.root).split(/[\\/]/).filter(Boolean);
    return parts.length ? parts[parts.length - 1] : String(ws.root);
  }

  function topBar() {
    var today = tokensToday();
    if (mobile()) {
      return [
        el("header", { class: "mtop" }, [
          el("span", { class: "mark" }, icon("office")),
          el("b", { class: "mname", text: "Mate Office" }),
          el("span", { class: "grow" }),
          liveNode(),
          themeButton()
        ]),
        el("div", { class: "msub" }, [
          segNav(),
          el("span", { class: "kpi" }, ["tokens today", el("b", { text: tok(today) })])
        ])
      ];
    }
    return el("header", { class: "top" }, [
      el("div", { class: "brand" }, [el("span", { class: "mark" }, icon("office")), "Mate Office"]),
      el("div", { class: "ws", title: ws ? ws.root : "" }, ["workspace ", el("b", { text: wsName() })]),
      liveNode(),
      el("div", { class: "grow" }),
      el("div", { class: "kpi", title: "tokens spent today by every Mate and crew (v_now.tokens_today)" }, ["Today ", el("b", { text: tok(today) }), " tokens"]),
      segNav(),
      themeButton()
    ]);
  }

  // ------------------------------------------------------------ the hallway

  function legend() {
    return el("div", { class: "board", role: "group", "aria-label": "Legend" }, [
      el("div", { class: "bh" }, [
        el("b", { text: "Crew states" }),
        el("span", { class: "hkey" }, ["face =",
          face("hk", harnessInfo("claude")), "Claude",
          face("hk", harnessInfo("codex")), "Codex",
          face("hk", harnessInfo("pi")), "Pi"])
      ]),
      el("div", { class: "lg" }, LEGEND.map(function (k) {
        var s = CREW_STATES[k];
        return el("span", { class: "li" }, [icoChip(s), s.word]);
      }).concat([el("span", { class: "li" }, [el("span", { class: "selsw" }), "selected"])]))
    ]);
  }

  // inboxSummary reads every project's inbox: the things waiting on a
  // decision, which is the captain's tray.
  function inboxSummary() {
    var items = [];
    var known = true;
    projectNames().forEach(function (n) {
      var b = body(n);
      if (b) items = items.concat(b.inbox || []);
      else known = false;
    });
    var questions = items.filter(function (i) { return i.kind === "status" && (i.verb === "needs-decision" || i.verb === "blocked"); }).length;
    return { n: items.length, questions: questions, known: known };
  }

  // short is the phone's wording, as the design's bottom-sheet board has it.
  function inboxWords(s, short) {
    if (!s.known && s.n === 0) return "inbox ?";
    if (s.n === 0) return "inbox empty";
    if (s.questions === s.n) return plural(s.n, "question", "questions") + (short ? "" : " waiting");
    return s.n + (short ? " in the inbox" : " waiting in the inbox");
  }

  function captainDesk() {
    var s = inboxSummary();
    return el("div", { class: "cap", role: "group", "aria-label": "Captain's desk" }, [
      el("span", { class: "mart", "aria-hidden": "true" }, [
        el("span", { class: "m-chair" }), el("span", { class: "m-head" }), el("span", { class: "c-hat" }),
        el("span", { class: "m-body", style: "background:#46566B" }), el("span", { class: "m-desk" }),
        el("span", { class: "m-mon m1" }),
        el("span", { class: "tray" }, s.n > 0 ? el("b", { text: String(s.n) }) : null),
        el("span", { class: "m-plate", text: "Captain · you" })
      ]),
      el("span", { class: "capq" + (s.n > 0 ? "" : " none") }, [icon("ask"), inboxWords(s)])
    ]);
  }

  function hallFigures() {
    var figs = [];
    projectNames().forEach(function (n) {
      openTasks(n).forEach(function (t) {
        if (isAway(t.state)) figs.push(figure(n, t));
      });
    });
    return el("div", { class: "hfigs" }, figs.length ? figs : [el("span", { class: "hnote", text: "Nobody at a Mate's door" })]);
  }

  function figure(project, t) {
    var key = "crew/" + enc(project) + "/" + enc(t.crew);
    var s = crewState(t.state);
    var h = harnessInfo(t.harness);
    var asks = t.state === "waiting_at_ceo";
    var fig = el("button", {
      class: "fig" + (asks ? "" : " walk") + (selKey(sel) === key ? " sel" : ""), type: "button",
      "aria-label": t.crew + " of " + project + ", " + s.word, "data-room": project
    }, [
      asks ? el("span", { class: "bubble" }, [icon("ask"), s.word]) : null,
      el("span", { class: "fart", "aria-hidden": "true" }, [
        el("span", { class: "f-shadow" }), el("span", { class: "f-leg l1" }), el("span", { class: "f-leg l2" }),
        el("span", { class: "f-body", style: "background:" + shirt(t.crew) }),
        face("f-head", h),
        asks ? null : el("span", { class: "f-motion" }, [el("i"), el("i"), el("i")])
      ]),
      el("span", { class: "lbl" }, [el("span", { class: "id", text: t.crew }), el("span", { class: "tok", text: age(t.since) })]),
      asks ? null : chip(s)
    ]);
    return pickable(fig, key);
  }

  // ------------------------------------------------------------ rooms

  function roomCols(n) { return n <= 1 ? 1 : n <= 6 ? 2 : 3; }

  function room(name, index) {
    var c = card(name) || {};
    var m = mateOf(name);
    var b = projects[name];
    var open = openTasks(name);
    var closed = closedTasks(name);
    var running = !!(c.mate && c.mate.running);
    var cols = roomCols(open.length);
    var lowCab = cols === 1;
    var wait = waitingCount(name);
    var node = el("section", {
      class: "room r" + (index % 3) + (running ? "" : " r-stopped") + " has-cab" + (lowCab ? " low-cab" : ""),
      "aria-label": name + " room", "data-room": name,
      style: "--basis:" + (cols === 1 ? 300 : cols === 2 ? 440 : 620) + "px;--cols:" + cols
    }, [
      el("div", { class: "door", "data-door": name }, [el("i", { class: "dl" }), el("i", { class: "dr2" })]),
      el("div", { class: "plate" }, [
        el("b", { text: name }),
        el("span", { class: "light" + (running ? "" : " off") }, [el("i"), running ? "running" : "stopped"]),
        wait > 0 ? el("span", { class: "wb" }, [icon("ask"), wait + " waiting"]) : null
      ]),
      lowCab ? null : cabinet(name, closed, false),
      mateDesk(name, m, running),
      b && b.__error ? el("p", { class: "room-err", text: "This room could not be read: " + (b.__error.reason || b.__error.message) })
        : open.length ? el("div", { class: "crews" }, open.map(function (t) { return desk(name, t); }))
          : el("p", { class: "empty-room", text: b ? "No open crews" : "loading…" }),
      running ? null : el("div", { class: "off" }, [icon("idle"), "Lights off · project stopped"]),
      lowCab ? cabinet(name, closed, true) : null
    ]);
    return node;
  }

  function mateDesk(name, m, running) {
    var key = "mate/" + enc(name);
    var s = mateState(m.state);
    var h = harnessInfo(m.harness);
    var present = running && m.state !== "gone";
    var btn = el("button", {
      class: "mate" + (present ? "" : " dim") + (selKey(sel) === key ? " sel" : ""), type: "button",
      "aria-label": "Mate of " + name + ", " + s.word
    }, [
      el("span", { class: "mart", "aria-hidden": "true" }, [
        el("span", { class: "m-chair" }),
        present ? face("m-head", h) : el("span", { class: "m-ghost" }),
        present ? el("span", { class: "m-body", style: "background:" + shirt("mate:" + name) }) : null,
        el("span", { class: "m-desk" }), el("span", { class: "m-mon m1" }), el("span", { class: "m-mon m2" }),
        el("span", { class: "m-plate", text: "Mate · " + name }),
        present && m.state === "deciding" ? el("span", { class: "think" }, [el("i"), el("i"), el("i")]) : null
      ]),
      el("span", { class: "mmeta" }, [chip(s), htag(h), el("span", { class: "tok", text: tok(m.tokens_today) + " today" })])
    ]);
    return pickable(btn, key);
  }

  function desk(project, t) {
    var key = "crew/" + enc(project) + "/" + enc(t.crew);
    var st = t.state || "";
    var s = crewState(st);
    var h = harnessInfo(t.harness);
    var away = isAway(st) || st === "arriving" || st === "leaving";
    var seated = st === "at_desk_working" || st === "idle" || st === "blocked" || st === "waiting_review" || st === "";
    var art = [el("span", { class: "a-chair" })];
    if (st === "asleep") {
      art.push(el("span", { class: "a-body slump", style: "background:" + shirt(t.crew) }));
    } else if (seated) {
      art.push(face("a-head", h), el("span", { class: "a-body", style: "background:" + shirt(t.crew) }));
    } else if (st === "gone") {
      art.push(el("span", { class: "a-ghost" }));
    }
    art.push(el("span", { class: "a-desk" }), el("span", { class: "a-paper" }));
    var lapOpen = st === "at_desk_working" || st === "idle" || st === "blocked" || isAway(st) || st === "";
    art.push(el("span", { class: lapOpen ? "a-lap" : "a-lapc" }));
    if (st !== "asleep" && st !== "gone" && st !== "arriving" && st !== "leaving") art.push(el("span", { class: "a-mug" }));
    if (st === "at_desk_working" || st === "idle" || st === "blocked") art.push(el("span", { class: "a-hand h1" }), el("span", { class: "a-hand h2" }));
    if (st === "waiting_review") {
      art.push(el("span", { class: "a-doc" }, [el("i"), el("i"), el("i"), el("i")]), el("span", { class: "a-hand h3" }), el("span", { class: "a-hand h4" }));
    }
    if (st === "asleep") {
      art.push(el("span", { class: "a-arms", style: "background:" + shirt(t.crew) }), face("a-head", h, "sleep"),
        el("span", { class: "a-zz" }, [el("i", { text: "z" }), el("i", { text: "z" }), el("i", { text: "Z" })]));
    }
    if (st === "blocked") art.push(el("span", { class: "a-alert" }, icon("bang")));
    if (away) art.push(el("span", { class: "a-note" }, icon(st === "arriving" ? "arriving" : st === "leaving" ? "leaving" : st === "walking_to_ceo" ? "walking" : "ask")));

    var btn = el("button", {
      class: "desk s-" + (st || "unknown") + (st === "gone" ? " dim" : "") + (isAway(st) ? " away" : "") + (selKey(sel) === key && !isAway(st) ? " sel" : ""),
      type: "button", "aria-label": t.crew + ", " + s.word + (st === "blocked" && t.detail ? ", " + detailWord(t.detail) : "")
    }, [
      el("span", { class: "art", "aria-hidden": "true" }, art),
      el("span", { class: "lbl" }, [el("span", { class: "id", text: t.crew }), el("span", { class: "tok", text: tok(t.tokens ? t.tokens.total : null) })]),
      chip(s),
      st === "blocked" ? el("span", { class: "quiet", text: (detailWord(t.detail) || "cannot be reached") + " · " + age(t.since) }) : null
    ]);
    return pickable(btn, key);
  }

  function cabinet(name, closed, low) {
    var key = "cabinet/" + enc(name);
    var on = selKey(sel) === key;
    var peek = el("span", { class: "peek" }, [el("span", { class: "pk-h", text: "Latest in the cabinet" })].concat(
      closed.length ? closed.slice(0, 3).map(function (t) {
        var f = finalState(t);
        return el("span", { class: "pk" }, [
          el("span", { class: "tn", text: t.crew }),
          el("span", { class: "pk-s " + f.tone, text: f.word }),
          el("span", { class: "tok", text: age(closedAt(t)) + " ago · " + tok(t.tokens ? t.tokens.total : null) + " tokens" })
        ]);
      }) : [el("span", { class: "pk-empty", text: "Nothing finished yet" })]));
    var btn = el("button", {
      class: "cab" + (low ? " low" : "") + (on ? " open sel" : ""), type: "button",
      "aria-label": "Filing cabinet, " + plural(closed.length, "finished task", "finished tasks")
    }, [
      el("span", { class: "cbody", "aria-hidden": "true" }, [el("span", { class: "dr" }, el("i")), el("span", { class: "dr" }, el("i")), el("span", { class: "dr" }, el("i"))]),
      el("span", { class: "cnt", text: String(closed.length) }),
      el("span", { class: "clbl", text: "finished" }),
      peek
    ]);
    return pickable(btn, key);
  }

  // ------------------------------------------------------------ phone

  function tile(project, t, standing) {
    var st = t.state || "";
    var s = crewState(st);
    var h = harnessInfo(t.harness);
    var parts = [];
    var seated = st === "at_desk_working" || st === "idle" || st === "blocked" || st === "waiting_review" || st === "asleep" || st === "";
    if (standing) {
      parts.push(el("span", { class: "s-legs" }), el("span", { class: "t-b", style: "background:" + shirt(t.crew) }), face("t-h", h));
    } else if (st === "gone") {
      parts.push(el("span", { class: "t-ghost" }));
    } else if (seated) {
      parts.push(el("span", { class: "t-b", style: "background:" + shirt(t.crew) }), face("t-h", h));
      if (st === "at_desk_working" || st === "idle" || st === "blocked") parts.push(el("span", { class: "t-lap" }));
      if (st === "waiting_review") parts.push(el("span", { class: "t-doc" }));
      if (st === "asleep") parts.push(el("span", { class: "t-z", text: "zZ" }));
    } else {
      parts.push(el("span", { class: "t-seat" }));
    }
    parts.push(el("span", { class: "t-st " + s.tone }, icon(s.icon)));
    var key = "crew/" + enc(project) + "/" + enc(t.crew);
    var cls = "mc" + (st === "at_desk_working" ? " p-working" : "") + (st === "asleep" ? " p-sleep" : "") +
      (st === "blocked" ? " p-blocked" : "") + (st === "gone" ? " dimt" : "") + (standing && st === "walking_to_ceo" ? " walk" : "") +
      (selKey(sel) === key ? " sel" : "");
    var btn = el("button", { class: cls, type: "button", style: "--rb2:" + (standing ? "var(--hall)" : "var(--rb)"),
      "aria-label": t.crew + ", " + s.word }, [
      el("span", { class: "tile" + (standing ? " tstand" : ""), "aria-hidden": "true" }, parts),
      el("span", { class: "id", text: t.crew }),
      el("span", { class: "mw " + s.tone, text: s.word })
    ]);
    return pickable(btn, key);
  }

  function mobileHall() {
    var s = inboxSummary();
    var figs = [];
    projectNames().forEach(function (n) {
      openTasks(n).forEach(function (t) { if (isAway(t.state)) figs.push(tile(n, t, true)); });
    });
    return el("section", { class: "mhall", "aria-label": "Hallway" }, [
      el("div", { class: "mcap" }, [
        el("span", { class: "cav", "aria-hidden": "true" }, [el("span", { class: "c-b" }), el("span", { class: "c-h" }), el("span", { class: "c-hat2" })]),
        el("span", { class: "mct" }, [el("b", { text: "Captain · you" }),
          el("span", { class: "capq" + (s.n > 0 ? "" : " none") }, [icon("ask"), inboxWords(s, true)])])
      ]),
      figs.length ? el("div", { class: "mfigs" }, figs) : null
    ]);
  }

  function mobileRoom(name, index) {
    var c = card(name) || {};
    var m = mateOf(name);
    var b = projects[name];
    var running = !!(c.mate && c.mate.running);
    var open = openTasks(name);
    var closed = closedTasks(name);
    var wait = waitingCount(name);
    var ms = mateState(m.state);
    var h = harnessInfo(m.harness);
    var present = running && m.state !== "gone";
    var mkey = "mate/" + enc(name);
    var cab = pickable(el("button", { class: "mcab" + (selKey(sel) === "cabinet/" + enc(name) ? " sel" : ""), type: "button",
      "aria-label": "Filing cabinet, " + plural(closed.length, "finished task", "finished tasks") },
    [icon("cabinet"), String(closed.length), el("small", { text: "done" })]), "cabinet/" + enc(name));
    var mate = pickable(el("button", { class: "mr-mate" + (selKey(sel) === mkey ? " sel" : ""), type: "button",
      "aria-label": "Mate of " + name + ", " + ms.word, style: "--rb2:var(--surface)" }, [
      el("span", { class: "tile lead", "aria-hidden": "true" }, present
        ? [el("span", { class: "t-b", style: "background:" + shirt("mate:" + name) }), face("t-h", h)]
        : [el("span", { class: "t-ghost" })]),
      el("span", { class: "mm-t" }, [el("span", { class: "mm-1" }, [el("b", { text: "Mate" }), htag(h)]), chip(ms)]),
      el("span", { class: "mm-v" }, [el("b", { text: tok(m.tokens_today) }), "today"])
    ]), mkey);
    return el("section", { class: "mroom r" + (index % 3) + (running ? "" : " r-stopped"), "aria-label": name + " room" }, [
      el("div", { class: "mr-h" }, [
        el("b", { text: name }),
        el("span", { class: "light" + (running ? "" : " off") }, [el("i"), running ? "running" : "stopped"]),
        wait > 0 ? el("span", { class: "wb" }, [icon("ask"), wait + " waiting"]) : null,
        el("span", { class: "grow" }),
        cab
      ]),
      mate,
      b && b.__error ? el("p", { class: "room-err", text: "This room could not be read: " + (b.__error.reason || b.__error.message) })
        : open.length ? el("div", { class: "mr-crews" }, open.map(function (t) { return tile(name, t, false); }))
          : el("p", { class: "m-empty", text: b ? "No open crews" : "loading…" })
    ]);
  }

  // ------------------------------------------------------------ the drawer

  function drawerHead(avatar, role, title, stateChip, since) {
    var close = el("button", { class: "ib", type: "button", "aria-label": "Close details", "data-focus": "drawer-close" }, icon("close"));
    close.addEventListener("click", function () { select(""); });
    return el("div", { class: "dh" }, [
      avatar,
      el("div", { class: "dt" }, [
        el("div", { class: "role" }, role),
        el("h2", { text: title }),
        el("div", { class: "dstate" }, [stateChip, since ? el("span", { class: "since", text: since }) : null])
      ]),
      close
    ]);
  }

  function personAvatar(h, colour, present) {
    return el("span", { class: "dav", "aria-hidden": "true" }, present
      ? [face("d-head", h), el("span", { class: "d-body", style: "background:" + colour })]
      : [el("span", { class: "m-ghost", style: "left:11px;top:11px" })]);
  }

  // usageSection is the design's "Token usage now": the total, the cost,
  // the four buckets in their fixed order, the context window and today.
  function usageSection(o) {
    var t = o.tokens || null;
    var total = t ? t.total : null;
    var names = [["input", "b-in", "input"], ["cache_read", "b-cr", "cache read"], ["cache_write", "b-cw", "cache write"], ["output", "b-out", "output"]];
    var segs = [];
    var rows = [];
    names.forEach(function (n) {
      var v = t ? t[n[0]] : null;
      var pct = v == null || !total ? "–" : v > 0 && v / total < 0.01 ? "<1%" : Math.round(100 * v / total) + "%";
      if (v > 0) segs.push(el("i", { class: n[1], style: "flex-grow:" + v, title: n[2] + " " + tok(v) }));
      rows.push(el("div", null, [el("i", { class: "sw " + n[1] }), n[2], el("b", { text: tok(v) }), el("em", { text: pct })]));
    });
    var stackLabel = names.map(function (n) { return n[2] + " " + tok(t ? t[n[0]] : null); }).join(", ");

    var pct = o.contextPct;
    var ctxTokens = o.contextTokens;
    var ctxOf;
    if (pct != null && ctxTokens != null && pct > 0) {
      ctxOf = tok(ctxTokens) + " of " + tok(Math.round(ctxTokens * 100 / pct));
    } else if (ctxTokens != null) {
      ctxOf = tok(ctxTokens) + " used · window unknown";
    } else {
      ctxOf = "window unknown";
    }
    var ctxKnown = pct != null;

    return el("section", { class: "ds", "aria-label": "Token usage now" }, [
      el("h3", { class: "sh" }, ["Token usage now ", el("span", { text: "updated " + clock(o.updated) })]),
      el("div", { class: "big" }, [
        el("span", { class: "n", text: tok(total) }),
        el("span", { class: "u", text: "tokens" }),
        el("span", { class: "c" }, [el("b", { text: cost(o.cost) }), el("span", { text: o.cost == null ? "cost not reported" : "estimated cost" })])
      ]),
      el("div", { class: "stack" + (segs.length ? "" : " none"), role: "img", "aria-label": stackLabel }, segs),
      el("div", { class: "bl" }, rows),
      t && t.thinking > 0 ? el("p", { class: "think-note", text: "thinking " + tok(t.thinking) + ", inside output already" }) : null,
      el("div", { class: "meter" }, [
        el("div", { class: "mh" }, [el("span", { text: "Context window" }),
          el("span", null, [el("b", { text: "context " + (ctxKnown ? Math.round(pct) + "%" : "?") }), " · " + ctxOf])]),
        el("div", { class: "bar" + (ctxKnown ? "" : " unk"), role: "img", "aria-label": "context used, " + (ctxKnown ? Math.round(pct) + "%" : "unknown") },
          ctxKnown ? el("i", { style: "width:" + Math.max(0, Math.min(100, pct)).toFixed(1) + "%" }) : null)
      ]),
      el("div", { class: "today" }, [
        el("div", { class: "tv" }, ["Tokens today", el("b", { text: tok(o.today) })]),
        el("div", null, [sparkNode(o.spark), el("div", { class: "sx" }, [el("span", { text: "6h ago" }), el("span", { text: "now" })])])
      ])
    ]);
  }

  // spark buckets dated token counts into twelve half hours ending now.
  // points is [{at, tokens}] or null while unknown.
  var SPARK_BINS = 12;
  var SPARK_BIN_MS = 30 * 60 * 1000;

  function sparkBins(points) {
    var end = Date.now();
    var start = end - SPARK_BINS * SPARK_BIN_MS;
    var bins = [];
    for (var i = 0; i < SPARK_BINS; i++) bins.push(0);
    points.forEach(function (p) {
      var t = parseAt(p.at);
      if (t == null || t < start || t > end) return;
      var i = Math.min(SPARK_BINS - 1, Math.floor((t - start) / SPARK_BIN_MS));
      bins[i] += p.tokens || 0;
    });
    return bins;
  }

  function sparkNode(spark) {
    if (!spark || spark.state === "loading") return el("div", { class: "spark unk", role: "img", "aria-label": "loading", text: "loading…" });
    if (spark.state === "error") return el("div", { class: "spark unk", role: "img", "aria-label": "unknown", text: "?" });
    var bins = sparkBins(spark.points);
    var max = Math.max.apply(null, bins);
    var label = max > 0 ? "Tokens per 30 minutes over the last 6 hours, " + spark.by : "No tokens recorded in the last 6 hours";
    return el("div", { class: "spark", role: "img", "aria-label": label, title: label }, bins.map(function (v) {
      return el("i", { style: "height:" + Math.max(4, max > 0 ? Math.round(100 * v / max) : 0) + "%", title: tok(v) });
    }));
  }

  function crewSpark() {
    if (!detail || detail.loading) return { state: "loading" };
    if (detail.error) return { state: "error" };
    return {
      by: "by model call",
      points: (detail.body.turns || []).map(function (u) { return { at: u.started_at, tokens: u.tokens ? u.tokens.total : 0 }; })
    };
  }

  // The Mate's calls are not in the API one by one, so its spark is built
  // from the prompt steps (internal/diagnostics' runs of consecutive calls
  // of one type), dated by when each step began.
  function mateSpark() {
    if (!detail || detail.loading) return { state: "loading" };
    if (detail.error) return { state: "error" };
    var points = [];
    (detail.body.exchanges || []).forEach(function (x) {
      ((x.overview && x.overview.steps) || []).forEach(function (s) {
        points.push({ at: s.started_at, tokens: s.tokens ? s.tokens.total : 0 });
      });
    });
    return { by: "by prompt step", points: points };
  }

  function mateDrawer(name) {
    var m = mateOf(name);
    var c = card(name) || {};
    var b = body(name);
    var running = !!(c.mate && c.mate.running);
    var s = mateState(m.state);
    var h = harnessInfo(m.harness);
    var present = running && m.state !== "gone";
    var nodes = [
      drawerHead(personAvatar(h, shirt("mate:" + name), present),
        ["Mate · team lead", htag(h)], name, chip(s), m.since ? "since " + age(m.since) : null),
      usageSection({
        tokens: b ? m.tokens : null, cost: b ? m.cost : null, updated: b ? b.generated_at : null,
        contextPct: m.context_pct, contextTokens: m.context_tokens, today: m.tokens_today, spark: mateSpark()
      })
    ];
    var last = m.last_turn;
    nodes.push(el("section", { class: "ds", "aria-label": "Current activity" }, [
      el("h3", { class: "sh", text: "Current activity" }),
      el("p", { class: "stl", text: mateSentence(m) }),
      el("dl", { class: "kv" }, [
        el("dt", { text: "Facing" }), el("dd", { class: "mono", text: m.target || "nobody" }),
        el("dt", { text: "Last turn" }), el("dd", { text: last ? age(last.ended_at || last.started_at) + " ago" : (b ? "none recorded" : "?") }),
        el("dt", { text: "Mode" }), el("dd", { text: (b && b.mode) || c.mode || "?" })
      ])
    ]));
    var open = openTasks(name);
    var max = Math.max.apply(null, [1].concat(open.map(function (t) { return t.tokens ? t.tokens.total : 0; })));
    nodes.push(el("section", { class: "ds", "aria-label": "Crews in this room" }, [
      el("h3", { class: "sh" }, ["Crews in this room ", el("span", { text: "total tokens" })]),
      open.length ? el("div", null, open.map(function (t) {
        var cs = crewState(t.state);
        var v = t.tokens ? t.tokens.total : null;
        var row = el("button", { class: "cr", type: "button", "aria-label": t.crew + ", " + cs.word + ", " + tok(v) + " tokens" }, [
          icoChip(cs),
          el("span", null, [el("span", { class: "id", text: t.crew }), el("small", { text: cs.word })]),
          el("span", { class: "crbar", "aria-hidden": "true" }, el("i", { style: "width:" + Math.max(3, Math.round(100 * (v || 0) / max)) + "%" })),
          el("span", { class: "crv", text: tok(v) })
        ]);
        return pickable(row, "crew/" + enc(name) + "/" + enc(t.crew));
      })) : el("p", { class: "loading-note", text: b ? "No open crews" : "loading…" })
    ]));
    return nodes;
  }

  function lastOf(list) { return list && list.length ? list[list.length - 1] : null; }

  function crewDrawer(name, crew) {
    var t = findTask(name, crew);
    var b = body(name);
    if (!t) {
      return [drawerHead(personAvatar(harnessInfo(""), shirt(crew), false), ["Crew · " + name], crew, chip(UNKNOWN_STATE), null),
        el("section", { class: "ds" }, el("p", { class: b ? "err-note" : "loading-note", text: b ? "No crew " + crew + " in " + name + "." : "loading…" }))];
    }
    var st = t.state || "";
    var s = crewState(st);
    var h = harnessInfo(t.harness);
    var word = s.word + ((st === "blocked" || st === "asleep") && t.detail ? ", " + detailWord(t.detail) : "");
    var d = detail && !detail.loading && !detail.error ? detail.body : null;
    var perf = d ? d.performance || {} : null;

    var nodes = [
      drawerHead(personAvatar(h, shirt(crew), st !== "gone"), ["Crew · " + name, htag(h)], crew, chip(s, word), t.since ? "since " + age(t.since) : null),
      usageSection({
        tokens: t.tokens, cost: t.cost, updated: b ? b.generated_at : null,
        contextPct: t.context_pct, contextTokens: t.turns > 0 ? t.context_tokens_last : null,
        today: crewTokensToday(name, crew), spark: crewSpark()
      })
    ];

    // Current activity: what the crew last said it was doing, and what the
    // recording shows it doing.
    var activity;
    if (detail && detail.error) {
      activity = [el("p", { class: "err-note", text: "Could not read this crew's work: " + (detail.error.reason || detail.error.message) })];
    } else if (!d) {
      activity = [el("p", { class: "loading-note", text: "loading…" })];
    } else {
      var line = lastOf(d.status_lines);
      var prompt = lastOf(perf.prompt_turns);
      var step = prompt && prompt.overview ? lastOf(prompt.overview.steps) : null;
      var seg = null;
      (perf.segments || []).forEach(function (x) { if (x.id === perf.current_segment_id) seg = x; });
      var lastTurn = lastOf(d.turns);
      activity = [
        el("p", { class: "stl" + (line ? "" : " muted"), text: line ? line.text : "No status line written yet", title: line && line.verb ? line.verb : null }),
        el("dl", { class: "kv" }, [
          el("dt", { text: "Step" }), el("dd", { text: step ? step.label + (step.open ? " · running" : "") : "none recorded" }),
          el("dt", { text: "Target" }), el("dd", { class: "mono", text: seg && seg.target ? seg.target : (t.target || "none recorded") }),
          el("dt", { text: "Last turn" }), el("dd", { text: lastTurn ? age(lastTurn.ended_at || lastTurn.started_at) + " ago" : "none recorded" })
        ])
      ];
    }
    nodes.push(el("section", { class: "ds", "aria-label": "Current activity" }, [el("h3", { class: "sh", text: "Current activity" })].concat(activity)));

    var branch = d ? d.branch || {} : null;
    var skills = perf ? perf.skills || [] : null;
    var link = el("a", { class: "link", href: "../#/p/" + enc(name) + "/t/" + enc(crew) }, ["Open timeline", icon("arrowR")]);
    nodes.push(el("section", { class: "ds", "aria-label": "Task" }, [
      el("h3", { class: "sh", text: "Task" }),
      el("p", { class: "stl", text: t.text || "no task text recorded" }),
      el("dl", { class: "kv" }, [
        el("dt", { text: "Branch" }),
        el("dd", { class: "mono" + (branch && !branch.exists ? " gone" : ""), title: branch && branch.reason ? branch.reason : null,
          text: (t.branch || "none recorded") + (branch && t.branch && !branch.exists ? " · gone" : "") }),
        el("dt", { text: "Turns" }), el("dd", { text: String(t.turns) }),
        el("dt", { text: "Skills" }),
        el("dd", { title: "a skill counts when the agent loaded it; loading is not proof it was followed",
          text: skills == null ? "?" : skills.length ? skills.map(function (k) { return k.name + (k.count > 1 ? " ×" + k.count : ""); }).join(", ") : "none loaded" })
      ]),
      link
    ]));
    return nodes;
  }

  // The cabinet's drawers, by when a task closed.
  var DRAWERS = [
    { name: "Top drawer", range: "this week", max: 7 },
    { name: "Middle drawer", range: "last week", max: 14 },
    { name: "Bottom drawer", range: "older", max: Infinity }
  ];

  function cabinetDrawer(name) {
    var b = body(name);
    var closed = closedTasks(name);
    var oldest = closed.length ? closedAt(closed[closed.length - 1]) : "";
    var head = drawerHead(el("span", { class: "dav dcab", "aria-hidden": "true" }, icon("cabinet")),
      [name + " · finished tasks"], "Filing cabinet",
      chip({ word: "", tone: "t-gone", icon: "cabinet" }, plural(closed.length, "finished", "finished")),
      closed.length ? "oldest " + monthDay(oldest) : null);
    if (!b) return [head, el("p", { class: "cab-empty", text: projects[name] ? "This room could not be read." : "loading…" })];
    if (!closed.length) return [head, el("p", { class: "cab-empty", text: "Nothing has finished in " + name + " yet." })];

    var counts = {};
    var order = [];
    var sum = 0;
    closed.forEach(function (t) {
      var f = finalState(t);
      if (!counts[f.word]) { counts[f.word] = { f: f, n: 0 }; order.push(f.word); }
      counts[f.word].n++;
      sum += t.tokens ? t.tokens.total : 0;
    });
    var nowMs = Date.now();
    var groups = DRAWERS.map(function (dr, i) {
      var min = i === 0 ? -Infinity : DRAWERS[i - 1].max;
      return { dr: dr, items: closed.filter(function (t) {
        var at = parseAt(closedAt(t));
        var days = at == null ? Infinity : (nowMs - at) / 86400000;
        return days >= min && days < dr.max || (at == null && dr.max === Infinity);
      }) };
    }).filter(function (g) { return g.items.length; });

    return [head,
      el("section", { class: "ds", "aria-label": "Summary" }, el("div", { class: "csum" }, order.map(function (w) {
        return chip(counts[w].f, counts[w].n + " " + w);
      }).concat([el("span", { class: "tt" }, [el("b", { text: tok(sum) }), " tokens"])]))),
      el("div", { class: "colh", "aria-hidden": "true" }, [el("span", { text: "Task" }), el("span", { text: "Final state" }), el("span", { text: "Closed" }), el("span", { text: "Tokens" })])
    ].concat(groups.map(function (g) {
      return el("div", { class: "cg" }, [
        el("div", { class: "cgh" }, [el("i", { class: "knob" }), el("b", { text: g.dr.name }), el("span", { text: g.dr.range }), el("span", { class: "n", text: plural(g.items.length, "task", "tasks") })]),
        el("ul", { class: "tl" }, g.items.map(function (t) {
          var f = finalState(t);
          return el("li", { class: "tr", title: t.text || null }, [
            el("span", { class: "tn", text: t.crew }),
            el("span", null, chip(f)),
            el("span", { class: "td", title: closedAt(t) }, [age(closedAt(t)) + " ago", el("small", { text: monthDay(closedAt(t)) })]),
            el("span", { class: "tt2", text: tok(t.tokens ? t.tokens.total : null) })
          ]);
        }))
      ]);
    }));
  }

  function drawer() {
    var aside = el("aside", { class: "drawer", "aria-label": "Details", id: "drawer" });
    if (!sel) { aside.setAttribute("hidden", ""); return aside; }
    aside.appendChild(el("span", { class: "grab", "aria-hidden": "true" }));
    var nodes = sel.kind === "mate" ? mateDrawer(sel.project) : sel.kind === "crew" ? crewDrawer(sel.project, sel.crew) : cabinetDrawer(sel.project);
    append(aside, nodes);
    return aside;
  }

  // ------------------------------------------------------------ the page

  function floor() {
    var f = el("main", { class: "floor", id: "floor" });
    f.addEventListener("click", function () { if (sel) select(""); });
    if (fatal) {
      f.appendChild(el("div", { class: "nothing" }, [
        el("p", { text: fatal.message }),
        fatal.reason && fatal.reason !== fatal.message ? el("p", { text: fatal.reason }) : null,
        el("p", { text: "The page keeps polling; it fills in as soon as the dashboard answers." })
      ]));
      return f;
    }
    if (!ws) { f.appendChild(el("p", { class: "boot", text: "loading…" })); return f; }
    var names = projectNames();
    if (mobile()) {
      f.setAttribute("class", "floor mfloor");
      f.appendChild(mobileHall());
      names.forEach(function (n, i) { f.appendChild(mobileRoom(n, i)); });
    } else {
      f.appendChild(el("section", { class: "hall", "aria-label": "Hallway" }, [legend(), captainDesk(), hallFigures()]));
      f.appendChild(el("div", { class: "rooms" }, names.map(room)));
    }
    if (!names.length) f.appendChild(el("p", { class: "nothing", text: "This workspace has no projects yet." }));
    return f;
  }

  function draw() {
    var y = window.scrollY || 0;
    var oldDrawer = root.querySelector("#drawer");
    var dy = oldDrawer ? oldDrawer.scrollTop || 0 : 0;
    var active = document.activeElement;
    var focusKey = active && active.getAttribute ? active.getAttribute("data-focus") : null;

    root.textContent = "";
    append(root, topBar());
    root.appendChild(floor());
    var scrim = el("div", { class: "scrim", "aria-hidden": "true" });
    if (!sel) scrim.setAttribute("hidden", "");
    scrim.addEventListener("click", function () { select(""); });
    root.appendChild(scrim);
    var d = drawer();
    root.appendChild(d);
    if (oldDrawer && sel) d.scrollTop = dy;

    if (focusKey) {
      var again = root.querySelector("[data-focus=\"" + focusKey + "\"]");
      if (again && again.focus) again.focus({ preventScroll: true });
    }
    if (window.scrollTo) window.scrollTo(0, y);
    scheduleTrails();
  }

  // Trails: a dotted line from each figure in the hallway back to the door
  // of the room it walked out of, drawn once the layout is known.
  var trailFrame = 0;
  function scheduleTrails() {
    if (!window.requestAnimationFrame) return;
    if (trailFrame) return;
    trailFrame = window.requestAnimationFrame(function () { trailFrame = 0; drawTrails(); });
  }

  function drawTrails() {
    var f = root.querySelector("#floor");
    if (!f || !f.getBoundingClientRect || mobile()) return;
    var old = f.querySelector(".trail");
    if (old && old.parentNode) old.parentNode.removeChild(old);
    var base = f.getBoundingClientRect();
    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", "trail");
    svg.setAttribute("aria-hidden", "true");
    var any = false;
    var hall = f.querySelector(".hall");
    var hallBottom = hall ? hall.getBoundingClientRect().bottom : 0;
    Array.prototype.forEach.call(f.querySelectorAll(".fig"), function (fig) {
      var door = f.querySelector("[data-door=\"" + cssEscape(fig.getAttribute("data-room")) + "\"]");
      if (!door) return;
      var a = door.getBoundingClientRect();
      var b = fig.getBoundingClientRect();
      // Only a room that opens onto the hallway gets a trail; one wrapped
      // to a lower row would have its line drawn across the rooms above.
      if (a.top - hallBottom > 80) return;
      var x1 = a.left + a.width / 2 - base.left, y1 = a.top - base.top;
      var x2 = b.left + b.width / 2 - base.left, y2 = b.bottom - base.top - 4;
      var p = document.createElementNS(SVG_NS, "path");
      p.setAttribute("d", "M" + x1 + " " + y1 + " C " + x1 + " " + (y1 - 40) + ", " + x2 + " " + (y2 + 40) + ", " + x2 + " " + y2);
      // A crew still walking is drawn in the walking colour; one already
      // waiting at the door leaves a fainter trail behind it.
      if (!/\bwalk\b/.test(fig.getAttribute("class") || "")) p.setAttribute("class", "faint");
      svg.appendChild(p);
      any = true;
    });
    if (any) f.insertBefore(svg, f.firstChild);
  }

  function cssEscape(s) {
    return window.CSS && window.CSS.escape ? window.CSS.escape(s) : String(s).replace(/["\\]/g, "\\$&");
  }

  // ------------------------------------------------------------ start

  applyTheme(storedTheme());
  window.addEventListener("hashchange", onHash);
  window.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && sel) select("");
  });
  window.addEventListener("resize", scheduleTrails);
  try {
    var mq = window.matchMedia && window.matchMedia("(max-width: 759px)");
    if (mq && mq.addEventListener) mq.addEventListener("change", draw);
    var dq = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)");
    if (dq && dq.addEventListener) dq.addEventListener("change", draw);
  } catch (e) { /* no media queries to follow */ }
  // Ages ("since 23m") move with the clock, not with events.
  setInterval(function () { if (ws) draw(); }, 60000);
  draw();
  loadAll().then(function () { poll(); });
})();
