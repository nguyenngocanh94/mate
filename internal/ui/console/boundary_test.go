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
// LoadFunc and an attach can only have gone through the `mate attach`
// subprocess (G6 gate, ADR 0010).
//
// Direct imports only. internal/query does import internal/persistence -
// that is what makes it the read boundary - so a transitive check would
// forbid the one dependency this package is supposed to have.
var forbiddenImports = []string{
	"github.com/nguyenngocanh94/matev2/internal/persistence",
	"github.com/nguyenngocanh94/matev2/internal/runtime",
	"github.com/nguyenngocanh94/matev2/internal/orchestration",
}

func TestConsoleImportsNeitherPersistenceNorRuntime(t *testing.T) {
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
