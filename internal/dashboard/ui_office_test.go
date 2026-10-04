package dashboard

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// Mate Office (docs/dashboard.md section 11) is a second UI beside the
// admin one, under ui/office/ and served at /office/. These are its proofs
// at the Go end: it is in the binary, everything it references is served,
// the fonts it embeds ship with their licence, its one API addition repeats
// v_now, and its behaviour tests run with `go test`. The off-origin rule is
// TestUIMakesNoOffOriginRequest's, which walks every embedded file and so
// covers ui/office/ too.

func TestOfficeIsServedFromTheBinary(t *testing.T) {
	f := newFixture(t)
	body := fetchText(t, f, "/office/")
	for _, want := range []string{
		`<title>Mate Office</title>`,
		`<link rel="stylesheet" href="office.css">`,
		`<script src="../humanize.js"></script>`,
		`<script src="office.js"></script>`,
		`id="office"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /office/ does not contain %q", want)
		}
	}
	want, err := fs.ReadFile(uiFS(), "office/index.html")
	if err != nil {
		t.Fatalf("office/index.html is not embedded: %v", err)
	}
	if body != string(want) {
		t.Error("GET /office/ is not the embedded office/index.html")
	}

	// /office without the slash lands on the page rather than a 404, so the
	// address can be typed either way.
	client := *f.http.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Get(f.http.URL + "/office")
	if err != nil {
		t.Fatalf("GET /office: %v", err)
	}
	_ = resp.Body.Close()
	// The Location may be relative ("office/"); what matters is where a
	// browser lands once it resolves it against /office.
	loc, _ := resp.Request.URL.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusMovedPermanently || loc == nil || loc.Path != "/office/" {
		t.Errorf("GET /office = %d to %q, want a redirect to /office/", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// TestOfficeAssetsAreServed follows every reference the page makes - the
// scripts and stylesheet in index.html, the fonts in office.css - from the
// URL a browser would resolve it against, and asks the server for it.
func TestOfficeAssetsAreServed(t *testing.T) {
	f := newFixture(t)
	base, _ := url.Parse(f.http.URL + "/office/")
	index := fetchText(t, f, "/office/")
	refs := referencedAssets(index)
	if len(refs) < 3 {
		t.Fatalf("office/index.html references %v; it should load its stylesheet, humanize.js and office.js", refs)
	}
	var css string
	for _, ref := range refs {
		u := base.ResolveReference(&url.URL{Path: ref})
		got := fetchText(t, f, u.Path)
		if strings.TrimSpace(got) == "" {
			t.Errorf("GET %s (from %q) served an empty body", u.Path, ref)
		}
		if strings.HasSuffix(u.Path, ".css") {
			css = got
		}
	}
	if !strings.Contains(strings.Join(refs, " "), "humanize.js") {
		t.Error("the office must share the admin UI's humanize.js rather than format numbers a second way")
	}

	fonts := regexp.MustCompile(`url\(\s*["']?([^"')]+)["']?\s*\)`).FindAllStringSubmatch(stripComments(css), -1)
	if len(fonts) < 3 {
		t.Fatalf("office.css references %d urls; the design's three typefaces should be embedded", len(fonts))
	}
	cssURL := base.ResolveReference(&url.URL{Path: "office.css"})
	for _, m := range fonts {
		u := cssURL.ResolveReference(&url.URL{Path: m[1]})
		resp, err := f.http.Client().Get(f.http.URL + u.Path)
		if err != nil {
			t.Fatalf("GET %s: %v", u.Path, err)
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d; office.css names a file the binary does not hold", u.Path, resp.StatusCode)
			continue
		}
		if !bytes.HasPrefix(data, []byte("wOF2")) {
			t.Errorf("%s is not a WOFF2 font", u.Path)
		}
	}
}

// TestOfficeFontsShipWithTheirLicence: every typeface under ui/office/fonts
// is OFL-licensed, and the OFL's one condition on shipping a font inside a
// program is that its licence ships too.
func TestOfficeFontsShipWithTheirLicence(t *testing.T) {
	f := newFixture(t)
	entries, err := fs.ReadDir(uiFS(), "office/fonts")
	if err != nil {
		t.Fatalf("read office/fonts: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no fonts embedded under office/fonts")
	}
	for _, e := range entries {
		family := strings.TrimSuffix(e.Name(), "-latin.woff2")
		if family == e.Name() {
			t.Errorf("%s: expected <Family>-latin.woff2", e.Name())
			continue
		}
		text := fetchText(t, f, "/office/fonts/licenses/"+family+"-OFL.txt")
		if !strings.Contains(text, "SIL Open Font License, Version 1.1") {
			t.Errorf("the licence served for %s is not the OFL 1.1", family)
		}
	}
}

func TestOfficeIndexCarriesAnETag(t *testing.T) {
	f := newFixture(t)
	resp, err := f.http.Client().Get(f.http.URL + "/office/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	tag := resp.Header.Get("ETag")
	if tag == "" {
		t.Fatal("/office/ served without an ETag; a browser would keep a stale page across an upgrade")
	}
	req, _ := http.NewRequest("GET", f.http.URL+"/office/", nil)
	req.Header.Set("If-None-Match", tag)
	again, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Body.Close()
	if again.StatusCode != http.StatusNotModified {
		t.Errorf("matching If-None-Match = %d, want 304", again.StatusCode)
	}
}

// TestNowRepeatsVNow is /api/now, the one endpoint Mate Office added: the
// whole scene in one call, every row equal to v_now's and to the slice
// /api/events hands out for the same actors.
func TestNowRepeatsVNow(t *testing.T) {
	f := newFixture(t)
	var got NowResponse
	f.get(t, "/api/now", http.StatusOK, &got)
	if got.LastEventID != f.lastEventID(t) || got.GeneratedAt == "" {
		t.Errorf("envelope = %+v, want last_event_id %d", got.envelope, f.lastEventID(t))
	}
	var count int
	if err := f.read.SQL().QueryRow(`SELECT COUNT(*) FROM v_now WHERE project = ?`, fixtureProject).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if len(got.Now) != count {
		t.Fatalf("/api/now has %d rows, v_now has %d", len(got.Now), count)
	}
	byID := map[string]SceneRow{}
	for i, row := range got.Now {
		if i > 0 && got.Now[i-1].ActorID >= row.ActorID {
			t.Errorf("rows are not sorted by actor id: %s before %s", got.Now[i-1].ActorID, row.ActorID)
		}
		byID[row.ActorID] = row
	}
	for _, id := range []string{timeline.MateActorID(fixtureProject), timeline.CrewActorID(fixtureProject, fixtureCrew)} {
		row, ok := byID[id]
		if !ok {
			t.Fatalf("/api/now has no row for %s", id)
		}
		state, since, tokens := f.nowRow(t, id)
		if row.State != state || row.Since != since || row.TokensToday != tokens {
			t.Errorf("%s = %q/%q/%d, v_now says %q/%q/%d", id, row.State, row.Since, row.TokensToday, state, since, tokens)
		}
	}

	var events EventsResponse
	f.get(t, "/api/events?since=0&project="+fixtureProject, http.StatusOK, &events)
	if len(events.Now) == 0 {
		t.Fatal("the fixture has moved actors; /api/events?since=0 should carry their rows")
	}
	for _, moved := range events.Now {
		if !reflect.DeepEqual(byID[moved.ActorID], moved) {
			t.Errorf("%s: /api/now has %+v, /api/events has %+v", moved.ActorID, byID[moved.ActorID], moved)
		}
	}

	var one NowResponse
	f.get(t, "/api/now?project="+fixtureProject, http.StatusOK, &one)
	if one.Project != fixtureProject || len(one.Now) != len(got.Now) {
		t.Errorf("?project=%s gave %d rows for %q, want %d", fixtureProject, len(one.Now), one.Project, len(got.Now))
	}
	var missing ErrorResponse
	f.get(t, "/api/now?project=nosuch", http.StatusNotFound, &missing)
	if !strings.Contains(missing.Error, "nosuch") {
		t.Errorf("404 reason %q does not name the missing project", missing.Error)
	}

	resp, err := f.http.Client().Post(f.http.URL+"/api/now", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("POST /api/now was accepted; this API has no write endpoint")
	}
}

// TestOfficeBehaviour runs ui_office_test.cjs - the drawers, the stuck
// crew, unknowns, untrusted text, GET-only - against office.js in node.
// Like TestUIHumanizeMatchesGo, it is not skipped on a machine without
// node: node is not a dependency of this repository, so the subtest is
// simply not registered there.
func TestOfficeBehaviour(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Log("no node on PATH, so ui_office_test.cjs is not run here")
		return
	}
	t.Run("node", func(t *testing.T) {
		cmd := exec.Command(node, "--test", "--test-timeout=30000", path.Join(".", "ui_office_test.cjs"))
		// The fixture's dates are read as local days ("Sep 29"); pin the
		// zone so the run does not depend on where the machine is.
		cmd.Env = append(os.Environ(), "TZ=UTC")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ui_office_test.cjs failed:\n%s", out)
		}
		if !strings.Contains(string(out), "fail 0") {
			t.Fatalf("unexpected node output:\n%s", out)
		}
	})
}
