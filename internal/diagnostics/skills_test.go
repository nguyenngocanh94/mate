package diagnostics

import (
	"reflect"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

func skillNames(refs []skillRef) []string {
	out := []string{}
	for _, ref := range refs {
		out = append(out, ref.via+":"+ref.name)
	}
	return out
}

// Each harness loads a skill its own way: Claude Code through its Skill
// tool, Codex by reading the file in a shell, pi with its read tool or bash.
func TestSkillLoadsAreNamedFromTheToolInputOrTheSkillFile(t *testing.T) {
	cases := []struct {
		name string
		e    Execution
		want []string
	}{
		{"claude skill tool", Execution{Tool: "Skill", Command: `{"skill":"token-review","args":"shop"}`}, []string{"tool:token-review"}},
		{"claude plugin skill", Execution{Tool: "Skill", Command: `{"skill":"anthropic-skills:pdf"}`}, []string{"tool:anthropic-skills:pdf"}},
		{"input cut by the ledger", Execution{Tool: "Skill", Command: `{"skill":"brief-writing","args":"a very long argument that was cu…`}, []string{"tool:brief-writing"}},
		{"input not recorded", Execution{Tool: "Skill"}, []string{"tool:" + skillUnnamed}},
		{"claude read", Execution{Tool: "Read", Target: "skills/stow/SKILL.md", Command: `{"file_path":"/w/.claude/skills/stow/SKILL.md"}`}, []string{"read:stow"}},
		{"claude read, target only", Execution{Tool: "Read", Target: ".claude/skills/stow/SKILL.md"}, []string{"read:stow"}},
		{"claude bash", Execution{Tool: "Bash", Command: "cat .claude/skills/decision-authority/SKILL.md"}, []string{"read:decision-authority"}},
		{"codex native", Execution{Tool: "exec_command", Command: "sed -n '1,260p' /Users/x/.codex/skills/terminal-browser/SKILL.md"}, []string{"read:terminal-browser"}},
		{"codex wrapper", Execution{Tool: "exec", IsWrapper: true, Command: `const r = await tools.exec_command({"cmd":"cat .claude/skills/stow/SKILL.md && git status"})`}, []string{"read:stow"}},
		{"pi read", Execution{Tool: "read", Command: `{"path":"/w/.claude/skills/crew-dispatch/SKILL.md"}`}, []string{"read:crew-dispatch"}},
		{"pi bash", Execution{Tool: "bash", Command: `{"command":"head -80 ~/.agents/skills/lavish/SKILL.md"}`}, []string{"read:lavish"}},
		{"two skills, one of them twice", Execution{Tool: "exec_command", Command: "cat skills/a/SKILL.md skills/b/SKILL.md\nsed -n '1,20p' skills/a/SKILL.md"}, []string{"read:a", "read:b"}},
		{"nested shell", Execution{Tool: "exec_command", Command: `bash -lc "cat skills/stow/SKILL.md"`}, []string{"read:stow"}},

		{"search over skills", Execution{Tool: "exec_command", Command: "rg -n 'restart' .claude/skills/stow/SKILL.md"}, nil},
		{"locating skills", Execution{Tool: "exec_command", Command: "find .claude/skills -name SKILL.md\nls .claude/skills/stow/SKILL.md"}, nil},
		{"editing a skill", Execution{Tool: "Edit", Target: ".claude/skills/stow/SKILL.md", Command: `{"file_path":"/w/.claude/skills/stow/SKILL.md"}`}, nil},
		{"writing a skill", Execution{Tool: "Write", Target: "skills/new/SKILL.md"}, nil},
		{"shell write", Execution{Tool: "exec_command", Command: "cat > skills/new/SKILL.md <<'EOF'\nbody\nEOF"}, nil},
		{"in-place edit", Execution{Tool: "exec_command", Command: "sed -i '' 's/a/b/' skills/stow/SKILL.md"}, nil},
		{"patching a skill", Execution{Tool: "exec", IsWrapper: true, Command: `await tools.apply_patch("*** Begin Patch\n*** Update File: skills/stow/SKILL.md\n@@\n-a\n+b\n*** End Patch\n")`}, nil},
		{"a template is not the file", Execution{Tool: "Read", Target: "assets/mate/skills/stow/SKILL.md.tmpl"}, nil},
		{"no directory names it", Execution{Tool: "Read", Target: "SKILL.md"}, nil},
		{"grep tool", Execution{Tool: "Grep", Target: "restart in .claude/skills/stow/SKILL.md"}, nil},
		{"mentioned, not read", Execution{Tool: "exec_command", Command: `echo "see skills/stow/SKILL.md"`}, nil},
	}
	for _, c := range cases {
		got := skillNames(skillLoads(c.e))
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: loads %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSkillsCountEveryLoadAndSitOnTheStepThatLoadedThem(t *testing.T) {
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p", 0)}}
	actions := []struct{ tool, summary string }{
		{"Skill", `{"skill":"token-review"}`},
		{"Bash", "go test ./..."},
		{"Skill", `{"skill":"token-review"}`},
		{"Read", `{"file_path":"/w/.claude/skills/stow/SKILL.md"}`},
	}
	for i, a := range actions {
		call := fixtureCall(i, "p")
		in.Calls = append(in.Calls, call)
		in.Actions = append(in.Actions, Action{ID: call.ID + "#action", SessionID: "s", CallID: call.ID, Tool: a.tool, Summary: a.summary, StartedAt: call.StartedAt, EndedAt: call.EndedAt})
	}
	p := Project(in, Options{Closed: true, Worktree: "/w"})
	if len(p.Skills) != 2 || p.Skills[0].Name != "token-review" || p.Skills[0].Count != 2 || p.Skills[1].Name != "stow" || p.Skills[1].Count != 1 {
		t.Fatalf("skills %+v", p.Skills)
	}
	first := p.Skills[0].Loads[0]
	if first.CallID != in.Calls[0].ID || first.Via != "tool" || first.PromptID != p.PromptTurns[0].ID || first.At == "" {
		t.Fatalf("load does not say where it happened: %+v", first)
	}
	if load := p.Skills[1].Loads[0]; load.Via != "read" || load.Path != ".claude/skills/stow/SKILL.md" {
		t.Fatalf("read load %+v", load)
	}
	steps := p.PromptTurns[0].Overview.Steps
	if got := stepKinds(p.PromptTurns[0].Overview); !reflect.DeepEqual(got, []string{"instructions", "test", "instructions"}) {
		t.Fatalf("steps %v", got)
	}
	if !reflect.DeepEqual(steps[0].Skills, []string{"token-review"}) || len(steps[1].Skills) != 0 || !reflect.DeepEqual(steps[2].Skills, []string{"token-review", "stow"}) {
		t.Fatalf("skills on steps: %v / %v / %v", steps[0].Skills, steps[1].Skills, steps[2].Skills)
	}
}

// Codex shows one read twice: in the wrapper's script and as the native
// command it ran. It is one load.
func TestSkillReadSeenThroughWrapperAndNativeCommandIsOneLoad(t *testing.T) {
	native := execution("n", 2, 3)
	native.Command = "sed -n '1,260p' /Users/x/.agents/skills/lavish/SKILL.md"
	facts := []telemetry.Fact{promptFact("s", "p", 0), native}
	facts = append(facts, wrapperFacts("w0", 0, 1, 4, `const r = await tools.exec_command({"cmd":"sed -n '1,260p' /Users/x/.agents/skills/lavish/SKILL.md"})`)...)
	call := fixtureCall(0, "p")
	p := Project(Input{Calls: []Call{call}, Facts: facts}, Options{Closed: true})
	if len(p.Skills) != 1 || p.Skills[0].Name != "lavish" || p.Skills[0].Count != 1 || p.Skills[0].Loads[0].CallID != call.ID {
		t.Fatalf("skills %+v", p.Skills)
	}
	if steps := p.PromptTurns[0].Overview.Steps; len(steps) != 1 || steps[0].Kind != "instructions" || !reflect.DeepEqual(steps[0].Skills, []string{"lavish"}) {
		t.Fatalf("steps %+v", steps)
	}
}

func TestNoSkillsIsAnEmptyListNotAMissingOne(t *testing.T) {
	p := Project(Input{Calls: []Call{fixtureCall(0, "p")}}, Options{Closed: true})
	if p.Skills == nil || len(p.Skills) != 0 {
		t.Fatalf("skills %#v", p.Skills)
	}
}
