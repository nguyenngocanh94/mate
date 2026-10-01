package codexlab

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness/codex"
)

// recorder runs Home's cleanups on demand and keeps what they report, so the
// leak check can be seen failing without failing this test.
type recorder struct {
	testing.TB
	cleanups []func()
	errs     []string
}

func (r *recorder) Cleanup(f func()) { r.cleanups = append(r.cleanups, f) }
func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
func (r *recorder) runCleanups() {
	for i := len(r.cleanups) - 1; i >= 0; i-- {
		r.cleanups[i]()
	}
}

// operatorHome stands in for the operator's ~/.codex: a login, a config
// with trust entries, and hooks the lab must never read.
func operatorHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		AuthFile:      `{"tokens":"operator"}`,
		"config.toml": "model = \"operator-model\"\n[projects.\"/Users/op/work\"]\ntrust_level = \"trusted\"\n",
		"hooks.json":  `{"hooks":{}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(codex.CodexHomeEnv, dir)
	return dir
}

func TestHomeCopiesOnlyTheLoginAndPointsCodexAtIt(t *testing.T) {
	operator := operatorHome(t)
	r := &recorder{TB: t}
	home := Home(r)
	defer r.runCleanups()

	if home == operator || os.Getenv(codex.CodexHomeEnv) != home {
		t.Fatalf("CODEX_HOME = %q after Home, want the new lab home %q (operator %q)", os.Getenv(codex.CodexHomeEnv), home, operator)
	}
	info, err := os.Lstat(filepath.Join(home, AuthFile))
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("lab %s: %v, mode %v; want a private copy, never a link", AuthFile, err, info)
	}
	if data, _ := os.ReadFile(filepath.Join(home, AuthFile)); string(data) != `{"tokens":"operator"}` {
		t.Fatalf("lab %s = %q", AuthFile, data)
	}
	if data, _ := os.ReadFile(filepath.Join(home, "config.toml")); string(data) != Config {
		t.Fatalf("lab config.toml = %q, want only %q: nothing of the operator's config is read into it", data, Config)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("the operator's hooks.json reached the lab home: %v", err)
	}
	r.runCleanups()
	r.cleanups = nil
	if len(r.errs) != 0 {
		t.Fatalf("an untouched operator config was reported as leaked: %v", r.errs)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("the lab home outlived the test: %v", err)
	}
}

// A trust entry for a temp directory that appears in the operator's config
// while the test runs is a Codex launch that escaped the lab, and fails the
// test; an entry the operator already had does not.
func TestHomeFailsTheTestWhenTheOperatorsConfigGainsATempPath(t *testing.T) {
	operator := operatorHome(t)
	r := &recorder{TB: t}
	Home(r)
	leak := fmt.Sprintf("[projects.%q]", filepath.Join(os.TempDir(), "TestLiveSomething", "worktree"))
	f, err := os.OpenFile(filepath.Join(operator, "config.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "%s\ntrust_level = \"trusted\"\n[projects.\"/Users/op/other\"]\n", leak)
	f.Close()
	r.runCleanups()
	if len(r.errs) != 1 || !strings.Contains(r.errs[0], leak) || strings.Contains(r.errs[0], "/Users/op/other") {
		t.Fatalf("leak report = %q, want exactly the temp-path line %q", r.errs, leak)
	}
}

func TestHomeRefusesASecondLabInOneTest(t *testing.T) {
	operatorHome(t)
	first := &recorder{TB: t}
	Home(first)
	defer first.runCleanups()
	var fatal string
	second := &fatalRecorder{recorder: recorder{TB: t}, fatal: &fatal}
	func() {
		defer func() { _ = recover() }()
		Home(second)
	}()
	if !strings.Contains(fatal, "already a lab home") {
		t.Fatalf("a second Home was not refused: %q", fatal)
	}
}

type fatalRecorder struct {
	recorder
	fatal *string
}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	*r.fatal = fmt.Sprintf(format, args...)
	panic("fatal")
}
