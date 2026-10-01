//go:build darwin || linux

package send_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
)

// No model tokens: /status is local and the isolated provider is unreachable.
// A private pane in an explicitly provisioned fm-lab session owns every input
// and signal. The test never addresses an operator's agent or CODEX_HOME.
func TestLiveCodexDelayedPasteAndRecovery(t *testing.T) {
	requireLive(t)
	session := os.Getenv("MATE_HERDR_LIVE_SESSION")
	if !strings.HasPrefix(session, "fm-lab-") {
		t.Fatal("provision an fm-lab-* session and set MATE_HERDR_LIVE_SESSION")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cli := func(args ...string) []byte {
		t.Helper()
		out, err := exec.CommandContext(ctx, "herdr", runtime.WithSession(session, args)...).CombinedOutput()
		if err != nil {
			t.Fatalf("herdr %v: %v\n%s", args, err, out)
		}
		return out
	}
	// Keep daemon Unix socket paths below macOS's 104-byte limit. A long
	// t.TempDir test-name prefix prevents Codex's daemon from starting.
	root, err := os.MkdirTemp("/tmp", "mate-paste-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "codex-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("model = \"gpt-6-sol\"\nmodel_provider = \"probe\"\n[model_providers.probe]\nname = \"Local input probe\"\nbase_url = \"http://127.0.0.1:1/v1\"\nwire_api = \"responses\"\n[projects.%q]\ntrust_level = \"trusted\"\n[tui]\nscreen_reader_detection_done = true\n", root)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var created struct {
		Result struct {
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
			Workspace struct {
				ID string `json:"workspace_id"`
			} `json:"workspace"`
		} `json:"result"`
	}
	if err := json.Unmarshal(cli("workspace", "create", "--cwd", root, "--label", "paste-probe", "--no-focus", "--env", "CODEX_HOME="+home), &created); err != nil {
		t.Fatal(err)
	}
	pane, workspace := created.Result.RootPane.PaneID, created.Result.Workspace.ID
	if pane == "" || workspace == "" {
		t.Fatal("missing owned pane/workspace")
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if out, err := exec.CommandContext(cleanup, "herdr", "--session", session, "workspace", "close", workspace).CombinedOutput(); err != nil {
			t.Errorf("close lab: %v %s", err, out)
		}
		cmd := exec.CommandContext(cleanup, "codex", "app-server", "daemon", "stop")
		cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("stop lab daemon: %v %s", err, out)
		}
		stopPasteLabProcesses(t, cleanup, home)
	}()
	// Bootstrap before asking Herdr to detect the TUI. The daemon installer
	// and updater are not a Codex composer and can be misidentified by Herdr.
	bootstrap := exec.CommandContext(ctx, "codex", "app-server", "daemon", "start")
	bootstrap.Env = append(os.Environ(), "CODEX_HOME="+home)
	if out, err := bootstrap.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap isolated daemon: %v\n%s", err, out)
	}
	name := fmt.Sprintf("paste-probe-%d", time.Now().UnixNano())
	for attempt := 0; ; attempt++ {
		args := runtime.WithSession(session, []string{"agent", "start", name, "--kind", "codex", "--pane", pane, "--timeout", "30000"})
		out, err := exec.CommandContext(ctx, "herdr", args...).CombinedOutput()
		if err == nil {
			break
		}
		if attempt >= 30 || !strings.Contains(string(out), "agent_pane_busy") {
			t.Fatalf("start lab Codex: %v\n%s", err, out)
		}
		time.Sleep(100 * time.Millisecond) // wait for the newly created shell
	}
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: session}, Name: name, Kind: harness.KindCodex, Tab: runtime.TabHandle{PaneID: pane}}
	rt := runtime.NewHerdr(process.ExecRunner{})
	read := func() string {
		t.Helper()
		s, err := rt.ReadAgentStyled(ctx, h, harness.ReadRecentUnwrapped, 80)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	waitState := func(want send.ComposerState, budget time.Duration) string {
		t.Helper()
		var s string
		for deadline := time.Now().Add(budget); time.Now().Before(deadline); {
			s = read()
			c := send.ClassifyComposer((harness.Codex{}).Screen(), s)
			if c.State == want && strings.Contains(send.StripSGR(s), " · ") {
				return s
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("wanted %s; screen:\n%s", want, s)
		return ""
	}
	waitState(send.StateEmpty, time.Minute)
	var proc struct {
		Result struct {
			Info struct {
				Processes []struct {
					Name string `json:"name"`
					PID  int    `json:"pid"`
					Cwd  string `json:"cwd"`
				} `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal(cli("pane", "process-info", "--pane", pane), &proc); err != nil {
		t.Fatal(err)
	}
	pid := 0
	for _, p := range proc.Result.Info.Processes {
		if p.Name == "codex" && p.Cwd == root {
			pid = p.PID
		}
	}
	if pid <= 1 {
		t.Fatal("cannot prove the owned Codex TUI process")
	}
	payload := "/status" + strings.Repeat(" ", 643)
	stalled := func() {
		t.Helper()
		if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		defer syscall.Kill(pid, syscall.SIGCONT)
		if err := rt.SendText(ctx, h, payload); err != nil {
			t.Fatal(err)
		}
		time.Sleep(send.DefaultSettle)
		for i := 0; i < send.DefaultRetries; i++ {
			if err := rt.SendKeys(ctx, h, []string{"enter"}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(send.DefaultRetrySleep)
		}
	}
	// Seed a known unsubmitted draft. The raw-byte race is scheduler-sensitive:
	// some of its delayed Enters can arrive after the heuristic expires. Its
	// separate A/B reproduction is in docs/evidence/enter-paste-2026-09-28.md.
	// Do not mistake a transient pending snapshot for a stranded draft here.
	if err := rt.SendText(ctx, h, payload); err != nil {
		t.Fatal(err)
	}
	pending := waitState(send.StatePending, 10*time.Second)
	t.Logf("owned unsubmitted draft:\n%s", send.StripSGR(pending))
	report, err := send.Send(ctx, send.Deps{Harnesses: catalog.Default(), Runtime: rt}, h, harness.KindCodex, payload, send.Options{ResumePending: true})
	if err != nil || !report.Delivered() || report.Typed || !report.Resumed {
		t.Fatalf("recovery: %+v %v\nscreen after refusal: %q", report, err, send.StripSGR(read()))
	}
	before := waitState(send.StateEmpty, 10*time.Second)
	stalled()
	// Let the resumed consumer actually process the queued input before
	// reading; its pre-resume empty screen is not a delivery confirmation.
	time.Sleep(time.Second)
	cleared := waitState(send.StateEmpty, 10*time.Second)
	// The echoed command above the first status card can scroll out of
	// Herdr's viewport. Both cards' token rows remain visible here.
	if strings.Count(send.StripSGR(cleared), "Token usage:") <= strings.Count(send.StripSGR(before), "Token usage:") {
		t.Fatalf("no local status output:\n%s", cleared)
	}
	t.Logf("bracketed paste clears under the same delay:\n%s", send.StripSGR(cleared))
}

// Codex daemon stop can leave an updater and plugin-fetch children. Find
// only commands naming this test's private home, then their descendants.
func stopPasteLabProcesses(t *testing.T, ctx context.Context, home string) {
	t.Helper()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,args=").Output()
	if err != nil {
		t.Errorf("inspect lab cleanup: %v", err)
		return
	}
	parents := map[int]int{}
	owned := map[int]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		parent, _ := strconv.Atoi(fields[1])
		if pid <= 1 {
			continue
		}
		parents[pid] = parent
		if strings.Contains(strings.Join(fields[2:], " "), home+"/") {
			owned[pid] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for pid, parent := range parents {
			if owned[parent] && !owned[pid] {
				owned[pid] = true
				changed = true
			}
		}
	}
	for pid := range owned {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}
