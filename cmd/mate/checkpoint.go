package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func cmdCheckpoint(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("checkpoint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return newUsageError("usage: mate checkpoint <project> <nonce> [--workspace <dir>]")
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	if err := writeCheckpoint(w, fs.Arg(0), fs.Arg(1)); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "checkpoint recorded; end your turn")
	return nil
}

func refreshMeta(w *store.Workspace, project, name string) (string, error) {
	if err := requireProject(w, project); err != nil {
		return "", err
	}
	return w.Resolve(filepath.Join(w.MateDir(project), "."+name+".meta"))
}

func checkpointFiles(w *store.Workspace, project string) (map[string]string, error) {
	out := map[string]string{}
	for key, path := range map[string]string{"memory": w.MemoryFile(project), "backlog": w.BacklogFile(project), "project": w.ProjectDoc(project)} {
		path, err := w.Resolve(path)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if key == "backlog" {
			for _, sec := range memory.BacklogSections {
				if !strings.Contains("\n"+string(data), "\n## "+sec+"\n") {
					return nil, fmt.Errorf("checkpoint: backlog lacks %s", sec)
				}
			}
			crews, err := spawn.ListCrews(w, project)
			if err != nil {
				return nil, err
			}
			// Crew IDs must still be discoverable after recall. Content correctness
			// remains the Mate's stow responsibility; hashes prove this exact receipt.
			_, _, inFlight := splitBacklog(string(data))
			for _, crew := range crews {
				if !crew.Closed && !slices.Contains(inFlight, crew.Crew) {
					return nil, fmt.Errorf("checkpoint: open crew %s absent from backlog", crew.Crew)
				}
			}
		}
		out[key] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	return out, nil
}

func writeCheckpoint(w *store.Workspace, project, nonce string) error {
	path, err := refreshMeta(w, project, "refresh-request")
	if err != nil {
		return err
	}
	req, err := store.ReadMeta(path)
	if err != nil {
		return err
	}
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		return err
	}
	if nonce == "" || req["nonce"] != nonce || req["session"] != meta[spawn.MetaSessionID] {
		return fmt.Errorf("checkpoint: no matching refresh request for this session")
	}
	files, err := checkpointFiles(w, project)
	if err != nil {
		return err
	}
	files["nonce"], files["session"] = nonce, req["session"]
	path, err = refreshMeta(w, project, "checkpoint")
	if err != nil {
		return err
	}
	return store.WriteMeta(path, files)
}

func verifyCheckpoint(w *store.Workspace, project, nonce, session string) error {
	path, err := refreshMeta(w, project, "checkpoint")
	if err != nil {
		return err
	}
	receipt, err := store.ReadMeta(path)
	if err != nil {
		return err
	}
	if receipt["nonce"] != nonce || receipt["session"] != session {
		return fmt.Errorf("refresh: no checkpoint receipt for this turn")
	}
	files, err := checkpointFiles(w, project)
	if err != nil {
		return err
	}
	for key, hash := range files {
		if receipt[key] != hash {
			return fmt.Errorf("refresh: %s changed after checkpoint; keeping the current session", key)
		}
	}
	return nil
}
