package harness

import (
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// A launch is two calls on a harness's Launcher
// (docs/plans/harness-registry-2026-09-30.md, section 3.2). Prepare lays the
// launch out: the files the harness needs beside the agent's instructions,
// and the data its own Build reads back. It writes nothing; spawn writes the
// files, so the workspace's path rules stay in one place. Build turns the
// role-generic AgentSpec into a LaunchSpec through NewLaunchSpec, the one
// constructor a startable spec has.

// PrepareRequest is everything a harness is told to lay out one launch.
type PrepareRequest struct {
	Role AgentRole
	// Cwd is the absolute directory the agent will run in: the Mate's
	// directory, or a Crew's worktree.
	Cwd string
	// StateDir is an absolute directory of mate's own for this agent that is
	// not a git working tree: the Mate's directory, or the Crew's
	// `crews/<id>/`. A harness puts there what must not land in a worktree.
	StateDir string
	// ContextPath is the absolute path of the agent's instructions, already
	// on disk: the Mate's rendered manual, or the Crew's brief.
	ContextPath string
	// Binary is the absolute path of the mate binary the harness's hooks
	// call back into.
	Binary string
	// ResumeSessionID is the harness session this launch resumes. Empty
	// starts a fresh one.
	ResumeSessionID string
	// NewSessionID mints the id of a fresh session, for a harness that names
	// its session at launch. Nil mints a random UUID; tests pin it.
	NewSessionID func() string
}

// Check refuses a request a harness cannot lay out: a path that is not
// absolute, or a role that is neither a Mate nor a Crew. kind names the
// harness in the refusal.
func (r PrepareRequest) Check(kind Kind) error {
	for _, p := range []struct{ name, path string }{
		{"cwd", r.Cwd}, {"state directory", r.StateDir}, {"context path", r.ContextPath},
	} {
		if p.path == "" || !filepath.IsAbs(p.path) {
			return observability.WrapError(observability.CodeUsage,
				fmt.Sprintf("%s launch: the %s must be an absolute path, got %q", kind, p.name, p.path), ErrContextRequired)
		}
	}
	if r.Role != RoleMate && r.Role != RoleCrew {
		return observability.NewError(observability.CodeUsage,
			fmt.Sprintf("%s launch: role %q is neither mate nor crew", kind, r.Role))
	}
	return nil
}

// MintSessionID is the id of a fresh session: NewSessionID's, or a random
// UUID.
func (r PrepareRequest) MintSessionID() string {
	if r.NewSessionID != nil {
		return r.NewSessionID()
	}
	return uuid.NewString()
}

// Prepared is one launch laid out.
type Prepared struct {
	// Files are what spawn writes before the launch.
	Files []LaunchFile
	// SessionID is the harness session the launch runs: the resumed one, a
	// fresh one for a harness that names its session at launch, or empty
	// for one that learns its id only after the first prompt.
	SessionID string
	// ContextPath is the file the launch hands the harness as the agent's
	// instructions, for AgentSpec.ContextPath. Empty when the harness loads
	// them from its cwd on its own.
	ContextPath string
	// Launch is the harness's own data for its Build, for AgentSpec.Launch.
	// Nothing else reads it.
	Launch any
}

// LaunchFile is one file a launch needs on disk.
type LaunchFile struct {
	// Path is absolute.
	Path string
	Data []byte
	// Exclude says the file is mate's, never the agent's work: in a git
	// working tree it is excluded locally, so a `git add -A` cannot commit
	// it onto the branch the Mate reviews. It must sit at the cwd's top.
	Exclude bool
}

// AgentSpec is the role-generic request Build turns into a LaunchSpec. Cwd
// is the directory the agent process will run in (the Herdr pane cwd), not
// Mate's process cwd.
type AgentSpec struct {
	ID   string
	Role AgentRole
	Kind Kind
	Cwd  string
	// ContextPath and Launch are Prepared.ContextPath and Prepared.Launch. A
	// caller that never prepared passes a context file of its own and no
	// launch data, and gets the harness's plain launch for that file.
	ContextPath string
	Launch      any
	// ResumeSessionID resumes a previous harness session instead of starting
	// a fresh one (docs/mvp.md task 10).
	ResumeSessionID string
	Env             []EnvVar
	// EnvKeys are the variables of every registered harness
	// (Registry.EnvKeys). Env may carry them besides the identity keys,
	// because an agent may launch another harness; the harness's own
	// Info().EnvKeys are allowed without them.
	EnvKeys []string
	// TaskPrompt is a Crew's first user message. It is intentionally separate
	// from ContextPath: every harness instruction delivery is passive.
	TaskPrompt string
	// Model and Effort are the launch profile (profile.go): empty is the
	// harness's own default. An effort the harness does not take stays on
	// the spec, so it is recorded, and is left out of the argv.
	Model  string
	Effort Effort
}

// LaunchPlan is a harness's launch before it is checked: every field a
// LaunchSpec carries. NewLaunchSpec is the only way to turn one into a spec,
// so a LaunchSpec outside this package is always one that passed its checks
// (plan section 3.5).
type LaunchPlan struct {
	// RuntimeKind is what Herdr is told at `agent start --kind`.
	RuntimeKind string
	Args        []string
	Cwd         string
	Env         []EnvVar
	// EnvKeys are the variables Env may carry besides the identity keys
	// (config.IdentityEnvKeys).
	EnvKeys []string
	// UnsetEnv are variables removed from the pane before the start, besides
	// NestedSessionEnv, which every launch removes.
	UnsetEnv        []string
	Delivery        Delivery
	ContextPath     string
	ContextRequired bool
	ContextFiles    []GeneratedFile
	MaxInlineBytes  int
	// MaxFileBytes is the file/chain size budget. An instruction-file
	// delivery needs one; 0 is refused.
	MaxFileBytes int
	// ContextFlag is the flag whose value is ContextPath, as the harness
	// spells it. DeliveryAppendSystemPromptFile needs one; "" is refused.
	ContextFlag   string
	TaskPrompt    string
	Model         string
	Effort        Effort
	EffortOmitted bool
	Notes         []string
	// CodexHome and ClaudeConfigDir are the provider roots the launch
	// resolved, recorded on the spec.
	CodexHome       string
	ClaudeConfigDir string
	// Screen is the profile the launched pane is read with. Every plan
	// names one, so whoever starts the pane knows what it looks like.
	Screen ScreenProfile
	// CheckContext is the harness's own check that the context is
	// deliverable. It runs after the generic checks, here and again each
	// time ValidateRequiredContext runs, so the runtime boundary repeats it.
	// Nil means the generic checks are the whole of it.
	CheckContext func(LaunchSpec) error
}

// NewLaunchSpec is the one constructor of a startable LaunchSpec. It runs the
// checks every launch passes - env on the allowlist, an absolute cwd, a
// required context that exists, fits and is what the argv names - and then
// the harness's CheckContext, and returns no spec when any of them fails.
func NewLaunchSpec(p LaunchPlan) (LaunchSpec, error) {
	if p.Screen == nil {
		return LaunchSpec{}, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("harness %q launch names no screen profile; mate refuses a pane it cannot read", p.RuntimeKind))
	}
	env, err := filterLaunchEnv(p.Env, p.EnvKeys)
	if err != nil {
		return LaunchSpec{}, err
	}
	s := LaunchSpec{
		kind:            p.RuntimeKind,
		args:            append([]string(nil), p.Args...),
		cwd:             p.Cwd,
		env:             env,
		contextFiles:    append([]GeneratedFile(nil), p.ContextFiles...),
		delivery:        p.Delivery,
		contextPath:     p.ContextPath,
		contextRequired: p.ContextRequired,
		maxInlineBytes:  p.MaxInlineBytes,
		maxFileBytes:    p.MaxFileBytes,
		contextFlag:     p.ContextFlag,
		taskPrompt:      p.TaskPrompt,
		model:           p.Model,
		effort:          p.Effort,
		effortOmitted:   p.EffortOmitted,
		notes:           append([]string(nil), p.Notes...),
		codexHome:       p.CodexHome,
		claudeConfigDir: p.ClaudeConfigDir,
		unsetEnv:        append([]string(nil), p.UnsetEnv...),
		envKeys:         append([]string(nil), p.EnvKeys...),
		checkContext:    p.CheckContext,
		screen:          p.Screen,
	}
	if len(p.ContextFiles) == 0 {
		s.contextFiles = nil
	}
	if len(p.Args) == 0 {
		s.args = nil
	}
	if len(p.UnsetEnv) == 0 {
		s.unsetEnv = nil
	}
	if err := s.ValidateRequiredContext(); err != nil {
		return LaunchSpec{}, err
	}
	return s, nil
}
