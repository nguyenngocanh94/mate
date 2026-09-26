package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
)

var testColumns = []Column{
	{Role: "stage", Argv: []string{"/bin/mate", "pane", "serve", "--role", "stage"}},
	{Role: "review", Argv: []string{"/bin/mate", "pane", "serve", "--role", "review"}},
}

// weztermRow is one tab: pane ids left to right, the Console first.
type weztermRow struct {
	panes []string
	next  int
	cols  int
}

func (r *weztermRow) handle(_ context.Context, spec process.Spec) (process.Result, error) {
	a := spec.Args
	switch {
	case len(a) >= 5 && a[1] == "get-pane-direction":
		i := slices.Index(r.panes, a[3])
		if i >= 0 && i+1 < len(r.panes) {
			return process.Result{Stdout: []byte(r.panes[i+1] + "\n")}, nil
		}
		return process.Result{}, nil
	case len(a) >= 2 && a[1] == "list":
		return process.Result{Stdout: []byte(`[{"pane_id":` + r.panes[0] + `,"size":{"cols":` + strconv.Itoa(r.cols) + `}}]`)}, nil
	case len(a) >= 5 && a[1] == "split-pane":
		i := slices.Index(r.panes, a[3])
		id := strconv.Itoa(r.next)
		r.next++
		if a[4] == "--right" {
			i++
		}
		r.panes = slices.Insert(r.panes, i, id)
		return process.Result{Stdout: []byte(id + "\n")}, nil
	}
	return process.Result{}, nil
}

func splits(calls []process.Spec) [][]string {
	var out [][]string
	for _, c := range calls {
		if len(c.Args) > 1 && c.Args[1] == "split-pane" {
			out = append(out, c.Args)
		}
	}
	return out
}

// The first layout makes both columns: the stage takes all but the
// Console's 20% (in cells, so the Console keeps its columns), the review
// splits off the stage's right, and focus goes back to the Console.
func TestWezTermLaysOutStageThenReview(t *testing.T) {
	t.Parallel()
	row := &weztermRow{panes: []string{"10"}, next: 20, cols: 200}
	fake := &process.FakeRunner{Handler: row.handle}
	h := Open(WezTerm, Options{Runner: fake, Pane: "10", WezTerm: "wezterm"})
	if err := h.Layout(context.Background(), testColumns); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(row.panes, []string{"10", "20", "21"}) {
		t.Fatalf("row = %v", row.panes)
	}
	got := splits(fake.Calls)
	want := [][]string{
		append([]string{"cli", "split-pane", "--pane-id", "10", "--right", "--cells", "159", "--"}, testColumns[0].Argv...),
		append([]string{"cli", "split-pane", "--pane-id", "20", "--right", "--percent", "45", "--"}, testColumns[1].Argv...),
	}
	if len(got) != 2 || !slices.Equal(got[0], want[0]) || !slices.Equal(got[1], want[1]) {
		t.Fatalf("splits = %q\nwant %q", got, want)
	}
	last := fake.Calls[len(fake.Calls)-1].Args
	if !slices.Equal(last, []string{"cli", "activate-pane", "--pane-id", "10"}) {
		t.Fatalf("last call = %q, want focus back on the Console", last)
	}
}

// A second layout with both columns in place changes nothing: no split, no
// kill, no focus.
func TestWezTermLayoutIsIdempotent(t *testing.T) {
	t.Parallel()
	row := &weztermRow{panes: []string{"10"}, next: 20, cols: 200}
	fake := &process.FakeRunner{Handler: row.handle}
	h := Open(WezTerm, Options{Runner: fake, Pane: "10", WezTerm: "wezterm"})
	ctx := context.Background()
	if err := h.Layout(ctx, testColumns); err != nil {
		t.Fatal(err)
	}
	n := len(fake.Calls)
	if err := h.Layout(ctx, testColumns); err != nil {
		t.Fatal(err)
	}
	for _, c := range fake.Calls[n:] {
		if c.Args[1] != "get-pane-direction" {
			t.Fatalf("a settled layout ran %q", c.Args)
		}
	}
}

// A column the captain closed is made again where it belongs: the review
// to the right of the stage, the stage to the left of the review.
func TestWezTermRemakesAClosedColumnInPlace(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		closed    string
		wantSplit []string
	}{
		{"21", []string{"cli", "split-pane", "--pane-id", "20", "--right", "--percent", "45"}},
		{"20", []string{"cli", "split-pane", "--pane-id", "21", "--left", "--percent", "55"}},
	} {
		row := &weztermRow{panes: []string{"10"}, next: 20, cols: 200}
		fake := &process.FakeRunner{Handler: row.handle}
		h := Open(WezTerm, Options{Runner: fake, Pane: "10", WezTerm: "wezterm"})
		ctx := context.Background()
		if err := h.Layout(ctx, testColumns); err != nil {
			t.Fatal(err)
		}
		row.panes = slices.DeleteFunc(row.panes, func(id string) bool { return id == tc.closed })
		n := len(fake.Calls)
		if err := h.Layout(ctx, testColumns); err != nil {
			t.Fatal(err)
		}
		got := splits(fake.Calls[n:])
		if len(got) != 1 || !slices.Equal(got[0][:len(tc.wantSplit)], tc.wantSplit) {
			t.Fatalf("closed %s: splits = %q, want %q", tc.closed, got, tc.wantSplit)
		}
		if len(row.panes) != 3 {
			t.Fatalf("closed %s: row = %v", tc.closed, row.panes)
		}
	}
}

