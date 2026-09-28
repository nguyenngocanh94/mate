package host

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestResolveExecKeepsAbsolute(t *testing.T) {
	t.Parallel()
	if got := ResolveExec("/opt/herdr"); got != "/opt/herdr" {
		t.Fatalf("ResolveExec(absolute) = %q, want /opt/herdr", got)
	}
}

func TestResolveExecLooksUpPATH(t *testing.T) {
	t.Parallel()
	got := ResolveExec("sh")
	if !filepath.IsAbs(got) || filepath.Base(got) != "sh" {
		t.Fatalf("ResolveExec(sh) = %q, want an absolute sh", got)
	}
}

func TestAttachArgvTakesTheAgentOver(t *testing.T) {
	t.Parallel()
	got := AttachArgv("/opt/herdr", "mate-acme", "mate-shop")
	want := []string{"/opt/herdr", "--session", "mate-acme", "agent", "attach", "mate-shop", "--takeover"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
