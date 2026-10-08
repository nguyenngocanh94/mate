package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/recovery"
)

func recoverySnapshot() query.Snapshot {
	return query.Snapshot{Projects: []query.ProjectNode{{
		ProjectID: "shop",
		Mate:      query.MateNode{Error: query.AbsentField[query.ErrorReason]("none")},
		Crews: []query.CrewNode{
			{CrewID: "k1", Error: query.AbsentField[query.ErrorReason]("none")},
			{CrewID: "k2", Error: query.AbsentField[query.ErrorReason]("none")},
		},
	}}}
}

func TestConsoleRecoveryReportsProgressThenASummaryAndRowErrors(t *testing.T) {
	step := make(chan struct{})
	release := make(chan struct{})
	r, stop := startRecovery(context.Background(), func(_ context.Context, progress func(recovery.Progress)) recovery.Result {
		progress(recovery.Progress{Total: 2})
		progress(recovery.Progress{Done: 1, Total: 2})
		close(step)
		<-release
		return recovery.Result{Items: []recovery.Item{
			{Project: "shop", Resumed: true},
			{Project: "shop", Crew: "k2", Err: errors.New("the worktree of crew k2 is gone\nsecond line")},
		}}
	})
	defer stop()
	<-step

	snap := r.apply(recoverySnapshot())
	if snap.Recovery.Line != "recovering 1 of 2…" || !snap.Recovery.Active {
		t.Fatalf("while running: %+v, want `recovering 1 of 2…` active", snap.Recovery)
	}
	close(release)
	<-r.done

	snap = r.apply(recoverySnapshot())
	want := "recovered 1; 1 failed: crew shop/k2: the worktree of crew k2 is gone"
	if snap.Recovery.Line != want || snap.Recovery.Active || !snap.Recovery.Failed {
		t.Fatalf("after: %+v, want %q failed", snap.Recovery, want)
	}
	if e := snap.Projects[0].Crews[1].Error; !e.IsKnown() || e.Value != "recovery failed: the worktree of crew k2 is gone" {
		t.Fatalf("k2 error = %+v, want the failure on its own row", e)
	}
	if snap.Projects[0].Crews[0].Error.IsKnown() || snap.Projects[0].Mate.Error.IsKnown() {
		t.Fatal("a row that came back must not carry an error")
	}

	// The summary leaves the status line; the row keeps its error.
	later := r.now().Add(recoverySummaryFor + time.Second)
	r.now = func() time.Time { return later }
	snap = r.apply(recoverySnapshot())
	if snap.Recovery.Line != "" || !snap.Projects[0].Crews[1].Error.IsKnown() {
		t.Fatalf("after the summary expired: line %q, k2 error %+v", snap.Recovery.Line, snap.Projects[0].Crews[1].Error)
	}
}

func TestRecoverySummaryWordsEachOutcome(t *testing.T) {
	cases := []struct {
		name   string
		res    recovery.Result
		line   string
		failed bool
	}{
		{"nothing to do says nothing", recovery.Result{}, "", false},
		{"two back", recovery.Result{Items: []recovery.Item{{Project: "a"}, {Project: "a", Crew: "k1"}}}, "recovered 2", false},
		{"links count", recovery.Result{Fixes: []recovery.Fix{{Step: "worktree", What: "x"}, {Step: "root", What: "y"}}}, "recovered 0; repaired 2 links", false},
		{"herdr would not start", recovery.Result{Err: errors.New("herdr session s could not be started")}, "1 failed: herdr session s could not be started", true},
	}
	for _, tc := range cases {
		got := recoverySummary(tc.res)
		if got.Line != tc.line || got.Failed != tc.failed {
			t.Errorf("%s: %+v, want %q failed=%v", tc.name, got, tc.line, tc.failed)
		}
	}
}
