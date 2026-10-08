package fresh_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/capability"
	"github.com/nguyenngocanh94/mate/internal/tool"
	"github.com/nguyenngocanh94/mate/internal/tool/fresh"
)

// The contract suite (internal/tool/catalog) holds Fresh to the rules every
// tool keeps; these pin what Fresh itself declares.

func TestFreshIsTheReportViewer(t *testing.T) {
	p := fresh.New()
	info := p.Info()
	if p.Name() != "fresh" || info.Name != "fresh" || info.Title != "Fresh" ||
		!slices.Equal(info.Binaries, []string{"fresh"}) || info.Install != "brew install fresh-editor" || info.Docs != "docs/mvp.md M13" {
		t.Fatalf("Info() = %+v", info)
	}
	caps := p.Capabilities()
	if err := capability.Check(caps); err != nil {
		t.Fatal(err)
	}
	if !caps.Viewer.Verified() || caps.Viewer.Evidence.Measured != "2026-09-26" || !strings.Contains(caps.Viewer.Evidence.Proof, "M13") {
		t.Fatalf("Viewer = %+v, want verified on M13's 2026-09-26 measurement", caps.Viewer)
	}
	want := []tool.Binding{{Key: "e", Label: "report", Scope: tool.ScopeCrew, Role: "review"}}
	if got := caps.Viewer.Impl.Bindings(); !slices.Equal(got, want) {
		t.Fatalf("Bindings() = %+v, want %+v", got, want)
	}
	if got := caps.Viewer.Impl.Placeholder(); got != "mate · report\r\n\r\ne on a crew opens its report here." {
		t.Fatalf("Placeholder() = %q", got)
	}
	for name, c := range map[string]capability.Declaration{
		"Command": caps.Command.Declaration(), "Recall": caps.Recall.Declaration(),
		"Skill": caps.Skill.Declaration(), "Data": caps.Data.Declaration(),
	} {
		if c.Status != capability.Unsupported || c.Reason != "Fresh is an editor; it owns no data of mate's" {
			t.Errorf("%s = %+v, want unsupported: Fresh owns no data of mate's", name, c)
		}
	}
}

// Argv opens report.md when the crew wrote one, else the crew's folder,
// with the fresh findTool found; without one it says how to install it.
func TestFreshArgv(t *testing.T) {
	v := fresh.New().Capabilities().Viewer.Impl
	found := func(name string) string { return "/opt/bin/" + name }
	ctx := tool.ViewerContext{ProjectDir: "/ws/shop", CrewDir: "/ws/.mate/projects/shop/crews/k3"}

	inv, err := v.Argv(ctx, found)
	if err != nil || inv.Name != "/opt/bin/fresh" || !slices.Equal(inv.Args, []string{ctx.CrewDir}) || inv.Dir != ctx.CrewDir || inv.Env != nil {
		t.Fatalf("no report: Argv = %+v, %v", inv, err)
	}
	ctx.ReportPath = ctx.CrewDir + "/report.md"
	inv, err = v.Argv(ctx, found)
	if err != nil || inv.Name != "/opt/bin/fresh" || !slices.Equal(inv.Args, []string{ctx.ReportPath}) || inv.Dir != ctx.CrewDir {
		t.Fatalf("report: Argv = %+v, %v", inv, err)
	}

	const missing = "a crew's report needs the Fresh editor: brew install fresh-editor"
	for name, find := range map[string]func(string) string{
		"not found": func(string) string { return "" },
		"relative":  func(name string) string { return name },
	} {
		if inv, err := v.Argv(ctx, find); err == nil || err.Error() != missing {
			t.Errorf("%s: Argv = %+v, %v; want %q", name, inv, err, missing)
		}
	}
}
