package timeline

import (
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// A Skill call is about the skill it loads. Without the name as its target
// the action row says only "Skill", and a reader cannot tell which one.
func TestSkillCallTargetIsTheSkillItLoads(t *testing.T) {
	call := harness.TranscriptToolCall{ToolName: "Skill", CommandClass: harness.CommandOther, InputJSON: `{"skill":"token-review","args":"shop"}`}
	target, summary, class := toolTarget(harness.Kind("claude"), call)
	if target != "token-review" {
		t.Fatalf("target %q, want the skill's name", target)
	}
	if summary != `{"skill":"token-review","args":"shop"}` || class != harness.CommandOther {
		t.Fatalf("summary %q class %q: the input and the class are the call's own", summary, class)
	}
}
