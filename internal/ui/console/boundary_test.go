package console

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// forbiddenImports is the boundary doc.go states in prose and this test
// enforces: the Console knows internal/query's read types and two
// caller-supplied closures, and nothing else. It never opens a database and
// never calls Herdr, so a snapshot on screen can only have come through
// LoadFunc and an attach can only have gone through the `matev2 attach`
// subprocess (G6 gate, ADR 0010).
//
// Direct imports only. internal/query is the read boundary: it is a pure
// DTO package, and whatever fills it (internal/query/load.go, over
// internal/store) is the caller's business, not this package's.
var forbiddenImports = []string{
	"github.com/nguyenngocanh94/matev2/internal/store",
	"github.com/nguyenngocanh94/matev2/internal/runtime",
	"github.com/nguyenngocanh94/matev2/internal/harness",
	"github.com/nguyenngocanh94/matev2/internal/process",
	// internal/box merges the message box over internal/store, and
	// internal/send types into a pane. The Console draws the box and offers
	// the three keys, but it sees only query.BoxView and reaches the pane
	// only through ActionFunc (mvp.md task 15).
	"github.com/nguyenngocanh94/matev2/internal/box",
	"github.com/nguyenngocanh94/matev2/internal/send",
	"github.com/nguyenngocanh94/matev2/internal/spawn",
	// internal/watch observes crews over Herdr and internal/autopilot types
	// into the Mate's pane. The Console draws what both produce - the health
	// column, the MODE cell's daemon indicator, the daemon's refusal on the
	// message line - and sees all of it as query DTOs that cmd/matev2 merges
	// into the snapshot (mvp.md tasks 18 and 19).
	"github.com/nguyenngocanh94/matev2/internal/watch",
	"github.com/nguyenngocanh94/matev2/internal/autopilot",
	// internal/outbox is the one sender into a Mate's composer (task 30).
	// The Console queues through ActionFunc and draws the queue's state as
	// query.BoxEntry.Assigned; it never reaches the sender itself.
	"github.com/nguyenngocanh94/matev2/internal/outbox",
}

func TestConsoleImportsNeitherStoreNorRuntime(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	checked := 0
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		checked++
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote import %s: %v", e.Name(), imp.Path.Value, err)
			}
			for _, forbidden := range forbiddenImports {
				if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
					t.Errorf("%s imports %s; the Console must reach state only through LoadFunc and attach only through AttachCmdFunc (see doc.go)", e.Name(), path)
				}
			}
			if strings.Contains(strings.ToLower(path), "herdr") {
				t.Errorf("%s imports %s; the Console never talks to Herdr", e.Name(), path)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no Go files were checked; the boundary test is vacuous")
	}
}
