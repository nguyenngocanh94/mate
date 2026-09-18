package mateassets

import "testing"

// Codex meters the raw bytes of every project instruction file it loads and
// silently truncates the tail past `project_doc_max_bytes` (harness/
// codexplan.go, ADR 0004 re-probed on codex-cli 0.152.1). matev2 refuses
// rather than letting that happen, so a Mate manual that has grown past the
// cap does not render badly - it stops `mate start --harness codex`
// outright, with an error about metered bytes that says nothing about which
// paragraph to cut.
//
// This test is the early warning. It fails where the manual is edited, not
// three packages away in a spawn test.
const (
	// codexProjectDocMaxBytes is Codex's observed default cap.
	codexProjectDocMaxBytes = 32768

	// agentsManualBudget is what the rendered manual may occupy. The
	// headroom under the cap is not slack - it is spent on two things the
	// fixture below understates:
	//
	//  1. The real workspace root. The fixture renders `/ws`, three bytes,
	//     and the manual repeats that path 26 times, so every extra
	//     character of the workspace's own path costs 26 bytes here. A
	//     workspace at /Users/someone/work/acme adds about 500.
	//  2. Whatever Codex meters above the Mate's cwd. Those bytes come out
	//     of the same cap before the manual gets any.
	//
	// The number is measured, not chosen: internal/spawn's Codex start
	// tests run under a `t.TempDir()` whose path carries the test's own
	// name, and the longest of them refused a manual of 28823 bytes by 31.
	// That is the tightest real case in the tree, so this budget sits just
	// under it.
	agentsManualBudget = 28790
)

func TestRenderedManualFitsCodexProjectDocBudget(t *testing.T) {
	out, err := Render(fixedParams())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(out) > agentsManualBudget {
		t.Fatalf("the rendered Mate manual is %d bytes, over the %d-byte budget (Codex's cap is %d, and the rest is spent on the real workspace path and on whatever Codex meters above the Mate's cwd).\n"+
			"Cut prose from assets/mate/AGENTS.md.tmpl rather than raising this number: past the cap, `mate start --harness codex` refuses outright.",
			len(out), agentsManualBudget, codexProjectDocMaxBytes)
	}
	t.Logf("rendered manual: %d bytes, %d under budget", len(out), agentsManualBudget-len(out))
}
