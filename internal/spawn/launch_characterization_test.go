package spawn_test

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The launch characterization pins, byte for byte, what a Mate start and a
// Crew spawn hand to Herdr and write to disk, for both harnesses: the Herdr
// argv, every field of the harness LaunchSpec (env set, env unset, delivery,
// profile), the pane environment, the first prompt, and every file the
// launch leaves anywhere the test can see. It is the safety net for the
// harness registry plan (docs/plans/harness-registry-2026-09-30.md, section
// 6, PRs 1-6: "argv và file sinh ra giống từng byte"). A diff here is a
// behaviour change; rerun with -update only when that change is intended,
// and read the golden diff before committing it.
var updateLaunch = flag.Bool("update", false, "rewrite testdata/launch/*.golden")

// launchFixture is one isolated machine: every directory the launch could
// read or write is the test's own, so the golden carries placeholders and
// never a temp path or the operator's home.
type launchFixture struct {
	w    *store.Workspace
	rt   *runtime.Fake
	rec  *promptRecorder
	deps spawn.Deps
	// roots are the directories a launch could write to.
	roots []string
	// places are replaced by their names wherever they appear; sizes are
	// replaced afterwards, in the already-normalized text.
	places []placeholder
	sizes  []placeholder
	before map[string]fileState
}

type placeholder struct{ path, name string }

func newLaunchFixture(t *testing.T, crew bool, env map[string]string) *launchFixture {
	t.Helper()
	home := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(config.EnvCodexHome, codexHome)
	t.Setenv(config.EnvClaudeConfigDir, "")
	t.Setenv(config.EnvLive, "")
	for k, v := range env {
		t.Setenv(k, v)
	}
	var w *store.Workspace
	if crew {
		w = crewWorkspace(t, "shop")
	} else {
		w = newWorkspace(t, "shop")
	}
	rt := runtime.NewFake()
	rec := &promptRecorder{Fake: rt}
	deps := fakeDeps(t, rt)
	deps.Runtime = rec
	deps.CodexSessionsDir = t.TempDir()
	f := &launchFixture{w: w, rt: rt, rec: rec, deps: deps}
	f.places = []placeholder{
		{w.Root(), "{{WORKSPACE}}"},
		{home, "{{HOME}}"},
		{codexHome, "{{CODEX_HOME}}"},
		{deps.ConfigHome, "{{CONFIG_HOME}}"},
		{filepath.Dir(deps.Binary), "{{BIN_DIR}}"},
		{deps.CodexSessionsDir, "{{CODEX_SESSIONS}}"},
		// The Herdr session name is a hash of the workspace root.
		{w.Session(), "{{HERDR_SESSION}}"},
	}
	f.roots = []string{w.Root(), home, codexHome, deps.ConfigHome, filepath.Dir(deps.Binary)}
	for k, v := range env {
		if filepath.IsAbs(v) {
			f.places = append(f.places, placeholder{v, "{{" + k + "}}"})
			f.roots = append(f.roots, v)
		}
	}
	return f
}

// normalize replaces every fixture directory, in both its given and its
// symlink-resolved spelling, with its placeholder. Longest first, so a
// directory nested in another keeps its own name.
func (f *launchFixture) normalize(s string) string {
	type pair struct{ old, new string }
	var pairs []pair
	for _, p := range f.places {
		pairs = append(pairs, pair{p.path, p.name})
		if r, err := filepath.EvalSymlinks(p.path); err == nil && r != p.path {
			pairs = append(pairs, pair{r, p.name})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].old) > len(pairs[j].old) })
	args := make([]string, 0, 2*len(pairs))
	for _, p := range pairs {
		args = append(args, p.old, p.new)
	}
	s = strings.NewReplacer(args...).Replace(s)
	for _, p := range f.sizes {
		s = strings.ReplaceAll(s, p.path, p.name)
	}
	return s
}

// pinDigests names the sha256 of every file that carries a fixture path,
// and its size where a record lists the two together, so a record that
// digests such a file (the Crew harness profile digests the brief, which
// carries absolute paths) reads "{{sha256 of <file>}}" and stays stable
// while still proving the digest is of that file's exact bytes. Identical
// files share the first name in path order.
func (f *launchFixture) pinDigests(files map[string]fileState) {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	seen := map[string]bool{}
	for _, path := range paths {
		raw := files[path].data
		sum := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
		if seen[sum] || f.normalize(raw) == raw {
			// A file with no fixture path in it digests the same on every
			// machine, so its digest stays literal.
			continue
		}
		seen[sum] = true
		name := f.normalize(path)
		f.places = append(f.places, placeholder{sum, "{{sha256 of " + name + "}}"})
		f.sizes = append(f.sizes, placeholder{
			fmt.Sprintf("\"sha256\": \"{{sha256 of %s}}\",\n      \"bytes\": %d,", name, len(raw)),
			fmt.Sprintf("\"sha256\": \"{{sha256 of %s}}\",\n      \"bytes\": {{bytes of %s}},", name, name),
		})
	}
}

