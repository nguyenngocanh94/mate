package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// HarnessProfile is a source snapshot captured before a Crew starts. It
// records configured inputs, not a claim that the model consumed those files.
// Only hashes and sizes of allowlisted documents are retained, never secrets
// from environment variables or the contents of harness settings files.
type HarnessProfile struct {
	// SnapshotPath locates the specific preserved source when read. It is
	// not part of the persisted profile or its configuration fingerprint.
	SnapshotPath    string            `json:"-"`
	Version         int               `json:"version"`
	CapturedAt      string            `json:"captured_at"`
	Source          string            `json:"source"`
	Project         string            `json:"project"`
	Crew            string            `json:"crew"`
	Repo            string            `json:"repo"`
	Harness         string            `json:"harness"`
	MateVersion     string            `json:"mate_version,omitempty"`
	RequestedModel  string            `json:"requested_model,omitempty"`
	RequestedEffort string            `json:"requested_effort,omitempty"`
	EffortOmitted   bool              `json:"effort_omitted"`
	RepoCommit      string            `json:"repo_commit,omitempty"`
	RepoDirty       *bool             `json:"repo_dirty"`
	GitError        string            `json:"git_error,omitempty"`
	Documents       []ProfileDocument `json:"documents"`
	Fingerprint     string            `json:"fingerprint"`
}

type ProfileDocument struct {
	Path   string `json:"path"`
	Role   string `json:"role"`
	SHA256 string `json:"sha256,omitempty"`
	Bytes  int64  `json:"bytes"`
	State  string `json:"state"`
}

func (w *Workspace) CrewHarnessProfile(project, crew string) string {
	return filepath.Join(w.CrewDir(project, crew), "harness-profile.json")
}

// FingerprintProfile compares configuration independently of task text,
// timestamps and worktree paths. Actual code revision stays in RepoCommit.
func FingerprintProfile(p HarnessProfile) string {
	type document struct{ Role, SHA256, State string }
	docs := make([]document, 0, len(p.Documents))
	for _, d := range p.Documents {
		if d.Role == "task_brief" || d.Role == "generated_prompt" {
			continue
		}
		docs = append(docs, document{d.Role, d.SHA256, d.State})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Role < docs[j].Role })
	data, _ := json.Marshal(struct {
		Harness, MateVersion, Model, Effort string
		EffortOmitted                       bool
		Documents                           []document
	}{p.Harness, p.MateVersion, p.RequestedModel, p.RequestedEffort, p.EffortOmitted, docs})
	return profileDigest(data)
}

func profileDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// WriteCrewHarnessProfile preserves a previous launch before replacing the
// latest snapshot. A stale Crew ID can be launched again; its old profile
// must remain available for reindex after that retry.
func (w *Workspace) WriteCrewHarnessProfile(project, crew string, p HarnessProfile) error {
	if err := validatePair(project, crew); err != nil {
		return err
	}
	p.Version, p.Project, p.Crew = 1, project, crew
	p.Fingerprint = FingerprintProfile(p)
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := w.CrewHarnessProfile(project, crew)
	resolved, err := w.resolve(path)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(resolved)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(old) > 0 && profileDigest(old) != profileDigest(data) {
		archive := filepath.Join(w.CrewDir(project, crew), "harness-profiles", profileDigest(old)+".json")
		if err := w.writeFile(archive, old, 0o600); err != nil {
			return err
		}
	}
	return w.writeFile(path, data, 0o600)
}

func (w *Workspace) ReadCrewHarnessProfile(project, crew string) (*HarnessProfile, error) {
	if err := validatePair(project, crew); err != nil {
		return nil, err
	}
	return w.readHarnessProfile(w.CrewHarnessProfile(project, crew))
}

func (w *Workspace) readHarnessProfile(path string) (*HarnessProfile, error) {
	resolved, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p HarnessProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("read harness profile: %w", err)
	}
	p.SnapshotPath = path
	return &p, nil
}

// ReadCrewHarnessProfiles includes archived launches so rebuilding the
// derived database does not silently discard their original configuration.
func (w *Workspace) ReadCrewHarnessProfiles(project, crew string) ([]HarnessProfile, error) {
	if err := validatePair(project, crew); err != nil {
		return nil, err
	}
	dir := filepath.Join(w.CrewDir(project, crew), "harness-profiles")
	if _, err := w.resolve(dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	out := []HarnessProfile{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p, err := w.readHarnessProfile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if p != nil {
			out = append(out, *p)
		}
	}
	p, err := w.ReadCrewHarnessProfile(project, crew)
	if err != nil {
		return nil, err
	}
	if p != nil {
		out = append(out, *p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CapturedAt < out[j].CapturedAt })
	return out, nil
}
