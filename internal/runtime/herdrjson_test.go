package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

func readRuntimeFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseWorkspaceCreatedFromLiveCapture(t *testing.T) {
	t.Parallel()
	got, err := parseWorkspaceCreated(readRuntimeFixture(t, "workspace-create.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkspaceID != "w1" || got.Label != "Project A" {
		t.Fatalf("workspace = %+v", got)
	}
	if got.TabID != "w1:t1" || got.PaneID != "w1:p1" || got.TerminalID == "" {
		t.Fatalf("initial tab/pane = %+v", got)
	}
}

func TestParseWorkspaceListEmptyFromLiveCapture(t *testing.T) {
	t.Parallel()
	ids, err := parseWorkspaceList(readRuntimeFixture(t, "workspace-list-empty.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("headless server must start with zero workspaces, got %#v", ids)
	}
}

func TestParseWorkspaceListFindsLabel(t *testing.T) {
	t.Parallel()
	list, err := parseWorkspaceList(readRuntimeFixture(t, "workspace-list-one.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].WorkspaceID != "w1" || list[0].Label != "Project A" {
		t.Fatalf("list = %#v", list)
	}
}

func TestParseTabCreatedFromLiveCapture(t *testing.T) {
	t.Parallel()
	got, err := parseTabCreated(readRuntimeFixture(t, "tab-create.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.TabID != "w1:t2" || got.Label != "Mate" {
		t.Fatalf("tab = %+v", got)
	}
	if got.PaneID != "w1:p2" || got.TerminalID == "" || got.WorkspaceID != "w1" {
		t.Fatalf("pane = %+v", got)
	}
}

func TestParseAgentListEmptyIsAnInventory(t *testing.T) {
	t.Parallel()
	got, err := parseAgentList(readRuntimeFixture(t, "agent-list-empty.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("empty list = %#v", got)
	}
}

func TestParseAgentListIgnoresFocus(t *testing.T) {
	t.Parallel()
	got, err := parseAgentList(readRuntimeFixture(t, "agent-list-after-start.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "mate-g4-01" || got[0].PaneID != "w1:p2" {
		t.Fatalf("list = %#v", got)
	}
	if got[0].WorkspaceID != "w1" {
		t.Fatalf("workspace id is identity, got %q", got[0].WorkspaceID)
	}
}

func TestParseAgentInfoFromLiveCapture(t *testing.T) {
	t.Parallel()
	got, err := parseAgentInfo(readRuntimeFixture(t, "agent-get.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "mate-g4-01" || got.Status != AgentBlocked {
		t.Fatalf("agent = %+v", got)
	}
	if !got.LaunchPending || got.PaneID != "w1:p2" {
		t.Fatalf("live blocked start = %+v", got)
	}
}

func TestParseWaitBlockedIsSuccessEnvelope(t *testing.T) {
	t.Parallel()
	got, err := parseAgentInfo(readRuntimeFixture(t, "wait-blocked.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != AgentBlocked {
		t.Fatalf("status = %q", got.Status)
	}
}

func TestParseCLIErrorFromLiveCaptures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file string
		code string
		tax  observability.Code
	}{
		{"error-agent-not-found-wait.json", HerdrAgentNotFound, observability.CodeNotFound},
		{"error-agent-not-found-get.json", HerdrAgentNotFound, observability.CodeNotFound},
		{"error-pane-not-found.json", HerdrAgentPaneNotFound, observability.CodeUsage},
		{"error-agent-blocked.json", HerdrAgentBlocked, observability.CodeTargetBlocked},
		{"error-agent-not-ready.json", HerdrAgentNotReady, observability.CodeTargetBlocked},
		{"error-timeout.json", HerdrTimeout, observability.CodeTimeout},
		{"error-name-taken.json", HerdrAgentNameTaken, observability.CodeAlreadyExists},
		{"error-invalid-name.json", HerdrInvalidAgentName, observability.CodeUsage},
		{"error-tab-workspace.json", HerdrWorkspaceNotFound, observability.CodeNotFound},
		{"error-start-timeout-small.json", HerdrInvalidAgentTimeout, observability.CodeUsage},
		{"error-pane-busy.json", HerdrAgentPaneBusy, observability.CodeStateConflict},
		// G4-07 captures. agent attach resolves its target before it touches
		// the terminal, so a missing agent is a clean envelope even with no
		// TTY; pane get and a down server are the other two attach paths.
		{"error-agent-not-found-attach.json", HerdrAgentNotFound, observability.CodeNotFound},
		{"error-pane-not-found-get.json", HerdrPaneNotFound, observability.CodeNotFound},
		{"error-server-not-running.json", HerdrServerNotRunning, observability.CodeRuntimeUnavailable},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.code+"/"+tc.file, func(t *testing.T) {
			t.Parallel()
			env, err := parseCLIError(readRuntimeFixture(t, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if env.Code != tc.code {
				t.Fatalf("code = %q, want %q", env.Code, tc.code)
			}
			if got := MapHerdrError(env.Code); got != tc.tax {
				t.Fatalf("taxonomy = %q, want %q", got, tc.tax)
			}
		})
	}
}

func TestHerdrLiveCapturesKeepPaneNotFoundDistinctFromAgentNotFound(t *testing.T) {
	t.Parallel()
	pane, err := parseCLIError(readRuntimeFixture(t, "error-pane-not-found.json"))
	if err != nil {
		t.Fatal(err)
	}
	missing, err := parseCLIError(readRuntimeFixture(t, "error-agent-not-found-wait.json"))
	if err != nil {
		t.Fatal(err)
	}
	if pane.Code == missing.Code {
		t.Fatal("live Herdr uses distinct codes for missing pane vs missing agent")
	}
	if MapHerdrError(pane.Code) == MapHerdrError(missing.Code) {
		t.Fatal("mapped taxonomy must keep the two codes in different buckets")
	}
}

func TestUnknownKindStderrIsNotJSONEnvelope(t *testing.T) {
	t.Parallel()
	raw := readRuntimeFixture(t, "error-unknown-kind.txt")
	if _, err := parseCLIError(raw); err == nil {
		t.Fatalf("human usage text must not parse as a JSON envelope: %s", raw)
	}
	err := mapProcessFailure(2, nil, raw)
	if observability.ExitCode(err) != observability.ExitUsage {
		t.Fatalf("exit 2 without JSON must be usage, got %v", err)
	}
}

func TestNamedSessionSocketFromLiveStatus(t *testing.T) {
	t.Parallel()
	st, err := parseStatus(readRuntimeFixture(t, "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Session == "default" {
		t.Fatalf("status = %+v", st)
	}
	man, err := loadHerdrManifest()
	if err != nil {
		t.Fatal(err)
	}
	if st.Protocol != man.Protocol {
		t.Fatalf("status protocol %d, manifest pins %d", st.Protocol, man.Protocol)
	}
	want := NamedSocketPath("/Users/anh/.config", st.Session)
	if st.Socket != want {
		t.Fatalf("socket = %q, want formula %q", st.Socket, want)
	}
}

func TestSessionListRefusesDefaultAsWorkspace(t *testing.T) {
	t.Parallel()
	list, err := parseSessionList(readRuntimeFixture(t, "session-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sawDefault, sawLab bool
	for _, s := range list {
		if s.Name == "default" {
			sawDefault = true
			if !s.Default {
				t.Fatalf("default session not marked default: %+v", s)
			}
		}
		if s.Name == "fm-lab-gomate-g4-s3-15761-16240" {
			sawLab = true
			if s.Default {
				t.Fatal("lab session must not be the default")
			}
			if !s.Running || s.SocketPath == "" {
				t.Fatalf("lab session = %+v", s)
			}
		}
	}
	if !sawDefault || !sawLab {
		t.Fatalf("fixture missing default or lab session: %#v", list)
	}
}
