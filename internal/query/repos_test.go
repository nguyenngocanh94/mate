package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// addProjectWithRepos registers project with the given repos, each a
// directory with a .git inside the workspace. No repos registers a project
// with no repo at all, which docs/mvp.md M9 allows.
func addProjectWithRepos(t *testing.T, ws *store.Workspace, project string, repos ...store.RepoConfig) {
	t.Helper()
	for _, r := range repos {
		if err := os.MkdirAll(filepath.Join(ws.Root(), r.Path, ".git"), 0o755); err != nil {
			t.Fatalf("create repo %s: %v", r.Path, err)
		}
	}
	if err := ws.AddProject(project, store.ProjectConfig{Repos: repos}); err != nil {
		t.Fatalf("add project %s: %v", project, err)
	}
}

func loadOnly(t *testing.T, ws *store.Workspace) ProjectNode {
	t.Helper()
	snap, err := Load(context.Background(), ws, testHarnesses)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snap.Projects) != 1 {
		t.Fatalf("projects = %+v, want one", snap.Projects)
	}
	if len(snap.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none", snap.Warnings)
	}
	return snap.Projects[0]
}

// TestLoadProjectRepos: a project's name is its registered name whatever
// its repos are called, and its repos are reported as project.yaml records
// them - none, one or several, each with its own name, workspace-relative
// path and default branch.
func TestLoadProjectRepos(t *testing.T) {
	for _, tc := range []struct {
		name  string
		repos []store.RepoConfig
		want  []RepoValue
	}{
		{name: "none"},
		{
			name:  "one",
			repos: []store.RepoConfig{{Path: "src/storefront"}},
			want:  []RepoValue{{RepoID: "storefront", DisplayName: "storefront", Path: "src/storefront", DefaultBranch: store.DefaultBranch}},
		},
		{
			name: "two",
			repos: []store.RepoConfig{
				{Name: "web", Path: "repos/shop-web"},
				{Name: "api", Path: "services/shop-api", DefaultBranch: "develop"},
			},
			want: []RepoValue{
				{RepoID: "web", DisplayName: "web", Path: "repos/shop-web", DefaultBranch: store.DefaultBranch},
				{RepoID: "api", DisplayName: "api", Path: "services/shop-api", DefaultBranch: "develop"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, err := store.Init(t.TempDir(), store.Defaults{})
			if err != nil {
				t.Fatal(err)
			}
			addProjectWithRepos(t, ws, "shop", tc.repos...)
			p := loadOnly(t, ws)
			if p.ProjectID != "shop" || p.Name != "shop" {
				t.Fatalf("project id/name = %q/%q, want shop: the name is the project's, never a repo's", p.ProjectID, p.Name)
			}
			// No repo is a fact the read established, not a missing field.
			if p.Repos.State != Known {
				t.Fatalf("repos = %+v, want Known", p.Repos)
			}
			if len(p.Repos.Value) != len(tc.want) {
				t.Fatalf("repos = %+v, want %+v", p.Repos.Value, tc.want)
			}
			for i, want := range tc.want {
				if got := p.Repos.Value[i]; got != want {
					t.Fatalf("repo %d = %+v, want %+v", i, got, want)
				}
			}
		})
	}
}

// TestLoadResolvesEachCrewsRepo: a crew's repo is the one its meta names;
// a meta written before M9 names none and belongs to the sole repo, and
// with no repo or several the answer is an honest Unknown.
func TestLoadResolvesEachCrewsRepo(t *testing.T) {
	web := store.RepoConfig{Name: "web", Path: "web"}
	api := store.RepoConfig{Name: "api", Path: "api"}
	for _, tc := range []struct {
		name     string
		repos    []store.RepoConfig
		metaRepo string
		wantID   string
		want     FieldState
		wantPath string
		reason   string
	}{
		{name: "named in a two-repo project", repos: []store.RepoConfig{web, api}, metaRepo: "api", wantID: "api", want: Known, wantPath: "api"},
		{name: "legacy meta with one repo", repos: []store.RepoConfig{web}, wantID: "web", want: Known, wantPath: "web"},
		{name: "legacy meta with two repos", repos: []store.RepoConfig{web, api}, want: Unknown, reason: "has 2"},
		{name: "legacy meta with no repo", want: Unknown, reason: "has none"},
		{name: "named repo since removed", repos: []store.RepoConfig{web}, metaRepo: "api", wantID: "api", want: Absent, reason: "api, which is not registered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, err := store.Init(t.TempDir(), store.Defaults{})
			if err != nil {
				t.Fatal(err)
			}
			addProjectWithRepos(t, ws, "shop", tc.repos...)
			meta := map[string]string{"task": "t", "harness": "claude"}
			if tc.metaRepo != "" {
				meta[store.MetaRepo] = tc.metaRepo
			}
			if err := ws.WriteCrewMeta("shop", "k1", meta); err != nil {
				t.Fatal(err)
			}
			p := loadOnly(t, ws)
			if len(p.Crews) != 1 {
				t.Fatalf("crews = %+v, want one", p.Crews)
			}
			c := p.Crews[0]
			if c.RepoID != tc.wantID {
				t.Fatalf("repo id = %q, want %q", c.RepoID, tc.wantID)
			}
			if c.Repo.State != tc.want {
				t.Fatalf("repo = %+v, want state %v", c.Repo, tc.want)
			}
			if tc.want == Known && (c.Repo.Value.RepoID != tc.wantID || c.Repo.Value.Path != tc.wantPath) {
				t.Fatalf("repo = %+v, want %s at %s", c.Repo.Value, tc.wantID, tc.wantPath)
			}
			if !strings.Contains(c.Repo.Reason, tc.reason) {
				t.Fatalf("repo reason = %q, want it to contain %q", c.Repo.Reason, tc.reason)
			}
		})
	}
}