// pinHead names the repo's HEAD commit, whose hash depends on the clock,
// like a path.
func (f *launchFixture) pinHead(t *testing.T) {
	t.Helper()
	head := strings.TrimSpace(git(t, f.w.RepoDir("shop"), "rev-parse", "HEAD"))
	f.places = append(f.places, placeholder{head, "{{HEAD}}"})
}

// snapshot renders the last launch and every file the launches since
// f.before created, changed or removed under the fixture's directories.
func (f *launchFixture) snapshot(t *testing.T, pane string) string {
	t.Helper()
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	if len(f.rt.StartArgv) == 0 || len(f.rt.StartLaunches) != len(f.rt.StartArgv) {
		t.Fatalf("starts recorded: argv %d, launches %d", len(f.rt.StartArgv), len(f.rt.StartLaunches))
	}
	line("starts: %d", len(f.rt.StartArgv))
	line("")
	line("== herdr argv (last start) ==")
	for _, a := range f.rt.StartArgv[len(f.rt.StartArgv)-1] {
		line("%q", a)
	}
	launch := f.rt.StartLaunches[len(f.rt.StartLaunches)-1]
	line("")
	line("== launch spec ==")
	line("kind: %s", launch.Kind())
	line("cwd: %s", launch.Cwd())
	line("delivery: %s", launch.Delivery())
	line("context_path: %s", launch.ContextPath())
	line("context_required: %v", launch.ContextRequired())
	for _, cf := range launch.ContextFiles() {
		line("context_file: %s role=%s", cf.Path, cf.Role)
	}
	line("max_inline_bytes: %d", launch.MaxInlineBytes())
	line("max_file_bytes: %d", launch.MaxFileBytes())
	line("model: %s", launch.Model())
	line("effort: %s", launch.Effort())
	line("effort_omitted: %v", launch.EffortOmitted())
	line("codex_home: %s", launch.CodexHome())
	line("claude_config_dir: %s", launch.ClaudeConfigDir())
	line("task_prompt: %q", launch.TaskPrompt())
	line("")
	line("== launch args ==")
	for _, a := range launch.Args() {
		line("%q", a)
	}
	line("")
	line("== launch env (set) ==")
	for _, v := range launch.Env() {
		line("%s=%s", v.Key, v.Value)
	}
	line("")
	line("== launch env (exported into the pane before start) ==")
	for _, v := range f.rt.StartEnv[pane] {
		line("%s=%s", v.Key, v.Value)
	}
	line("")
	line("== launch env (unset) ==")
	for _, k := range launch.UnsetEnv() {
		line("%s", k)
	}
	line("")
	line("== pane env (tab create) ==")
	tab, ok := f.rt.Tabs[pane]
	if !ok {
		t.Fatalf("no live pane %s", pane)
	}
	line("label: %s", tab.Label)
	line("cwd: %s", tab.Cwd)
	for _, v := range tab.Env {
		line("%s=%s", v.Key, v.Value)
	}
	line("")
	line("== prompts ==")
	for _, p := range f.rec.prompts {
		line("%q", p)
	}
	line("")
	line("== notes ==")
	for _, n := range launch.Notes() {
		line("- %s", n)
	}
	line("")
	line("== files the launch created or changed ==")
	after := f.collect(t)
	f.pinDigests(after)
	var paths []string
	for path := range after {
		paths = append(paths, path)
	}
	for path := range f.before {
		if _, ok := after[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		was, existed := f.before[path]
		now, exists := after[path]
		switch {
		case !exists:
			line("")
			line("--- removed %s ---", path)
		case existed && was == now:
			continue
		default:
			f.writeFile(&b, path, now, was, existed)
		}
	}
	return f.normalize(b.String())
}

// fileState is one file as the snapshot sees it.
type fileState struct {
	mode os.FileMode
	data string
}

// collect reads every regular file under the fixture's directories. Git's
// own directories are skipped except the one file mate writes into them,
// info/exclude. The Codex sessions directory holds only what the test
// itself wrote to model a rollout, so it is not a launch output.
func (f *launchFixture) collect(t *testing.T) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	for _, root := range f.roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Name() == ".git" {
				if d.IsDir() {
					exclude := filepath.Join(path, "info", "exclude")
					if data, err := os.ReadFile(exclude); err == nil {
						out[exclude] = fileState{mode: 0o644, data: string(data)}
					}
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = fileState{mode: info.Mode().Perm(), data: string(data)}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return out
}

// renderedAsset reports a file whose bytes come from an embedded mateassets
// template - the operating manual, its Codex copy, the Mate's skills and a
// Crew's brief. Those texts have their own goldens in internal/mateassets;
// here they are pinned by sha256, so a template edit shows as a one-line
// hash change while where the file lands, under which name, stays readable.
func renderedAsset(path string) bool {
	base := filepath.Base(path)
	return base == "AGENTS.md" || base == "AGENTS.override.md" || base == "brief.md" ||
		strings.Contains(filepath.ToSlash(path), "/skills/")
}

// writeFile renders one created or changed file. A file the launch only
// appended to (info/exclude) shows the appended bytes alone, so the golden
// does not depend on the template text of the installed version control.
func (f *launchFixture) writeFile(b *strings.Builder, path string, st, was fileState, existed bool) {
	verb := "created"
	content := f.normalize(st.data)
	if existed {
		verb = "changed"
		if strings.HasPrefix(st.data, was.data) && st.mode == was.mode {
			verb = "appended to"
			content = f.normalize(strings.TrimPrefix(st.data, was.data))
		}
	}
	fmt.Fprintf(b, "\n--- %s %s mode=%v bytes=%d ---\n", verb, path, st.mode, len(content))
	switch {
	case content == "":
		b.WriteString("(empty)\n")
	case !utf8.ValidString(content) || strings.ContainsRune(content, 0):
		fmt.Fprintf(b, "(binary) sha256=%x\n", sha256.Sum256([]byte(content)))
	case renderedAsset(path):
		fmt.Fprintf(b, "(rendered asset) sha256=%x\n", sha256.Sum256([]byte(content)))
	default:
		b.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			b.WriteString("\n(no trailing newline)\n")
		}
	}
	fmt.Fprintf(b, "--- end %s ---\n", path)
}

func checkLaunchGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "launch", name+".golden")
	if *updateLaunch {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run go test ./internal/spawn -run %s -update to create it)", path, err, t.Name())
	}
	if string(want) != got {
		t.Errorf("launch characterization %s changed; a diff here is a behaviour change.\n"+
			"Rerun with -update only if it is intended, and review the golden diff.\n%s",
			name, firstDiff(string(want), got))
	}
}

