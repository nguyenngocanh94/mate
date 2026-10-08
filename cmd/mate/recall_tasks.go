package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/beads"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func recallTaskPlan(w *store.Workspace, project string, out *strings.Builder) {
	t, err := beads.Open(w, project, nil)
	if err != nil {
		fmt.Fprintf(out, "Beads: unreadable (%s)\n", recallClip(err.Error(), 200))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	active, ready, err := t.Work(ctx)
	if err != nil {
		fmt.Fprintf(out, "Beads: unreadable (%s); run mate beads %s -- ready\n", recallClip(err.Error(), 200), project)
		return
	}
	if len(active)+len(ready) == 0 {
		return
	}
	fmt.Fprintf(out, "Beads work (up to 10 active/blocked and 10 ready; mate tasks %s --list):\n", project)
	for _, issue := range active {
		fmt.Fprintf(out, "  %s [%s P%d] %s\n", issue.ID, issue.Status, issue.Priority, recallClip(issue.Title, 120))
	}
	for _, issue := range ready {
		fmt.Fprintf(out, "  %s [ready P%d] %s\n", issue.ID, issue.Priority, recallClip(issue.Title, 120))
	}
}
