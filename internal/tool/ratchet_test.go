package tool_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The tool ratchet counts how much of the code outside internal/tool still
// knows an outside tool - Beads (bd, bv) or the Fresh editor - by name
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, sections 5-7). It
// may only go down: the plan's later PRs move that knowledge behind the
// tool registry.
//
// Two kinds of reference are counted, in every non-test .go file of the
// module outside internal/tool/...:
//
//   - literal: a string literal equal to a tool name (ratchetToolNames), or
//     carrying one as a whole path segment (".beads/metadata.json"). Never
//     a substring: "bd" must not hit "bdd", ".beads" must not hit
//     ".beads-old", "fresh" must not hit "refresh".
//   - identifier: a use of an exported identifier of package
//     internal/beads (beads.Open, beads.Runner, beads.Issue{}), under
//     whatever name the file imports it as.
//
// The mechanism is internal/harness/ratchet_test.go's, which is a test file
// of another package and cannot be imported: moduleRoot, declName and
// recvTypeName are copied from it, and ratchetFiles and the allowlist
// follow it.

// toolRatchetCeiling is the count recorded when the ratchet was added. The
// test fails if the count rises above it. When the count falls, lower this
// constant to the new count in the same change, so the ground gained is
// kept.
const toolRatchetCeiling = 6

// ratchetToolNames is every spelling of a tool name a literal can carry:
// the executables, Beads' directory, and Fresh's Homebrew formula.
var ratchetToolNames = []string{"bd", "bv", "fresh", ".beads", "fresh-editor"}

const beadsImportPath = "github.com/nguyenngocanh94/mate/internal/beads"

// ratchetSkipDirs are module-relative directories never scanned, each with
// its reason.
var ratchetSkipDirs = []struct{ dir, reason string }{
	{"internal/tool", "the tool registry is where tool names belong"},
	{"internal/beads", "the Beads wrapper itself; the plan deletes it in PR 5"},
	{"docs", "documentation, not code"},
}

// ratchetAllow are the places allowed to carry a tool name, each with its
// reason. An entry names a file, and optionally the top-level declaration
// and the literal inside it. An entry that no longer matches anything fails
// the test, so the list cannot rot.
var ratchetAllow = []struct {
	file, decl, literal string
	reason              string
}{
	{
		file: "cmd/mate/mate.go", decl: "cmdMateStart", literal: "fresh",
		reason: "the `mate mate start --fresh` flag: a new harness session, not the Fresh editor",
	},
	{
		file: "internal/spawn/status.go", decl: "Status.Line", literal: "fresh",
		reason: "a Mate that started a new session rather than resuming, not the Fresh editor",
	},
	{
		file: "internal/quota/quota.go", decl: "Reading.Line", literal: "fresh",
		reason: "a quota reading's freshness status, not the Fresh editor",
	},
}

type ratchetHit struct {
	file string // module-relative, slash-separated
	line int
	kind string // "identifier" or "literal"
	what string
	decl string // enclosing top-level declaration
}

func (h ratchetHit) String() string {
	return fmt.Sprintf("%s:%d: %s %s", h.file, h.line, h.kind, h.what)
}

func TestToolNameRatchet(t *testing.T) {
	root := moduleRoot(t)
	var hits []ratchetHit
	allowUsed := make([]int, len(ratchetAllow))
	for _, file := range ratchetFiles(t, root) {
		for _, h := range scanRatchetFile(t, root, file) {
			if i := ratchetAllowed(h); i >= 0 {
				allowUsed[i]++
				continue
			}
			hits = append(hits, h)
		}
	}
	for i, n := range allowUsed {
		if n == 0 {
			a := ratchetAllow[i]
			t.Errorf("ratchet allowlist entry %s %s %q matches nothing; remove it", a.file, a.decl, a.literal)
		}
	}

	byFile := map[string]int{}
	byKind := map[string]int{}
	for _, h := range hits {
		byFile[h.file]++
		byKind[h.kind]++
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	var breakdown strings.Builder
	fmt.Fprintf(&breakdown, "tool ratchet: %d references outside internal/tool (%d identifier, %d literal; ceiling %d)\n",
		len(hits), byKind["identifier"], byKind["literal"], toolRatchetCeiling)
	for _, f := range files {
		fmt.Fprintf(&breakdown, "  %-36s %d\n", f, byFile[f])
	}
	t.Log(breakdown.String())

	switch {
	case len(hits) > toolRatchetCeiling:
		var all strings.Builder
		for _, h := range hits {
			all.WriteString(h.String() + "\n")
		}
		t.Fatalf("%s\nthe tool ratchet rose from %d to %d: code outside internal/tool learned a tool by name.\n"+
			"Ask the tool registry instead (plan section 5). Every reference:\n%s",
			breakdown.String(), toolRatchetCeiling, len(hits), all.String())
	case len(hits) < toolRatchetCeiling:
		t.Logf("the tool ratchet fell from %d to %d: lower toolRatchetCeiling in %s to %d in this change",
			toolRatchetCeiling, len(hits), "internal/tool/ratchet_test.go", len(hits))
	}
}

// moduleRoot is the directory holding go.mod, found upward from the test's
// working directory. Copied from internal/harness/ratchet_test.go.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

// ratchetFiles is every non-test .go file of the module outside
// ratchetSkipDirs, skipping what the go tool skips (testdata, and
// directories starting with "." or "_"). After
// internal/harness/ratchet_test.go's.
func ratchetFiles(t *testing.T, root string) []string {
	t.Helper()
	skip := map[string]bool{}
	for _, s := range ratchetSkipDirs {
		skip[filepath.Join(root, filepath.FromSlash(s.dir))] = true
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			if skip[path] {
				return filepath.SkipDir
			}
			// A nested module is not this module.
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func scanRatchetFile(t *testing.T, root, path string) []ratchetHit {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	rel = filepath.ToSlash(rel)

	var hits []ratchetHit
	add := func(pos token.Pos, kind, what, decl string) {
		hits = append(hits, ratchetHit{file: rel, line: fset.Position(pos).Line, kind: kind, what: what, decl: decl})
	}

	// beadsName is the name this file gives package internal/beads, if it
	// imports it.
	beadsName := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == beadsImportPath {
			beadsName = "beads"
			if imp.Name != nil {
				beadsName = imp.Name.Name
			}
		}
	}
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue
		}
		decl := declName(d)
		ast.Inspect(d, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(n.Value)
				if err != nil {
					return true
				}
				for _, name := range literalToolNames(s, ratchetToolNames) {
					add(n.Pos(), "literal", strconv.Quote(name), decl)
				}
			case *ast.SelectorExpr:
				if x, ok := n.X.(*ast.Ident); ok && beadsName != "" && x.Name == beadsName && n.Sel.IsExported() {
					add(n.Pos(), "identifier", x.Name+"."+n.Sel.Name, decl)
				}
			}
			return true
		})
	}
	return hits
}

