package spawn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/nguyenngocanh94/mate/assets"
	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func captureCrewHarnessProfile(ctx context.Context, w *store.Workspace, deps Deps, plan crewPlan, launch harness.LaunchSpec, at time.Time) error {
	p := store.HarnessProfile{CapturedAt: at.UTC().Format(time.RFC3339Nano), Source: "launch",
		Repo: plan.repoCfg.Name, Harness: string(plan.kind), MateVersion: config.Version,
		RequestedModel: plan.model, RequestedEffort: string(plan.effort), EffortOmitted: launch.EffortOmitted(),
		Documents: []store.ProfileDocument{},
	}
	git := deps.git()
	commit, err := git.HeadCommit(ctx, plan.worktree, "HEAD")
	if err != nil {
		p.GitError = "could not read launch commit"
	} else {
		p.RepoCommit = commit
	}
	// The worktree already contains generated harness files. They are part of
	// the observed launch state, so dirty includes those untracked inputs.
	status, err := git.IsDirty(ctx, plan.worktree)
	if err != nil {
		p.GitError = "could not read launch working tree state"
	} else {
		dirty := status > 0
		p.RepoDirty = &dirty
	}
	inputs := []struct{ path, role string }{
		{w.CrewBrief(plan.project, plan.crew), "task_brief"},
		{w.WorkspaceCrewDoc(), "workspace_crew_rules"},
		{w.ProjectCrewDoc(plan.project), "project_crew_rules"},
		{filepath.Join(plan.worktree, "AGENTS.md"), "repo_agents"},
	}
	for _, k := range deps.Harnesses.Kinds() {
		profile, err := deps.Harnesses.Lookup(k)
		if err != nil {
			return err
		}
		for _, d := range profile.Info().Documents {
			inputs = append(inputs, struct{ path, role string }{filepath.Join(plan.worktree, filepath.FromSlash(d.Path)), d.Role})
		}
	}
	for _, input := range inputs {
		p.Documents = append(p.Documents, profileDocument(w, input.path, input.role))
	}
	// The generated prompt mixes task text with shared harness instructions.
	// Hash the embedded template separately so editing the harness changes its
	// fingerprint while assigning a different task does not.
	template, err := assets.FS.ReadFile("crew/brief.md.tmpl")
	if err != nil {
		return err
	}
	digest := sha256.Sum256(template)
	p.Documents = append(p.Documents, store.ProfileDocument{
		Path: "embedded:crew/brief.md.tmpl", Role: "crew_prompt_template",
		SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(template)), State: "configured",
	})
	return w.WriteCrewHarnessProfile(plan.project, plan.crew, p)
}

func profileDocument(w *store.Workspace, path, role string) store.ProfileDocument {
	rel, err := filepath.Rel(w.Root(), path)
	if err != nil {
		rel = filepath.Base(path)
	}
	d := store.ProfileDocument{Path: filepath.ToSlash(rel), Role: role, State: "unreadable"}
	resolved, err := w.Resolve(path)
	if err != nil {
		d.State = "outside_workspace"
		return d
	}
	f, err := os.Open(resolved)
	if errors.Is(err, os.ErrNotExist) {
		d.State = "missing"
		return d
	}
	if err != nil {
		return d
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return d
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return d
	}
	d.Bytes, d.SHA256, d.State = n, hex.EncodeToString(h.Sum(nil)), "configured"
	return d
}
