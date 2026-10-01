package mateassets

import "testing"

// Codex meters the raw bytes of every project instruction file it loads and
// silently truncates the tail past `project_doc_max_bytes` (harness/
// internal/harness/codex/plan.go, ADR 0004 re-probed on codex-cli 0.152.1). mate refuses
// rather than letting that happen, so a Mate manual that has grown past the
// cap does not render badly - it stops `mate start --harness codex`
// outright, with an error about metered bytes that says nothing about which
// paragraph to cut.
//
// This test is the early warning. It fails where the manual is edited, not
// three packages away in a spawn test.
const (
	// codexProjectDocMaxBytes is the cap every mate Codex launch runs
	// under: harness.CodexDefaultMaxBytes, which the launch also passes to
	// Codex as `-c project_doc_max_bytes=`. Spelled here as a number so
	// this package stays free of harness.
	codexProjectDocMaxBytes = 128 * 1024

	// Keep always-loaded instructions bounded; detailed contracts live in skills.
	agentsManualBudget = 25 * 1024
)

func TestRenderedManualFitsCodexProjectDocBudget(t *testing.T) {
	out, err := Render(fixedParams())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(out) > agentsManualBudget {
		t.Fatalf("the rendered Mate manual is %d bytes, over the %d-byte budget (Codex's cap is %d, and the rest is spent on the real workspace path and on whatever Codex meters above the Mate's cwd).\n"+
			"Move action-specific instructions to skills; do not raise this core budget.",
			len(out), agentsManualBudget, codexProjectDocMaxBytes)
	}
	t.Logf("rendered manual: %d bytes, %d under budget", len(out), agentsManualBudget-len(out))
}
