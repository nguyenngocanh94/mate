package runtime

import (
	"os"
	"slices"
	"testing"
)

// requireLive gates every TestLive* proof in this package. They need a
// provisioned Herdr lab session, or the herdr binary itself, so MATE_LIVE=1
// is the single opt-in that says this machine has one. scripts/gotestreport
// allows TestLive* and nothing else to skip: a green suite that skipped these
// is not evidence the Herdr call paths work.
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MATE_LIVE") != "1" {
		t.Skip("set MATE_LIVE=1 to run live Herdr proofs")
	}
}

// sessionStreamHelperFlag re-execs the test binary as the fake herdr the
// session-stream tests attach to. It is an argv flag rather than a skipping
// TestSessionStreamHelperProcess so the suite has no unexplained skip.
const sessionStreamHelperFlag = "--session-stream-helper"

func TestMain(m *testing.M) {
	if slices.Contains(os.Args[1:], sessionStreamHelperFlag) {
		sessionStreamHelperProcess()
		return
	}
	os.Exit(m.Run())
}
