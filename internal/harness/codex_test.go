package harness

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCodexRefusesMissingOverride(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	_, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind: KindCodex,
		Cwd:  cwd,
	})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("missing override: err = %v, want ErrContextRequired (Codex would start anyway)", err)
	}
}

// TestCodexResumeLaunchesTheSubcommandWithTheID is task 35's B11: codex-cli
// 0.154.0 resumes a given session with `codex resume [OPTIONS] <id>` and only
// opens its picker when no id is given, so a resume is the fresh launch's
// flags under the subcommand, with the id last.
func TestCodexResumeLaunchesTheSubcommandWithTheID(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	const id = "01a0d260-cd47-77d2-bee7-46d98aa0461a"
	spec, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind:            KindCodex,
		Cwd:             cwd,
		ResumeSessionID: id,
	})
	if err != nil {
		t.Fatalf("BuildLaunchSpec resume: %v", err)
	}
	want := []string{"resume", "--dangerously-bypass-approvals-and-sandbox", "-c", CodexDisableUpdateCheck, "-c", CodexProjectDocMaxBytesOverride, id}
	if !slices.Equal(spec.Args(), want) {
		t.Fatalf("resume args = %#v, want %#v", spec.Args(), want)
	}
}

// TestCodexRefusesAResumeIDThatIsNotAUUID keeps whatever mate.meta carries
// from reaching the argv as something Codex could read as a flag or as a
// session name: only a session UUID is ever resumed.
func TestCodexRefusesAResumeIDThatIsNotAUUID(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"--last", "some-thread-id", "01a0d260-cd47"} {
		_, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{
			Kind:            KindCodex,
			Cwd:             cwd,
			ResumeSessionID: id,
		})
		if !errors.Is(err, ErrContextRequired) {
			t.Fatalf("resume id %q: err = %v, want a refusal", id, err)
		}
	}
}

func TestCodexRefusesEmptyOverride(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{Cwd: cwd})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("empty override: err = %v", err)
	}
}

func TestCodexRefusesTrackedAgentsMDAsDiscoveryFile(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	tracked := filepath.Join(cwd, CodexBaseName)
	if err := os.WriteFile(tracked, []byte("tracked project instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Cwd:         cwd,
		ContextPath: tracked,
	})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("AGENTS.md must not be accepted as Mate discovery: err = %v", err)
	}
}

func TestCodexBindsCwdAndOverride(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{Cwd: cwd, TaskPrompt: "FIRST-CREW-TASK"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Cwd() != cwd {
		t.Fatalf("cwd = %q", spec.Cwd())
	}
	if spec.ContextPath() != CodexInstructionPath(cwd) {
		t.Fatalf("path = %q", spec.ContextPath())
	}
	if spec.Delivery() != DeliveryInstructionFile {
		t.Fatalf("delivery = %q", spec.Delivery())
	}
	if spec.TaskPrompt() != "FIRST-CREW-TASK" {
		t.Fatalf("task prompt = %q", spec.TaskPrompt())
	}
	// The launch carries the permission bypass and the documented override
	// that suppresses codex's startup release-update prompt; nothing else,
	// because codex discovers its instruction chain from the cwd.
	wantArgs := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", CodexDisableUpdateCheck, "-c", CodexProjectDocMaxBytesOverride}
	if !slices.Equal(spec.Args(), wantArgs) {
		t.Fatalf("codex uses cwd discovery, args = %#v", spec.Args())
	}
	if CodexDisableUpdateCheck != "check_for_update_on_startup=false" {
		t.Fatalf("the update-check override must stay the documented config key, got %q", CodexDisableUpdateCheck)
	}
	notes := strings.Join(spec.Notes(), " ")
	if strings.Contains(notes, "externally isolated") {
		t.Fatalf("notes must not claim Herdr provides isolation, notes=%q", notes)
	}
	if !strings.Contains(notes, "directory-trust") {
		t.Fatalf("notes must record the directory-trust wall the flag does not clear, notes=%q", notes)
	}
	if strings.Contains(notes, "prompts only") {
		t.Fatalf("notes must not understate the combined flag as a mere prompt bypass, notes=%q", notes)
	}
	if !strings.Contains(notes, "git commit") {
		t.Fatalf("notes must record why the sandbox is dropped (a Crew cannot git commit under workspace-write), notes=%q", notes)
	}
	// A substring check for a bare noun ("home directory") pins presence but
	// not direction: a softened rewrite that CLAIMS containment ("nothing
	// outside the home directory is affected") keeps the noun and still
	// passes. Assert the reachable-direction clause itself, and refuse the
	// softening phrasings that can carry the same nouns while saying the
	// opposite.
	for _, must := range []string{
		"can write outside its own worktree",
		".mate/mate.db",
		"other Crews' worktrees",
		"operator's home directory",
	} {
		if !strings.Contains(notes, must) {
			t.Fatalf("notes must state the combined flag's cost in the reachable direction (%q), notes=%q", must, notes)
		}
	}
	for _, soft := range []string{"prompts only", "effectively inside", "nothing outside"} {
		if strings.Contains(notes, soft) {
			t.Fatalf("notes must not soften the cost (%q), notes=%q", soft, notes)
		}
	}
}

func TestCodexPinsEffectiveHomeIntoLaunchEnv(t *testing.T) {
	cwd := t.TempDir()
	home := filepath.Join(t.TempDir(), "codex-home")
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "client-home"))
	spec, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Kind: KindCodex, Cwd: cwd, Config: Config{CodexHome: home},
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.CodexHome() != home {
		t.Fatalf("CodexHome = %q, want explicit effective home %q", spec.CodexHome(), home)
	}
	env := spec.Env()
	if len(env) != 1 || env[0].Key != "CODEX_HOME" || env[0].Value != home {
		t.Fatalf("launch env = %#v, want pinned CODEX_HOME", env)
	}
}

