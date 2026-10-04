package diagnostics

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// executionWork classifies operations, never descriptions of requested work or
// output text. Literal shell/JavaScript inputs are inspected, never executed.
// Unknown scripts stay unknown; native FileChange evidence can still establish
// edits without guessing what a Python program or shell variable would do.
func executionWork(e Execution) []string {
	if kinds := operationWork(e, true); len(kinds) > 0 {
		return kinds
	}
	return []string{"unknown"}
}

// wrapperOwnWork is what a wrapper did besides running commands: its
// patches, polls, image views and other tools. It is all that is left to
// say about a wrapper whose commands its native children record, and it is
// empty when the wrapper only ran commands.
func wrapperOwnWork(e Execution) []string { return operationWork(e, false) }

// toolWork is the kind a tool's own name establishes, or "" when the name
// says nothing and the input has to be read.
func toolWork(tool string) string {
	for _, name := range []string{"spawn_agent", "send_message", "followup_task", "list_agents", "interrupt_agent"} {
		if strings.Contains(tool, name) {
			return "coordination"
		}
	}
	if strings.Contains(tool, "review") {
		return "review"
	}
	if tool == "write" || strings.HasSuffix(tool, ".write") || strings.Contains(tool, "write_file") || strings.Contains(tool, "create_file") {
		return "write_code"
	}
	if strings.Contains(tool, "edit") || strings.Contains(tool, "patch") {
		return "edit_code"
	}
	if skillTool(tool) {
		return "instructions"
	}
	for _, name := range []string{"read", "grep", "glob", "search", "web", "browser", "fetch", "screenshot", "list_directory"} {
		if strings.Contains(tool, name) {
			return "research"
		}
	}
	return ""
}

// wrapperCommandTools are the tools a wrapper script invokes that
// operationWork reads by their input instead of by their name.
var wrapperCommandTools = map[string]bool{"exec_command": true, "apply_patch": true, "write_stdin": true, "view_image": true}