// firstDiff shows the first differing line with a little context.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl || i >= len(w) || i >= len(g) {
			return fmt.Sprintf("line %d:\n  want: %q\n  got:  %q", i+1, wl, gl)
		}
	}
	return "(no line differs)"
}

type mateCase struct {
	name   string
	kind   harness.Kind
	mate   store.MateConfig
	env    map[string]string
	resume bool
}

func TestLaunchCharacterizationMate(t *testing.T) {
	cases := []mateCase{
		// The Mate profile defaults (opus, medium) are filled by the Claude
		// adapter when the project says nothing.
		{name: "mate-claude-fresh-default", kind: harness.KindClaude},
		{name: "mate-claude-fresh-model-effort", kind: harness.KindClaude, mate: store.MateConfig{Model: "sonnet", Effort: "high"}},
		{name: "mate-claude-fresh-effort-only", kind: harness.KindClaude, mate: store.MateConfig{Effort: "max"}},
		// CLAUDE_CONFIG_DIR set: the launch assigns it instead of unsetting it.
		{name: "mate-claude-fresh-config-dir", kind: harness.KindClaude, env: map[string]string{config.EnvClaudeConfigDir: "dir"}},
		{name: "mate-claude-resume", kind: harness.KindClaude, resume: true},
		{name: "mate-codex-fresh-default", kind: harness.KindCodex},
		{name: "mate-codex-fresh-model-effort", kind: harness.KindCodex, mate: store.MateConfig{Model: "gpt-5.5", Effort: "high"}},
		// Codex does not take max: recorded, left out of the argv.
		{name: "mate-codex-fresh-effort-omitted", kind: harness.KindCodex, mate: store.MateConfig{Effort: "max"}},
		{name: "mate-codex-resume", kind: harness.KindCodex, resume: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range tc.env {
				if v == "dir" {
					v = t.TempDir()
				}
				env[k] = v
			}
			f := newLaunchFixture(t, false, env)
			if tc.mate != (store.MateConfig{}) {
				cfg, err := f.w.LoadProject("shop")
				if err != nil {
					t.Fatal(err)
				}
				cfg.Mate = tc.mate
				if err := f.w.SaveProject("shop", cfg); err != nil {
					t.Fatal(err)
				}
			}
			f.before = f.collect(t)
			ctx := context.Background()
			req := spawn.StartRequest{Project: "shop", Harness: tc.kind, Resume: true}
			res, err := spawn.StartMate(ctx, f.w, f.deps, req)
			if err != nil {
				t.Fatalf("StartMate: %v", err)
			}
			if tc.resume {
				if tc.kind == harness.KindCodex {
					setSessionRef(t, f.rt, f.w, res.Agent, codexSessionA)
					writeCodexRollout(t, f.deps.CodexSessionsDir, codexSessionA, f.w.MateDir("shop"), f.deps.Now())
				}
				if _, err := spawn.StopMate(ctx, f.w, f.deps, "shop"); err != nil {
					t.Fatalf("StopMate: %v", err)
				}
				f.rec.prompts = nil
				res, err = spawn.StartMate(ctx, f.w, f.deps, req)
				if err != nil {
					t.Fatalf("resumed StartMate: %v", err)
				}
				if !res.Resumed {
					t.Fatalf("second start did not resume: %+v", res)
				}
			} else if res.Resumed {
				t.Fatalf("a first start resumed: %+v", res)
			}
			checkLaunchGolden(t, tc.name, f.snapshot(t, res.Pane))
		})
	}
}

