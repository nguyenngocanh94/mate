package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// cliEnvelope is Herdr 0.8.2's machine-readable CLI wrapper for workspace,
// tab, pane, and agent commands. status --json and session list --json are
// bare objects and do not use this wrapper (live-captured 2026-09-05).
type cliEnvelope struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *cliError       `json:"error"`
}

type cliError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type createdWorkspace struct {
	WorkspaceID string
	Label       string
	TabID       string
	TabLabel    string
	PaneID      string
	TerminalID  string
	Cwd         string
}

type listedWorkspace struct {
	WorkspaceID string
	Label       string
	ActiveTabID string
}

type listedTab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
}

type listedPane struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	TerminalID  string `json:"terminal_id"`
	Cwd         string `json:"cwd"`
}

type createdTab struct {
	WorkspaceID string
	TabID       string
	PaneID      string
	TerminalID  string
	Label       string
	Cwd         string
}

type agentInfo struct {
	Name          string
	Kind          string
	Status        AgentStatus
	LaunchPending bool
	PaneID        string
	TabID         string
	TerminalID    string
	WorkspaceID   string
	Cwd           string
	// SessionRef is the harness's own session identity as Herdr reports it
	// (agent_session.value). Measured 2026-09-20 on Herdr 0.8.2: it is the
	// rollout session uuid for a Codex agent and absent for a Claude one, so
	// a caller reads an empty value as "the runtime did not say" and never
	// as "this agent has no session".
	SessionRef string
}

type sessionInfo struct {
	Name       string `json:"name"`
	Default    bool   `json:"default"`
	Running    bool   `json:"running"`
	SocketPath string `json:"socket_path"`
}

type statusInfo struct {
	Running  bool
	Session  string
	Socket   string
	Version  string
	Protocol int
}

func parseCLIEnvelope(data []byte) (cliEnvelope, error) {
	var env cliEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return cliEnvelope{}, fmt.Errorf("herdr json: %w", err)
	}
	return env, nil
}

func parseCLIError(data []byte) (*cliError, error) {
	env, err := parseCLIEnvelope(data)
	if err != nil {
		return nil, err
	}
	if env.Error == nil || env.Error.Code == "" {
		return nil, fmt.Errorf("herdr json: no error field")
	}
	return env.Error, nil
}

func parseResult(data []byte, dest any) error {
	env, err := parseCLIEnvelope(data)
	if err != nil {
		return err
	}
	if env.Error != nil {
		return NewHerdrError(env.Error.Code, env.Error.Message)
	}
	if len(env.Result) == 0 {
		return fmt.Errorf("herdr json: missing result")
	}
	if err := json.Unmarshal(env.Result, dest); err != nil {
		return fmt.Errorf("herdr json result: %w", err)
	}
	return nil
}

func parseWorkspaceCreated(data []byte) (createdWorkspace, error) {
	var raw struct {
		Workspace struct {
			WorkspaceID string `json:"workspace_id"`
			Label       string `json:"label"`
		} `json:"workspace"`
		Tab struct {
			TabID string `json:"tab_id"`
			Label string `json:"label"`
		} `json:"tab"`
		RootPane struct {
			PaneID     string `json:"pane_id"`
			TabID      string `json:"tab_id"`
			TerminalID string `json:"terminal_id"`
			Cwd        string `json:"cwd"`
		} `json:"root_pane"`
	}
	if err := parseResult(data, &raw); err != nil {
		return createdWorkspace{}, err
	}
	if raw.Workspace.WorkspaceID == "" {
		return createdWorkspace{}, fmt.Errorf("herdr json: workspace create missing workspace_id")
	}
	return createdWorkspace{
		WorkspaceID: raw.Workspace.WorkspaceID,
		Label:       raw.Workspace.Label,
		TabID:       raw.Tab.TabID,
		TabLabel:    raw.Tab.Label,
		PaneID:      raw.RootPane.PaneID,
		TerminalID:  raw.RootPane.TerminalID,
		Cwd:         raw.RootPane.Cwd,
	}, nil
}

func parseWorkspaceList(data []byte) ([]listedWorkspace, error) {
	var raw struct {
		Workspaces []struct {
			WorkspaceID string `json:"workspace_id"`
			Label       string `json:"label"`
			ActiveTabID string `json:"active_tab_id"`
		} `json:"workspaces"`
	}
	if err := parseResult(data, &raw); err != nil {
		return nil, err
	}
	out := make([]listedWorkspace, 0, len(raw.Workspaces))
	for _, w := range raw.Workspaces {
		out = append(out, listedWorkspace{WorkspaceID: w.WorkspaceID, Label: w.Label, ActiveTabID: w.ActiveTabID})
	}
	return out, nil
}

func parseTabList(data []byte) ([]listedTab, error) {
	var raw struct {
		Tabs []listedTab `json:"tabs"`
	}
	if err := parseResult(data, &raw); err != nil {
		return nil, err
	}
	return raw.Tabs, nil
}

