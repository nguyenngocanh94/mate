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
	return m.render()
}
