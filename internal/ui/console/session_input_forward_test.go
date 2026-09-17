package console

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// ADR 0026 step 6: these tests drive the full Model (not just encodeKeyMsg
// in isolation) to prove keys and mouse events reach the PTY exactly the
// way session_mode.go/update.go route them once stream mode is active, and
// that the composer never gets a byte of it. streamControllerFixture and
// controllerTestFactory/controllerTestChannel are session_stream_controller_test.go's
// own doubles - reused rather than re-invented so this file exercises the
// same wiring the rest of that file already pins.
//
// A counter-review of an earlier draft found writes going straight to
// SessionChannel.Write from a per-key tea.Cmd: Bubble Tea 1.2.4 runs every
// Cmd on its own unsynchronised goroutine, so two keystrokes typed in order
// were not guaranteed to reach the PTY in that order. The fix
// (streamSession.enqueueWrite/writeLoop, session_stream_controller.go) moves
// the actual SessionChannel.Write call onto one dedicated goroutine per
// stream, fed by a queue Update enqueues into synchronously - so
// onSessionStreamKey/onSessionStreamMouse no longer return a write Cmd at
// all, and these tests poll the channel's recorded writes instead of
// invoking one.

// waitForWrites polls channel.writtenBytes() until it has at least n
// entries or a short timeout elapses, since writeLoop applies an enqueued
// write on its own goroutine rather than synchronously within send(). This
// is condition-based waiting (poll until the actual state changes), not an
// arbitrary sleep: it returns as soon as the condition is met and only
// times out if the write genuinely never happens.
func waitForWrites(t *testing.T, channel *controllerTestChannel, n int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := channel.writtenBytes()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d write(s); got %d: %q", n, len(got), got)
		}
		time.Sleep(time.Millisecond)
	}
}

// enterStreamMode drives a Model through Enter -> open -> first read, the
// same three-send sequence every test in session_stream_controller_test.go
// uses, and hands back the one channel the fixture opened so a test can
// inspect what was written to it.
func enterStreamMode(t *testing.T, factory *controllerTestFactory) (Model, *controllerTestChannel) {
	t.Helper()
	reader := func(context.Context, SessionTarget) (SessionSnapshot, error) {
		return SessionSnapshot{}, errors.New("snapshot reader must be fallback only")
	}
	m := streamControllerFixture(t, factory.Open, reader)
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter did not start stream open")
	}
	m, cmd = send(t, m, cmd())
	if cmd == nil {
		t.Fatal("successful stream open did not start its reader")
	}
	m, _ = send(t, m, cmd())
	if m.sess.phase != sessionActive || m.sess.stream == nil {
		t.Fatalf("setup: stream mode did not become active (phase=%v, stream=%v)", m.sess.phase, m.sess.stream != nil)
	}
	if len(factory.channels) != 1 {
		t.Fatalf("setup: want exactly one opened channel, got %d", len(factory.channels))
	}
	return m, factory.channels[0]
}

func TestStreamModeForwardsPrintableUTF8IncludingVietnameseAndNeverTouchesTheComposer(t *testing.T) {
	m, channel := enterStreamMode(t, &controllerTestFactory{})

	m, cmd := send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Tiếng Việt")})
	if cmd != nil {
		t.Fatalf("printable key produced a Cmd; want nil (the write is enqueued synchronously, not via a Cmd)")
	}

	got := waitForWrites(t, channel, 1)
	if len(got) != 1 || string(got[0]) != "Tiếng Việt" {
		t.Fatalf("written bytes = %q, want %q", got, "Tiếng Việt")
	}
	if m.sess.composer != "" {
		t.Fatalf("composer = %q, want empty: stream mode must never accumulate a composer string", m.sess.composer)
	}
}

func TestStreamModeForwardsEscToTheAgentInsteadOfLeavingSessionMode(t *testing.T) {
	m, channel := enterStreamMode(t, &controllerTestFactory{})

	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.sess.phase != sessionActive {
		t.Fatalf("esc left session mode (phase=%v); ADR 0026 step 6 forwards it to the agent instead", m.sess.phase)
	}
	got := waitForWrites(t, channel, 1)
	if len(got) != 1 || !bytes.Equal(got[0], []byte{0x1b}) {
		t.Fatalf("written bytes = %x, want %x", got, []byte{0x1b})
	}
}

