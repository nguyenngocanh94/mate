package console

// View implements tea.Model. It has one job: hand the frame back. Every
// line it returns came from the cell grid in cells.go, which is what makes
// "exactly h lines of exactly w display cells" a property of the package
// rather than of each screen (see frame.go).
func (m Model) View() string {
	if m.quitting {
		// Bubble Tea leaves the alt screen after this; drawing one more
		// frame would flash it onto the shell the reader is returning to.
		return ""
	}
	if m.sess.phase == sessionActive || m.sess.phase == sessionFallback {
		if m.w <= 0 || m.h <= 0 {
			return ""
		}
		if m.peek.open {
			// The peek overlay is modal (box_keys.go's onPeekKey), so it takes
			// the whole frame rather than a pane of it: it is a crew's screen,
			// and cropping someone else's terminal into a 36-column rail would
			// misalign every line the harness drew.
			s := newScreen(m.w, m.h)
			pushAll(s, m.peekLines(m.w, m.h))
			return s.String()
		}
		if m.sess.terminal != nil {
			// sessionFallback keeps the last live frame frozen on screen (see
			// beginStreamFallback): the renderer crops that buffer's HEAD, not
			// its tail, once the notice banner takes a frame row.
			return RenderStreamSessionFrame(m.sess.snapshot, m.sess.terminal, m.sess.phase == sessionFallback, m.sessionRailState(), m.w, m.h, m.g, m.p)
		}
		return RenderSessionFrame(m.sess.snapshot, m.sess.composer, m.sessionRailState(), m.w, m.h, m.g, m.p)
	}
	return m.render()
}
