package host

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
)

const ghSelf = "AAAA0000-0000-0000-0000-000000000000"

// ghosttyTab answers the AppleScript the driver sends: one tab of terminal
// ids, the Console focused. windows are terminals in other windows.
type ghosttyTab struct {
	ids        []string
	windowIDs  []string
	windowCmds []string
	selected   []string
	next       int
	splits     []string // "<from> <direction> <command>"
	actions    []string
}

var (
	reAnchor   = regexp.MustCompile(`first terminal whose id is "([^"]+)"`)
	reCommand  = regexp.MustCompile(`set command of cfg to "([^"]*)"`)
	reDir      = regexp.MustCompile(`split anchor direction (\w+)`)
	reAction   = regexp.MustCompile(`perform action "([^"]+)"`)
	reContains = regexp.MustCompile(`contains "([^"]+)"`)
)

func (g *ghosttyTab) handle(_ context.Context, spec process.Spec) (process.Result, error) {
	src := string(spec.Stdin)
	switch {
	case strings.Contains(src, "new tab in"):
		id := fmt.Sprintf("CCCC0000-0000-0000-0000-%012d", g.next)
		g.next++
		g.windowIDs = append(g.windowIDs, id)
		g.windowCmds = append(g.windowCmds, reCommand.FindStringSubmatch(src)[1])
		return process.Result{Stdout: []byte(id + "\n")}, nil
	case strings.Contains(src, "select tab"):
		g.selected = append(g.selected, reContains.FindStringSubmatch(src)[1])
		return process.Result{Stdout: []byte("ok\n")}, nil
	case strings.Contains(src, "count of (every terminal whose id is"):
		id := reAnchorAny.FindStringSubmatch(src)[1]
		state := "gone"
		if slices.Contains(g.ids, id) || slices.Contains(g.windowIDs, id) {
			state = "open"
		}
		return process.Result{Stdout: []byte(state + "\n")}, nil
	case strings.Contains(src, "focused terminal of selected tab"):
		return process.Result{Stdout: []byte(ghSelf + "\n")}, nil
	case strings.Contains(src, "repeat with w in windows"):
		return process.Result{Stdout: []byte(strings.Join(g.ids, "\n") + "\n")}, nil
	case strings.Contains(src, "split anchor"):
		id := fmt.Sprintf("BBBB0000-0000-0000-0000-%012d", g.next)
		g.next++
		g.splits = append(g.splits, reAnchor.FindStringSubmatch(src)[1]+" "+reDir.FindStringSubmatch(src)[1]+" "+reCommand.FindStringSubmatch(src)[1])
		g.ids = append(g.ids, id)
		return process.Result{Stdout: []byte(id + "\n")}, nil
	case strings.Contains(src, "perform action"):
		g.actions = append(g.actions, reAction.FindStringSubmatch(src)[1])
	}
	return process.Result{}, nil
}

func newGhosttyForTest(tab *ghosttyTab, cols func() int) *ghostty {
	g := newGhostty(Options{Runner: &process.FakeRunner{Handler: tab.handle}, Osascript: "osascript", SelfCols: cols})
	g.settle = time.Millisecond
	return g
}

// The first layout splits the stage off the Console and the review off the
// stage, each running its program as one shell string, then evens the
// columns.
func TestGhosttyLaysOutStageThenReview(t *testing.T) {
	t.Parallel()
	tab := &ghosttyTab{ids: []string{ghSelf}}
	g := newGhosttyForTest(tab, nil)
	if err := g.Layout(context.Background(), testColumns); err != nil {
		t.Fatal(err)
	}
	stage := "BBBB0000-0000-0000-0000-000000000000"
	want := []string{
		ghSelf + " right /bin/mate pane serve --role stage",
		stage + " right /bin/mate pane serve --role review",
	}
	if !slices.Equal(tab.splits, want) {
		t.Fatalf("splits = %q\nwant %q", tab.splits, want)
	}
	if err := g.Layout(context.Background(), testColumns); err != nil || len(tab.splits) != 2 {
		t.Fatalf("a settled layout split again: %v %q", err, tab.splits)
	}
}

// Ghostty splits in halves, so after evening the columns the Console
// measures itself and moves its divider until it is its design width.
func TestGhosttyNarrowsTheConsoleByMeasuring(t *testing.T) {
	t.Parallel()
	tab := &ghosttyTab{ids: []string{ghSelf}}
	width := 58 // an even third of a 174-column window
	g := newGhosttyForTest(tab, func() int { return width })
	orig := tab.handle
	g.runner = &process.FakeRunner{Handler: func(ctx context.Context, spec process.Spec) (process.Result, error) {
		res, err := orig(ctx, spec)
		if m := reAction.FindStringSubmatch(string(spec.Stdin)); m != nil && strings.HasPrefix(m[1], "resize_split:left,") {
			var points int
			fmt.Sscanf(strings.TrimPrefix(m[1], "resize_split:left,"), "%d", &points)
			width -= points * 10 / 83 // this window: 8.3 points a cell
		}
		return res, err
	}}
	if err := g.Layout(context.Background(), testColumns); err != nil {
		t.Fatal(err)
	}
	if tab.actions[0] != "equalize_splits" {
		t.Fatalf("actions = %q, want the columns evened first", tab.actions)
	}
	if want := consoleCols(174); width < want-1 || width > want+1 {
		t.Fatalf("console width %d, want about %d (actions %q)", width, want, tab.actions)
	}
}

