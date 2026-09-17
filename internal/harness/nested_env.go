package harness

// NestedSessionEnv lists the variables a running Claude Code session exports
// to its child processes. A harness launched in a pane that inherits them
// treats itself as a nested child session. Measured on Claude Code 2.1.274
// (2026-09-17): such a session fires its Stop hook with a transcript_path it
// never writes, while the same launch with these variables removed writes the
// transcript before the hook runs.
//
// The Herdr server is long-lived and hands its environment to every pane it
// creates, so whoever starts the server (often an agent, whose shell carries
// these variables) would otherwise poison every Mate and Crew launched after
// it. The runtime therefore strips them both from the spawned server's
// environment and from each pane right before an agent starts.
var NestedSessionEnv = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_SESSION_ATTENDED",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN",
	"CLAUDE_PID",
	"CLAUDE_EFFORT",
}

// IsNestedSessionEnv reports whether key is one of NestedSessionEnv.
func IsNestedSessionEnv(key string) bool {
	for _, k := range NestedSessionEnv {
		if k == key {
			return true
		}
	}
	return false
}
