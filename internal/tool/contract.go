package tool

import (
	"context"
	"io"

	"github.com/nguyenngocanh94/mate/internal/capability"
)

// Name names a tool as mate's CLI and files spell it: lower-case and
// trimmed.
type Name string

// Info is what a tool is called and what it takes to have it.
type Info struct {
	// Name is the tool's registered name; it equals Profile.Name().
	Name Name
	// Title is the tool as people call it, for text a Mate or the captain
	// reads.
	Title string
	// Binaries are the executables that must be on PATH or in mate's tool
	// directories for the tool to run.
	Binaries []string
	// Install is one line saying how to install them. A Viewer whose
	// binary is missing puts it in its error.
	Install string
	// Docs is the tool's documentation, as a path in this repo.
	Docs string
	// Measured is the version, of each binary, its verified capabilities
	// were measured on.
	Measured string
}

// Profile is everything the core may ask about one tool.
type Profile interface {
	Name() Name
	Info() Info
	Capabilities() Capabilities
}

// Capabilities are everything a tool may do for mate. Every field must be
// declared by every tool: the zero Cap is undeclared, which the contract
// suite (internal/tool/catalog) refuses, so a new field forces every
// registered tool to answer it.
type Capabilities struct {
	// Viewer opens the tool in a host pane on a console key.
	Viewer capability.Cap[Viewer]
	// Command is the passthrough `mate tool <name> <project> -- args`.
	Command capability.Cap[Command]
	// Recall is the tool's lines in a Mate's context at startup.
	Recall capability.Cap[Recall]
	// Skill is the Mate's skill for the tool, generated from the registry.
	Skill capability.Cap[Skill]
	// Data is where the tool keeps its data, under the project directory.
	Data capability.Cap[Data]
}

// Viewer opens the tool in a host pane.
type Viewer interface {
	// Bindings are the console keys the tool takes. Scope is the row of the
	// console tree the key means something on.
	Bindings() []Binding
	// Argv builds the command for the host pane. findTool resolves a binary
	// name to an absolute path, or "" when it is not installed (cmd/mate's
	// seam). A missing binary is an error that carries Info.Install.
	Argv(ctx ViewerContext, findTool func(string) string) ([]string, error)
	// Placeholder is the pane's waiting line while there is nothing to
	// show.
	Placeholder() string
}

// Binding is one console key a Viewer takes.
type Binding struct {
	// Key is the key pressed: "e", "t".
	Key string
	// Label is the word the console draws for it on the key line.
	Label string
	// Scope is the row of the console tree the key acts on.
	Scope BindScope
	// Role is the socket name of the host pane the tool opens in.
	Role string
	// Tool is the tool the key belongs to. Registry.Bindings fills it from
	// the profile's Name; a profile leaves it empty.
	Tool Name
}

// BindScope is a row of the console tree a key acts on.
type BindScope string

const (
	// ScopeProject is a project row.
	ScopeProject BindScope = "project"
	// ScopeCrew is a crew row.
	ScopeCrew BindScope = "crew"
)

// ViewerContext is what a Viewer is opened on.
type ViewerContext struct {
	// ProjectDir is <root>/<project>. It may not exist yet on a workspace
	// in the old layout.
	ProjectDir string
	// CrewDir is .mate/projects/<p>/crews/<id>; empty on a project row.
	CrewDir string
	// ReportPath is the crew's report.md when it has one, else empty.
	ReportPath string
}

// Command is the passthrough `mate tool <name> <project> -- args`. The
// args reach the tool whole, never through a shell.
type Command interface {
	Run(ctx context.Context, env CommandEnv, args []string, in io.Reader, out, stderr io.Writer) error
}

// CommandEnv is what the core hands a tool to run in.
type CommandEnv struct {
	// ProjectDir is <root>/<project>.
	ProjectDir string
	// DataDir is Data.Dir(ProjectDir).
	DataDir string
	// Lock takes the tool's lock for the project, held by the store, and
	// returns its release.
	Lock func(context.Context) (func(), error)
	// Run starts the tool's processes.
	Run Runner
}

// Runner runs one process with the caller's streams attached, the way a
// passthrough command must: the tool reads the user's stdin and writes its
// output as it goes. internal/process's Runner buffers stdout and stderr and
// takes stdin as bytes, which fits a probe but not a passthrough, so the
// shape here is internal/beads' Runner, which Beads' passthrough runs on
// today.
type Runner func(ctx context.Context, inv Invocation, in io.Reader, out, stderr io.Writer) error

// Invocation is one process a Runner starts: no shell, args whole.
type Invocation struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

// Recall prints a few lines for a Mate's context at startup.
type Recall interface {
	// Render is the tool's block, at most maxBytes long. present is false
	// when the tool has nothing for the project (ABSENT).
	Render(ctx context.Context, env CommandEnv, maxBytes int) (text string, present bool, err error)
}

// Skill is the Mate's skill for the tool, generated from the registry.
type Skill interface {
	// SkillName is the skill's directory name: "task-management".
	SkillName() string
	// SkillMarkdown is the skill's SKILL.md.
	SkillMarkdown() string
	// ManualSection is the tool's section of a Mate's AGENTS.md; empty
	// when it needs none.
	ManualSection() string
}

// Data is where a tool keeps its data: under the project directory, never
// under .mate.
type Data interface {
	// Dir is the tool's data directory for the project at projectDir.
	Dir(projectDir string) string
	// Exists reports whether the tool's data is there.
	Exists(projectDir string) (bool, error)
	// Init creates it.
	Init(ctx context.Context, env CommandEnv, stderr io.Writer) error
}