func parsePaneList(data []byte) ([]listedPane, error) {
	var raw struct {
		Panes []listedPane `json:"panes"`
	}
	if err := parseResult(data, &raw); err != nil {
		return nil, err
	}
	return raw.Panes, nil
}

func parseTabCreated(data []byte) (createdTab, error) {
	var raw struct {
		Tab struct {
			TabID       string `json:"tab_id"`
			WorkspaceID string `json:"workspace_id"`
			Label       string `json:"label"`
		} `json:"tab"`
		RootPane struct {
			PaneID      string `json:"pane_id"`
			TabID       string `json:"tab_id"`
			WorkspaceID string `json:"workspace_id"`
			TerminalID  string `json:"terminal_id"`
			Cwd         string `json:"cwd"`
		} `json:"root_pane"`
	}
	if err := parseResult(data, &raw); err != nil {
		return createdTab{}, err
	}
	if raw.Tab.TabID == "" || raw.RootPane.PaneID == "" {
		return createdTab{}, fmt.Errorf("herdr json: tab create missing tab_id or pane_id")
	}
	ws := raw.Tab.WorkspaceID
	if ws == "" {
		ws = raw.RootPane.WorkspaceID
	}
	return createdTab{
		WorkspaceID: ws,
		TabID:       raw.Tab.TabID,
		PaneID:      raw.RootPane.PaneID,
		TerminalID:  raw.RootPane.TerminalID,
		Label:       raw.Tab.Label,
		Cwd:         raw.RootPane.Cwd,
	}, nil
}

type agentJSON struct {
	Name          string `json:"name"`
	Kind          string `json:"agent"`
	Status        string `json:"agent_status"`
	LaunchPending bool   `json:"launch_pending"`
	PaneID        string `json:"pane_id"`
	TabID         string `json:"tab_id"`
	TerminalID    string `json:"terminal_id"`
	WorkspaceID   string `json:"workspace_id"`
	Cwd           string `json:"cwd"`
	// AgentSession is the harness session Herdr tracks for this agent. Its
	// value is the transcript identity the timeline's locator needs, and
	// kind/source say what sort of identity it is ("id", "herdr:codex").
	// A Claude agent carries no agent_session at all.
	AgentSession *struct {
		Agent  string `json:"agent"`
		Kind   string `json:"kind"`
		Source string `json:"source"`
		Value  string `json:"value"`
	} `json:"agent_session"`
}

func (a agentJSON) info() agentInfo {
	sessionRef := ""
	if a.AgentSession != nil {
		sessionRef = a.AgentSession.Value
	}
	return agentInfo{
		SessionRef:    sessionRef,
		Name:          a.Name,
		Kind:          a.Kind,
		Status:        parseAgentStatus(a.Status),
		LaunchPending: a.LaunchPending,
		PaneID:        a.PaneID,
		TabID:         a.TabID,
		TerminalID:    a.TerminalID,
		WorkspaceID:   a.WorkspaceID,
		Cwd:           a.Cwd,
	}
}

func parseAgentInfo(data []byte) (agentInfo, error) {
	var raw struct {
		Agent  *agentJSON  `json:"agent"`
		Agents []agentJSON `json:"agents"`
	}
	if err := parseResult(data, &raw); err != nil {
		return agentInfo{}, err
	}
	src := raw.Agent
	if src == nil && len(raw.Agents) == 1 {
		src = &raw.Agents[0]
	}
	if src == nil {
		return agentInfo{}, fmt.Errorf("herdr json: missing agent object")
	}
	return src.info(), nil
}

// parseAgentList is the live inventory. Empty is a valid inventory, not an
// error. Unlike parseAgentInfo it does not require exactly one agent.
// focused / active_tab_id are not on agentInfo: they are UI state and must
// not answer an identity question (ADR 0007).
func parseAgentList(data []byte) ([]agentInfo, error) {
	var raw struct {
		Agents []agentJSON `json:"agents"`
	}
	if err := parseResult(data, &raw); err != nil {
		return nil, err
	}
	out := make([]agentInfo, 0, len(raw.Agents))
	for _, a := range raw.Agents {
		out = append(out, a.info())
	}
	return out, nil
}

type paneInfo struct {
	PaneID     string
	TabID      string
	TerminalID string
	// Agent is the harness kind Herdr reports for the agent occupying this
	// pane ("claude", …). Live 0.8.2 omits the key entirely for a pane with
	// no agent, so an empty value means "no agent here".
	Agent         string
	WorkspaceID   string
	Cwd           string
	TerminalTitle string
	AgentStatus   string
}

