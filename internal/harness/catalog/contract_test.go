package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
)

// The contract suite (docs/plans/harness-registry-2026-09-30.md, section 4)
// runs over every harness catalog.Default() registers. A harness is
// onboarded when this suite passes for it; nothing here names one.
//
// This is the skeleton of items 1 and 2: every capability declared, and a
// launch built for each role. The screen captures (item 3), transcript
// fixtures (item 4) and the live conformance run (item 5) join it with the
// plan PRs that give the contract those parts.

func eachProfile(t *testing.T, run func(t *testing.T, p harness.Profile)) {
	t.Helper()
	reg := catalog.Default()
	kinds := reg.Kinds()
	if len(kinds) == 0 {
		t.Fatal("catalog.Default() registers no harness")
	}
	for _, k := range kinds {
		p, err := reg.Lookup(k)
		if err != nil {
			t.Fatalf("Lookup(%q) of a registered kind: %v", k, err)
		}
		t.Run(string(k), func(t *testing.T) { run(t, p) })
	}
}

// capDecl is one Capabilities field with its implementation erased.
type capDecl struct {
	name     string
	status   harness.CapStatus
	hasImpl  bool
	evidence harness.Evidence
	reason   string
}

// declarations reads every field of Capabilities by reflection, so a field
// added to the contract is checked here without anyone listing it.
func declarations(t *testing.T, c harness.Capabilities) []capDecl {
	t.Helper()
	v := reflect.ValueOf(c)
	var out []capDecl
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		impl := f.FieldByName("Impl")
		if !impl.IsValid() || impl.Kind() != reflect.Interface {
			t.Fatalf("Capabilities.%s is not a Cap[T] over an interface", v.Type().Field(i).Name)
		}
		out = append(out, capDecl{
			name:     v.Type().Field(i).Name,
			status:   f.FieldByName("Status").Interface().(harness.CapStatus),
			hasImpl:  !impl.IsNil(),
			evidence: f.FieldByName("Evidence").Interface().(harness.Evidence),
			reason:   f.FieldByName("Reason").String(),
		})
	}
	return out
}

// Item 1: no capability is undeclared; Impl is set exactly when verified;
// verified rests on evidence, unsupported and unknown on a reason.
func TestContractCapabilitiesDeclared(t *testing.T) {
	eachProfile(t, func(t *testing.T, p harness.Profile) {
		decls := declarations(t, p.Capabilities())
		if len(decls) == 0 {
			t.Fatal("Capabilities has no fields; the reflection read nothing")
		}
		for _, d := range decls {
			switch d.status {
			case harness.CapUndeclared:
				t.Errorf("%s is undeclared: every harness answers every capability", d.name)
				continue
			case harness.CapVerified, harness.CapUnsupported, harness.CapUnknown:
			default:
				t.Errorf("%s has status %q, not one of verified, unsupported, unknown", d.name, d.status)
				continue
			}
			verified := d.status == harness.CapVerified
			if d.hasImpl != verified {
				t.Errorf("%s is %s with Impl set = %v: Impl is set exactly when verified", d.name, d.status, d.hasImpl)
			}
			if verified {
				if d.evidence.Version == "" || d.evidence.Measured == "" || d.evidence.Proof == "" {
					t.Errorf("%s is verified without full evidence (version, when, proof): %+v", d.name, d.evidence)
				}
			} else if strings.TrimSpace(d.reason) == "" {
				t.Errorf("%s is %s without a reason", d.name, d.status)
			}
		}
	})
}

// The required part of the contract is there, and the kind a profile
// reports is the kind it is registered under.
func TestContractRequiredParts(t *testing.T) {
	eachProfile(t, func(t *testing.T, p harness.Profile) {
		reg := catalog.Default()
		if k, err := reg.Parse(string(p.Kind())); err != nil || k != p.Kind() {
			t.Errorf("Parse(%q) = %q, %v; want the profile's own kind", p.Kind(), k, err)
		}
		info := p.Info()
		if info.RuntimeKind == "" {
			t.Error("Info().RuntimeKind is empty: Herdr would get no --kind")
		}
		if p.Launcher() == nil {
			t.Error("Launcher() is nil")
		}
		if p.Screen() == nil {
			t.Fatal("Screen() is nil")
		}
	})
}

