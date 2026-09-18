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
	// codexProjectDocMaxBytes is the cap every matev2 Codex launch runs
	// under: harness.CodexDefaultMaxBytes, which the launch also passes to
	// Codex as `-c project_doc_max_bytes=`. Spelled here as a number so
	// this package stays free of harness.
	codexProjectDocMaxBytes = 128 * 1024

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
	// 8 KiB covers both with room; the manual itself is ~29 KiB today.
	agentsManualBudget = codexProjectDocMaxBytes - 8*1024
)

func TestRenderedManualFitsCodexProjectDocBudget(t *testing.T) {
	out, err := Render(fixedParams())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(out) > agentsManualBudget {
		t.Fatalf("the rendered Mate manual is %d bytes, over the %d-byte budget (Codex's cap is %d, and the rest is spent on the real workspace path and on whatever Codex meters above the Mate's cwd).\n"+
			"Raise harness.CodexDefaultMaxBytes (and this constant with it) or cut prose from assets/mate/AGENTS.md.tmpl: past the cap, `mate start --harness codex` refuses outright.",
			len(out), agentsManualBudget, codexProjectDocMaxBytes)
	}
	t.Logf("rendered manual: %d bytes, %d under budget", len(out), agentsManualBudget-len(out))
}
