package harness_test

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
	"unicode"

	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
)

// The harness ratchet counts how much of the code outside internal/harness
// still knows a harness by name (docs/plans/harness-registry-2026-09-30.md,
// section 4). It may only go down: plan PRs 1-6 move that knowledge behind
// the registry, and PR 6 brings it to zero outside the allowlist.
//
// Two kinds of reference are counted, in every non-test .go file of the
// module outside internal/harness/...:
//
//   - identifier: a use of an exported identifier of package harness whose
//     name carries a harness name as a camel-case word (harness.KindCodex,
//     harness.Claude{}, harness.CodexRolloutPath, and fields or methods such
//     as LaunchSpec.CodexHome() or LaunchPlan.ClaudeConfigDir). The set is
//     derived from package harness's source, not listed by hand.
//   - literal: a string literal equal to a harness name, or carrying one as a
//     whole token: a path segment (".claude/settings.json"), a word
//     ("--harness codex"), an env key ("CODEX_HOME="), or the namespace of a
//     dotted name ("claude.projects", "codex.compacted"). Never a substring:
//     a short name like "pi" must not hit "api", "pipe" or "spin".

// harnessRatchetCeiling is the count recorded when the ratchet was added.
// The test fails if the count rises above it. When the count falls, lower
// this constant to the new count in the same change, so the ground gained is
// kept.
const harnessRatchetCeiling = 0

