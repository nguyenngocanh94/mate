package harnesstest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// The fixtures below are shared by the tests of every harness package and
// of the catalog, which lay out and build launches the same way.

// SessionID is the session id PrepareFixture mints.
const SessionID = "11111111-2222-3333-4444-555555555555"

// PrepareFixture is one agent's directories: its cwd, its state directory
// and its instructions, already on disk where the role keeps them.
func PrepareFixture(t *testing.T, role harness.AgentRole) harness.PrepareRequest {
	t.Helper()
	cwd, state := t.TempDir(), t.TempDir()
	instructions := filepath.Join(state, "brief.md")
	if role == harness.RoleMate {
		instructions = filepath.Join(cwd, "AGENTS.md")
	}
	if err := os.WriteFile(instructions, []byte("# instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return harness.PrepareRequest{
		Role: role, Cwd: cwd, StateDir: state, ContextPath: instructions,
		Binary: "/usr/local/bin/mate", NewSessionID: func() string { return SessionID },
	}
}

// WritePrepared lays the files down the way spawn does.
func WritePrepared(t *testing.T, prep harness.Prepared) {
	t.Helper()
	for _, f := range prep.Files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.Path, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// FileData is the content Prepare names for path, or a test failure listing
// what it does name.
func FileData(t *testing.T, prep harness.Prepared, path string) []byte {
	t.Helper()
	for _, f := range prep.Files {
		if f.Path == path {
			return f.Data
		}
	}
	var names []string
	for _, f := range prep.Files {
		names = append(names, f.Path)
	}
	t.Fatalf("Prepare names no %s; it names %v", path, names)
	return nil
}

// WriteAbs writes body to dir/name and returns that absolute path.
func WriteAbs(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