// A closed stage comes back to the left of the review that is still there.
func TestGhosttyRemakesAClosedStageLeftOfTheReview(t *testing.T) {
	t.Parallel()
	tab := &ghosttyTab{ids: []string{ghSelf}}
	g := newGhosttyForTest(tab, nil)
	ctx := context.Background()
	if err := g.Layout(ctx, testColumns); err != nil {
		t.Fatal(err)
	}
	stage := tab.ids[1]
	tab.ids = slices.DeleteFunc(tab.ids, func(id string) bool { return id == stage })
	if err := g.Layout(ctx, testColumns); err != nil {
		t.Fatal(err)
	}
	review := "BBBB0000-0000-0000-0000-000000000001"
	if last := tab.splits[len(tab.splits)-1]; last != review+" left /bin/mate pane serve --role stage" {
		t.Fatalf("remade stage by %q", last)
	}
}

func TestGhosttyRefusesAForeignTerminal(t *testing.T) {
	t.Parallel()
	tab := &ghosttyTab{ids: []string{ghSelf, "CCCC0000-0000-0000-0000-000000000000"}}
	g := newGhosttyForTest(tab, nil)
	err := g.Layout(context.Background(), testColumns)
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict || len(tab.splits) != 0 {
		t.Fatalf("err = %v, splits %q; want a refusal and nothing split", err, tab.splits)
	}
}

// An answer that is not a Ghostty id never reaches a later script.
func TestGhosttyRefusesAnIDItCannotQuote(t *testing.T) {
	t.Parallel()
	g := newGhostty(Options{Runner: &process.FakeRunner{Default: process.Result{Stdout: []byte(`x" & do shell script "rm`)}}})
	if err := g.Layout(context.Background(), testColumns); err == nil {
		t.Fatal("an unquotable id was accepted")
	}
}

// Close closes every column this process made, and nothing else.
func TestGhosttyCloseClosesOnlyItsColumns(t *testing.T) {
	t.Parallel()
	tab := &ghosttyTab{ids: []string{ghSelf}}
	var closed []string
	g := newGhosttyForTest(tab, nil)
	g.runner = &process.FakeRunner{Handler: func(ctx context.Context, spec process.Spec) (process.Result, error) {
		if src := string(spec.Stdin); strings.Contains(src, "close t") {
			closed = append(closed, reAnchorAny.FindStringSubmatch(src)[1])
		}
		return tab.handle(ctx, spec)
	}}
	if err := g.Layout(context.Background(), testColumns); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	slices.Sort(closed)
	if len(closed) != 2 || slices.Contains(closed, ghSelf) {
		t.Fatalf("closed %q, want the two columns and never the Console", closed)
	}
}

var reAnchorAny = regexp.MustCompile(`whose id is "([^"]+)"`)

// The review is a tab in the Console's window, not a split and not a new
// window. A second open keeps it, and closing the role closes only that
// terminal.
func TestGhosttyOpensTheReviewInATab(t *testing.T) {
	t.Parallel()
	tab := &ghosttyTab{ids: []string{ghSelf}}
	g := newGhosttyForTest(tab, nil)
	ctx := context.Background()
	review := testColumns[1]
	if err := g.Tab(ctx, review); err != nil {
		t.Fatal(err)
	}
	if len(tab.splits) != 0 {
		t.Fatalf("splits %q; a review tab must not split the Console", tab.splits)
	}
	if !slices.Equal(tab.windowCmds, []string{"/bin/mate pane serve --role review"}) {
		t.Fatalf("tabs = %q", tab.windowCmds)
	}
	if err := g.Front(ctx, "review"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tab.selected, []string{"CCCC0000-0000-0000-0000-000000000000"}) {
		t.Fatalf("selected %q, want the review tab", tab.selected)
	}
	if err := g.Tab(ctx, review); err != nil || len(tab.windowCmds) != 1 {
		t.Fatalf("an open review tab was opened again: %v %q", err, tab.windowCmds)
	}
	// The stage still splits off the Console. The review tab is not a
	// terminal of this tab, so it is not a foreign pane.
	if err := g.Layout(ctx, testColumns[:1]); err != nil {
		t.Fatal(err)
	}
	if len(tab.splits) != 1 || !strings.HasPrefix(tab.splits[0], ghSelf+" right /bin/mate pane serve --role stage") {
		t.Fatalf("splits = %q, want only the stage", tab.splits)
	}
	var closed []string
	g.runner = &process.FakeRunner{Handler: func(ctx context.Context, spec process.Spec) (process.Result, error) {
		if src := string(spec.Stdin); strings.Contains(src, "close t") {
			closed = append(closed, reAnchorAny.FindStringSubmatch(src)[1])
			tab.windowIDs = slices.DeleteFunc(tab.windowIDs, func(id string) bool { return id == closed[len(closed)-1] })
		}
		return tab.handle(ctx, spec)
	}}
	if err := g.Close(ctx, "review"); err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 || closed[0] != "CCCC0000-0000-0000-0000-000000000000" {
		t.Fatalf("closed %q, want only the review tab", closed)
	}
	if err := g.Tab(ctx, review); err != nil {
		t.Fatal(err)
	}
	if len(tab.windowCmds) != 2 {
		t.Fatalf("tabs = %q, want the review opened again after it was closed", tab.windowCmds)
	}
}
