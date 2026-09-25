package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// multiRepoTree is sampleTree with the shapes docs/mvp.md M9 allows: the
// first Project owns two repos, each with its own default branch, and the
// second was created without a repo and has none yet.
func multiRepoTree() query.Snapshot {
	tree := sampleTree()
	tree.Projects[0].Repos = query.KnownField([]query.RepoValue{
		{RepoID: "web", DisplayName: "web", Path: "repos/payments-web", DefaultBranch: "main"},
		{RepoID: "api", DisplayName: "api", Path: "services/payments-api", DefaultBranch: "develop"},
	})
	for i := range tree.Projects[0].Crews {
		c := &tree.Projects[0].Crews[i]
		c.RepoID, c.Repo = "api", query.KnownField(tree.Projects[0].Repos.Value[1])
	}
	tree.Projects[1].Repos = query.KnownField([]query.RepoValue{})
	return tree
}

// TestGoldenFramesInspectorProjectRepos: the Project inspector lists every
// repo by name, default branch and workspace-relative path, and a Project
// with no repo says so and names the command that adds one.
//
//	go test ./internal/ui/console -run TestGoldenFramesInspectorProjectRepos -update
func TestGoldenFramesInspectorProjectRepos(t *testing.T) {
	m := newFixture(t, multiRepoTree(), 120, 36, unicodeGlyphs)
	frame := renderFrame(t, m)
	assertGolden(t, "inspector-project-two-repos-120x36-unicode", frame)
	for _, want := range []string{"Repos            2", "web · main", "  repos/payments-web", "api · develop", "  services/payments-api"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("two-repo inspector missing %q:\n%s", want, frame)
		}
	}

	m, _ = send(t, m, key("down"))
	frame = renderFrame(t, m)
	assertGolden(t, "inspector-project-no-repo-120x36-unicode", frame)
	for _, want := range []string{"none yet", "mate project repo add", "  ledger-worker <path>"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("no-repo inspector missing %q:\n%s", want, frame)
		}
	}

	// The Crew block names the one repo the crew works in.
	m = newFixture(t, multiRepoTree(), 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	frame = renderFrame(t, m)
	assertGolden(t, "inspector-crew-two-repos-120x36-unicode", frame)
	if !strings.Contains(frame, "Repo             api  services/payments-api") {
		t.Fatalf("crew inspector does not name the crew's repo:\n%s", frame)
	}
}