// literalToolNames returns the tool names a string literal carries: the
// whole literal, or one of its path segments, equal to a name. Never a
// substring and never a word of prose, where "fresh" is English.
func literalToolNames(s string, names []string) []string {
	var out []string
	for _, seg := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
		for _, name := range names {
			if seg == name {
				out = append(out, name)
			}
		}
	}
	return out
}

// declName names a top-level declaration: a function, Type.Method, or the
// first name a var, const or type declaration introduces. Copied from
// internal/harness/ratchet_test.go.
func declName(d ast.Decl) string {
	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil && len(d.Recv.List) > 0 {
			return recvTypeName(d.Recv.List[0].Type) + "." + d.Name.Name
		}
		return d.Name.Name
	case *ast.GenDecl:
		var names []string
		for _, s := range d.Specs {
			switch s := s.(type) {
			case *ast.ValueSpec:
				for _, n := range s.Names {
					names = append(names, n.Name)
				}
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			}
		}
		return strings.Join(names, ",")
	}
	return ""
}

// recvTypeName is copied from internal/harness/ratchet_test.go.
func recvTypeName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(e.X)
	case *ast.IndexExpr:
		return recvTypeName(e.X)
	case *ast.IndexListExpr:
		return recvTypeName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return ""
}

func ratchetAllowed(h ratchetHit) int {
	for i, a := range ratchetAllow {
		if a.file != h.file {
			continue
		}
		if a.decl != "" && a.decl != h.decl {
			continue
		}
		if a.literal != "" && (h.kind != "literal" || h.what != strconv.Quote(a.literal)) {
			continue
		}
		return i
	}
	return -1
}

// TestToolRatchetCountsBeadsIdentifiers: every use of an exported name of
// package internal/beads is counted, under whatever name the file gives
// it; the import alone is not, and neither is another package's selector.
func TestToolRatchetCountsBeadsIdentifiers(t *testing.T) {
	root := t.TempDir()
	src := `package x

import (
	"context"

	bd "github.com/nguyenngocanh94/mate/internal/beads"
)

var (
	_ bd.Runner = bd.Exec
	_           = context.Background
	_           = bd.Issue{ID: "x"}
)
`
	path := filepath.Join(root, "x.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range scanRatchetFile(t, root, path) {
		got = append(got, h.kind+" "+h.what)
	}
	want := []string{
		"identifier bd.Runner",
		"identifier bd.Exec",
		"identifier bd.Issue",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("hits:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestToolRatchetLiteralMatchingIsWholeSegment pins the matching rule the
// ratchet depends on: a name counts as the whole literal or a whole path
// segment, never as a substring or a word of prose.
func TestToolRatchetLiteralMatchingIsWholeSegment(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"bd", []string{"bd"}},
		{"bv", []string{"bv"}},
		{"fresh", []string{"fresh"}},
		{"fresh-editor", []string{"fresh-editor"}},
		{".beads", []string{".beads"}},
		{".beads/metadata.json", []string{".beads"}},
		{"/opt/homebrew/bin/fresh", []string{"fresh"}},
		{"/usr/local/bin/bd", []string{"bd"}},
		{`C:\bin\bv`, []string{"bv"}},
		{"bdd", nil},
		{"refresh", nil},
		{".beads-old", nil},
		{"beads", nil},
		{"Fresh", nil},
		{"a fresh session", nil},
		{"bd export", nil},
	} {
		got := literalToolNames(tc.in, ratchetToolNames)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("literalToolNames(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
