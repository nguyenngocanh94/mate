package harness

import (
	"os"
	"testing"
)

// requireLive gates every TestLive* proof in this package. A live test needs
// a real Claude or Codex installation, or a real transcript corpus on the
// developer's machine; MATEV2_LIVE=1 is the single opt-in that says the
// machine has them. scripts/gotestreport allows TestLive* and nothing else to
// skip, so a green suite that skipped these is not evidence they pass.
//
// The corpus tests need a second opt-in (MATEV2_CLAUDE_PROJECTS_DIR,
// MATEV2_CODEX_SESSIONS_DIR) because they read whatever transcripts happen to
// be on the machine: they are a census of a moving corpus, and a newer
// harness release writing a shape the parser has not seen is a finding to
// read, not a build to break.
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MATEV2_LIVE") != "1" {
		t.Skip("set MATEV2_LIVE=1 to run live harness proofs")
	}
}
