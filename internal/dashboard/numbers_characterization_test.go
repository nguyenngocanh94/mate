package dashboard

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFixtureNumbersCharacterization pins what the dashboard serves for the
// fixture corpus, byte for byte. It was recorded before the transcript
// capability moved behind the harness registry
// (docs/plans/harness-registry-2026-09-30.md, PR 5): that change must leave
// every number here, and `mate usage`'s on the same corpus
// (cmd/mate/usage_characterization_test.go), as it found them.
func TestFixtureNumbersCharacterization(t *testing.T) {
	f := newFixture(t)
	// generated_at is the wall clock of the request, not a number the
	// ingest produced.
	generated := regexp.MustCompile(`"generated_at":"[^"]*"`)
	var out bytes.Buffer
	for _, path := range []string{
		"/api/workspace",
		"/api/projects/" + fixtureProject,
		"/api/projects/" + fixtureProject + "/mate",
		"/api/projects/" + fixtureProject + "/tasks/" + fixtureCrew,
	} {
		resp, err := f.http.Client().Get(f.http.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, %v\n%s", path, resp.StatusCode, err, body)
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, generated.ReplaceAll(body, []byte(`"generated_at":"-"`)), "", "  "); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		out.WriteString("== " + path + "\n")
		out.Write(pretty.Bytes())
		out.WriteString("\n")
	}
	testdata, err := filepath.Abs(filepath.Join("..", "timeline", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	root := f.ws.Root()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.NewReplacer(testdata, "{{TESTDATA}}", resolved, "{{ROOT}}", root, "{{ROOT}}").Replace(out.String())

	golden := filepath.Join("testdata", "fixture-numbers.golden")
	if os.Getenv("MATE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (re-run with MATE_UPDATE_GOLDEN=1 to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("the dashboard's numbers for the fixture corpus changed (%s):\n%s", golden, got)
	}
}