// A pane to the right that mate did not make is the captain's: refused,
// never replaced.
func TestWezTermRefusesAForeignPane(t *testing.T) {
	t.Parallel()
	row := &weztermRow{panes: []string{"10", "99"}, next: 20, cols: 200}
	fake := &process.FakeRunner{Handler: row.handle}
	h := Open(WezTerm, Options{Runner: fake, Pane: "10", WezTerm: "wezterm"})
	err := h.Layout(context.Background(), testColumns)
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("err = %v, want state_conflict", err)
	}
	if len(splits(fake.Calls)) != 0 || !slices.Equal(row.panes, []string{"10", "99"}) {
		t.Fatal("a foreign pane was split around")
	}
}

// TestWezTermLeavesTheConsoleItsColumns: mate is the left ~20% pane, 40 to
// 48 columns (the console design). The first column is split off in cells
// so the Console keeps them, whatever the window's width: 70% of an
// 80-column window left mate 23 columns and the too-small screen (found
// live on 2026-09-25).
func TestWezTermLeavesTheConsoleItsColumns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cols int
		want string
	}{
		{80, "39"},   // mate keeps 40
		{200, "159"}, // 20% is 40
		{300, "251"}, // 20% is 60, capped at 48
	} {
		row := &weztermRow{panes: []string{"10"}, next: 20, cols: tc.cols}
		fake := &process.FakeRunner{Handler: row.handle}
		h := Open(WezTerm, Options{Runner: fake, Pane: "10", WezTerm: "wezterm"})
		if err := h.Layout(context.Background(), testColumns[:1]); err != nil {
			t.Fatal(err)
		}
		if got := splits(fake.Calls); len(got) != 1 || argAfter(got[0], "--cells") != tc.want {
			t.Fatalf("%d cols: splits %q, want --cells %s", tc.cols, got, tc.want)
		}
	}
}

func TestLayoutRefusesColumnsGhosttyCouldNotRun(t *testing.T) {
	t.Parallel()
	h := Open(WezTerm, Options{Runner: &process.FakeRunner{}, Pane: "10", WezTerm: "wezterm"})
	for _, cols := range [][]Column{
		nil,
		{{Role: "stage", Argv: []string{"/bin/mate", "pane serve"}}},
		{{Role: "stage", Argv: []string{"/bin/mate"}}, {Role: "stage", Argv: []string{"/bin/mate"}}},
		{{Role: "", Argv: []string{"/bin/mate"}}},
	} {
		if err := h.Layout(context.Background(), cols); err == nil {
			t.Errorf("Layout(%q) accepted it", cols)
		}
	}
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// pane; env measured in a WezTerm 20240203 pane).
func TestWezTermRunsTheCLIWezTermNamesForItsPanes(t *testing.T) {
	t.Parallel()
	row := &weztermRow{panes: []string{"10"}, next: 20, cols: 200}
	fake := &process.FakeRunner{Handler: row.handle}
	dir := t.TempDir()
	exe := filepath.Join(dir, "wezterm")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := Open(WezTerm, Options{Runner: fake, Env: func(k string) string {
		switch k {
		case "WEZTERM_PANE":
			return "10"
		case "WEZTERM_EXECUTABLE_DIR":
			return dir
		case "WEZTERM_EXECUTABLE":
			return filepath.Join(dir, "wezterm-gui")
		}
		return ""
	}})
	if err := h.Layout(context.Background(), testColumns); err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(fake.Calls) == 0 {
		t.Fatal("Layout ran nothing")
	}
	for _, c := range fake.Calls {
		if c.Name != exe {
			t.Fatalf("ran %q, want the CLI in WEZTERM_EXECUTABLE_DIR %q", c.Name, exe)
		}
	}
}

// TestWezTermFindsTheAppBundleCLIWithoutItsEnv: a pane whose environment
// lost WEZTERM_EXECUTABLE_DIR (tmux or ssh inside WezTerm) and has no
// `wezterm` on PATH still reaches the CLI in the app bundle rather than
// failing with `exec: "wezterm": executable file not found` (reported
// 2026-09-25).
func TestWezTermFindsTheAppBundleCLIWithoutItsEnv(t *testing.T) {
	t.Parallel()
	bundle := filepath.Join(t.TempDir(), "wezterm")
	if err := os.WriteFile(bundle, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := weztermCLI(Options{Env: func(string) string { return "" }}, func(string) (string, error) {
		return "", errors.New("not found")
	}, []string{filepath.Join(t.TempDir(), "missing"), bundle})
	if got != bundle {
		t.Fatalf("CLI = %q, want the bundle's %q", got, bundle)
	}
	// PATH wins over the bundle, and the pane's own dir wins over both.
	got = weztermCLI(Options{Env: func(string) string { return "" }}, func(string) (string, error) {
		return "/usr/local/bin/wezterm", nil
	}, []string{bundle})
	if got != "/usr/local/bin/wezterm" {
		t.Fatalf("CLI = %q, want the one on PATH", got)
	}
	dir := filepath.Dir(bundle)
	got = weztermCLI(Options{Env: func(k string) string {
		if k == "WEZTERM_EXECUTABLE_DIR" {
			return dir
		}
		return ""
	}}, func(string) (string, error) { return "/usr/local/bin/wezterm", nil }, nil)
	if got != bundle {
		t.Fatalf("CLI = %q, want the pane's own %q", got, bundle)
	}
}
