// Command m7measure prints docs/mvp.md task 34's before/after comparison of
// the M7 prompting from one or more timeline databases.
//
//	go run ./scripts/m7measure [--reindex] <label>=<workspace dir or .mate/mate.db> ...
//
// Each argument is one run. With --reindex each workspace's timeline is
// first rebuilt from its own files and transcripts, exactly as `mate
// reindex` does with no Herdr to ask, so the numbers come from what the run
// left on disk rather than from whatever a live observer happened to catch.
// The output is two Markdown tables: one row per task (crew), and one row
// per Mate.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "m7measure:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	reindex := len(args) > 0 && args[0] == "--reindex"
	if reindex {
		args = args[1:]
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: m7measure [--reindex] <label>=<workspace or db> ...")
	}
	var tasks []Task
	var mates []Mate
	for _, arg := range args {
		label, path, ok := strings.Cut(arg, "=")
		if !ok || label == "" || path == "" {
			return fmt.Errorf("argument %q: want <label>=<path>", arg)
		}
		if reindex {
			if err := Reindex(context.Background(), path); err != nil {
				return fmt.Errorf("%s: reindex: %w", label, err)
			}
		}
		t, m, err := Measure(context.Background(), label, path)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		tasks = append(tasks, t...)
		mates = append(mates, m...)
	}
	printTables(out, tasks, mates)
	return nil
}

func printTables(out io.Writer, tasks []Task, mates []Mate) {
	fmt.Fprintln(out, "| run | project | crew | kind | questions | rework | calls | turns | input | cache read | cache write | output | total | spawn→wait-mate | handback |")
	fmt.Fprintln(out, "| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |")
	for _, t := range tasks {
		fmt.Fprintf(out, "| %s | %s | %s | %s | %d | %d | %d | %d | %s | %s | %s | %s | %s | %s | %s |\n",
			t.Label, t.Project, t.Crew, t.Kind, t.Questions, t.Rework, t.Calls, t.Turns,
			k(t.In), k(t.CacheRead), k(t.CacheWrite), k(t.Out), k(t.In+t.CacheRead+t.CacheWrite+t.Out),
			dur(t.ToWaitMate), t.Handback)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "| run | Mate | calls | turns | input | cache read | cache write | output | total | captain lines | lines to crews |")
	fmt.Fprintln(out, "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, m := range mates {
		fmt.Fprintf(out, "| %s | %s | %d | %d | %s | %s | %s | %s | %s | %d | %d |\n",
			m.Label, m.Project, m.Calls, m.Turns,
			k(m.In), k(m.CacheRead), k(m.CacheWrite), k(m.Out), k(m.In+m.CacheRead+m.CacheWrite+m.Out),
			m.CaptainLines, m.ToCrew)
	}
}

// k prints a token count the way `mate usage` does: exact below a
// thousand, then one decimal of k.
func k(n int64) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func dur(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return d.Round(time.Second).String()
}
