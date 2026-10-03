package diagnostics

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// A skill is loaded in one of two ways the recording can show. Claude Code
// has a tool for it, whose input names the skill. Every other harness, and
// Claude Code when it is told a path, reads the skill's SKILL.md: Codex with
// a shell command, pi with its read tool. The skill is then named by the
// directory that holds the file, which is how every harness lays skills out
// (<skills dir>/<name>/SKILL.md).
//
// Either is a load, not a use: nothing in a transcript says the agent went
// on to follow what it read.

const (
	skillViaTool = "tool"
	skillViaRead = "read"
	// skillUnnamed stands in for a skill tool call whose input the recording
	// kept no readable name for. The call still happened.
	skillUnnamed = "(name not recorded)"
)

type skillRef struct{ name, path, via string }

// skillTool reports the harness's own skill tool by its lowercased name.
func skillTool(tool string) bool { return tool == "skill" || strings.HasSuffix(tool, ".skill") }

var (
	skillFile = regexp.MustCompile(`(?i)(?:^|/)([^/]+)/skill\.md$`)
	// skillInput reads the name out of a skill tool's input. The ledger
	// keeps that input as text and may have cut it, so it is matched rather
	// than decoded.
	skillInput = regexp.MustCompile(`"(?:skill|name)"\s*:\s*"((?:[^"\\]|\\.)+)"`)
)

// skillAt names the skill a path is the SKILL.md of.
func skillAt(path string) (string, bool) {
	m := skillFile.FindStringSubmatch(filepath.ToSlash(strings.TrimSpace(path)))
	if m == nil || m[1] == "." || m[1] == ".." {
		return "", false
	}
	return m[1], true
}

// skillLoads lists the skills one execution loaded, each once. It reads the
// same literal inputs executionWork does and guesses nothing: a search over
// a skills directory, an edit of a SKILL.md and a file written to one are
// not loads.
func skillLoads(e Execution) []skillRef {
	tool := strings.ToLower(e.Tool)
	if skillTool(tool) {
		name := skillUnnamed
		if m := skillInput.FindStringSubmatch(e.Command); m != nil {
			name = m[1]
		}
		return []skillRef{{name: name, via: skillViaTool}}
	}
	paths := []string{}
	switch {
	case waitTool(tool) || strings.Contains(tool, "apply_patch"):
	case toolWork(tool) == "":
		commands := []string{e.Command}
		var object map[string]json.RawMessage
		if e.IsWrapper || json.Unmarshal([]byte(e.Command), &object) == nil {
			commands = literalCommands(e.Command)
		}
		for _, command := range commands {
			paths = append(paths, shellSkillFiles(command, 0)...)
		}
	case tool == "read" || strings.HasSuffix(tool, ".read") || strings.Contains(tool, "read_file"):
		var input map[string]json.RawMessage
		if json.Unmarshal([]byte(e.Command), &input) == nil {
			for _, field := range []string{"file_path", "path", "filePath"} {
				var path string
				if json.Unmarshal(input[field], &path) == nil && path != "" {
					paths = append(paths, path)
				}
			}
		}
		if len(paths) == 0 {
			paths = append(paths, e.Target)
		}
	}
	out, seen := []skillRef{}, map[string]bool{}
	for _, path := range paths {
		if name, ok := skillAt(path); ok && !seen[name] {
			seen[name] = true
			out = append(out, skillRef{name: name, path: path, via: skillViaRead})
		}
	}
	return out
}

// skillReaders are the commands that print a file whole or in part. A
// search (rg, grep, find) names files without loading one.
var skillReaders = map[string]bool{"cat": true, "head": true, "tail": true, "nl": true, "sed": true}

// shellSkillFiles lists the SKILL.md files a shell command reads.
func shellSkillFiles(command string, depth int) []string {
	if depth > 2 {
		return nil
	}
	out := []string{}
	for _, part := range shellParts(command) {
		words := part.words
		for len(words) > 0 && (strings.Contains(words[0], "=") || words[0] == "env" || words[0] == "command" || words[0] == "exec" || words[0] == "sudo") {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		bin, args := strings.ToLower(filepath.Base(words[0])), words[1:]
		if (bin == "bash" || bin == "zsh" || bin == "sh") && len(args) >= 2 && (args[0] == "-c" || args[0] == "-lc") {
			out = append(out, shellSkillFiles(args[1], depth+1)...)
			continue
		}
		// What commandWork calls an edit or a write is not a read.
		if !skillReaders[bin] || commandWork(bin, args) != "instructions" {
			continue
		}
		for _, arg := range args {
			if _, ok := skillAt(arg); ok {
				out = append(out, arg)
			}
		}
	}
	return out
}

// makeSkills records every skill load the executions show, and what each
// model call loaded. A wrapper whose commands its native children record is
// skipped, so a load seen from both sides is counted once.
func (p *projection) makeSkills() {
	index := map[string]int{}
	for _, e := range p.out.Executions {
		if e.IsWrapper && p.parents[e.ID] {
			continue
		}
		for _, ref := range skillLoads(e) {
			i, ok := index[ref.name]
			if !ok {
				i = len(p.out.Skills)
				index[ref.name] = i
				p.out.Skills = append(p.out.Skills, SkillUse{Name: ref.name, Loads: []SkillLoad{}})
			}
			use := &p.out.Skills[i]
			use.Count++
			use.Loads = append(use.Loads, SkillLoad{At: e.StartedAt, PromptID: e.PromptID, CallID: e.CallID, ExecutionID: e.ID,
				Via: ref.via, Path: normalizeTarget(ref.path, p.opts.Worktree)})
			if e.CallID != "" {
				p.callSkills[e.CallID] = appendUnique(p.callSkills[e.CallID], ref.name)
			}
		}
	}
}