func parsePaneInfo(data []byte) (paneInfo, error) {
	var raw struct {
		Pane *struct {
			PaneID                string `json:"pane_id"`
			TabID                 string `json:"tab_id"`
			TerminalID            string `json:"terminal_id"`
			Agent                 string `json:"agent"`
			WorkspaceID           string `json:"workspace_id"`
			Cwd                   string `json:"cwd"`
			TerminalTitle         string `json:"terminal_title"`
			TerminalTitleStripped string `json:"terminal_title_stripped"`
			AgentStatus           string `json:"agent_status"`
		} `json:"pane"`
	}
	if err := parseResult(data, &raw); err != nil {
		return paneInfo{}, err
	}
	if raw.Pane == nil || raw.Pane.PaneID == "" {
		return paneInfo{}, fmt.Errorf("herdr json: missing pane object")
	}
	title := raw.Pane.TerminalTitleStripped
	if title == "" {
		title = raw.Pane.TerminalTitle
	}
	return paneInfo{
		PaneID:        raw.Pane.PaneID,
		TabID:         raw.Pane.TabID,
		TerminalID:    raw.Pane.TerminalID,
		Agent:         raw.Pane.Agent,
		WorkspaceID:   raw.Pane.WorkspaceID,
		Cwd:           raw.Pane.Cwd,
		TerminalTitle: title,
		AgentStatus:   raw.Pane.AgentStatus,
	}, nil
}

// paneProcessInfo is what `herdr pane process-info` reports about the
// processes running in a pane. Herdr's own `name` field is truncated (live
// 0.8.2 reports "zsh (kiro-cli-t" for a 19-character command), so Cmdline is
// the field worth showing a human.
type paneProcessInfo struct {
	PaneID     string
	ShellPID   int
	Foreground []string
}

func parsePaneProcessInfo(data []byte) (paneProcessInfo, error) {
	var raw struct {
		ProcessInfo *struct {
			PaneID              string `json:"pane_id"`
			ShellPID            int    `json:"shell_pid"`
			ForegroundProcesses []struct {
				Cmdline string `json:"cmdline"`
				Name    string `json:"name"`
			} `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := parseResult(data, &raw); err != nil {
		return paneProcessInfo{}, err
	}
	if raw.ProcessInfo == nil {
		return paneProcessInfo{}, fmt.Errorf("herdr json: missing process_info object")
	}
	out := paneProcessInfo{PaneID: raw.ProcessInfo.PaneID, ShellPID: raw.ProcessInfo.ShellPID}
	for _, p := range raw.ProcessInfo.ForegroundProcesses {
		name := strings.TrimSpace(p.Cmdline)
		if name == "" {
			name = strings.TrimSpace(p.Name)
		}
		if name != "" {
			out.Foreground = append(out.Foreground, name)
		}
	}
	return out, nil
}

func parseAgentStatus(s string) AgentStatus {
	switch AgentStatus(s) {
	case AgentIdle, AgentWorking, AgentBlocked, AgentDone:
		return AgentStatus(s)
	default:
		return AgentUnknown
	}
}

func parseSessionList(data []byte) ([]sessionInfo, error) {
	var raw struct {
		Sessions []sessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("herdr session list: %w", err)
	}
	return raw.Sessions, nil
}

func parseStatus(data []byte) (statusInfo, error) {
	var raw struct {
		Server struct {
			Running  bool   `json:"running"`
			Socket   string `json:"socket"`
			Session  string `json:"session"`
			Version  string `json:"version"`
			Protocol int    `json:"protocol"`
		} `json:"server"`
		Client struct {
			Protocol int `json:"protocol"`
		} `json:"client"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return statusInfo{}, fmt.Errorf("herdr status: %w", err)
	}
	proto := raw.Server.Protocol
	if proto == 0 {
		proto = raw.Client.Protocol
	}
	return statusInfo{
		Running:  raw.Server.Running,
		Session:  raw.Server.Session,
		Socket:   raw.Server.Socket,
		Version:  raw.Server.Version,
		Protocol: proto,
	}, nil
}

func decodeCLIError(data []byte) (*cliError, bool) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	errObj, err := parseCLIError(trimmed)
	if err != nil {
		return nil, false
	}
	return errObj, true
}

// mapProcessFailure turns a herdr process result into a coded error.
// JSON envelopes on stderr (exit 1) are the observed machine-readable path.
// Exit 2 without a JSON envelope is CLI usage (live: unknown --kind).
func mapProcessFailure(exit int, stdout, stderr []byte) error {
	if exit == 0 {
		return nil
	}
	if errObj, ok := decodeCLIError(stderr); ok {
		return NewHerdrError(errObj.Code, errObj.Message)
	}
	if errObj, ok := decodeCLIError(stdout); ok {
		return NewHerdrError(errObj.Code, errObj.Message)
	}
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = strings.TrimSpace(string(stdout))
	}
	if msg == "" {
		msg = fmt.Sprintf("herdr exited %d", exit)
	}
	code := observability.CodeUnknown
	if exit == 2 {
		code = observability.CodeUsage
	}
	return observability.NewError(code, msg).WithDetails(map[string]any{
		"exit": exit,
	})
}

func herdrCodeOf(err error) string {
	var coded *observability.Error
	if !errors.As(err, &coded) || coded == nil || coded.Details == nil {
		return ""
	}
	s, _ := coded.Details["herdr_code"].(string)
	return s
}
