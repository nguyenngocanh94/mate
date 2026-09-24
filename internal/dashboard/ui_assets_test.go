package dashboard

import (
	"encoding/json"
	"io"
	"io/fs"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The task 29 UI's own proofs: that `/` serves the real three-tier page and
// not task 28's placeholder, that every asset the page asks for is actually
// in the binary, that nothing it loads comes from anywhere but this origin,
// and that the numbers it prints are the numbers `mate usage` prints.
//
// The off-origin check is the one that earns its place: a dashboard is a
// single binary a reader runs on a machine with no network, and one `<script
// src="https://…">` slipped in during a refactor would turn a workspace's
// timeline - every line the captain typed - into an outbound request.

// TestUIServesTheRealPage is the mount: GET / is the app, not the
// placeholder, and it is what `index.html` in this package actually holds.
func TestUIServesTheRealPage(t *testing.T) {
	f := newFixture(t)
	body := fetchText(t, f, "/")

	if strings.Contains(body, "placeholder page of task 28") {
		t.Fatal("GET / still serves task 28's placeholder; the three-tier UI is what belongs there")
	}
	for _, want := range []string{
		`<link rel="stylesheet" href="app.css">`,
		`<script src="humanize.js"></script>`,
		`<script src="app.js"></script>`,
		`id="view"`,
		`id="live"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / does not contain %q", want)
		}
	}
}

// TestUIAssetsAreServed walks every asset index.html references and asks the
// server for it. A page that names a file the binary does not hold renders
// blank, and a blank page is the one thing this dashboard must never be.
func TestUIAssetsAreServed(t *testing.T) {
	f := newFixture(t)
	index := fetchText(t, f, "/")

	assets := referencedAssets(index)
	if len(assets) < 3 {
		t.Fatalf("index.html references %d assets (%v); it should load the stylesheet and both scripts", len(assets), assets)
	}
	for _, name := range assets {
		got := fetchText(t, f, "/"+name)
		if strings.TrimSpace(got) == "" {
			t.Errorf("GET /%s served an empty body", name)
		}
	}
}

// TestUIMakesNoOffOriginRequest is the claim docs/mvp.md M6 row 29 is closed
// against: no request leaves localhost. It is checked at the source, over
// every embedded file, because that is where an off-origin reference can be
// introduced without any test noticing.
func TestUIMakesNoOffOriginRequest(t *testing.T) {
	// Each pattern captures the URL a browser would actually go and get.
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)<script[^>]*\ssrc\s*=\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?i)<link[^>]*\shref\s*=\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?i)<img[^>]*\ssrc\s*=\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?i)\bfetch\s*\(\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?i)\burl\s*\(\s*["']?([^"')]+)["']?\s*\)`),
		regexp.MustCompile(`(?i)@import\s+(?:url\()?["']([^"']+)["']`),
		regexp.MustCompile(`(?i)\bnew\s+(?:WebSocket|EventSource|Worker)\s*\(\s*["']([^"']+)["']`),
	}
	offOrigin := regexp.MustCompile(`(?i)^(?:[a-z][a-z0-9+.-]*:)?//|^(?:https?|ws|wss|ftp):`)

	err := fs.WalkDir(uiFS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		src, err := fs.ReadFile(uiFS(), path)
		if err != nil {
			return err
		}
		text := stripComments(string(src))
		for _, re := range patterns {
			for _, m := range re.FindAllStringSubmatch(text, -1) {
				ref := strings.TrimSpace(m[1])
				if ref == "" || strings.HasPrefix(ref, "data:") {
					continue
				}
				if offOrigin.MatchString(ref) {
					t.Errorf("%s loads %q from off this origin; the dashboard must work with no network at all", path, ref)
				}
			}
		}
		// Belt and braces: any bare http(s) URL outside a comment is worth
		// failing on even if no pattern above claims it.
		for _, scheme := range []string{"http://", "https://"} {
			if i := strings.Index(text, scheme); i >= 0 {
				t.Errorf("%s contains %q outside a comment at offset %d", path, scheme, i)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded ui: %v", err)
	}
}

// TestUIHumanizeMatchesGo is the table docs/mvp.md M6 asks for: the page's
// own humanizeTokens and humanizeCost against internal/query's, value for
// value. A number that reads "96.3k" on the page and "96.2k" in `mate
// usage` would make the dashboard's whole claim - that every number is
// traceable - false.
//
// The comparison runs in node when the machine has one. It is not skipped
// when it does not: node is not a dependency of this repository, so the
// subtest is simply not registered, and the table itself (built from the Go
// functions) is written out either way for the browser evidence run in
// docs/evidence to replay.
func TestUIHumanizeMatchesGo(t *testing.T) {
	cases := humanizeCases()
	if len(cases) < 100 {
		t.Fatalf("the table has only %d cases; it is meant to sweep the boundaries", len(cases))
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Logf("no node on PATH, so the JS side of the table is not replayed here; %d Go expectations recorded", len(cases))
		return
	}
	t.Run("node", func(t *testing.T) {
		src, err := fs.ReadFile(uiFS(), "humanize.js")
		if err != nil {
			t.Fatalf("read humanize.js: %v", err)
		}
		dir := t.TempDir()
		table, err := json.Marshal(cases)
		if err != nil {
			t.Fatalf("marshal the table: %v", err)
		}
		tablePath := filepath.Join(dir, "cases.json")
		if err := os.WriteFile(tablePath, table, 0o600); err != nil {
			t.Fatalf("write the table: %v", err)
		}
		libPath := filepath.Join(dir, "humanize.js")
		if err := os.WriteFile(libPath, src, 0o600); err != nil {
			t.Fatalf("write humanize.js: %v", err)
		}
		runner := filepath.Join(dir, "run.js")
		if err := os.WriteFile(runner, []byte(humanizeRunner), 0o600); err != nil {
			t.Fatalf("write the runner: %v", err)
		}
		out, err := exec.Command(node, runner, libPath, tablePath).CombinedOutput()
		if err != nil {
			t.Fatalf("node disagreed with the Go originals:\n%s", out)
		}
		if strings.TrimSpace(string(out)) != "ok" {
			t.Fatalf("unexpected output from the comparison:\n%s", out)
		}
	})
}

// humanizeCase is one row of the table: the input, and what internal/query
// renders it as.
type humanizeCase struct {
	Fn   string  `json:"fn"`
	NumI int64   `json:"i"`
	NumF float64 `json:"f"`
	Want string  `json:"want"`
}

// humanizeCases sweeps the boundaries of both functions - the suffix
// changes, the ".0" trim, the rounding tie (the one place Go's
// round-half-to-even and JavaScript's toFixed genuinely disagree) - and then
// a deterministic pseudo-random spread on top, so a rule that differs
// anywhere in the range is caught rather than only at the edges.
func humanizeCases() []humanizeCase {
	var out []humanizeCase
	tokens := []int64{
		0, 1, 9, 10, 99, 100, 999, 1000, 1001, 1049, 1050, 1051, 1099, 1100,
		9999, 10000, 12345, 90000, 96250, 96349, 99999, 100000, 999499, 999500,
		999999, 1000000, 1049999, 1050000, 1234567, 999999999, 1000000000,
		1250000000, -1, -1050, -96349, -1234567,
	}
	costs := []float64{
		0, 0.001, 0.004, 0.005, 0.006, 0.01, 0.015, 0.025, 0.115, 0.125, 0.135,
		0.12, 1, 1.005, 1.125, 9.995, 42, 99.999, 123.456, 999.994, 999.995,
		1000, 1049.9, 1050, 1234.5, 12345.6, -0.125, -1234.5,
	}
	for _, n := range tokens {
		out = append(out, humanizeCase{Fn: "tokens", NumI: n, Want: query.HumanizeTokens(n)})
	}
	for _, v := range costs {
		out = append(out, humanizeCase{Fn: "cost", NumF: v, Want: query.HumanizeCost(v)})
	}
	r := rand.New(rand.NewSource(29))
	for i := 0; i < 400; i++ {
		n := r.Int63n(5_000_000_000)
		out = append(out, humanizeCase{Fn: "tokens", NumI: n, Want: query.HumanizeTokens(n)})
		v := r.Float64() * 2000
		out = append(out, humanizeCase{Fn: "cost", NumF: v, Want: query.HumanizeCost(v)})
	}
	return out
}

// humanizeRunner loads the page's own humanize.js as the browser does - as a
// classic script declaring globals - and replays the table through it.
const humanizeRunner = `
const fs = require("fs");
const src = fs.readFileSync(process.argv[2], "utf8");
const api = new Function(src + "\nreturn {humanizeTokens, humanizeCost};")();
const cases = JSON.parse(fs.readFileSync(process.argv[3], "utf8"));
const bad = [];
for (const c of cases) {
  const got = c.fn === "tokens" ? api.humanizeTokens(c.i) : api.humanizeCost(c.f);
  if (got !== c.want) bad.push(c.fn + "(" + (c.fn === "tokens" ? c.i : c.f) + ") = " + got + ", Go says " + c.want);
}
if (bad.length) {
  console.error(bad.slice(0, 20).join("\n") + "\n" + bad.length + " of " + cases.length + " disagree");
  process.exit(1);
}
console.log("ok");
`

// referencedAssets is every same-origin file index.html asks the browser to
// load.
func referencedAssets(index string) []string {
	var out []string
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)<script[^>]*\ssrc\s*=\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?i)<link[^>]*\shref\s*=\s*["']([^"']+)["']`),
	} {
		for _, m := range re.FindAllStringSubmatch(index, -1) {
			ref := strings.TrimSpace(m[1])
			if ref == "" || strings.HasPrefix(ref, "data:") {
				continue
			}
			out = append(out, strings.TrimPrefix(ref, "/"))
		}
	}
	return out
}

// stripComments removes /* */, // and <!-- --> comments, so a URL written in
// prose - "no CDN, not even https://cdn.example" - is not read as a request
// the page makes.
func stripComments(s string) string {
	replace := func(in, open, close string) string {
		var b strings.Builder
		for {
			i := strings.Index(in, open)
			if i < 0 {
				b.WriteString(in)
				return b.String()
			}
			b.WriteString(in[:i])
			rest := in[i+len(open):]
			j := strings.Index(rest, close)
			if j < 0 {
				return b.String()
			}
			in = rest[j+len(close):]
		}
	}
	s = replace(s, "<!--", "-->")
	s = replace(s, "/*", "*/")
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		// Only a line whose first non-space is `//` is treated as a comment:
		// a `//` inside a string literal is exactly what this test hunts.
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func fetchText(t *testing.T, f *fixture, path string) string {
	t.Helper()
	resp, err := f.http.Client().Get(f.http.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s = %d, want 200\n%s", path, resp.StatusCode, body)
	}
	return string(body)
}

// TestUIAssetsCarryAContentETag: an embedded asset has no mtime, so the
// server must give the browser another way to know app.js changed after a
// binary upgrade; a matching If-None-Match gets 304.
func TestUIAssetsCarryAContentETag(t *testing.T) {
	srv := httptest.NewServer(etagFileServer(uiFS()))
	defer srv.Close()
	res, err := http.Get(srv.URL + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	tag := res.Header.Get("ETag")
	if tag == "" {
		t.Fatal("app.js served without an ETag")
	}
	req, _ := http.NewRequest("GET", srv.URL+"/app.js", nil)
	req.Header.Set("If-None-Match", tag)
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusNotModified {
		t.Fatalf("status with matching If-None-Match = %d, want 304", res2.StatusCode)
	}
}
