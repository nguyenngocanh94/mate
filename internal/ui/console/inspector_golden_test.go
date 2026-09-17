package console

import (
	"fmt"
	"testing"
)

// TestGoldenFramesInspectorPerNodeKind is the inspector task's own golden
// coverage: each node kind's block (Project, Mate, Crew), at both
// required breakpoints (160x48, 120x36), matching design/mate-console-
// states.html.
//
// Accept a deliberate change with:
//
//	go test ./internal/ui/console -run TestGoldenFramesInspectorPerNodeKind -update
func TestGoldenFramesInspectorPerNodeKind(t *testing.T) {
	for _, size := range []struct{ w, h int }{{160, 48}, {120, 36}} {
		suffix := fmt.Sprintf("%dx%d-unicode", size.w, size.h)

		// Project block: selected at the Workspace level, before entering
		// anything.
		goldenFrame(t, "inspector-project-"+suffix, sampleTree(), size.w, size.h, unicodeGlyphs)

		// Mate block: the Project's first row.
		m := newFixture(t, sampleTree(), size.w, size.h, unicodeGlyphs)
		m, _ = send(t, m, key("enter")) // payments-api; Mate row selected by default
		assertGolden(t, "inspector-mate-"+suffix, renderFrame(t, m))

		// Crew block: the Project's second row. The failed Crew is in the
		// Completed group, so the one active Crew sits directly under the
		// Mate row.
		//
		// TODO(task 21): a Task block sat between the Mate and Crew blocks
		// in v1. matev2 has no Task.
		m, _ = send(t, m, key("down"))
		assertGolden(t, "inspector-crew-"+suffix, renderFrame(t, m))
	}
}
