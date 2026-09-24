package brieftest

import (
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/brief"
)

func TestHelpersPassTheCheck(t *testing.T) {
	for _, instruction := range []string{
		"work",
		"Append needs-decision: pick A or B to the status file and stop; do nothing else",
		"Line one.\n\nLine two.\n",
	} {
		if ps := brief.Check(Ship(instruction), brief.Ship); len(ps) != 0 {
			t.Errorf("Ship(%q): %v", instruction, ps)
		}
		if ps := brief.Check(Scout(instruction, "What is there?"), brief.Scout); len(ps) != 0 {
			t.Errorf("Scout(%q): %v", instruction, ps)
		}
	}
}
