package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/store"
)

const reportSummaryLimit = 6000

func cmdReport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	summary := fs.Bool("summary", true, "print only the report's Summary (default)")
	full := fs.Bool("full", false, "print the complete report")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return newUsageError("usage: mate report <project> <crew> [--summary|--full] [--workspace <dir>]")
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	project, crew := fs.Arg(0), fs.Arg(1)
	if err := requireProject(w, project); err != nil {
		return err
	}
	if err := store.ValidateCrewID(crew); err != nil {
		return err
	}
	path, err := w.Resolve(w.CrewReport(project, crew))
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if *full || !*summary {
		_, err = stdout.Write(data)
		return err
	}
	text, err := reportSummary(string(data))
	if err != nil {
		return fmt.Errorf("%w; read %s or use --full", err, path)
	}
	fmt.Fprintf(stdout, "%s\nfull report: %s\n", text, path)
	return nil
}

func reportSummary(text string) (string, error) {
	var lines []string
	found, fenced := false, false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
		}
		if !fenced && strings.TrimSpace(line) == "## Summary" {
			found = true
			continue
		}
		if found && !fenced && (strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "# ")) {
			break
		}
		if found {
			lines = append(lines, line)
		}
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	if !found || out == "" {
		return "", fmt.Errorf("report has no nonempty ## Summary")
	}
	if len([]rune(out)) > reportSummaryLimit {
		return "", fmt.Errorf("report Summary exceeds %d characters; ask the scout to shorten it without losing limitations", reportSummaryLimit)
	}
	return out, nil
}