// TestStreamModeForwardsCtrlCToTheAgentInsteadOfQuitting is stream mode's
// counterpart to session_lifecycle_test.go's
// TestCtrlCWhileSessionActiveQuitsTheWholeConsoleRatherThanBeingComposerInput,
// which pins the OLD, snapshot-mode-only behavior. ADR 0026 inverts Ctrl+C
// for stream mode specifically (session-view-contract.md, "Ctrl+C is the
// second key that inverts"); this is the regression that would catch a
// change that let the old quit-on-Ctrl+C branch swallow it again.
func TestStreamModeForwardsCtrlCToTheAgentInsteadOfQuitting(t *testing.T) {
	m, channel := enterStreamMode(t, &controllerTestFactory{})

	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quitting {
		t.Fatalf("ctrl+c quit the Console while stream mode was active")
	}
	got := waitForWrites(t, channel, 1)
	if len(got) != 1 || !bytes.Equal(got[0], []byte{0x03}) {
		t.Fatalf("written bytes = %x, want %x", got, []byte{0x03})
	}
}

// TestStreamModeForwardsEveryRoutedKeyThroughTheFullModel closes a
// counter-review gap: encodeKeyMsg's own unit tests cover Backspace, Tab,
// arrows and Home/End at the encoder level, but nothing previously drove
// them through the real Model/Update routing this step adds (the layer
// where an ordering or routing bug would actually surface, not the encoder
// itself). One fresh stream per case keeps them independent.
func TestStreamModeForwardsEveryRoutedKeyThroughTheFullModel(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyMsg
		want []byte
	}{
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, []byte{0x7f}},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}, []byte{0x09}},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, []byte("\x1b[A")},
		{"down", tea.KeyMsg{Type: tea.KeyDown}, []byte("\x1b[B")},
		{"home", tea.KeyMsg{Type: tea.KeyHome}, []byte("\x1b[H")},
		{"end", tea.KeyMsg{Type: tea.KeyEnd}, []byte("\x1b[F")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, channel := enterStreamMode(t, &controllerTestFactory{})
			m, cmd := send(t, m, tc.msg)
			if cmd != nil {
				t.Fatalf("%s produced a Cmd; want nil", tc.name)
			}
			got := waitForWrites(t, channel, 1)
			if len(got) != 1 || !bytes.Equal(got[0], tc.want) {
				t.Fatalf("%s written bytes = %x, want %x", tc.name, got, tc.want)
			}
		})
	}
}

