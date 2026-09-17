package harness_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/harness"
)

// The Codex 0.151.0 metering contract in internal/harness/codex.go rests on
// live-probed behavior (ADR 0004). The probe is preserved as a runnable
// artifact in testdata/codexprobe: scenario fixtures, the driver
// (probe.py), the codex version (manifest.json), and the raw captured
// instruction text per scenario (captures/). These tests replay the
// committed captures against the Go model, so the constants and the metering
// law are pinned to real captured output, not to values restated in test
// code. TestLiveCodexProbeMatchesCommittedCaptures re-runs the probe against
// the installed CLI on demand, so a Codex release that changes the behavior
// breaks a test instead of silently invalidating the contract.

type probeScenario struct {
	ID      string `json:"id"`
	Note    string `json:"note"`
	Cwd     string `json:"cwd"`
	GitRoot string `json:"git_root"`
	// GitRootKind is "" or "dir" for a .git directory, "file" for a worktree
	// whose .git is a gitdir pointer file (the Phase 1 Crew layout).
	GitRootKind string `json:"git_root_kind"`
	// OuterGitDir names an enclosing repo whose .git directory discovery
	// must stop short of; empty when the scenario has none.
	OuterGitDir string            `json:"outer_git_dir"`
	Files       map[string]string `json:"files"`
	Global      map[string]string `json:"global_files"`
	MaxBytes    int               `json:"project_doc_max_bytes"`
	Capture     string            `json:"capture"`
}

func loadProbeScenarios(t *testing.T) []probeScenario {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "codexprobe", "scenarios.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Scenarios []probeScenario `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Scenarios) == 0 {
		t.Fatal("no probe scenarios")
	}
	return doc.Scenarios
}

func readCapture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "codexprobe", "captures", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// buildFixture recreates one scenario's file tree and returns the discovery
// cwd and CODEX_HOME directory.
func buildFixture(t *testing.T, sc probeScenario) (cwd, home string) {
	t.Helper()
	tmp := t.TempDir()
	home = filepath.Join(tmp, "codex-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range sc.Global {
		writeFixtureFile(t, filepath.Join(home, rel), content)
	}
	root := filepath.Join(tmp, "tree")
	for rel, content := range sc.Files {
		writeFixtureFile(t, filepath.Join(root, rel), content)
	}
	writeGitMarkers(t, root, sc)
	cwd = filepath.Join(root, sc.Cwd)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return cwd, home
}

// worktreeGitFile mirrors probe.py: a worktree's .git is a regular file with
// a gitdir pointer. Codex 0.151.0 stats the marker and never resolves the
// pointer (a real `git worktree add` tree captured identically), so the
// fixture needs no git.
const worktreeGitFile = "gitdir: ../../../main/.git/worktrees/crew-1\n"