func TestCodexRefusesSilentChainTruncation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	initFakeGit(t, root)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two files that each fit but together cross the cap the launch runs
	// under (CodexDefaultMaxBytes, which the launch also hands Codex).
	half := CodexDefaultMaxBytes/2 + 1024
	if err := os.WriteFile(filepath.Join(root, CodexBaseName), []byte(strings.Repeat("R", half)), 0o644); err != nil {
		t.Fatal(err)
	}
	required := strings.Repeat("C", half)
	if err := os.WriteFile(CodexInstructionPath(sub), []byte(required), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Cwd:    sub,
		Config: Config{ProjectDocMaxBytes: CodexDefaultMaxBytes},
	})
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("parent+cwd chain exceeds the cap: err = %v, want ErrContextTooLarge (Codex would truncate silently)", err)
	}
}

func TestCodexRefusesWhenRequiredFileItselfExceedsCap(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte(strings.Repeat("x", CodexDefaultMaxBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{Cwd: cwd})
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexAcceptsChainUnderBudget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	initFakeGit(t, root)
	sub := filepath.Join(root, "api")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, CodexBaseName), []byte("root docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexInstructionPath(sub), []byte("you are crew\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{Cwd: sub})
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Startable() {
		t.Fatal("expected startable spec")
	}
}

func TestCodexWithoutGitDoesNotWalkParents(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	cwd := filepath.Join(parent, "proj")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, CodexBaseName), []byte(strings.Repeat("P", CodexDefaultMaxBytes)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("you are mate"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{Cwd: cwd})
	if err != nil {
		t.Fatalf("parent AGENTS.md must not be in the chain without git: %v", err)
	}
	if spec.ContextPath() != CodexInstructionPath(cwd) {
		t.Fatalf("path = %q", spec.ContextPath())
	}
}

