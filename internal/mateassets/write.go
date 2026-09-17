package mateassets

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nguyenngocanh94/matev2/assets"
)

// Write renders AGENTS.md and CLAUDE.md into dir, replacing whatever is
// there, and creates memory.md and backlog.md with a one-line header only if
// they do not already exist. dir must already exist; Write does not create
// it. It never touches an existing memory.md or backlog.md.
func Write(dir string, p Params) error {
	agentsMD, err := Render(p)
	if err != nil {
		return err
	}
	claudeMD, err := assets.FS.ReadFile("mate/CLAUDE.md")
	if err != nil {
		return fmt.Errorf("mateassets: read CLAUDE.md template: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, "AGENTS.md"), agentsMD); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "CLAUDE.md"), claudeMD); err != nil {
		return err
	}
	if err := ensureFile(filepath.Join(dir, "memory.md"), "# Memory\n"); err != nil {
		return err
	}
	if err := ensureFile(filepath.Join(dir, "backlog.md"), "# Backlog\n"); err != nil {
		return err
	}
	return writeSkills(dir, p)
}

// ClaudeSkillsDir is where Claude Code discovers skills relative to its own
// working directory: `<cwd>/.claude/skills/<name>/SKILL.md`.
const ClaudeSkillsDir = ".claude/skills"

// writeSkills installs every SkillNames entry under dir, replacing whatever
// is there. Skills are generated content like the manual, not the Mate's own
// memory, so they are rewritten on every start rather than preserved.
func writeSkills(dir string, p Params) error {
	for _, name := range SkillNames {
		data, err := RenderSkill(name, p)
		if err != nil {
			return err
		}
		skillDir := filepath.Join(dir, filepath.FromSlash(ClaudeSkillsDir), name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(skillDir, "SKILL.md"), data); err != nil {
			return err
		}
	}
	return nil
}

// ensureFile writes header to path only if path does not already exist. An
// existing file, of any content, is left untouched.
func ensureFile(path, header string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeAtomic(path, []byte(header))
}

// writeAtomic writes data to path via a temp file in the same directory plus
// rename, so a reader never observes a partial write.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
