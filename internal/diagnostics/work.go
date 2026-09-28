package diagnostics

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// executionWork classifies operations, never descriptions of requested work or
// output text. Literal shell/JavaScript inputs are inspected, never executed.
// Unknown scripts stay unknown; native FileChange evidence can still establish
// edits without guessing what a Python program or shell variable would do.
func executionWork(e Execution) []string {
	tool := strings.ToLower(e.Tool)
	if (e.Poll && !e.IsWrapper) || strings.Contains(tool, "write_stdin") || tool == "wait" || strings.HasSuffix(tool, ".wait") || strings.Contains(tool, "wait_agent") || strings.Contains(tool, "sleep") {
		return []string{"wait"}
	}
	for _, name := range []string{"spawn_agent", "send_message", "followup_task", "list_agents", "interrupt_agent"} {
		if strings.Contains(tool, name) {
			return []string{"coordination"}
		}
	}
	if strings.Contains(tool, "review") {
		return []string{"review"}
	}
	if strings.Contains(tool, "apply_patch") {
		return patchWork(e.Command)
	}
	if tool == "write" || strings.HasSuffix(tool, ".write") || strings.Contains(tool, "write_file") || strings.Contains(tool, "create_file") {
		return []string{"write_code"}
	}
	if strings.Contains(tool, "edit") || strings.Contains(tool, "patch") {
		return []string{"edit_code"}
	}
	if tool == "skill" || strings.HasSuffix(tool, ".skill") {
		return []string{"instructions"}
	}
	for _, name := range []string{"read", "grep", "glob", "search", "web", "browser", "fetch", "screenshot", "list_directory"} {
		if strings.Contains(tool, name) {
			if instructionPath(e.Target) || instructionPath(e.Command) {
				return []string{"instructions"}
			}
			return []string{"research"}
		}
	}
	kinds := []string{}
	commands := []string{e.Command}
	if e.IsWrapper {
		commands = literalCommands(e.Command)
		if e.Poll || wrapperInvokes(e.Command, "write_stdin") {
			kinds = appendKind(kinds, "wait")
		}
		if wrapperInvokes(e.Command, "apply_patch") {
			for _, kind := range patchWork(e.Command) {
				kinds = appendKind(kinds, kind)
			}
		}
	} else {
		// Direct shell text may contain examples such as rg 'cmd:"go test"'.
		// Only a complete structured input object establishes a command field.
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(e.Command), &object) == nil {
			commands = literalCommands(e.Command)
		}
	}
	for _, command := range commands {
		for _, kind := range shellWork(command, 0) {
			kinds = appendKind(kinds, kind)
		}
	}
	if len(kinds) == 0 {
		return []string{"unknown"}
	}
	return kinds
}

func patchWork(command string) []string {
	kinds := []string{}
	if strings.Contains(command, "*** Add File:") {
		kinds = append(kinds, "write_code")
	}
	if strings.Contains(command, "*** Update File:") || strings.Contains(command, "*** Delete File:") {
		kinds = append(kinds, "edit_code")
	}
	if len(kinds) == 0 {
		return []string{"edit_code"}
	}
	return kinds
}

// Recognize an explicit tools invocation outside quoted strings and comments.
// This deliberately leaves template interpolation and dynamic calls unknown.
func wrapperInvokes(raw, name string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) == nil {
		var command string
		if json.Unmarshal(object["command"], &command) == nil {
			raw = command
		}
	}
	quote := byte(0)
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			quote = c
			continue
		}
		if strings.HasPrefix(raw[i:], "//") {
			if end := strings.IndexByte(raw[i:], '\n'); end >= 0 {
				i += end
				continue
			}
			return false
		}
		if strings.HasPrefix(raw[i:], "/*") {
			if end := strings.Index(raw[i+2:], "*/"); end >= 0 {
				i += end + 3
				continue
			}
			return false
		}
		prefix := "tools." + name
		if strings.HasPrefix(raw[i:], prefix) && strings.HasPrefix(strings.TrimSpace(raw[i+len(prefix):]), "(") {
			return true
		}
	}
	return false
}

var instructionName = regexp.MustCompile(`(?i)(^|[/\s"'])((agents|claude|skill)\.md)([\s"']|$)`)

func instructionPath(s string) bool { return instructionName.MatchString(s) }

type shellPart struct {
	words []string
	piped bool
}

