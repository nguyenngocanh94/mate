package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRulesIgnoresCommentsAndBlankLines(t *testing.T) {
	t.Parallel()
	rules := parseRules("# comment\n\nTestFoo\nTestBar linux\nTestLive*\n")
	if len(rules) != 3 {
		t.Fatalf("rules = %#v, want 3", rules)
	}
	if rules[0] != (skipRule{pattern: "TestFoo"}) {
		t.Fatalf("rules[0] = %#v", rules[0])
	}
	if rules[1] != (skipRule{pattern: "TestBar", goos: "linux"}) {
		t.Fatalf("rules[1] = %#v", rules[1])
	}
	if rules[2] != (skipRule{pattern: "TestLive*"}) {
		t.Fatalf("rules[2] = %#v", rules[2])
	}
}

func TestSkipExpectedExactPrefixAndGOOS(t *testing.T) {
	t.Parallel()
	rules := []skipRule{
		{pattern: "TestCrashHelperProcess"},
		{pattern: "TestLiveAttach*"},
		{pattern: "TestCreatedLayoutDirsKeepInheritedSetgid", goos: "darwin"},
	}
	cases := []struct {
		test, goos string
		want       bool
	}{
		{"TestCrashHelperProcess", "linux", true},
		{"TestLiveAttachAgentPaneAndDetachLeavesTheAgentRunning", "linux", true},
		{"TestLiveAttach", "darwin", true},
		{"TestCreatedLayoutDirsKeepInheritedSetgid", "darwin", true},
		{"TestCreatedLayoutDirsKeepInheritedSetgid", "linux", false},
		{"TestTighteningPreservesSetgidOnEveryPath", "darwin", false},
		{"TestHerdrLiveSessionWorkspaceTabStart", "linux", false},
	}
	for _, tc := range cases {
		if got := skipExpected(tc.test, tc.goos, rules); got != tc.want {
			t.Fatalf("skipExpected(%q, %q) = %v, want %v", tc.test, tc.goos, got, tc.want)
		}
	}
}

func TestEmbeddedAllowlistDeclaresKnownSkips(t *testing.T) {
	t.Parallel()
	rules := parseRules(expectedSkipsFile)
	if !skipExpected("TestLiveHerdrSessionWorkspaceTabStart", "linux", rules) {
		t.Fatal("live Herdr tests skip without MATE_LIVE=1")
	}
	if !skipExpected("TestLiveSpawnCrewCodex", "darwin", rules) {
		t.Fatal("live harness tests skip without MATE_LIVE=1")
	}
	if skipExpected("TestStoreAppendStatusRefusesSymlinkEscape", "darwin", rules) {
		t.Fatal("ordinary unit tests must never be allowlisted")
	}
}

