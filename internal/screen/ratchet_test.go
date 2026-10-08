package screen_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// observeSites is every call to an Observer's Observe in the packages that
// act on a pane - send, spawn, outbox - each with the deterministic check
// that stands between the Observation and the key it leads to.
//
// The ratchet guards the plan's criterion (docs/plans/
// jev-observer-2026-10-08.md section 9): no irreversible action follows
// from Jev's Observation without passing a string compare or a highlight
// confirmation. A new call site, a moved one or a removed verifier fails
// TestEveryObserveCallSiteHasItsVerifier; the fix is a row here naming the
// check that guards the new site, never a row without one.
//
// verifiers are source texts that must each follow the call in the same
// function. When the call is in a helper that returns the reading, via
// names every function of the package that calls the helper, each with the
// verifier that must follow every one of its calls to it; a caller via does
// not name fails the test.
var observeSites = []struct {
	file, fn  string
	verifiers []string
	via       map[string]string
}{
	// The line is typed only into a composer the observer read; when an
	// observer other than the fixture read it, only while a fresh read the
	// fixture classifies still shows a composer the send types into: that
	// observer can take seconds, and the pane may have moved on.
	{file: "send/send.go", fn: "Send", verifiers: []string{"stillTypeable("}},
	// A startup dialog is answered only by answerStartupDialog, below.
	{file: "spawn/settle.go", fn: "settleStartupPrompt", verifiers: []string{"answerStartupDialog("}},
	// The confirm key goes only when the highlight is on the option the
	// harness's StartupAnswer confirms (the fixture's Highlight, from
	// StartupTargetSelected), and a fresh read classified by the profile
	// itself still shows it there.
	{file: "spawn/settle.go", fn: "answerStartupDialog", verifiers: []string{"observed.Highlight != answer.Target",
		"!screens.StartupTargetSelected(dialog, plain)"}},
	// The stow line itself goes through send.Send; the restart after it
	// waits for the turn's end, and on the composer alone only after two
	// looks in a row the fixture reads as empty (empty: Deterministic, which
	// Jev cannot make empty).
	{file: "outbox/stow.go", fn: "composer", via: map[string]string{"Stow": "empty(c)"}},
}

// observeCall is one Observe call: the function it is in and its offset.
type observeCall struct {
	file, fn string
	offset   int
}

func TestEveryObserveCallSiteHasItsVerifier(t *testing.T) {
	files := map[string]*parsedFile{}
	var calls []observeCall
	for _, pkg := range []string{"send", "spawn", "outbox"} {
		names, err := filepath.Glob(filepath.Join("..", pkg, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			f := parse(t, name)
			rel := pkg + "/" + filepath.Base(name)
			files[rel] = f
			for _, fn := range f.funcs {
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Observe" {
							calls = append(calls, observeCall{file: rel, fn: fn.Name.Name, offset: f.fset.Position(call.Pos()).Offset})
						}
					}
					return true
				})
			}
		}
	}

	var got, want []string
	for _, c := range calls {
		got = append(got, c.file+" "+c.fn)
	}
	for _, s := range observeSites {
		want = append(want, s.file+" "+s.fn)
	}
	sort.Strings(got)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Observe is called at %d sites %q; the ratchet names %d %q. Name the deterministic check after each new site in observeSites",
			len(got), got, len(want), want)
	}

	for _, site := range observeSites {
		f := files[site.file]
		if site.via == nil {
			for _, c := range calls {
				if c.file == site.file && c.fn == site.fn {
					for _, v := range site.verifiers {
						f.mustFollow(t, site.fn, c.offset, v)
					}
				}
			}
			continue
		}
		// Every function of the helper's package that calls it.
		pkg := strings.Split(site.file, "/")[0] + "/"
		callers := map[string]bool{}
		for rel, pf := range files {
			if !strings.HasPrefix(rel, pkg) {
				continue
			}
			for name, fn := range pf.funcs {
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					if call, ok := node.(*ast.CallExpr); ok && callsNamed(call, site.fn) {
						callers[name] = true
						verifier, named := site.via[name]
						if !named {
							t.Errorf("%s: %s calls %s, which reads the pane, and observeSites names no verifier for it", rel, name, site.fn)
						} else {
							pf.mustFollow(t, name, pf.fset.Position(call.Pos()).Offset, verifier)
						}
					}
					return true
				})
			}
		}
		for name := range site.via {
			if !callers[name] {
				t.Errorf("%s: %s does not call %s", site.file, name, site.fn)
			}
		}
	}
}

// callsNamed reports whether call calls a function or method named name.
func callsNamed(call *ast.CallExpr, name string) bool {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name == name
	case *ast.SelectorExpr:
		return f.Sel.Name == name
	}
	return false
}

type parsedFile struct {
	name  string
	src   []byte
	fset  *token.FileSet
	funcs map[string]*ast.FuncDecl
}

func parse(t *testing.T, name string) *parsedFile {
	t.Helper()
	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := &parsedFile{name: name, src: src, fset: fset, funcs: map[string]*ast.FuncDecl{}}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			f.funcs[fn.Name.Name] = fn
		}
	}
	return f
}

// mustFollow fails unless verifier appears in fn's body after offset.
func (f *parsedFile) mustFollow(t *testing.T, fn string, offset int, verifier string) {
	t.Helper()
	end := f.fset.Position(f.funcs[fn].Body.End()).Offset
	if !strings.Contains(string(f.src[offset:end]), verifier) {
		line := f.fset.Position(f.fset.File(f.funcs[fn].Pos()).Pos(offset)).Line
		t.Errorf("%s:%d: %s reads the pane and no %q follows it", f.name, line, fn, verifier)
	}
}
