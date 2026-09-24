package store

import (
	"fmt"
	"os"
	"strings"
)

// CREW.md is the captain's standing rules for the people doing the work,
// after firstmate's config/brief-include.md (bin/fm-brief.sh 286-309): the
// app appends it to the end of every brief, and every other section of the
// brief takes precedence. It is a separate file from WORKSPACE.md and
// PROJECT.md because those are read by the Mate and never by a Crew.
//
// Both files are optional. Init and AddProject seed each once with nothing
// but an HTML comment explaining it, and CrewRules strips comments, so a
// seeded file adds nothing to a brief until the captain writes a rule.

const workspaceCrewDocSeed = `<!--
Standing rules for every Crew in every project of this workspace, for example
"Reproduce a bug end-to-end before you fix it."
matev2 appends whatever you write below this comment to the end of every
Crew's brief, under "Captain's standing crew rules". Every other part of the
brief takes precedence over these rules where they conflict. A project's own
CREW.md is appended after this one and wins over it.
This comment is never sent.
-->
`

func projectCrewDocSeed(project string) string {
	return fmt.Sprintf(`<!--
Standing rules for every Crew of project %s, for example
"Run make check before you hand back."
matev2 appends whatever you write below this comment to the end of every
Crew's brief for this project, after the workspace's .matev2/CREW.md, and
these win where the two conflict. Every other part of the brief takes
precedence over both.
This comment is never sent.
-->
`, project)
}

// seedOnce writes body to path unless path already exists. It never
// overwrites: from the moment it exists the file is the captain's.
func (w *Workspace) seedOnce(path, body string) error {
	if _, err := w.resolve(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	_, werr := f.WriteString(body)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// CrewRules returns the text of the workspace's and the project's CREW.md,
// HTML comments removed and surrounding blank lines trimmed. A missing file
// is an empty string, not an error.
func (w *Workspace) CrewRules(project string) (workspaceRules, projectRules string, err error) {
	if workspaceRules, err = readRules(w.WorkspaceCrewDoc()); err != nil {
		return "", "", err
	}
	if projectRules, err = readRules(w.ProjectCrewDoc(project)); err != nil {
		return "", "", err
	}
	return workspaceRules, projectRules, nil
}

func readRules(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.Trim(StripComments(string(data)), "\n \t"), nil
}

// ReplaceCrewBrief rewrites `crews/<crew>/brief.md` atomically. Its one
// caller is `matev2 brief append`, the only edit the app makes to a brief
// after spawn; the brief must already exist, because appending the
// captain's words to a crew that was never briefed has no meaning.
func (w *Workspace) ReplaceCrewBrief(project, crew string, data []byte) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	if err := ValidateCrewID(crew); err != nil {
		return err
	}
	path := w.CrewBrief(project, crew)
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return w.writeFile(path, data, 0o644)
}

// StripComments removes every `<!-- ... -->` span. An unterminated comment
// runs to the end of the text, the way a Markdown renderer would hide it.
func StripComments(text string) string {
	var b strings.Builder
	for {
		i := strings.Index(text, "<!--")
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		b.WriteString(text[:i])
		j := strings.Index(text[i+4:], "-->")
		if j < 0 {
			return b.String()
		}
		text = text[i+4+j+3:]
	}
}