// ratchetHarnessNames is every spelling of a harness name a literal can
// carry: the kinds of catalog.Default(), and the names each profile
// declares in Info() (config directory, instruction file, env keys). A new
// harness enters the ratchet by being registered.
var ratchetHarnessNames = func() []string {
	var names []string
	reg := catalog.Default()
	for _, k := range reg.Kinds() {
		names = append(names, string(k))
	}
	for _, k := range reg.Kinds() {
		p, _ := reg.Lookup(k)
		info := p.Info()
		for _, n := range append([]string{info.ConfigDir, info.InstructionFile}, info.EnvKeys...) {
			if n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}()

// ratchetKinds are the harness kinds; an identifier is harness-specific
// when one of its camel-case words is one of them.
var ratchetKinds = func() []string {
	var kinds []string
	for _, k := range catalog.Default().Kinds() {
		kinds = append(kinds, string(k))
	}
	return kinds
}()

// ratchetAllow are the places allowed to name a harness, each with its
// reason (plan section 4). An entry names a file, and optionally the
// top-level declaration and the literal inside it. An entry that no longer
// matches anything fails the test, so the list cannot rot.
var ratchetAllow = []struct {
	file, decl, literal string
	reason              string
}{
	{
		file: "internal/facts/facts.go", decl: "docNames", literal: "CLAUDE.md",
		reason: "a repository's own documentation file name, not knowledge of a harness",
	},
	{
		file: "internal/diagnostics/work.go", decl: "instructionName",
		reason: "a repository's instruction file names, matched in a command as facts.docNames lists them, not knowledge of a harness",
	},
	{
		file:   "internal/dispatch/builtin.go",
		reason: "the captain's model and effort policy per task kind; the registry validates each row when it is read",
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

func (h ratchetHit) pkg() string { return filepath.ToSlash(filepath.Dir(h.file)) }

func TestHarnessNameRatchet(t *testing.T) {
	root := moduleRoot(t)
	top, members := harnessSpecificIdentifiers(t, filepath.Join(root, "internal", "harness"))
	if len(top) == 0 || len(members) == 0 {
		t.Fatalf("found %d top-level and %d member harness-specific identifiers in package harness; the scan is broken", len(top), len(members))
	}

	var hits []ratchetHit
	allowUsed := make([]int, len(ratchetAllow))
	for _, file := range ratchetFiles(t, root) {
		for _, h := range scanRatchetFile(t, root, file, top, members) {
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

	byPkg := map[string]int{}
	byKind := map[string]int{}
	for _, h := range hits {
		byPkg[h.pkg()]++
		byKind[h.kind]++
	}
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	var breakdown strings.Builder
	fmt.Fprintf(&breakdown, "harness ratchet: %d references outside internal/harness (%d identifier, %d literal; ceiling %d)\n",
		len(hits), byKind["identifier"], byKind["literal"], harnessRatchetCeiling)
	for _, p := range pkgs {
		fmt.Fprintf(&breakdown, "  %-28s %d\n", p, byPkg[p])
	}
	t.Log(breakdown.String())

	switch {
	case len(hits) > harnessRatchetCeiling:
		var all strings.Builder
		for _, h := range hits {
			all.WriteString(h.String() + "\n")
		}
		t.Fatalf("%s\nthe harness ratchet rose from %d to %d: code outside internal/harness learned a harness by name.\n"+
			"Ask the harness profile instead (plan section 3). Every reference:\n%s",
			breakdown.String(), harnessRatchetCeiling, len(hits), all.String())
	case len(hits) < harnessRatchetCeiling:
		t.Logf("the harness ratchet fell from %d to %d: lower harnessRatchetCeiling in %s to %d in this change",
			harnessRatchetCeiling, len(hits), "internal/harness/ratchet_test.go", len(hits))
	}
}

// moduleRoot is the directory holding go.mod, found upward from the test's
// working directory.
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

// harnessSpecificIdentifiers reads package harness's own source (not its
// subpackages, not its tests) and returns its exported harness-specific
// names: top-level declarations, and struct fields and methods.
func harnessSpecificIdentifiers(t *testing.T, dir string) (top, members map[string]bool) {
	t.Helper()
	top, members = map[string]bool{}, map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		specific := func(id *ast.Ident) bool { return id.IsExported() && namesAHarness(id.Name, ratchetKinds) }
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if !specific(d.Name) {
					continue
				}
				if d.Recv != nil {
					members[d.Name.Name] = true
				} else {
					top[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if specific(s.Name) {
							top[s.Name.Name] = true
						}
						if st, ok := s.Type.(*ast.StructType); ok {
							for _, field := range st.Fields.List {
								for _, n := range field.Names {
									if specific(n) {
										members[n.Name] = true
									}
								}
							}
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if specific(n) {
								top[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return top, members
}

// namesAHarness reports whether one of an identifier's camel-case words is
// a harness kind: KindCodex and CodexHome do, Pipeline would not for "pi".
func namesAHarness(ident string, kinds []string) bool {
	for _, w := range camelWords(ident) {
		for _, k := range kinds {
			if strings.EqualFold(w, k) {
				return true
			}
		}
	}
	return false
}

func camelWords(s string) []string {
	var words []string
	runes := []rune(s)
	start := 0
	for i := 1; i <= len(runes); i++ {
		boundary := i == len(runes) || runes[i] == '_' ||
			(unicode.IsUpper(runes[i]) && (unicode.IsLower(runes[i-1]) ||
				(i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1]))))
		if boundary {
			if w := strings.Trim(string(runes[start:i]), "_"); w != "" {
				words = append(words, w)
			}
			start = i
		}
	}
	return words
}

// ratchetFiles is every non-test .go file of the module outside
// internal/harness/..., skipping what the go tool skips (testdata, and
// directories starting with "." or "_").
func ratchetFiles(t *testing.T, root string) []string {
	t.Helper()
	harnessDir := filepath.Join(root, "internal", "harness")
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
			if path == harnessDir {
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

const harnessImportPath = "github.com/nguyenngocanh94/mate/internal/harness"

func scanRatchetFile(t *testing.T, root, path string, top, members map[string]bool) []ratchetHit {
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

	pkgName := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == harnessImportPath {
			pkgName = "harness"
			if imp.Name != nil {
				pkgName = imp.Name.Name
			}
		}
	}

	var hits []ratchetHit
	add := func(pos token.Pos, kind, what, decl string) {
		hits = append(hits, ratchetHit{file: rel, line: fset.Position(pos).Line, kind: kind, what: what, decl: decl})
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
				for _, name := range literalHarnessNames(s, ratchetHarnessNames) {
					add(n.Pos(), "literal", strconv.Quote(name), decl)
				}
			case *ast.SelectorExpr:
				if pkgName == "" {
					return true
				}
				if x, ok := n.X.(*ast.Ident); ok && x.Name == pkgName {
					if top[n.Sel.Name] || members[n.Sel.Name] {
						add(n.Pos(), "identifier", pkgName+"."+n.Sel.Name, decl)
					}
					return true
				}
				// No type information: a field or method of a harness type
				// is recognised by name, in a file that imports harness.
				if members[n.Sel.Name] {
					add(n.Sel.Pos(), "identifier", "."+n.Sel.Name, decl)
				}
			case *ast.CompositeLit:
				sel, ok := n.Type.(*ast.SelectorExpr)
				if pkgName == "" || !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); !ok || x.Name != pkgName {
					return true
				}
				for _, elt := range n.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if k, ok := kv.Key.(*ast.Ident); ok && members[k.Name] {
							add(k.Pos(), "identifier", pkgName+"."+sel.Sel.Name+"{"+k.Name+"}", decl)
						}
					}
				}
			}
			return true
		})
	}
	return hits
}

// declName names a top-level declaration: a function, Type.Method, or the
// first name a var, const or type declaration introduces.
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

// literalHarnessNames returns the harness names a string literal carries as
// whole tokens. A token is what lies between separators - whitespace, path
// separators, and the punctuation that frames a word, a flag value or an
// env key in a command line ("=", "$", quotes, brackets) - with trailing
// sentence punctuation removed. A token must equal a name exactly, or use
// a name as a dotted namespace: its first "."-separated segment equals the
// name ("claude.projects", "codex.compacted"). A token that starts with a
// dot (".claude") or names a file ("CLAUDE.md") is matched whole only.
func literalHarnessNames(s string, names []string) []string {
	var out []string
	tokens := strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("/\\=,;:\"'`()[]{}$<>|", r)
	})
	for _, tok := range tokens {
		tok = strings.TrimRight(tok, ".!?")
		namespace, _, dotted := strings.Cut(tok, ".")
		for _, name := range names {
			if tok == name || (dotted && namespace == name) {
				out = append(out, name)
			}
		}
	}
	return out
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

// TestRatchetLiteralMatchingIsWholeToken pins the matching rule the ratchet
// depends on: a name counts as a whole token, never as a substring.
func TestRatchetLiteralMatchingIsWholeToken(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"claude", []string{"claude"}},
		{".claude/settings.json", []string{".claude"}},
		{"/home/x/.codex/hooks.json", []string{".codex"}},
		{"CLAUDE.md", []string{"CLAUDE.md"}},
		{"/w/AGENTS.override.md", []string{"AGENTS.override.md"}},
		{"AGENTS.md", nil},
		{"CODEX_HOME=/tmp/x", []string{"CODEX_HOME"}},
		{"export $CLAUDE_CONFIG_DIR", []string{"CLAUDE_CONFIG_DIR"}},
		{"hook mate-session --harness codex", []string{"codex"}},
		{"the codex.", []string{"codex"}},
		{"claude.projects", []string{"claude"}},
		{"codex.adopt", []string{"codex"}},
		{"claude.compact_boundary", []string{"claude"}},
		{"source codex.compacted", []string{"codex"}},
		{"herdr:codex", []string{"codex"}},
		{"Claude Code", nil},
		{"claudecode", nil},
		{"codexlab", nil},
		{"MY_CODEX_HOME", nil},
		{"CLAUDE.md.tmpl", nil},
		{"internal/harness/codexlab", nil},
	} {
		got := literalHarnessNames(tc.in, ratchetHarnessNames)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("literalHarnessNames(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A short name is where substring matching turns the ratchet into noise
	// (plan section 4): "pi" is a whole token in a command line, never in
	// "api", "pipe" or "spin".
	pi := []string{"pi", ".pi"}
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"pi", []string{"pi"}},
		{"pi --approve", []string{"pi"}},
		{"/home/x/.pi/agent", []string{".pi"}},
		{"pi.data", []string{"pi"}},
		{"api.pi", nil},
		{"spin.data", nil},
		{"api", nil},
		{"pipe", nil},
		{"spin", nil},
		{"/v1/api/pi-data", nil},
	} {
		got := literalHarnessNames(tc.in, pi)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("literalHarnessNames(%q, pi) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, ident := range []string{"Pipeline", "APIKey", "Spinner", "Pick"} {
		if namesAHarness(ident, []string{"pi"}) {
			t.Errorf("namesAHarness(%q, pi) = true, want false", ident)
		}
	}
	if !namesAHarness("KindPi", []string{"pi"}) {
		t.Error(`namesAHarness("KindPi", pi) = false, want true`)
	}
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"KindCodex", true},
		{"CodexRolloutPath", true},
		{"ClaudeSessionID", true},
		{"TranscriptClaude", true},
		{"ParseCodexHookEvent", true},
		{"Codexlab", false},
		{"LaunchSpec", false},
	} {
		if got := namesAHarness(tc.in, ratchetKinds); got != tc.want {
			t.Errorf("namesAHarness(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
