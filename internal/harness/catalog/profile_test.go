package catalog

import (
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/harness/grok"
)

// Claude accepts all five (claude 2.1.282 --help: "low, medium, high,
// xhigh, max"); Codex's model_reasoning_effort advertises four for its
// catalogue models, max only for some (firstmate's codex record, verified
// on codex-cli 0.142.1 and 0.153.4). A level a harness does not take is
// recorded but not passed.
func TestEffortSupportPerHarness(t *testing.T) {
	for _, e := range []harness.Effort{harness.EffortLow, harness.EffortMedium, harness.EffortHigh, harness.EffortXHigh, harness.EffortMax} {
		if !(claude.Claude{}).Info().SupportsEffort(e) {
			t.Errorf("claude does not take %s", e)
		}
	}
	for _, e := range []harness.Effort{harness.EffortLow, harness.EffortMedium, harness.EffortHigh, harness.EffortXHigh} {
		if !(codex.Codex{}).Info().SupportsEffort(e) {
			t.Errorf("codex does not take %s", e)
		}
	}
	if (codex.Codex{}).Info().SupportsEffort(harness.EffortMax) {
		t.Error("codex takes max; its catalogue does not advertise it for every model")
	}
	for _, e := range []harness.Effort{harness.EffortLow, harness.EffortMedium, harness.EffortHigh, harness.EffortXHigh} {
		if !(grok.Grok{}).Info().SupportsEffort(e) {
			t.Errorf("grok does not take %s", e)
		}
	}
	if (grok.Grok{}).Info().SupportsEffort(harness.EffortMax) {
		t.Error("grok takes max; grok-4.7's menu does not list it")
	}
}