// The screen profile reads its pane through a source the runtime knows, and
// its own ready screen - what the fake runtime shows for a clean start -
// classifies as ready at startup and as an idle, empty composer afterwards.
func TestContractScreenReadsItsOwnReadyScreen(t *testing.T) {
	eachProfile(t, func(t *testing.T, p harness.Profile) {
		s := p.Screen()
		switch src := s.ReadSource(); src {
		case harness.ReadRecentUnwrapped, harness.ReadVisible:
		default:
			t.Errorf("ReadSource() = %q, not a source the runtime reads", src)
		}
		ready := s.ReadyScreen()
		if got := s.ClassifyStartup(ready); got != harness.StartupScreenReady {
			t.Errorf("ClassifyStartup(ReadyScreen()) = %s, want %s", got, harness.StartupScreenReady)
		}
		if got := s.ClassifyStartup(""); got != harness.StartupScreenUnrecognized {
			t.Errorf("ClassifyStartup on an empty pane = %s, want %s", got, harness.StartupScreenUnrecognized)
		}
		lines := strings.Split(ready, "\n")
		if evidence, busy := s.Busy(lines); busy {
			t.Errorf("Busy(ReadyScreen()) found a turn in flight: %q", evidence)
		}
		content, ok := s.Composer(lines)
		if !ok {
			t.Fatal("Composer(ReadyScreen()) found no composer")
		}
		if c := strings.TrimSpace(content); c != "" && !slices.Contains(s.ComposerPlaceholders(), c) {
			t.Errorf("ReadyScreen()'s composer holds %q, neither empty nor a placeholder", c)
		}
		if s.ComposerGlyph() == "" {
			t.Error("ComposerGlyph() is empty")
		}
	})
}

// Item 2: Prepare lays out each role on fixture directories and Build turns
// that into a Crew spec, and a Mate spec or a refusal that names the
// capability it lacks. Every file Prepare names lands inside the agent's cwd
// or its state directory, and one to keep out of git sits at the cwd's top,
// where spawn's local exclude rule can name it. Every env key a profile
// declares points at an empty directory so no file of the operator's
// reaches the launch.
func TestContractLauncherBuildsEachRole(t *testing.T) {
	eachProfile(t, func(t *testing.T, p harness.Profile) {
		info := p.Info()
		for _, key := range info.EnvKeys {
			t.Setenv(key, t.TempDir())
		}
		ctx := context.Background()
		launch := func(role harness.AgentRole) (harness.LaunchSpec, string, error) {
			t.Helper()
			cwd, state := t.TempDir(), t.TempDir()
			// A Mate's manual is in its cwd; a Crew's brief is in its state
			// directory, outside the worktree.
			instructions := filepath.Join(state, "brief.md")
			if role == harness.RoleMate {
				instructions = filepath.Join(cwd, "AGENTS.md")
			}
			if err := os.WriteFile(instructions, []byte("you are a contract fixture\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			prep, err := p.Launcher().Prepare(ctx, harness.PrepareRequest{
				Role: role, Cwd: cwd, StateDir: state, ContextPath: instructions, Binary: "/usr/local/bin/mate",
			})
			if err != nil {
				return harness.LaunchSpec{}, cwd, err
			}
			for _, f := range prep.Files {
				inside := func(dir string) bool {
					rel, err := filepath.Rel(dir, f.Path)
					return err == nil && filepath.IsAbs(f.Path) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
				}
				if !inside(cwd) && !inside(state) {
					t.Errorf("%s: Prepare names %s, outside the cwd %s and the state directory %s", role, f.Path, cwd, state)
				}
				if f.Exclude && filepath.Dir(f.Path) != cwd {
					t.Errorf("%s: %s is to be kept out of git but is not at the top of the cwd %s", role, f.Path, cwd)
				}
				if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.Path, f.Data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			spec, err := p.Launcher().Build(ctx, harness.AgentSpec{
				ID: "contract-" + string(role), Role: role, Kind: p.Kind(), Cwd: cwd,
				ContextPath: prep.ContextPath, Launch: prep.Launch,
			})
			return spec, cwd, err
		}

		crew, cwd, err := launch(harness.RoleCrew)
		if err != nil {
			t.Fatalf("Crew launch: %v", err)
		}
		if !crew.Startable() || crew.Kind() != info.RuntimeKind || crew.Cwd() != cwd {
			t.Errorf("Crew launch: startable %v, kind %q (want %q), cwd %q (want %q)",
				crew.Startable(), crew.Kind(), info.RuntimeKind, crew.Cwd(), cwd)
		}
		if err := crew.ValidateRequiredContext(); err != nil {
			t.Errorf("Crew launch fails the runtime's own context check: %v", err)
		}

		mate, _, err := launch(harness.RoleMate)
		if err != nil {
			var missing []string
			for _, d := range declarations(t, p.Capabilities()) {
				if d.status != harness.CapVerified && strings.Contains(err.Error(), d.name) {
					missing = append(missing, d.name)
				}
			}
			if len(missing) == 0 {
				t.Fatalf("Mate launch refused without naming a capability the harness lacks: %v", err)
			}
			return
		}
		if !mate.Startable() || mate.Kind() != info.RuntimeKind {
			t.Errorf("Mate launch: startable %v, kind %q (want %q)", mate.Startable(), mate.Kind(), info.RuntimeKind)
		}
		if err := mate.ValidateRequiredContext(); err != nil {
			t.Errorf("Mate launch fails the runtime's own context check: %v", err)
		}
	})
}
