package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHarnessProfileFingerprintSeparatesConfigurationFromTask(t *testing.T) {
	p := HarnessProfile{Harness: "codex", RequestedModel: "gpt-test", Documents: []ProfileDocument{
		{Path: "first/AGENTS.md", Role: "repo_agents", SHA256: "rules-a", State: "configured"},
		{Path: "first/brief.md", Role: "task_brief", SHA256: "task-a", State: "configured"},
		{Path: "embedded:crew/brief.md.tmpl", Role: "crew_prompt_template", SHA256: "template-a", State: "configured"},
	}}
	before := FingerprintProfile(p)
	p.Crew = "different"
	p.CapturedAt = "later"
	p.RepoCommit = "another-commit"
	p.Documents[0].Path = "another/AGENTS.md"
	p.Documents[1].SHA256 = "task-b"
	if FingerprintProfile(p) != before {
		t.Fatal("different worktree/task changed configuration fingerprint")
	}
	p.Documents[0].SHA256 = "rules-b"
	if FingerprintProfile(p) == before {
		t.Fatal("changed instructions kept the same fingerprint")
	}
	before = FingerprintProfile(p)
	p.Documents[2].SHA256 = "template-b"
	if FingerprintProfile(p) == before {
		t.Fatal("changed harness template kept the same fingerprint")
	}
}

func TestHarnessProfileRetainsPriorLaunchAndRejectsEscapingSource(t *testing.T) {
	w, err := Init(t.TempDir(), Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	p := HarnessProfile{CapturedAt: "2026-09-28T00:00:00Z", Source: "launch", Harness: "codex"}
	if err := w.WriteCrewHarnessProfile("shop", "k1", p); err != nil {
		t.Fatal(err)
	}
	p.CapturedAt = "2026-09-28T00:01:00Z"
	p.RequestedModel = "another-model"
	if err := w.WriteCrewHarnessProfile("shop", "k1", p); err != nil {
		t.Fatal(err)
	}
	all, err := w.ReadCrewHarnessProfiles("shop", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].RequestedModel != "" || all[1].RequestedModel != "another-model" {
		t.Fatalf("history = %+v", all)
	}
	missing, err := w.ReadCrewHarnessProfile("shop", "old-crew")
	if err != nil || missing != nil {
		t.Fatalf("historical missing profile should be unknown: %+v, %v", missing, err)
	}
	path := w.CrewHarnessProfile("shop", "k1")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReadCrewHarnessProfile("shop", "k1"); err == nil {
		t.Fatal("profile escaped the workspace through a symlink")
	}
}