func operationWork(e Execution, withCommands bool) []string {
	tool := strings.ToLower(e.Tool)
	if (e.Poll && !e.IsWrapper) || waitTool(tool) {
		return []string{"wait"}
	}
	if strings.Contains(tool, "apply_patch") {
		return patchWork(e.Command)
	}
	switch kind := toolWork(tool); kind {
	case "":
	case "research":
		if instructionPath(e.Target) || instructionPath(e.Command) {
			return []string{"instructions"}
		}
		return []string{kind}
	default:
		return []string{kind}
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
			// The patch body is never scanned for commands: a patched line
			// shaped like cmd: "go test" is content, not an operation. Only
			// another invocation's own argument can name a command.
			commands = invocationCommands(e.Command)
		}
		// Looking at an image is inspection, like a screenshot or a read tool.
		if wrapperInvokes(e.Command, "view_image") {
			kinds = appendKind(kinds, "research")
		}
		// Any other tool a wrapper invokes is classified by its name, as it
		// would be had the harness called it directly: tools.web__run is a
		// web tool whether or not a script stands around it.
		for _, name := range invokedTools(e.Command) {
			if wrapperCommandTools[name] {
				continue
			}
			if kind := toolWork(strings.ToLower(name)); kind != "" {
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
	if !withCommands {
		return kinds
	}
	for _, command := range commands {
		for _, kind := range shellWork(command, 0) {
			kinds = appendKind(kinds, kind)
		}
	}
	return settleStatus(kinds)
}

// statusLine is shellWork's own word for a line appended to $MATE_STATUS. It
// never leaves this file: settleStatus turns it into a work kind or drops it.
const statusLine = "status_line"

// settleStatus decides what a status line was. On its own it is the Crew
// talking to the Mate. Beside other work in the same execution it is the
// Crew saying what that work is, which the brief asks of every step, so it
// adds no type of its own and cannot make the call mixed.
func settleStatus(kinds []string) []string {
	out := []string{}
	for _, kind := range kinds {
		if kind != statusLine {
			out = appendKind(out, kind)
		}
	}
	if len(out) == 0 && len(kinds) > 0 {
		return []string{"coordination"}
	}
	return out
}

// waitTool recognizes a harness tool whose whole purpose is waiting on a
// process, such as Codex's native wait and write_stdin polls.
func waitTool(tool string) bool {
	tool = strings.ToLower(tool)
	return strings.Contains(tool, "write_stdin") || tool == "wait" || strings.HasSuffix(tool, ".wait") || strings.Contains(tool, "wait_agent") || strings.Contains(tool, "sleep")
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

var patchFileMarkers = []string{"*** Update File:", "*** Add File:", "*** Delete File:"}

// patchTargets lists the files a patch names, once each, in patch order and
// relative to the worktree. Inside a wrapper the patch is a JavaScript string
// literal whose escapes hide its line breaks, so the literal is decoded
// before the lines are read; a variable argument names nothing.
func patchTargets(command, worktree string) []string {
	text := command
	if wrapperInvokes(command, "apply_patch") {
		literal, ok := invocationLiteral(command, "apply_patch")
		if !ok {
			return nil
		}
		text = literal
	}
	out, seen := []string{}, map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, marker := range patchFileMarkers {
			if !strings.HasPrefix(line, marker) {
				continue
			}
			path := normalizeTarget(strings.TrimSpace(strings.TrimPrefix(line, marker)), worktree)
			if path != "" && !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
		}
	}
	return out
}

type invocation struct {
	name string
	args int
}

// wrapperScript is the JavaScript of a wrapper record, which a harness may
// have stored as a structured input object around the script.
func wrapperScript(raw string) string {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) == nil {
		var command string
		if json.Unmarshal(object["command"], &command) == nil {
			return command
		}
	}
	return raw
}

// invocations lists the explicit tools.<name>( calls of a script outside
// quoted strings and comments, each with the offset of its first argument.
// Template interpolation and dynamic calls stay invisible: this is not a
// JavaScript interpreter.
func invocations(script string) []invocation {
	out := []invocation{}
	quote := byte(0)
	for i := 0; i < len(script); i++ {
		c := script[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch {
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case strings.HasPrefix(script[i:], "//"):
			end := strings.IndexByte(script[i:], '\n')
			if end < 0 {
				return out
			}
			i += end
		case strings.HasPrefix(script[i:], "/*"):
			end := strings.Index(script[i+2:], "*/")
			if end < 0 {
				return out
			}
			i += end + 3
		case strings.HasPrefix(script[i:], "tools."):
			start := i + len("tools.")
			end := start
			for end < len(script) && isIdentifierByte(script[end]) {
				end++
			}
			rest := strings.TrimLeft(script[end:], " \t\r\n")
			if end > start && strings.HasPrefix(rest, "(") {
				out = append(out, invocation{name: script[start:end], args: len(script) - len(rest) + 1})
			}
			i = end - 1
		}
	}
	return out
}

func isIdentifierByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func wrapperInvokes(raw, name string) bool {
	for _, call := range invocations(wrapperScript(raw)) {
		if call.name == name {
			return true
		}
	}
	return false
}

// invokedTools names the tools a wrapper script calls, once each, in order.
func invokedTools(raw string) []string {
	out := []string{}
	for _, call := range invocations(wrapperScript(raw)) {
		out = appendUnique(out, call.name)
	}
	return out
}

// invocationCommands lists the literal commands passed to tool invocations
// other than apply_patch, each read from that invocation's own argument, so
// a patch body can never contribute a command.
func invocationCommands(raw string) []string {
	script := wrapperScript(raw)
	out := []string{}
	for _, call := range invocations(script) {
		if call.name == "apply_patch" {
			continue
		}
		out = append(out, literalCommands(script[call.args:argumentEnd(script, call.args)])...)
	}
	return out
}

// argumentEnd finds the parenthesis that closes the call whose arguments
// start at the offset, skipping quoted strings and nested brackets.
func argumentEnd(script string, at int) int {
	depth := 0
	quote := byte(0)
	for i := at; i < len(script); i++ {
		c := script[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return len(script)
}

// invocationLiteral decodes the string literal passed as the first argument
// of tools.<name>( when there is one. A variable or expression argument
// yields nothing: no value is guessed.
func invocationLiteral(raw, name string) (string, bool) {
	script := wrapperScript(raw)
	for _, call := range invocations(script) {
		if call.name != name {
			continue
		}
		if literal, ok := jsStringLiteral(script, call.args); ok {
			return literal, true
		}
	}
	return "", false
}

// jsStringLiteral decodes the string literal found at or after the offset,
// in any of the three JavaScript quote styles. A plain double-quoted literal
// follows Go's escape rules closely enough for strconv.Unquote; the other
// styles, and the rare escapes Go rejects, are decoded by hand.
func jsStringLiteral(script string, at int) (string, bool) {
	at += len(script[at:]) - len(strings.TrimLeft(script[at:], " \t\r\n"))
	if at >= len(script) {
		return "", false
	}
	quote := script[at]
	if quote != '"' && quote != '\'' && quote != '`' {
		return "", false
	}
	for i := at + 1; i < len(script); i++ {
		switch script[i] {
		case '\\':
			i++
		case quote:
			body := script[at+1 : i]
			if quote == '"' {
				if decoded, err := strconv.Unquote(`"` + body + `"`); err == nil {
					return decoded, true
				}
			}
			return unescapeJS(body), true
		}
	}
	return "", false
}

// unescapeJS decodes JavaScript string escapes. An unknown escape keeps its
// character; a malformed code point is dropped rather than invented.
func unescapeJS(body string) string {
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' || i+1 >= len(body) {
			out.WriteByte(c)
			continue
		}
		i++
		switch body[i] {
		case 'n':
			out.WriteByte('\n')
		case 't':
			out.WriteByte('\t')
		case 'r':
			out.WriteByte('\r')
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'v':
			out.WriteByte('\v')
		case '0':
			out.WriteByte(0)
		case '\n':
			// A line continuation joins the two lines.
		case 'x', 'u':
			r, n, ok := jsCodePoint(body[i:])
			i += n - 1
			if !ok {
				continue
			}
			// A code point above U+FFFF arrives as a UTF-16 surrogate pair;
			// an unpaired surrogate stays the replacement character.
			if utf16.IsSurrogate(r) && strings.HasPrefix(body[i+1:], `\u`) {
				if low, m, ok := jsCodePoint(body[i+2:]); ok && utf16.IsSurrogate(low) {
					if pair := utf16.DecodeRune(r, low); pair != utf8.RuneError {
						r = pair
						i += 1 + m
					}
				}
			}
			out.WriteRune(r)
		default:
			out.WriteByte(body[i])
		}
	}
	return out.String()
}

// jsCodePoint decodes the \xHH, \uXXXX or \u{X…} escape whose letter starts
// s, returning the code point and the bytes consumed. A malformed escape
// consumes its letter and digits but yields nothing.
func jsCodePoint(s string) (rune, int, bool) {
	digits, n := "", 1
	switch {
	case s[0] == 'x' && len(s) >= 3:
		digits, n = s[1:3], 3
	case s[0] == 'u' && len(s) >= 2 && s[1] == '{':
		if end := strings.IndexByte(s, '}'); end > 2 {
			digits, n = s[2:end], end+1
		}
	case s[0] == 'u' && len(s) >= 5:
		digits, n = s[1:5], 5
	}
	value, err := strconv.ParseUint(digits, 16, 32)
	if err != nil || digits == "" {
		return 0, n, false
	}
	return rune(value), n, true
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

// shellKeyword are the words that open a branch or a loop body: the command
// after one is classified as it would be on a line of its own.
var shellKeyword = map[string]bool{"if": true, "then": true, "else": true, "elif": true, "do": true, "while": true, "until": true, "!": true, "{": true}

func shellWork(command string, depth int) []string {
	if depth > 2 {
		return []string{"unknown"}
	}
	kinds := []string{}
	written := map[string]bool{}
	previous := ""
	for _, part := range shellParts(command) {
		words := part.words
		// A comment runs nothing.
		if len(words) > 0 && strings.HasPrefix(words[0], "#") {
			continue
		}
		for len(words) > 0 && (strings.Contains(words[0], "=") || words[0] == "env" || words[0] == "command" || words[0] == "exec" || words[0] == "sudo" || shellKeyword[words[0]]) {
			// `command -v name` asks where a program is; it runs nothing.
			if words[0] == "command" && len(words) > 1 && (words[1] == "-v" || words[1] == "-V") {
				words = []string{"which"}
				break
			}
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		bin := strings.ToLower(filepath.Base(words[0]))
		// The shell's own `test` is `[`; a program at a path keeps its name.
		if words[0] == "test" {
			bin = "["
		}
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
		// The work before this command; a status line in between is not it.
		last := ""
		for i := len(kinds) - 1; i >= 0 && last == ""; i-- {
			if kinds[i] != statusLine {
				last = kinds[i]
			}
		}
		switch {
		case kind == "":
		// A log filter after a test/review does not establish a separate repo
		// research operation. Its output remains evidence of that execution.
		// The same holds when the filter reads a file this very command wrote.
		case kind == "research" && part.piped && last != "" && last != "research" && last != "instructions":
		case kind == "research" && last != "" && containsAny(args, written):
		// A git query chained after a git write or review reports that
		// operation's result; it is not repository research of its own. An
		// independent second git query chained after a write is deliberately
		// folded into that operation as well.
		case kind == "research" && bin == "git" && previous == "git" && (last == "coordination" || last == "review"):
		default:
			kinds = appendKind(kinds, kind)
		}
		if file := redirectFile(args); file != "" {
			written[file] = true
		}
		// A status line between a git write and the query that reports it
		// does not separate them.
		if kind != statusLine {
			previous = bin
		}
	}
	return kinds
}

func containsAny(args []string, set map[string]bool) bool {
	for _, arg := range args {
		if set[arg] {
			return true
		}
	}
	return false
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
			case "status", "log", "ls-files", "grep", "rev-parse", "merge-base", "show-ref", "describe", "ls-tree", "cat-file", "for-each-ref", "name-rev":
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
	case "xcrun":
		// Result bundles and simulator queries are inspection. Building,
		// booting or installing through xcrun is not a known work type.
		if len(args) > 0 && args[0] == "xcresulttool" {
			return "research"
		}
		if len(args) > 1 && args[0] == "simctl" {
			for _, verb := range []string{"list", "get", "diagnose"} {
				if strings.HasPrefix(args[1], verb) {
					return "research"
				}
			}
		}
	case "open", "which":
		// Asking where a program is inspects, like ls.
		return "research"
	case "plutil":
		for _, arg := range args {
			if arg == "-p" {
				return "research"
			}
		}
	case "defaults":
		if len(args) > 0 && args[0] == "read" {
			return "research"
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
	case "cat", "head", "tail", "nl", "rg", "grep", "find", "ls", "jq", "wc", "curl", "wget", "file", "stat":
		if bin == "cat" && redirectFile(args) != "" {
			return "write_code"
		}
		// find -delete removes files instead of listing them.
		if bin == "find" && containsAny(args, map[string]bool{"-delete": true}) {
			return ""
		}
		// Locating or measuring an instruction file is not reading it.
		if bin == "find" || bin == "ls" || bin == "wc" || bin == "file" || bin == "stat" {
			return "research"
		}
		if instructionPath(strings.Join(args, " ")) {
			return "instructions"
		}
		return "research"
	case "sort", "uniq", "cut", "tr", "column":
		// Text filters shape another command's output; they are no work type
		// of their own, so a pipeline keeps the kind of what produced it.
		return ""
	case "printf", "echo", "tee":
		file := redirectFile(args)
		if bin == "tee" && file == "" {
			file = strings.Join(args, " ")
		}
		// A status line appended to $MATE_STATUS is the Crew talking to the
		// Mate, not a file being written.
		if strings.Contains(file, "MATE_STATUS") {
			return statusLine
		}
		if file != "" {
			return "write_code"
		}
		return ""
	case "cd", "pwd", "mkdir", "rmdir", "cp", "mv", "touch", "rm", "true", "false", "set", "export":
		// Housekeeping is not a work type: it neither inspects nor writes code.
		return ""
	case "[", "[[", "for", "case", "fi", "done", "esac", "}":
		// A guard and the words that shape a branch or a loop are control
		// flow; what they guard is classified on its own.
		return ""
	}
	return "unknown"
}

// redirectFile is the file an output redirect writes to, or "" when there is
// none. shellParts emits each '>' as its own word, so an append (>>) arrives
// as two consecutive '>' words and is accepted like a plain '>'. A duplicated
// descriptor (2>&1) and /dev/null are not files.
func redirectFile(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] != ">" {
			continue
		}
		file := ""
		if i+1 < len(args) {
			file = args[i+1]
		}
		if file == ">" && i+2 < len(args) {
			file = args[i+2]
			i++
		}
		if file == "" || file == ">" || strings.HasPrefix(file, "&") || file == "/dev/null" {
			continue
		}
		return file
	}
	return ""
}
