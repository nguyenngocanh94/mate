package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

// recallToolDeadline is how long each tool has to say its part of recall;
// a variable so a test need not wait it out.
var recallToolDeadline = 3 * time.Second

// recallToolMaxBytes bounds one tool's block of recall.
const recallToolMaxBytes = 4096

// recallTools writes a block for each tool of reg whose Recall is verified
// and has something to say about project, in registration order:
// `== Tool: <Title> ==` and the tool's lines. A tool that fails, or does
// not answer within recallToolDeadline, is one `<Title>: unreadable
// (<reason>)` line under its heading, and the rest of recall still loads.
// A tool with nothing to say writes nothing. A tool whose Data cannot be
// used (toolDataRefusal: left in the old place, or a legacy plan to import
// first) is the same unreadable line, with the refusal's sentence.
func recallTools(w *store.Workspace, project string, reg tool.Registry, out *strings.Builder) {
	for _, name := range reg.Names() {
		p, err := reg.Lookup(name)
		if err != nil {
			continue
		}
		title := p.Info().Title
		// A tracker left where it lived before layout 2 is one the tool
		// cannot see; recall says so instead of saying nothing.
		if err := toolDataRefusal(w, project, p, false); err != nil {
			fmt.Fprintf(out, "== Tool: %s ==\n%s: unreadable (%s)\n", title, title, err.Error())
			continue
		}
		rc := p.Capabilities().Recall
		if !rc.Verified() {
			continue
		}
		text, present, err := recallTool(rc.Impl, toolEnv(w, project, p))
		switch {
		case err != nil:
			fmt.Fprintf(out, "== Tool: %s ==\n%s: unreadable (%s)\n", title, title, recallClip(err.Error(), 200))
		case present:
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			fmt.Fprintf(out, "== Tool: %s ==\n%s", title, text)
		}
	}
}

// recallTool is one tool's Render under recallToolDeadline. A Render that
// outlives it, ignoring its context, is left behind and reported as timed
// out: recall does not wait on a tool.
func recallTool(rc tool.Recall, env tool.CommandEnv) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), recallToolDeadline)
	defer cancel()
	type result struct {
		text    string
		present bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		text, present, err := rc.Render(ctx, env, recallToolMaxBytes)
		done <- result{text, present, err}
	}()
	select {
	case r := <-done:
		return r.text, r.present, r.err
	case <-ctx.Done():
		return "", false, fmt.Errorf("no answer within %s", recallToolDeadline)
	}
}