func writeGitMarkers(t *testing.T, root string, sc probeScenario) {
	t.Helper()
	marker := filepath.Join(root, sc.GitRoot, ".git")
	switch sc.GitRootKind {
	case "", "dir":
		if err := os.MkdirAll(marker, 0o755); err != nil {
			t.Fatal(err)
		}
	case "file":
		writeFixtureFile(t, marker, worktreeGitFile)
	default:
		t.Fatalf("scenario %s: unknown git_root_kind %q", sc.ID, sc.GitRootKind)
	}
	if sc.OuterGitDir != "" {
		if err := os.MkdirAll(filepath.Join(root, sc.OuterGitDir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// renderWithCap renders what Codex delivers for a chain under
// project_doc_max_bytes, per the metered-content law the captures prove:
// global doc and marker exempt, project content charged in load order,
// joiners rendered unmetered. (No committed scenario cuts a file to zero
// bytes; whether Codex still renders a joiner for an empty remainder is
// unprobed.)
func renderWithCap(chain harness.CodexChain, maxBytes int) []byte {
	if maxBytes <= 0 {
		return chain.RenderedInstructions()
	}
	var buf bytes.Buffer
	if chain.Global != nil {
		buf.Write(chain.Global.Bytes)
		if len(chain.Project) > 0 {
			buf.WriteString(harness.CodexProjectDocMarker)
		}
	}
	budget := maxBytes
	for i, f := range chain.Project {
		if i > 0 {
			buf.WriteString(harness.CodexChainJoiner)
		}
		take := len(f.Bytes)
		if take > budget {
			take = budget
		}
		buf.Write(f.Bytes[:take])
		budget -= take
	}
	return buf.Bytes()
}

// The Go model must reproduce every committed capture byte-for-byte, and the
// refusal contract must refuse exactly the scenarios whose capture shows
// truncated content.
func TestCodexModelReproducesCommittedProbeCaptures(t *testing.T) {
	t.Parallel()
	for _, sc := range loadProbeScenarios(t) {
		sc := sc
		t.Run(sc.ID, func(t *testing.T) {
			t.Parallel()
			capture := readCapture(t, sc.Capture)
			cwd, home := buildFixture(t, sc)
			req := harness.DiscoverRequest{Cwd: cwd}
			if len(sc.Global) > 0 {
				req.CodexHome = home
			}
			chain, err := harness.DiscoverCodexChain(req)
			if err != nil {
				t.Fatal(err)
			}
			if got := renderWithCap(chain, sc.MaxBytes); !bytes.Equal(got, capture) {
				t.Fatalf("model render disagrees with the captured codex output\n model:   %q\n capture: %q", got, capture)
			}
			// The required Mate file is the override when the scenario
			// writes one at cwd (it shadows the sibling AGENTS.md), else the
			// tracked AGENTS.md.
			required := filepath.Join(cwd, harness.CodexBaseName)
			if _, ok := sc.Files[path.Join(sc.Cwd, harness.CodexOverrideName)]; ok {
				required = harness.CodexInstructionPath(cwd)
			}
			truncated := !bytes.Equal(capture, chain.RenderedInstructions())
			err = chain.RefuseIfRequiredMissingOrTruncated(required, sc.MaxBytes)
			if truncated && !errors.Is(err, harness.ErrContextTooLarge) {
				t.Fatalf("capture shows truncation but the chain was not refused: err = %v", err)
			}
			if !truncated && err != nil {
				t.Fatalf("capture shows full delivery but the chain was refused: %v", err)
			}
		})
	}
}

// Opt-in live re-probe: reruns every scenario against the installed codex
// CLI and diffs the extracted instruction text against the committed
// captures. Run with MATEV2_LIVE=1 (testdata/codexprobe/probe.py
// does the same standalone). A mismatch means a Codex release changed the
// chain behavior: re-verify the metering contract in codex.go before
// re-baselining with probe.py --write.
func TestLiveCodexProbeMatchesCommittedCaptures(t *testing.T) {
	if os.Getenv("MATEV2_LIVE") != "1" {
		t.Skip("set MATEV2_LIVE=1 to run live harness proofs")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Fatalf("MATEV2_LIVE=1 but no codex CLI is in PATH: %v", err)
	}
	for _, sc := range loadProbeScenarios(t) {
		sc := sc
		t.Run(sc.ID, func(t *testing.T) {
			capture := readCapture(t, sc.Capture)
			cwd, home := buildFixture(t, sc)
			args := []string{}
			if sc.MaxBytes > 0 {
				args = append(args, "-c", "project_doc_max_bytes="+strconv.Itoa(sc.MaxBytes))
			}
			args = append(args, "debug", "prompt-input", "x")
			cmd := exec.Command("codex", args...)
			cmd.Dir = cwd
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"CODEX_HOME=" + home,
				"HOME=" + filepath.Dir(home),
			}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("codex run failed: %v", err)
			}
			got := extractInstructions(t, out)
			if !bytes.Equal(got, capture) {
				t.Fatalf("live codex output disagrees with the committed capture\n live:    %q\n capture: %q", got, capture)
			}
		})
	}
}

// extractInstructions mirrors probe.py: JSON-decode the prompt string
// starting with "# AGENTS.md instructions", keep the text between
// "<INSTRUCTIONS>\n" and the final "\n</INSTRUCTIONS>".
func extractInstructions(t *testing.T, stdout []byte) []byte {
	t.Helper()
	i := bytes.Index(stdout, []byte(`"# AGENTS.md instructions`))
	if i < 0 {
		t.Fatalf("no AGENTS.md instructions block in codex output")
	}
	dec := json.NewDecoder(bytes.NewReader(stdout[i:]))
	var text string
	if err := dec.Decode(&text); err != nil {
		t.Fatalf("decode instruction string: %v", err)
	}
	const openTag = "<INSTRUCTIONS>\n"
	const closeTag = "\n</INSTRUCTIONS>"
	start := strings.Index(text, openTag)
	end := strings.LastIndex(text, closeTag)
	if start < 0 || end < 0 || end < start {
		t.Fatalf("instruction wrapper not found in %q", text)
	}
	return []byte(text[start+len(openTag) : end])
}