// TestStreamModeKeystrokesReachThePTYInOrder is the direct regression test
// for the ordering bug a counter-review found: several keys sent to Update
// in one order must be written to the channel in that same order, even
// though writeLoop applies them on its own goroutine.
func TestStreamModeKeystrokesReachThePTYInOrder(t *testing.T) {
	m, channel := enterStreamMode(t, &controllerTestFactory{})

	letters := "abcdefgh"
	for _, r := range letters {
		m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	got := waitForWrites(t, channel, len(letters))
	var joined []byte
	for _, w := range got[:len(letters)] {
		joined = append(joined, w...)
	}
	if string(joined) != letters {
		t.Fatalf("written bytes = %q, want %q (in-order)", joined, letters)
	}
}

// TestStreamModeForwardsCtrlBToThePTY: the prefix is gone. Ctrl+b belongs
// to the agent like every other key under terminal focus - a harness that
// binds it (or a shell running under one) gets it byte-exactly.
func TestStreamModeForwardsCtrlBToThePTY(t *testing.T) {
	m, channel := enterStreamMode(t, &controllerTestFactory{})

	m, cmd := send(t, m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if cmd != nil {
		t.Fatalf("ctrl+b produced a Cmd; want the byte forwarded and nothing else")
	}
	if m.sess.phase != sessionActive {
		t.Fatalf("ctrl+b left session mode; nothing about it is a Console key any more")
	}
	got := waitForWrites(t, channel, 1)
	if len(got) != 1 || !bytes.Equal(got[0], []byte{0x02}) {
		t.Fatalf("written bytes = %q, want ctrl+b's own byte", got)
	}
}

// TestStreamModeEscFromBoxFocusDetachesWithoutForwardingIt: under box focus
// nothing reaches the PTY, and Esc is the way out of the view.
func TestStreamModeEscFromBoxFocusDetachesWithoutForwardingIt(t *testing.T) {
	m, channel := enterStreamMode(t, &controllerTestFactory{})

	m, cmd := send(t, m, tea.KeyMsg{Type: tea.KeyF2})
	if cmd != nil || m.sess.zone != zoneBox {
		t.Fatalf("F2 did not focus the box (zone %v, cmd %v)", m.sess.zone, cmd != nil)
	}
	m, closeCmd := send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.sess.phase == sessionActive {
		t.Fatalf("Esc under box focus did not leave session mode")
	}
	if closeCmd != nil {
		send(t, m, closeCmd())
	}
	if len(channel.writtenBytes()) != 0 {
		t.Fatalf("keys under box focus reached the PTY: %q", channel.writtenBytes())
	}
}

// TestStreamModeForwardsMouseEventsToThePTYInPaneCoordinates: a click
// inside the terminal zone is forwarded, translated to the agent's own
// screen origin. The agent draws at (0,0) of its pane, not of the Console's
// frame, so an untranslated report would name a different cell entirely.
func TestStreamModeForwardsMouseEventsToThePTYInPaneCoordinates(t *testing.T) {
	m, channel := enterStreamMode(t, &controllerTestFactory{})
	geo := m.sessionGeom()

	m, cmd := send(t, m, tea.MouseMsg{
		X: geo.paneX + 4, Y: geo.paneTop + 9,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if cmd != nil {
		t.Fatalf("mouse press produced a Cmd; want nil (the write is enqueued synchronously)")
	}
	if m.sess.zone != zoneTerminal {
		t.Fatalf("a click in the terminal zone left focus at %v", m.sess.zone)
	}
	got := waitForWrites(t, channel, 1)
	want := []byte("\x1b[<0;5;10M")
	if len(got) != 1 || !bytes.Equal(got[0], want) {
		t.Fatalf("written bytes = %q, want %q (pane-relative)", got, want)
	}
}

// TestMouseEventsOutsideStreamModeAreDropped proves onMouse's guard: a
// MouseMsg reaching a Model that is not in an active stream (the ordinary
// navigation tree, here) must not panic on a nil stream and must produce no
// Cmd - mouse mode is enabled Program-wide (cmd/matev2/console.go), so this
// case is reachable in production any time the reader moves the mouse
// outside an open Agent View session.
func TestMouseEventsOutsideStreamModeAreDropped(t *testing.T) {
	spy := &attachSpy{tree: sameTree(sampleTree())}
	m := New(spy.load, spy.attach)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
	m, _ = send(t, m, m.Init()())

	m, cmd := send(t, m, tea.MouseMsg{X: 1, Y: 1, Action: tea.MouseActionMotion})
	if cmd != nil {
		t.Fatalf("mouse event outside stream mode produced a Cmd, want nil")
	}
}

// TestRenderStreamSessionFrameNeverDrawsComposerChrome is the rendering
// half of the captain's ruling (2026-09-12): the composer box's corner
// glyphs, which every snapshot-mode frame draws, must not appear anywhere
// in a stream-mode frame - the PTY's own screen already carries whatever
// composer the harness draws, and a second Console-drawn one on top is the
// exact defect the ruling was written to close.
func TestRenderStreamSessionFrameNeverDrawsComposerChrome(t *testing.T) {
	buffer := NewTerminalBuffer(76, 20)
	frame := RenderStreamSessionFrame(SessionSnapshot{Target: SessionTarget{Kind: SessionTargetMate}}, buffer, false, boxRail{sel: -1}, 160, 48, unicodeGlyphs, plainPalette())
	if strings.Contains(frame, unicodeGlyphs.CornerTL) || strings.Contains(frame, unicodeGlyphs.CornerBL) {
		t.Fatalf("stream frame drew composer chrome:\n%s", frame)
	}
	snapshotFrame := RenderSessionFrame(SessionSnapshot{Target: SessionTarget{Kind: SessionTargetMate}}, "", boxRail{sel: -1}, 160, 48, unicodeGlyphs, plainPalette())
	if !strings.Contains(snapshotFrame, unicodeGlyphs.CornerTL) {
		t.Fatalf("setup: snapshot mode's own composer chrome is missing from its frame, this test's contrast is meaningless")
	}
}

// TestStreamFallbackDropsKeysInsteadOfAccumulatingAnInvisibleComposer is a
// counter-review regression: beginStreamFallback deliberately keeps
// m.sess.terminal set (the dead PTY frame stays on screen so it does not
// flash away and back) while phase moves to sessionFallback and the
// snapshot's own entry read is in flight. RenderStreamSessionFrame draws no
// composer for that frame (the captain's ruling), so a key typed during
// this window used to be silently appended to a composer nothing shows -
// invisible input an Enter would later submit for real. Update must drop
// it instead.
func TestStreamFallbackDropsKeysInsteadOfAccumulatingAnInvisibleComposer(t *testing.T) {
	factory := &controllerTestFactory{}
	reader := &recordingSessionController{snap: SessionSnapshot{Runtime: SessionRuntime{Status: query.Known}}}
	m := streamControllerFixture(t, factory.Open, reader.Read)

	m, cmd := send(t, m, key("enter"))
	m, cmd = send(t, m, cmd())
	m, cmd = send(t, m, cmd()) // apply the queued "old\n" chunk, schedule the next read
	if cmd == nil {
		t.Fatal("setup: no further read was scheduled after the first chunk")
	}
	m, _ = send(t, m, cmd()) // the next read hits EOF (no more queued output) -> triggers fallback
	if m.sess.phase != sessionFallback {
		t.Fatalf("setup: want sessionFallback after the stream's EOF, got phase=%v", m.sess.phase)
	}
	if m.sess.terminal == nil {
		t.Fatalf("setup: want the dead stream frame to remain on screen during fallback (beginStreamFallback's own documented behavior)")
	}

	m, keyCmd := send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if keyCmd != nil {
		t.Fatalf("a key during stream fallback produced a Cmd; want nil (dropped)")
	}
	if m.sess.composer != "" {
		t.Fatalf("composer = %q, want empty: input must be dropped while the composer-less dead stream frame is on screen, not accumulated invisibly", m.sess.composer)
	}
}

// TestRenderStreamSessionFrameAlwaysSaysWhichZoneOwnsTheKeyboard: once
// every key under terminal focus goes to the agent, the hint line is the
// only thing on screen that says so and names the key that moves focus
// back. Checked at a Crew target (no rail ever) and a narrow (80-column)
// Mate target (rail suppressed) - the two shapes with the least chrome.
func TestRenderStreamSessionFrameAlwaysSaysWhichZoneOwnsTheKeyboard(t *testing.T) {
	crewBuffer := NewTerminalBuffer(78, 22)
	crewFrame := RenderStreamSessionFrame(SessionSnapshot{Target: SessionTarget{Kind: SessionTargetCrew}}, crewBuffer, false, boxRail{sel: -1}, 160, 48, unicodeGlyphs, plainPalette())
	for _, want := range []string{"TERMINAL", "every key goes to the agent", "F2"} {
		if !strings.Contains(crewFrame, want) {
			t.Fatalf("Crew stream frame does not name %q:\n%s", want, crewFrame)
		}
	}

	narrowMateBuffer := NewTerminalBuffer(78, 20)
	narrowMateFrame := RenderStreamSessionFrame(SessionSnapshot{Target: SessionTarget{Kind: SessionTargetMate}}, narrowMateBuffer, false, boxRail{sel: -1}, 80, 24, unicodeGlyphs, plainPalette())
	for _, want := range []string{"TERMINAL", "F2"} {
		if !strings.Contains(narrowMateFrame, want) {
			t.Fatalf("narrow Mate stream frame does not name %q:\n%s", want, narrowMateFrame)
		}
	}

	boxFocused := RenderStreamSessionFrame(SessionSnapshot{Target: SessionTarget{Kind: SessionTargetMate}}, NewTerminalBuffer(76, 20), false,
		boxRail{sel: -1, zone: zoneBox}, 160, 48, unicodeGlyphs, plainPalette())
	if !strings.Contains(boxFocused, "BOX") || strings.Contains(boxFocused, "every key goes to the agent") {
		t.Fatalf("a box-focused frame still names the terminal zone's keys:\n%s", boxFocused)
	}
}
