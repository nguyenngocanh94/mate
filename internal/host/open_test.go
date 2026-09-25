package host

import (
	"path/filepath"
	"testing"
)

func TestResolveExecKeepsAbsolute(t *testing.T) {
	t.Parallel()
	got := resolveExec("/opt/herdr")
	if got != "/opt/herdr" {
		t.Fatalf("resolveExec(absolute) = %q, want /opt/herdr", got)
	}
}

func TestResolveExecLooksUpPATH(t *testing.T) {
	t.Parallel()
	got := resolveExec("sh")
	if !filepath.IsAbs(got) {
		t.Fatalf("resolveExec(sh) = %q, want absolute", got)
	}
	if filepath.Base(got) != "sh" {
		t.Fatalf("resolveExec(sh) = %q, want a sh binary", got)
	}
}

func TestGhosttyAttachCommandIsAbsoluteShellString(t *testing.T) {
	t.Parallel()
	got := ghosttyAttachCommand("/opt/herdr", "mate-acme", "mate-shop")
	want := "/opt/herdr --session mate-acme agent attach mate-shop"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