// shellParts needs only literal command words and boundaries. Quotes are
// retained as one argument, so `echo "go test"` never becomes a test command.
// Heredoc contents are removed: a brief quoting commands is not their execution.
func shellParts(raw string) []shellPart {
	raw = withoutHeredocBodies(raw)
	parts := []shellPart{}
	words := []string{}
	var token strings.Builder
	quote := rune(0)
	escaped, started, piped := false, false, false
	flushWord := func() {
		if started {
			words = append(words, token.String())
			token.Reset()
			started = false
		}
	}
	flushPart := func() {
		flushWord()
		if len(words) > 0 {
			parts = append(parts, shellPart{words: words, piped: piped})
			words = nil
		}
		piped = false
	}
	runes := []rune(raw)
	for i, r := range runes {
		if escaped {
			token.WriteRune(r)
			started = true
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				token.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if r == '&' && i > 0 && (runes[i-1] == '>' || runes[i-1] == '<') {
			token.WriteRune(r)
			started = true
			continue
		}
		if r == '\n' || r == ';' || r == '|' || r == '&' {
			flushPart()
			piped = r == '|' && (i+1 >= len(runes) || runes[i+1] != '|') && (i == 0 || runes[i-1] != '|')
			continue
		}
		if r == '>' || r == '<' {
			flushWord()
			words = append(words, string(r))
			continue
		}
		if unicode.IsSpace(r) {
			flushWord()
			continue
		}
		token.WriteRune(r)
		started = true
	}
	flushPart()
	return parts
}

var heredocStart = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

func withoutHeredocBodies(raw string) string {
	lines := strings.Split(raw, "\n")
	delimiter := ""
	for i, line := range lines {
		if delimiter != "" {
			lines[i] = ""
			if strings.TrimSpace(line) == delimiter {
				delimiter = ""
			}
			continue
		}
		if match := heredocStart.FindStringSubmatch(line); len(match) > 1 {
			delimiter = match[1]
		}
	}
	return strings.Join(lines, "\n")
}

func shellWork(command string, depth int) []string {
	if depth > 2 {
		return []string{"unknown"}
	}
	kinds := []string{}
	for _, part := range shellParts(command) {
		words := part.words
		for len(words) > 0 && (strings.Contains(words[0], "=") || words[0] == "env" || words[0] == "command" || words[0] == "exec" || words[0] == "sudo") {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		bin := strings.ToLower(filepath.Base(words[0]))
		args := words[1:]
		if bin == "apply_patch" {
			for _, kind := range patchWork(command) {
				kinds = appendKind(kinds, kind)
			}
			continue
		}
		if (bin == "bash" || bin == "zsh" || bin == "sh") && len(args) >= 2 && (args[0] == "-c" || args[0] == "-lc") {
			for _, kind := range shellWork(args[1], depth+1) {
				kinds = appendKind(kinds, kind)
			}
			continue
		}
		kind := commandWork(bin, args)
		if kind == "" {
			continue
		}
		// A log filter after a test/review does not establish a separate repo
		// research operation. Its output remains evidence of that execution.
		if part.piped && kind == "research" && len(kinds) > 0 && kinds[len(kinds)-1] != "research" && kinds[len(kinds)-1] != "instructions" {
			continue
		}
		kinds = appendKind(kinds, kind)
	}
	return kinds
}

func commandWork(bin string, args []string) string {
	switch bin {
	case "mate":
		for _, arg := range args {
			if arg == "review" {
				return "review"
			}
			if !strings.HasPrefix(arg, "-") {
				break
			}
		}
		return "coordination"
	case "git":
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "-C" || arg == "-c" || arg == "--git-dir" || arg == "--work-tree" {
				i++
				continue
			}
			if strings.HasPrefix(arg, "-") {
				continue
			}
			switch arg {
			case "diff", "show":
				return "review"
			case "status", "log", "ls-files", "grep", "rev-parse":
				return "research"
			default:
				return "coordination"
			}
		}
	case "go", "cargo":
		if len(args) > 0 && (args[0] == "test" || args[0] == "build" || args[0] == "check" || args[0] == "vet") {
			return "test"
		}
	case "xcodebuild", "pytest", "pytest-3", "jest", "vitest", "gradle", "gradlew", "mvn", "swift":
		if bin != "swift" || (len(args) > 0 && (args[0] == "test" || args[0] == "build")) {
			return "test"
		}
	case "npm", "pnpm", "yarn", "bun", "make", "playwright":
		for _, arg := range args {
			switch arg {
			case "test", "tests", "test:unit", "test:e2e", "check", "build", "lint", "verify":
				return "test"
			}
		}
	case "sleep", "wait":
		return "wait"
	case "apply_patch":
		return "edit_code"
	case "sed", "perl":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-i") {
				return "edit_code"
			}
		}
		if bin == "perl" {
			return "unknown"
		}
		fallthrough
	case "cat", "head", "tail", "rg", "grep", "find", "ls", "jq", "wc", "curl", "wget", "file", "stat":
		if bin == "cat" && hasRedirect(args) {
			return "write_code"
		}
		if instructionPath(strings.Join(args, " ")) {
			return "instructions"
		}
		return "research"
	case "printf", "echo", "tee":
		if hasRedirect(args) || bin == "tee" {
			return "write_code"
		}
		return ""
	case "cd", "pwd", "mkdir", "true", "false", "set", "export":
		return ""
	}
	return "unknown"
}

func hasRedirect(args []string) bool {
	for i, arg := range args {
		if arg == ">" && i+1 < len(args) && args[i+1] != "&" && args[i+1] != "/dev/null" {
			return true
		}
	}
	return false
}
