package store

// ReplaceMemoryFile rewrites `mate/memory.md` atomically, through the
// workspace boundary. Its one caller is `matev2 remember`, which reads the
// file, adds one entry and writes the whole result back; the Mate edits the
// file itself when it curates, and the app's start never overwrites it
// (internal/mateassets.Write).
func (w *Workspace) ReplaceMemoryFile(project string, data []byte) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	return w.writeFile(w.MemoryFile(project), data, 0o644)
}
