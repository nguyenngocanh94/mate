package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/capability"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

// recallerTool is a tool whose only capability is a Recall answering with
// render.
type recallerTool struct {
	name   tool.Name
	title  string
	render func(context.Context, tool.CommandEnv, int) (string, bool, error)
}

func (r recallerTool) Name() tool.Name { return r.name }
func (r recallerTool) Info() tool.Info { return tool.Info{Name: r.name, Title: r.title} }
func (r recallerTool) Capabilities() tool.Capabilities {
	none := "a recall fake has none"
	return tool.Capabilities{
		Viewer:  capability.Cap[tool.Viewer]{Status: capability.Unsupported, Reason: none},
		Command: capability.Cap[tool.Command]{Status: capability.Unsupported, Reason: none},
		Recall:  capability.Cap[tool.Recall]{Status: capability.Verified, Impl: recallFunc(r.render)},
		Skill:   capability.Cap[tool.Skill]{Status: capability.Unsupported, Reason: none},
		Data:    capability.Cap[tool.Data]{Status: capability.Unsupported, Reason: none},
	}
}

type recallFunc func(context.Context, tool.CommandEnv, int) (string, bool, error)

func (f recallFunc) Render(ctx context.Context, env tool.CommandEnv, maxBytes int) (string, bool, error) {
	return f(ctx, env, maxBytes)
}

// Each tool with something to say is a block under its title, in
// registration order; one with nothing says nothing; one that fails, or
// does not answer in time even ignoring its context, is one unreadable
// line, and recall goes on without waiting for it.
func TestRecallToolsBlocksAndDeadline(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	old := recallToolDeadline
	recallToolDeadline = 50 * time.Millisecond
	t.Cleanup(func() { recallToolDeadline = old })
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	var seen tool.CommandEnv
	reg, err := tool.NewRegistry(
		recallerTool{"alpha", "Alpha", func(_ context.Context, env tool.CommandEnv, maxBytes int) (string, bool, error) {
			seen = env
			if maxBytes != recallToolMaxBytes {
				t.Errorf("maxBytes = %d, want %d", maxBytes, recallToolMaxBytes)
			}
			return "two ready", true, nil
		}},
		recallerTool{"quiet", "Quiet", func(context.Context, tool.CommandEnv, int) (string, bool, error) { return "", false, nil }},
		recallerTool{"stuck", "Stuck", func(context.Context, tool.CommandEnv, int) (string, bool, error) {
			<-release // ignores its context
			return "late", true, nil
		}},
		recallerTool{"broken", "Broken", func(context.Context, tool.CommandEnv, int) (string, bool, error) {
			return "", false, errors.New("database\nis locked")
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	start := time.Now()
	recallTools(w, "shop", reg, &out)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("recall waited %s on a stuck tool", took)
	}
	want := "== Tool: Alpha ==\ntwo ready\n" +
		"== Tool: Stuck ==\nStuck: unreadable (no answer within 50ms)\n" +
		"== Tool: Broken ==\nBroken: unreadable (database is locked)\n"
	if out.String() != want {
		t.Fatalf("recall blocks:\n%s\nwant:\n%s", out.String(), want)
	}
	if seen.ProjectDir != w.ProjectHome("shop") || seen.DataDir != "" || seen.Lock == nil || seen.Run == nil {
		t.Fatalf("env = %+v, want the project directory, a lock and a runner", seen)
	}
}
