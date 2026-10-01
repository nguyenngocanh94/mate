package harness

// The ScreenProfile of each harness: the startup screens measured in
// startup_prompt.go and the composer layout measured in composer.go, read
// through the pane source those measurements were taken with.

// claudeScreen is Claude Code's ScreenProfile.
type claudeScreen struct{}

// ReadSource implements ScreenProfile.
func (claudeScreen) ReadSource() ReadSource { return ReadRecentUnwrapped }

// ClassifyStartup implements ScreenProfile.
func (claudeScreen) ClassifyStartup(screen string) StartupScreen {
	return claudeStartup().classify(screen)
}

// StartupAnswer implements ScreenProfile.
func (claudeScreen) StartupAnswer(dialog StartupScreen) (StartupDialogAnswer, error) {
	return claudeStartup().answer(dialog)
}

// StartupTargetSelected implements ScreenProfile.
func (claudeScreen) StartupTargetSelected(dialog StartupScreen, screen string) bool {
	return claudeStartup().targetSelected(dialog, screen)
}

// ReadyScreen implements ScreenProfile: the marker alone between two rules,
// each long enough (claudeRuleMin) for the composer to be found as well.
func (claudeScreen) ReadyScreen() string {
	return "──────────\n" + ClaudeComposerMarker + "\n──────────\n"
}

// ComposerGlyph implements ScreenProfile.
func (claudeScreen) ComposerGlyph() string { return claudeComposerGlyph }

// Composer implements ScreenProfile.
func (claudeScreen) Composer(lines []string) (string, bool) { return locateClaudeComposer(lines) }

// ComposerPlaceholders implements ScreenProfile. Claude's empty composer
// holds nothing, or a faint suggestion the caller tells by its attributes.
func (claudeScreen) ComposerPlaceholders() []string { return []string{} }

// ComposerRows implements ScreenProfile.
func (claudeScreen) ComposerRows(lines []string) ([]string, bool) { return claudeComposerRows(lines) }

// Busy implements ScreenProfile.
func (claudeScreen) Busy(lines []string) (string, bool) { return claudeBusy(lines) }

// codexScreen is codex-cli's ScreenProfile.
type codexScreen struct{}

// ReadSource implements ScreenProfile.
func (codexScreen) ReadSource() ReadSource { return ReadRecentUnwrapped }

// ClassifyStartup implements ScreenProfile.
func (codexScreen) ClassifyStartup(screen string) StartupScreen {
	return codexStartup().classify(screen)
}

// StartupAnswer implements ScreenProfile.
func (codexScreen) StartupAnswer(dialog StartupScreen) (StartupDialogAnswer, error) {
	return codexStartup().answer(dialog)
}

// StartupTargetSelected implements ScreenProfile.
func (codexScreen) StartupTargetSelected(dialog StartupScreen, screen string) bool {
	return codexStartup().targetSelected(dialog, screen)
}

// ReadyScreen implements ScreenProfile: the composer holding its
// placeholder.
func (codexScreen) ReadyScreen() string {
	return "› " + CodexComposerPlaceholder + "\n"
}

// ComposerGlyph implements ScreenProfile.
func (codexScreen) ComposerGlyph() string { return codexComposerGlyph }

// Composer implements ScreenProfile.
func (codexScreen) Composer(lines []string) (string, bool) { return locateCodexComposer(lines) }

// ComposerPlaceholders implements ScreenProfile.
func (codexScreen) ComposerPlaceholders() []string { return []string{CodexComposerPlaceholder} }

// ComposerRows implements ScreenProfile.
func (codexScreen) ComposerRows(lines []string) ([]string, bool) { return codexComposerRows(lines) }

// Busy implements ScreenProfile.
func (codexScreen) Busy(lines []string) (string, bool) { return seedBusy(lines) }
