package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/nguyenngocanh94/mate/internal/beads"
)

func cmdTasks(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tasks", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	list := fs.Bool("list", false, "list Beads issues")
	jsonFlag := fs.Bool("json", false, "list Beads issues as JSON")
	init := fs.Bool("init", false, "initialize Beads and refresh its viewer export")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		return newUsageError("usage: mate tasks <project> [--list|--json|--init] [--workspace <dir>]")
	}
	if !*list && !*jsonFlag && !*init && !term.IsTerminal(os.Stdin.Fd()) {
		return newUsageError("mate tasks needs a terminal; use --list or --json")
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	t, err := beads.Open(w, fs.Arg(0), nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if *init {
		if err := t.Init(ctx, stderr); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, t.Dir())
		return err
	}
	if *list || *jsonFlag {
		bdArgs := []string{"list", "--all", "--limit", "0", "--no-pager"}
		if *jsonFlag {
			bdArgs = append(bdArgs, "--json")
		}
		return t.Run(ctx, bdArgs, nil, stdout, stderr)
	}
	// The captain decides the interactive process lifetime. Initialization is
	// bounded; the TUI stays open until they quit it.
	return t.Viewer(context.Background(), nil, os.Stdin, stdout, stderr)
}

// -- separates Mate flags from upstream flags, preserving every bd argument
// (including titles, paths with spaces and multiline descriptions) literally.
func cmdBeads(args []string, stdout, stderr io.Writer) error {
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return newUsageError("usage: mate beads <project> [--workspace <dir>] -- <bd arguments>")
	}
	fs := flag.NewFlagSet("beads", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args[:sep])); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 || len(args[sep+1:]) == 0 {
		return newUsageError("usage: mate beads <project> [--workspace <dir>] -- <bd arguments>")
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	t, err := beads.Open(w, fs.Arg(0), nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return t.Run(ctx, args[sep+1:], os.Stdin, stdout, stderr)
}

// Viewer automation stays non-interactive for agents.
func cmdTaskTriage(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("task-triage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		return newUsageError("usage: mate task-triage <project> [--workspace <dir>]")
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	t, err := beads.Open(w, fs.Arg(0), nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return t.Viewer(ctx, []string{"--robot-triage", "--brief"}, nil, stdout, stderr)
}
