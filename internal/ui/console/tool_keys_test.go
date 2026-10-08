package console

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/tool"
)

// A third tool needs no Console change: a key it binds that the Console
// does not own is pressed through to it from every pane, and the key line
// and the key sheet offer it in its own words (plan section 10).
func TestAThirdToolKeyIsRoutedFromEveryPane(t *testing.T) {
	tree := sampleTree()
	tree.Tools = append(tree.Tools, query.ToolBinding{Key: "x", Label: "notes", Scope: "project", Role: "notes", Tool: "notebook"})
	for _, focus := range []pane{paneList, paneDetail} {
		view := &toolSpy{}
		m := loaded(t, tree, nil).WithToolView(view.fn)
		m.focus = focus
		want := m.modeTarget()
		m, cmd := send(t, m, key("x"))
		if cmd == nil {
			t.Fatalf("focus %v: x returned no Cmd: %+v", focus, m.msg)
		}
		cmd()
		if len(view.calls) != 1 || view.keys[0] != "x" || view.calls[0] != (StageTarget{ProjectID: want}) {
			t.Fatalf("focus %v: calls %q %+v, want x on %s", focus, view.keys, view.calls, want)
		}
	}
	m := loaded(t, tree, nil)
	if !slices.Contains(m.keyHints(), keyHint{"x", "notes"}) {
		t.Fatalf("key line = %+v, want x notes", m.keyHints())
	}
	if !slices.Contains(m.keyRows(), [2]string{"x", "project notes"}) {
		t.Fatalf("key rows = %q, want x", m.keyRows())
	}

	m, _, _ = bxPayments(t, 80, 36)
	m = bxFocusBox(t, m)
	m.tree.Tools = append(m.tree.Tools, query.ToolBinding{Key: "x", Label: "notes", Scope: "project", Role: "notes", Tool: "notebook"})
	view := &toolSpy{}
	m = m.WithToolView(view.fn)
	if _, cmd := send(t, m, key("x")); cmd == nil {
		t.Fatal("x from the box returned no Cmd")
	} else {
		cmd()
	}
	if len(view.calls) != 1 || view.keys[0] != "x" {
		t.Fatalf("x from the box opened %q %+v", view.keys, view.calls)
	}
}

// keyHandlers are the functions that handle a key on the tree.
var keyHandlers = []string{"onKey", "onListKey", "onDetailKey", "onBoxKey"}

// TestOwnedKeysAreTheHandlersKeys holds three lists equal: ownedKeys, the
// keys the tree's handlers compare `key` with in their source, and
// tool.ConsoleKeys, the keys the tool registry refuses a binding on.
func TestOwnedKeysAreTheHandlersKeys(t *testing.T) {
	fset := token.NewFileSet()
	handled := map[string]bool{}
	found := map[string]bool{}
	for _, file := range []string{"keys.go", "box_keys.go"} {
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !slices.Contains(keyHandlers, fn.Name.Name) {
				continue
			}
			found[fn.Name.Name] = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SwitchStmt:
					if isKeyIdent(n.Tag) {
						for _, c := range n.Body.List {
							for _, e := range c.(*ast.CaseClause).List {
								if s, ok := stringLit(e); ok {
									handled[s] = true
								}
							}
						}
					}
				case *ast.BinaryExpr:
					if n.Op != token.EQL {
						break
					}
					for _, pair := range [][2]ast.Expr{{n.X, n.Y}, {n.Y, n.X}} {
						if s, ok := stringLit(pair[1]); ok && isKeyIdent(pair[0]) {
							handled[s] = true
						}
					}
				}
				return true
			})
		}
	}
	for _, h := range keyHandlers {
		if !found[h] {
			t.Fatalf("key handler %s not found in keys.go or box_keys.go", h)
		}
	}
	owned := slices.Sorted(maps.Keys(ownedKeys))
	if got := slices.Sorted(maps.Keys(handled)); !slices.Equal(got, owned) {
		t.Errorf("the handlers switch on %q, ownedKeys is %q", got, owned)
	}
	if reserved := slices.Sorted(slices.Values(tool.ConsoleKeys())); !slices.Equal(reserved, owned) {
		t.Errorf("tool.ConsoleKeys is %q, ownedKeys is %q", reserved, owned)
	}
}

func isKeyIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "key"
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}
