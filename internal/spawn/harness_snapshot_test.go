package spawn_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

func TestCrewHarnessSnapshotSurvivesLaterInstructionChanges(t *testing.T) {
	w := crewWorkspace(t, "shop")
	instructions := []byte("Run the focused package test before the full suite.\n")
	if err := os.WriteFile(filepath.Join(w.RepoDir("shop"), "AGENTS.md"), instructions, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, w.RepoDir("shop"), "add", "AGENTS.md")
	git(t, w.RepoDir("shop"), "commit", "-m", "Add crew instructions")
	res, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, runtime.NewFake()), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "snapshot", Harness: harness.KindCodex, Model: "gpt-5.5", Effort: harness.EffortHigh,
		BriefFile: briefFile(t, w, brieftest.Ship("Add a healthcheck endpoint.\n")),
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := w.ReadCrewHarnessProfile("shop", "snapshot")
	if err != nil || p == nil {
		t.Fatalf("profile = %+v, %v", p, err)
	}
	if p.Source != "launch" || p.RepoCommit == "" || p.RepoDirty == nil || p.RequestedModel != "gpt-5.5" || p.RequestedEffort != "high" || p.Fingerprint == "" {
		t.Fatalf("incomplete launch profile: %+v", p)
	}
	want := sha256.Sum256(instructions)
	var observed, templateDigest string
	for _, d := range p.Documents {
		if d.Role == "crew_prompt_template" && d.Bytes > 0 {
			templateDigest = d.SHA256
		}
		if d.Role == "repo_agents" {
			observed = d.SHA256
			if d.Bytes != int64(len(instructions)) || d.State != "configured" {
				t.Fatalf("document: %+v", d)
			}
		}
	}
	if observed != hex.EncodeToString(want[:]) {
		t.Fatalf("AGENTS digest = %q", observed)
	}
	if templateDigest == "" {
		t.Fatal("shared prompt template absent from launch baseline")
	}
	if err := os.WriteFile(filepath.Join(res.Worktree, "AGENTS.md"), []byte("Changed after launch.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := w.ReadCrewHarnessProfile("shop", "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if after.Fingerprint != p.Fingerprint {
		t.Fatal("launch profile changed after crew edited its instructions")
	}
}