type crewCase struct {
	name     string
	kind     harness.Kind
	model    string
	effort   harness.Effort
	env      map[string]string
	relaunch bool
}

func TestLaunchCharacterizationCrew(t *testing.T) {
	cases := []crewCase{
		{name: "crew-claude-default", kind: harness.KindClaude},
		{name: "crew-claude-model-effort", kind: harness.KindClaude, model: "sonnet", effort: harness.EffortMax},
		{name: "crew-claude-config-dir", kind: harness.KindClaude, env: map[string]string{config.EnvClaudeConfigDir: "dir"}},
		{name: "crew-claude-relaunch", kind: harness.KindClaude, model: "sonnet", effort: harness.EffortHigh, relaunch: true},
		{name: "crew-codex-default", kind: harness.KindCodex},
		{name: "crew-codex-model-effort", kind: harness.KindCodex, model: "gpt-5.5", effort: harness.EffortHigh},
		{name: "crew-codex-effort-omitted", kind: harness.KindCodex, effort: harness.EffortMax},
		{name: "crew-codex-relaunch", kind: harness.KindCodex, model: "gpt-5.5", effort: harness.EffortLow, relaunch: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range tc.env {
				if v == "dir" {
					v = t.TempDir()
				}
				env[k] = v
			}
			f := newLaunchFixture(t, true, env)
			f.pinHead(t)
			f.before = f.collect(t)
			ctx := context.Background()
			res, err := spawn.SpawnCrew(ctx, f.w, f.deps, spawn.SpawnCrewRequest{
				Project: "shop", Crew: "k3", Harness: tc.kind, Model: tc.model, Effort: tc.effort,
				BriefText: brieftest.Ship("Add a healthcheck endpoint.\nKeep it small.\n"),
			})
			if err != nil {
				t.Fatalf("SpawnCrew: %v", err)
			}
			pane := res.Pane
			if tc.relaunch {
				f.rt.ClosePane(res.Pane)
				f.rec.prompts = nil
				again, err := spawn.RelaunchCrew(ctx, f.w, f.deps, "shop", "k3", "pushed two commits")
				if err != nil {
					t.Fatalf("RelaunchCrew: %v", err)
				}
				pane = again.Pane
			}
			checkLaunchGolden(t, tc.name, f.snapshot(t, pane))
		})
	}
}

// TestLaunchCharacterizationEnvSets pins the two env lists every launch is
// filtered through: the keys a launch may assign, and the Claude Code
// session variables stripped from every pane before an agent starts.
func TestLaunchCharacterizationEnvSets(t *testing.T) {
	var b strings.Builder
	b.WriteString("== config.LaunchEnvKeys (allowlist) ==\n")
	for _, k := range config.LaunchEnvKeys() {
		b.WriteString(k + "\n")
	}
	b.WriteString("\n== harness.NestedSessionEnv (always unset) ==\n")
	for _, k := range harness.NestedSessionEnv {
		b.WriteString(k + "\n")
	}
	checkLaunchGolden(t, "env-sets", b.String())
}
