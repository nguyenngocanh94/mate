package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ArchiveMateSession retains transcript provenance before replacing mate.meta,
// so a reindex after context refresh can still reconstruct the old spend.
func (w *Workspace) ArchiveMateSession(project string, meta map[string]string) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	id := meta["session_id"]
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\n\r") {
		return fmt.Errorf("invalid Mate session id")
	}
	path := filepath.Join(w.MateDir(project), "sessions", id+".meta")
	data, err := encodeMeta(meta)
	if err != nil {
		return err
	}
	return w.writeFile(path, data, 0644)
}

func (w *Workspace) MateSessionArchives(project string) ([]map[string]string, error) {
	if err := ValidateProjectName(project); err != nil {
		return nil, err
	}
	dir, err := w.resolve(filepath.Join(w.MateDir(project), "sessions"))
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []map[string]string
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".meta") {
			continue
		}
		path, err := w.resolve(filepath.Join(dir, file.Name()))
		if err != nil {
			return nil, err
		}
		meta, err := ReadMeta(path)
		if err != nil {
			return nil, err
		}
		out = append(out, meta)
	}
	return out, nil
}

// FreezeMateSession must be called only after the runtime confirmed the agent
// gone. Its immutable transcript snapshot lets the ledger count the final
// message group without guessing whether a live writer has finished it.
func (w *Workspace) FreezeMateSession(project string, meta map[string]string) error {
	copyMeta := make(map[string]string, len(meta)+2)
	for key, value := range meta {
		copyMeta[key] = value
	}
	if source := meta["transcript"]; source != "" {
		// Validate the ID before using it as a filename.
		id := meta["session_id"]
		if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\n\r") {
			return fmt.Errorf("invalid Mate session id")
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		path := filepath.Join(w.MateDir(project), "sessions", id+".jsonl")
		if err := w.writeFile(path, data, 0600); err != nil {
			return err
		}
		copyMeta["original_transcript"], copyMeta["transcript"], copyMeta["finalized"] = source, path, "true"
	}
	return w.ArchiveMateSession(project, copyMeta)
}