func TestRunFailsOnUnexpectedSkipAndPrintsSummary(t *testing.T) {
	t.Parallel()
	in := strings.NewReader(strings.Join([]string{
		`{"Action":"run","Package":"example.com/p","Test":"TestSecret"}`,
		`{"Action":"output","Package":"example.com/p","Test":"TestSecret","Output":"    secret_test.go:3: filesystem dropped the bit\n"}`,
		`{"Action":"skip","Package":"example.com/p","Test":"TestSecret","Elapsed":0.01}`,
		`{"Action":"pass","Package":"example.com/p"}`,
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	code := run(in, &stdout, &stderr, "darwin", "TestLive*\n", "")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 for an undeclared skip", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "--- SKIP: example.com/p TestSecret") {
		t.Fatalf("skip must be visible as it happens:\n%s", out)
	}
	if !strings.Contains(out, "=== SKIP SUMMARY (1) ===") || !strings.Contains(out, "UNEXPECTED") {
		t.Fatalf("summary must call out the unexpected skip:\n%s", out)
	}
	if !strings.Contains(stderr.String(), "UNEXPECTED SKIP: example.com/p TestSecret") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunAllowsDeclaredSkipAndStillFailsOnTestFailure(t *testing.T) {
	t.Parallel()
	in := strings.NewReader(strings.Join([]string{
		`{"Action":"run","Package":"example.com/p","Test":"TestLiveAttachX"}`,
		`{"Action":"output","Package":"example.com/p","Test":"TestLiveAttachX","Output":"    live_test.go:1: set MATE_LIVE\n"}`,
		`{"Action":"skip","Package":"example.com/p","Test":"TestLiveAttachX","Elapsed":0}`,
		`{"Action":"run","Package":"example.com/p","Test":"TestBoom"}`,
		`{"Action":"output","Package":"example.com/p","Test":"TestBoom","Output":"    boom_test.go:1: exploded\n"}`,
		`{"Action":"fail","Package":"example.com/p","Test":"TestBoom","Elapsed":0.2}`,
		`{"Action":"fail","Package":"example.com/p"}`,
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	code := run(in, &stdout, &stderr, "linux", "TestLiveAttach*\n", "")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 because a test failed", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "[expected]: example.com/p TestLiveAttachX") {
		t.Fatalf("declared skip should be labelled expected:\n%s", out)
	}
	if !strings.Contains(out, "--- FAIL: example.com/p TestBoom") {
		t.Fatalf("failure must be visible:\n%s", out)
	}
	if strings.Contains(stderr.String(), "UNEXPECTED SKIP") {
		t.Fatalf("declared skip must not be unexpected:\n%s", stderr.String())
	}
}

func TestSkipReasonDropsGoTestBanners(t *testing.T) {
	t.Parallel()
	in := "=== RUN   TestFoo\n=== PAUSE TestFoo\n=== CONT  TestFoo\n    foo_test.go:3: reason\n--- SKIP: TestFoo (0.00s)\n"
	got := skipReason(in)
	if got != "    foo_test.go:3: reason" {
		t.Fatalf("skipReason = %q", got)
	}
}

func TestRunEmptyStdinIsAFailure(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run(strings.NewReader(""), &stdout, &stderr, "linux", "", "")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no go test JSON events") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunPrintsPackagesWithNoTests(t *testing.T) {
	t.Parallel()
	in := strings.NewReader(strings.Join([]string{
		`{"Action":"output","Package":"example.com/empty","Output":"?   \texample.com/empty\t[no test files]\n"}`,
		`{"Action":"skip","Package":"example.com/empty"}`,
		`{"Action":"pass","Package":"example.com/p"}`,
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	code := run(in, &stdout, &stderr, "linux", "", "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "example.com/empty\t[no test files]") {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "UNEXPECTED") {
		t.Fatalf("a package with no tests is not a test skip:\n%s", stdout.String())
	}
}

func TestRunForwardsCompilerErrorsNotJustBuildFailedBanner(t *testing.T) {
	t.Parallel()
	in := strings.NewReader(strings.Join([]string{
		`{"Action":"output","Package":"example.com/p","Output":"# example.com/p\n"}`,
		`{"Action":"output","Package":"example.com/p","Output":"./broken.go:3:2: undefined: Foo\n"}`,
		`{"Action":"output","Package":"example.com/p","Output":"FAIL\texample.com/p [build failed]\n"}`,
		`{"Action":"fail","Package":"example.com/p"}`,
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	code := run(in, &stdout, &stderr, "linux", "", "")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "undefined: Foo") {
		t.Fatalf("compiler error must not be dropped:\n%s", out)
	}
	if !strings.Contains(out, "[build failed]") {
		t.Fatalf("build-failed banner missing:\n%s", out)
	}
}

func TestRunSucceedsWhenOnlyExpectedSkips(t *testing.T) {
	t.Parallel()
	in := strings.NewReader(strings.Join([]string{
		`{"Action":"skip","Package":"example.com/p","Test":"TestCrashHelperProcess","Elapsed":0}`,
		`{"Action":"pass","Package":"example.com/p"}`,
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	code := run(in, &stdout, &stderr, "linux", "TestCrashHelperProcess\n", "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "=== SKIP SUMMARY (1) ===") {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "=== NOT PROVEN: 1 expected skip(s) ===") {
		t.Fatalf("expected skips must not read as an unqualified pass:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "A green result is not evidence that skipped behaviour works.") {
		t.Fatalf("NOT PROVEN banner must say a green result is not evidence:\n%s", stdout.String())
	}
}

func TestRunOmitsNotProvenBannerWhenNothingSkipped(t *testing.T) {
	t.Parallel()
	in := strings.NewReader(`{"Action":"pass","Package":"example.com/p"}` + "\n")
	var stdout, stderr bytes.Buffer
	code := run(in, &stdout, &stderr, "linux", "TestLive*\n", "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "NOT PROVEN") {
		t.Fatalf("no skips means nothing is unproven:\n%s", stdout.String())
	}
}

func TestSkipReportFileNamesExpectedSkips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skip.md")
	in := strings.NewReader(strings.Join([]string{
		`{"Action":"output","Package":"example.com/p","Test":"TestLiveAttachX","Output":"    live_test.go:1: set MATE_LIVE\n"}`,
		`{"Action":"skip","Package":"example.com/p","Test":"TestLiveAttachX","Elapsed":0}`,
		`{"Action":"pass","Package":"example.com/p"}`,
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	code := run(in, &stdout, &stderr, "linux", "TestLiveAttach*\n", path)
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "## NOT PROVEN: 1 expected skip(s)") {
		t.Fatalf("report missing heading:\n%s", got)
	}
	if !strings.Contains(got, "`example.com/p TestLiveAttachX`") {
		t.Fatalf("report must name the skipped test:\n%s", got)
	}
	if !strings.Contains(got, "set MATE_LIVE") {
		t.Fatalf("report must carry the skip reason:\n%s", got)
	}
}