func TestCodexOverrideTakesPrecedenceOverBaseAtSameDir(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, CodexBaseName), []byte("BASE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("OVERRIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	chain, err := DiscoverCodexChain(DiscoverRequest{Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Project) != 1 || string(chain.Project[0].Bytes) != "OVERRIDE" {
		t.Fatalf("chain = %+v", chain.Project)
	}
}

func TestCodexRelativeCwdRejected(t *testing.T) {
	t.Parallel()
	_, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{Cwd: "."})
	if !errors.Is(err, ErrContextRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexFallbackFilenamesCannotEscapeDir(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("LOCAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(filepath.Dir(cwd), "secret.md")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	chain, err := DiscoverCodexChain(DiscoverRequest{
		Cwd:               cwd,
		FallbackFilenames: []string{"../secret.md", "secret.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if chain.Global != nil {
		t.Fatalf("no CodexHome was given, global = %+v", chain.Global)
	}
	for _, f := range chain.Project {
		if strings.Contains(string(f.Bytes), "SECRET") {
			t.Fatalf("fallback escaped directory: %+v", f)
		}
	}
}

func TestCodexGlobalHomeIsPrefixedWhenAbsolute(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, CodexOverrideName), []byte("GLOBAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexInstructionPath(cwd), []byte("LOCAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	chain, err := DiscoverCodexChain(DiscoverRequest{Cwd: cwd, CodexHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if chain.Global == nil || string(chain.Global.Bytes) != "GLOBAL" {
		t.Fatalf("global = %+v", chain.Global)
	}
	if len(chain.Project) != 1 || string(chain.Project[0].Bytes) != "LOCAL" {
		t.Fatalf("project chain = %+v", chain.Project)
	}
}

func initFakeGit(t *testing.T, root string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Live probe (codex-cli 0.151.0, `codex debug prompt-input`, ADR 0004):
// project_doc_max_bytes meters only the raw content bytes of project files,
// concatenated in load order. The global $CODEX_HOME doc, the
// --- project-doc --- marker, and the per-file joiners are rendered after
// metering and never charged. The budget must count exactly what Codex
// counts: a missed metered byte delivers a silently truncated context, and
// a charged exempt byte refuses a chain Codex delivers in full.
func TestCodexMeterExemptsGlobalDocMarkerAndJoiners(t *testing.T) {
	t.Parallel()
	const max = 512
	home := t.TempDir()
	root := t.TempDir()
	initFakeGit(t, root)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, CodexBaseName), bytes.Repeat([]byte("g"), 200), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, CodexBaseName), bytes.Repeat([]byte("r"), 200), 0o644); err != nil {
		t.Fatal(err)
	}
	required := CodexInstructionPath(sub)
	if err := os.WriteFile(required, bytes.Repeat([]byte("c"), max-200), 0o644); err != nil {
		t.Fatal(err)
	}
	chain, err := DiscoverCodexChain(DiscoverRequest{Cwd: sub, CodexHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(chain.RenderedInstructions()); got <= max {
		t.Fatalf("test setup: the rendered chain must exceed the cap so the chain fits only because Codex exempts the global doc, marker and joiner (rendered %d, max %d)", got, max)
	}
	if err := chain.RefuseIfRequiredMissingOrTruncated(required, max); err != nil {
		t.Fatalf("metered content is exactly %d bytes; Codex 0.151.0 does not charge the global doc, marker or joiners: %v", max, err)
	}
	if err := os.WriteFile(required, bytes.Repeat([]byte("c"), max-200+1), 0o644); err != nil {
		t.Fatal(err)
	}
	chain, err = DiscoverCodexChain(DiscoverRequest{Cwd: sub, CodexHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.RefuseIfRequiredMissingOrTruncated(required, max); !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("one metered byte over the cap must refuse: err = %v", err)
	}
}

// End-to-end boundary through the constructor: a three-file chain whose
// content bytes sum to exactly project_doc_max_bytes must start (the two
// rendered joiners are exempt), and one more content byte must refuse.
func TestCodexBuildLaunchSpecRefusesOneMeteredByteOverCap(t *testing.T) {
	t.Parallel()
	const max = 256
	root := t.TempDir()
	initFakeGit(t, root)
	deep := filepath.Join(root, "sub", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, CodexBaseName), bytes.Repeat([]byte("r"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", CodexBaseName), bytes.Repeat([]byte("s"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	required := CodexInstructionPath(deep)
	if err := os.WriteFile(required, bytes.Repeat([]byte("c"), max-200), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Cwd:    deep,
		Config: Config{ProjectDocMaxBytes: max},
	})
	if err != nil {
		t.Fatalf("chain metering exactly at the cap must start: %v", err)
	}
	if !spec.Startable() {
		t.Fatal("expected startable spec")
	}
	if err := os.WriteFile(required, bytes.Repeat([]byte("c"), max-200+1), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Codex{}.BuildLaunchSpec(context.Background(), AgentSpec{
		Cwd:    deep,
		Config: Config{ProjectDocMaxBytes: max},
	})
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("one metered byte over project_doc_max_bytes must refuse: err = %v", err)
	}
}

// The rendered and metered concatenations are different streams, both
// measured byte-for-byte on codex-cli 0.151.0 (ADR 0004).
func TestCodexRenderedAndMeteredMatchProbedConcatenations(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	root := t.TempDir()
	initFakeGit(t, root)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, CodexBaseName), []byte("GLOBAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, CodexBaseName), []byte("ROOT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexInstructionPath(sub), []byte("SUB"), 0o644); err != nil {
		t.Fatal(err)
	}
	chain, err := DiscoverCodexChain(DiscoverRequest{Cwd: sub, CodexHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(chain.RenderedInstructions()), "GLOBAL\n\n--- project-doc ---\n\nROOT\n\nSUB"; got != want {
		t.Fatalf("rendered = %q, want %q", got, want)
	}
	if got, want := string(chain.MeteredProjectBytes()), "ROOTSUB"; got != want {
		t.Fatalf("metered = %q, want %q", got, want)
	}
	chain, err = DiscoverCodexChain(DiscoverRequest{Cwd: sub})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(chain.RenderedInstructions()), "ROOT\n\nSUB"; got != want {
		t.Fatalf("rendered without global = %q, want %q (no marker without a global doc)", got, want)
	}
	if got, want := string(chain.MeteredProjectBytes()), "ROOTSUB"; got != want {
		t.Fatalf("metered without global = %q, want %q", got, want)
	}
}
