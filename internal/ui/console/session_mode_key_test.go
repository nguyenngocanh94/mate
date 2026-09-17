package console

import (
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// mvp.md task 09 step 3: the communication mode is on screen and there is a
// key that flips it. These tests pin the three things that can silently go
// wrong: the key reaching the ActionFunc with the Project it is looking at,
// the stream-mode binding living behind the detach prefix (a bare 'm' would
// be eaten by the agent's own terminal), and the header naming the mode.

// hasModeHint reports whether the key line offers the mode toggle by name.
// The description matters: mvp.md task 09 requires it to read "Mode" and
// not something that promises the daemon task 19 will add.
func hasModeHint(hints []keyHint) bool {
	for _, h := range hints {
		if h.key == "m" && h.desc == "Mode" {
			return true
		}
	}
	return false
}

func TestTheModeKeyAsksTheActionFuncToFlipTheSelectedProject(t *testing.T) {
	t.Parallel()
	var got []ActionRequest
	tree := sampleTree()
	m := loaded(t, tree, nil)
	m.action = func(_ context.Context, req ActionRequest) (string, error) {
		got = append(got, req)
		return "payments-api is now auto", nil
	}

	// At the Workspace the key does nothing: the frame lists several
	// Projects and the mode is a per-Project setting.
	ws, cmd := send(t, m, key("m"))
	if cmd != nil || len(got) != 0 {
		t.Fatalf("m at the Workspace ran %d action(s); want none", len(got))
	}
	if hasModeHint(ws.keyHints(layout(ws.w, ws.h))) {
		t.Fatalf("the Workspace key line offers m: %s", keysOf(ws.keyHints(layout(ws.w, ws.h))))
	}

	proj, _ := send(t, m, key("enter"))
	if !hasModeHint(proj.keyHints(layout(proj.w, proj.h))) {
		t.Fatalf("the Project key line does not offer m Mode: %s", keysOf(proj.keyHints(layout(proj.w, proj.h))))
	}
	after, cmd := send(t, proj, key("m"))
	if cmd == nil {
		t.Fatal("m on a Project frame produced no command")
	}
	if !after.actionBusy {
		t.Fatal("m did not mark the action in flight")
	}
	after, _ = send(t, after, cmd())
	if len(got) != 1 {
		t.Fatalf("ActionFunc calls = %d, want 1", len(got))
	}
	if got[0].Action != ActionMode || got[0].Target != tree.Projects[0].ProjectID || got[0].TargetKind != "project" {
		t.Fatalf("request = %+v, want a mode request for the open Project", got[0])
	}
	if !strings.Contains(after.msg.text, "is now auto") {
		t.Fatalf("message = %q, want the outcome the ActionFunc returned", after.msg.text)
	}
}

// TestStreamModeBindsTheModeKeyBehindTheDetachPrefix: stream mode hands
// every unprefixed key to the agent, so 'm' alone must reach the PTY and
// only "ctrl+b m" may flip the mode.
func TestStreamModeBindsTheModeKeyBehindTheDetachPrefix(t *testing.T) {
	factory := &controllerTestFactory{}
	m, channel := enterStreamMode(t, factory)
	var got []ActionRequest
	m.action = func(_ context.Context, req ActionRequest) (string, error) {
		got = append(got, req)
		return "flipped", nil
	}

	bare, cmd := send(t, m, key("m"))
	if cmd != nil {
		t.Fatal("a bare m in stream mode produced a Cmd; want the byte forwarded and nothing else")
	}
	if len(got) != 0 {
		t.Fatalf("a bare m ran %d action(s); it belongs to the agent's terminal", len(got))
	}
	writes := waitForWrites(t, channel, 1)
	if string(writes[0]) != "m" {
		t.Fatalf("bare m wrote %q to the PTY, want \"m\"", writes[0])
	}

	prefixed, cmd := send(t, bare, key("ctrl+b"))
	if cmd != nil || !prefixed.sess.awaitingDetach {
		t.Fatal("ctrl+b did not arm the prefix")
	}
	prefixed, cmd = send(t, prefixed, key("m"))
	if cmd == nil {
		t.Fatal("ctrl+b m produced no command")
	}
	if prefixed.sess.phase != sessionActive {
		t.Fatalf("ctrl+b m left stream mode (phase %v); only ctrl+b q does", prefixed.sess.phase)
	}
	prefixed, _ = send(t, prefixed, cmd())
	if len(got) != 1 || got[0].Action != ActionMode {
		t.Fatalf("ctrl+b m requests = %+v, want one mode request", got)
	}
	if got[0].Target != m.sess.target.ProjectID {
		t.Fatalf("mode target = %q, want the streamed session's Project %q", got[0].Target, m.sess.target.ProjectID)
	}
	if len(channel.writtenBytes()) != 1 {
		t.Fatalf("ctrl+b m forwarded bytes to the PTY: %q", channel.writtenBytes())
	}
	_ = prefixed
}

// TestTheSessionHeaderNamesTheCommunicationMode: the header is where a
// reader looking at a live Mate learns whether anything may be typed into
// that pane on their behalf.
func TestTheSessionHeaderNamesTheCommunicationMode(t *testing.T) {
	t.Parallel()
	for _, mode := range []query.Mode{query.ModeSupervised, query.ModeAuto} {
		target := sessionTestTarget(SessionTargetMate)
		target.Mode = mode
		line := sessionHeaderLine(target, query.KnownField("running"), unicodeGlyphs, defaultPalette())
		if !strings.Contains(line.render(120), string(mode)) {
			t.Fatalf("header %q does not name mode %q", line.render(120), mode)
		}
	}
	// An unpopulated mode is omitted rather than guessed at "supervised".
	target := sessionTestTarget(SessionTargetMate)
	target.Mode = ""
	rendered := sessionHeaderLine(target, query.KnownField("running"), unicodeGlyphs, defaultPalette()).render(120)
	if strings.Contains(rendered, "supervised") || strings.Contains(rendered, "auto") {
		t.Fatalf("header %q invented a mode", rendered)
	}
}

// TestARefreshRepointsTheOpenSessionsModeLabel: the session view keeps its
// own target while it is open, so without the re-read hook the header would
// keep naming the mode the target carried at entry - the very value the
// key just changed.
func TestARefreshRepointsTheOpenSessionsModeLabel(t *testing.T) {
	t.Parallel()
	tree := sampleTree()
	m := loaded(t, tree, nil)
	m.sess.target = SessionTarget{Kind: SessionTargetMate, ProjectID: tree.Projects[0].ProjectID, Mode: query.ModeSupervised}
	m.sess.snapshot.Target = m.sess.target

	flipped := sampleTree()
	flipped.Projects[0].Mode = query.ModeAuto
	after := m.onTreeLoaded(treeLoadedMsg{tree: flipped})
	if after.sess.target.Mode != query.ModeAuto || after.sess.snapshot.Target.Mode != query.ModeAuto {
		t.Fatalf("session target mode = %q/%q, want auto after the re-read",
			after.sess.target.Mode, after.sess.snapshot.Target.Mode)
	}
}
