// humanize.js - the number vocabulary of `matev2 usage` and the console's
// TOKENS column, re-implemented for the page.
//
// It is a classic script, not a module: it declares three functions on the
// global scope, which is what a page with no build step can load and what
// `internal/dashboard/ui_assets_test.go` can pull into node with `new
// Function(src)` to compare against internal/query's Go originals value for
// value. A divergence here would put a number on the page that the terminal
// disagrees with, which is the one thing a traceable dashboard cannot do.

/**
 * goFixed formats v with `digits` decimals exactly the way Go's
 * fmt.Sprintf("%.Nf", v) does.
 *
 * JavaScript's own toFixed breaks a tie by rounding away from zero, while
 * Go (strconv) breaks it to even. Ties are rare but real - $0.125 is a
 * double with no rounding error at all, and the two languages disagree
 * about it ("0.13" against "0.12"). So the rounding is done here on the
 * decimal expansion rather than left to toFixed.
 */
function goFixed(v, digits) {
  if (!isFinite(v)) return String(v);
  var neg = v < 0;
  var x = Math.abs(v);
  // toFixed is specified to return the closest decimal with that many
  // places, so a long enough expansion shows an exact tie as a 5 followed
  // by nothing but zeroes.
  var s = x.toFixed(Math.min(digits + 18, 100));
  var dot = s.indexOf(".");
  var head = dot < 0 ? s : s.slice(0, dot);
  var frac = dot < 0 ? "" : s.slice(dot + 1);
  while (frac.length < digits + 1) frac += "0";
  var keep = frac.slice(0, digits);
  var rest = frac.slice(digits);
  var first = rest.charCodeAt(0) - 48;
  var up = false;
  if (first > 5) {
    up = true;
  } else if (first === 5) {
    if (/[1-9]/.test(rest.slice(1))) {
      up = true;
    } else {
      var last = digits > 0 ? keep.charCodeAt(digits - 1) - 48 : head.charCodeAt(head.length - 1) - 48;
      up = last % 2 === 1; // half to even, as Go does
    }
  }
  var all = head + keep;
  if (up) all = bumpDigits(all);
  var intLen = all.length - digits;
  var out = all.slice(0, intLen) + (digits > 0 ? "." + all.slice(intLen) : "");
  return (neg ? "-" : "") + out;
}

/** bumpDigits adds one to a string of digits, carrying into a new leading digit. */
function bumpDigits(s) {
  var out = s.split("");
  for (var i = out.length - 1; i >= 0; i--) {
    if (out[i] === "9") {
      out[i] = "0";
    } else {
      out[i] = String(Number(out[i]) + 1);
      return out.join("");
    }
  }
  return "1" + out.join("");
}

/** trimHumanZero is internal/query's own: "%.1f" with a trailing ".0" dropped. */
function trimHumanZero(v) {
  var s = goFixed(v, 1);
  return s.endsWith(".0") ? s.slice(0, -2) : s;
}

/**
 * humanizeTokens is query.HumanizeTokens: exact under 1000, then one
 * decimal with a k/M/B suffix - "523", "96.3k", "1.2M".
 */
function humanizeTokens(n) {
  n = Math.trunc(Number(n) || 0);
  var neg = n < 0;
  if (neg) n = -n;
  var out;
  if (n < 1000) out = String(n);
  else if (n < 1000000) out = trimHumanZero(n / 1000) + "k";
  else if (n < 1000000000) out = trimHumanZero(n / 1000000) + "M";
  else out = trimHumanZero(n / 1000000000) + "B";
  return neg ? "-" + out : out;
}

/**
 * humanizeCost is query.HumanizeCost: cents under $1000, one decimal with a
 * k suffix above it - "$0.12", "$42.00", "$1.2k".
 */
function humanizeCost(v) {
  v = Number(v);
  var neg = v < 0;
  if (neg) v = -v;
  var out = v < 1000 ? "$" + goFixed(v, 2) : "$" + trimHumanZero(v / 1000) + "k";
  return neg ? "-" + out : out;
}

/**
 * humanizeDuration renders a span of milliseconds the way the task page
 * reads it aloud: "840ms", "6.2s", "1m7s", "2h04m", "3d 4h".
 *
 * There is no Go original to match - the console's shortDuration answers a
 * different question, "how long has this been quiet", and rounds a minute
 * and seven seconds down to "1m" because its NOTE column has three cells.
 * A turn's duration is read for its size, so the seconds stay.
 */
function humanizeDuration(ms) {
  ms = Number(ms);
  if (!isFinite(ms)) return "?";
  var neg = ms < 0;
  if (neg) ms = -ms;
  var out;
  if (ms < 1000) {
    out = Math.round(ms) + "ms";
  } else if (ms < 10000) {
    out = trimHumanZero(ms / 1000) + "s";
  } else if (ms < 60000) {
    out = Math.floor(ms / 1000) + "s";
  } else if (ms < 3600000) {
    var m = Math.floor(ms / 60000);
    var s = Math.floor((ms % 60000) / 1000);
    out = s === 0 ? m + "m" : m + "m" + s + "s";
  } else if (ms < 86400000) {
    var h = Math.floor(ms / 3600000);
    var hm = Math.floor((ms % 3600000) / 60000);
    out = hm === 0 ? h + "h" : h + "h" + String(hm).padStart(2, "0") + "m";
  } else {
    var d = Math.floor(ms / 86400000);
    var dh = Math.floor((ms % 86400000) / 3600000);
    out = dh === 0 ? d + "d" : d + "d " + dh + "h";
  }
  return neg ? "-" + out : out;
}
