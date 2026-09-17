package runtime

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestStartHerdrServerRefusesSunPathOverflow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/"+strings.Repeat("x", 200))
	t.Setenv("HERDR_CONFIG_PATH", "")
	err := startHerdrServer("herdr", "mate-0123456789abcdef0123456789abcdef")
	if err == nil {
		t.Fatal("expected sun_path overflow to be refused before spawn")
	}
	if !strings.Contains(err.Error(), "sun_path") {
		t.Fatalf("err = %v", err)
	}
}

// TestHerdrServerEnvPassesOperatorEnvironmentMinusMateNamespace pins the
// ADR 0007 "unproven" question it left open ("whether Herdr needs env keys
// beyond the server allowlist") as now proven: a herdr server mate cold-starts
// forks the login shell for every new pane with its own environment, and a
// fixed allowlist has no way to anticipate what an operator's shell profile
// needs (live-diagnosed: Amazon Q's shell integration on one machine, but the
// defect generalizes to any shell-startup dependency) - the missing variable
// made every freshly created pane's shell fail Herdr's own `agent start`
// permanently (agent_pane_busy), not transiently: proven live not to clear
// after 20s of extra warm-up or on a second pane in the same session. The
// fix inherits the calling process's own environment (already just the
// operator's shell environment) and strips only mate's own MATEV2_* namespace
// - deliberately not ambient here (ADR 0007), injected per pane instead via
// `--env` / AllowlistedEnv.
func TestHerdrServerEnvPassesOperatorEnvironmentMinusMateNamespace(t *testing.T) {
	t.Setenv("SOME_SHELL_INTEGRATION_VAR", "keep-me")
	t.Setenv("MATEV2_AGENT_ID", "strip-me")
	t.Setenv("MATEV2_WORKSPACE_ID", "strip-me-too")

	env := herdrServerEnv()

	found := map[string]bool{}
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		found[key] = true
		if strings.HasPrefix(key, "MATEV2_") {
			t.Fatalf("herdrServerEnv leaked a mate identity var into the spawned server: %q", kv)
		}
	}
	for _, want := range []string{"PATH", "HOME", "SOME_SHELL_INTEGRATION_VAR"} {
		if !found[want] {
			t.Fatalf("herdrServerEnv dropped %q; the operator's own shell environment must reach the spawned server so its panes' login shells behave the way the operator's own shell does", want)
		}
	}
}

func TestHerdrServerCommandSetsSetsid(t *testing.T) {
	t.Parallel()
	cmd := herdrServerCommand("herdr", "lab-session")
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("adapter-spawned herdr server must Setsid so SIGHUP on the caller's terminal does not kill the Mate")
	}
}

func TestSetsidDetachesFromCallerProcessGroup(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sleep", "30")
	applyHerdrServerProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	parent, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	child, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if child == parent {
		t.Fatalf("child pgid %d shares the caller's pgid %d; SIGHUP to the terminal group would kill the server", child, parent)
	}
}
