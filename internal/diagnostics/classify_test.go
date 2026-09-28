package diagnostics

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

func TestSegmentKindsUseTheWorkVocabulary(t *testing.T) {
	cases := []struct {
		name         string
		e            Execution
		kind, target string
	}{
		{"poll", Execution{Tool: "functions.exec", IsWrapper: true, Poll: true, ProcessID: "77", Command: `const r = await tools.write_stdin({session_id: 77})`}, "wait", "process 77"},
		{"read", Execution{Tool: "exec_command", Command: "sed -n '20,58p' README.md"}, "research", "sed -n '20,58p' README.md"},
		{"test", Execution{Tool: "exec_command", Command: "set -o pipefail; xcodebuild -scheme HelloVietnam test | tail -40"}, "test", "set -o pipefail; xcodebuild -scheme HelloVietnam test | tail -40"},
		{"git", Execution{Tool: "exec_command", Command: "git add -A && git commit -m x"}, "coordination", "git add -A && git commit -m x"},
		{"instructions", Execution{Tool: "Read", Target: "AGENTS.md"}, "instructions", "AGENTS.md"},
		{"claude edit", Execution{Tool: "Edit", Target: "src/main.go"}, "edit_code", "src/main.go"},
	}
	for _, c := range cases {
		kind, target, label := classifyCall([]Execution{c.e}, "")
		if kind != c.kind || target != c.target {
			t.Errorf("%s: kind=%s target=%q label=%q", c.name, kind, target, label)
		}
		if !strings.HasPrefix(label, workLabels[c.kind]) {
			t.Errorf("%s: label %q does not start with %q", c.name, label, workLabels[c.kind])
		}
	}
}

func TestCodexApplyPatchWrapperIsAnEditWithFileTargets(t *testing.T) {
	patch := `text(await tools.apply_patch("*** Begin Patch\n*** Update File: /w/HelloVietnam/TripViews.swift\n@@\n-a\n+b\n*** Add File: /w/HelloVietnam/New.swift\n+x\n*** End Patch\n"))`
	kind, target, label := classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: patch}}, "/w")
	if kind != "mixed" || !strings.Contains(target, "HelloVietnam/TripViews.swift") || !strings.Contains(target, "HelloVietnam/New.swift") {
		t.Fatalf("kind=%s target=%q label=%q", kind, target, label)
	}
	update := `text(await tools.apply_patch("*** Begin Patch\n*** Update File: /w/HelloVietnam/TripViews.swift\n@@\n-a\n+b\n*** End Patch\n"))`
	kind, target, label = classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: update}}, "/w")
	if kind != "edit_code" || target != "HelloVietnam/TripViews.swift" || !strings.HasPrefix(label, "Edit code / files · HelloVietnam/TripViews.swift") {
		t.Fatalf("kind=%s target=%q label=%q", kind, target, label)
	}
	if strings.Contains(label, "tools.") || strings.Contains(label, "Begin Patch") {
		t.Fatalf("wrapper JavaScript leaked into the label: %q", label)
	}
}

func TestHeredocWriteIsWriteCodeNotResearch(t *testing.T) {
	cmd := "cat >> HelloVietnam/TripService.swift <<'EOF'\nimport MapKit\nEOF"
	kind, target, _ := classifyCall([]Execution{{Tool: "exec_command", Command: cmd}}, "")
	if kind != "write_code" || target != cmd {
		t.Fatalf("kind=%s target=%q", kind, target)
	}
	wrapped := `const r=await tools.exec_command({cmd:"cat >> HelloVietnam/TripService.swift <<'EOF'\nimport MapKit\nEOF"}); text(r)`
	kind, _, label := classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: wrapped}}, "")
	if kind != "write_code" || strings.Contains(label, "tools.") {
		t.Fatalf("kind=%s label=%q", kind, label)
	}
}

func TestStatusEchoIsCoordinationAndScriptsStayUnknownButNamed(t *testing.T) {
	kind, _, label := classifyCall([]Execution{{Tool: "exec_command", Command: `echo "working: polishing layout" >> "$MATE_STATUS"`}}, "")
	if kind != "coordination" || !strings.Contains(label, "working: polishing layout") {
		t.Fatalf("status line kind=%s label=%q", kind, label)
	}
	kind, _, label = classifyCall([]Execution{{Tool: "exec_command", Command: "python3 - <<'PY'\nprint(1)\nPY"}}, "")
	if kind != "unknown" || !strings.HasPrefix(label, "Unclassified activity · python3 - <<'PY'") {
		t.Fatalf("script kind=%s label=%q", kind, label)
	}
	kind, _, _ = classifyCall([]Execution{{Tool: "exec_command", Command: "xcrun xcresulttool get test-results tests --path /tmp/x.xcresult"}}, "")
	if kind != "research" {
		t.Fatalf("xcresulttool kind=%s", kind)
	}
}

// Every rule below is about an operation the ios7 recording actually
// contains. Housekeeping is not a work type, an fd duplication is not a file
// write, and a script body is never interpreted.
func TestCommandRulesFromTheRecording(t *testing.T) {
	cases := []struct {
		name, command, kind string
	}{
		{"cp", "cp Config/Local.example.xcconfig Config/Local.xcconfig", "unknown"},
		{"rm", "rm -rf .tmp-screens", "unknown"},
		{"housekeeping does not dilute", "mkdir -p .tmp && rm -f .tmp/x && rg -n foo src", "research"},
		{"simctl list", "xcrun simctl list devices", "research"},
		{"simctl boot", "xcrun simctl boot 'iPhone 17 Pro'", "unknown"},
		{"plutil", "plutil -p Info.plist", "research"},
		{"defaults read", "defaults read com.apple.dt.Xcode IDEBuildLocationStyle", "research"},
		{"defaults write", "defaults write com.apple.dt.Xcode IDEBuildLocationStyle Custom", "unknown"},
		{"open", "open -a Simulator", "research"},
		{"nl", "nl -ba HelloVietnam/TripViews.swift", "research"},
		{"fd duplication is not a write", "cat foo.txt 2>&1", "research"},
		{"printf status", `printf "working: x\n" > "$MATE_STATUS"`, "coordination"},
		{"echo to file", "echo hi > notes.txt", "write_code"},
		{"echo append", "echo hi >> notes.txt", "write_code"},
		{"echo alone", "echo hi", "unknown"},
		{"perl script", `perl -e 'print 1'`, "unknown"},
		{"text filters keep the pipeline's kind", `rg --files Logs/Test | rg "Info.plist" | sort | tail -1`, "research"},
		{"find -delete and rmdir are housekeeping", "find .tmp -maxdepth 1 -type f -delete && rmdir .tmp", "unknown"},
		{"housekeeping then git status", "find .tmp -type f -delete && rmdir .tmp && git status --short --branch", "research"},
		{"merge-base is a query", "git rev-parse --short HEAD && git merge-base --is-ancestor main HEAD && echo fast-forward-ready", "research"},
		{"status after commit reports the commit", "git add a.swift && git commit --amend --no-edit && git status --short --branch", "coordination"},
		{"status between diffs reports the review", "git diff --check && git status --short --branch && git diff --stat", "review"},
		{"review then commit stays mixed", "git diff --check && git add a.swift && git commit -m x", "mixed"},
		{"git status after mate is its own research", "mate crew spawn shop crew --brief b.md && git status", "mixed"},
		{"filter over the log this command wrote", "xcodebuild build > /tmp/b.log 2>&1; rg -n 'error:' /tmp/b.log | tail -30", "test"},
		{"redirecting research output is still research", "go test ./... ; rg foo src > out.txt", "mixed"},
	}
	for _, c := range cases {
		kind, _, label := classifyCall([]Execution{{Tool: "exec_command", Command: c.command}}, "")
		if kind != c.kind || !strings.HasPrefix(label, workLabels[c.kind]+" · "+strings.Fields(c.command)[0]) {
			t.Errorf("%s: kind=%s label=%q", c.name, kind, label)
		}
	}
}

func TestOpaqueWrappersAreNamedByTheToolTheyInvokeWithoutLeakingJavaScript(t *testing.T) {
	viewImage := "const r=await tools.view_image({path:`/w/.tmp-screens/${name}`,detail:'high'});image(r.image_url);"
	kind, target, label := classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: viewImage}}, "/w")
	if kind != "research" || target != "view_image" || label != "Research / inspect · view_image" {
		t.Fatalf("view_image kind=%s target=%q label=%q", kind, target, label)
	}
	dynamic := `const cmds=['cat AGENTS.md','git status'];const r=await Promise.allSettled(cmds.map(cmd=>tools.exec_command({cmd,workdir:'/w'})));`
	kind, target, label = classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: dynamic}}, "/w")
	if kind != "unknown" || target != "exec_command" || strings.Contains(label, "tools.") || strings.Contains(label, "cmds") {
		t.Fatalf("dynamic kind=%s target=%q label=%q", kind, target, label)
	}
	kind, target, label = classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: "text(await tools.apply_patch(patch))"}}, "/w")
	if kind != "edit_code" || target != "apply_patch" || label != "Edit code / files · apply_patch" {
		t.Fatalf("variable patch kind=%s target=%q label=%q", kind, target, label)
	}
	kind, target, label = classifyCall(nil, "/w")
	if kind != "unknown" || target != "" || label != "Unclassified activity · no operations recorded" {
		t.Fatalf("no executions kind=%s target=%q label=%q", kind, target, label)
	}
}

func TestPatchTargetsDecodeEveryLiteralStyleAndTheShellForm(t *testing.T) {
	quoted := `text(await tools.apply_patch("*** Begin Patch\n*** Update File: /w/A.swift\n@@\n-    if x { return \"\\(a)\" }\n+    if x { return \"\\(b)\" }\n*** Add File: /w/B.swift\n+x\n*** Update File: /w/A.swift\n*** End Patch\n"))`
	if got := patchTargets(quoted, "/w"); fmt.Sprint(got) != "[A.swift B.swift]" {
		t.Fatalf("double-quoted literal %v", got)
	}
	single := `text(await tools.apply_patch('*** Begin Patch\n*** Delete File: /w/old.go\n*** End Patch\n'))`
	if got := patchTargets(single, "/w"); fmt.Sprint(got) != "[old.go]" {
		t.Fatalf("single-quoted literal %v", got)
	}
	template := "text(await tools.apply_patch(`*** Begin Patch\n*** Update File: /w/c/D.swift\n@@\n-a\n+b\n*** End Patch\n`))"
	if got := patchTargets(template, "/w"); fmt.Sprint(got) != "[c/D.swift]" {
		t.Fatalf("template literal %v", got)
	}
	if got := patchTargets("text(await tools.apply_patch(patch))", "/w"); len(got) != 0 {
		t.Fatalf("variable argument invented targets %v", got)
	}
	shell := "apply_patch <<'PATCH'\n*** Begin Patch\n*** Add File: new.go\n+package main\n*** End Patch\nPATCH"
	kind, target, _ := classifyCall([]Execution{{Tool: "exec_command", Command: shell}}, "")
	if kind != "write_code" || target != "new.go" {
		t.Fatalf("shell patch kind=%s target=%q", kind, target)
	}
	// Quoting a patch in an echo is not applying one: the target stays the command.
	kind, target, _ = classifyCall([]Execution{{Tool: "exec_command", Command: `echo '*** Begin Patch\n*** Update File: src.go'`}}, "")
	if kind != "unknown" || !strings.HasPrefix(target, "echo ") {
		t.Fatalf("quoted patch kind=%s target=%q", kind, target)
	}
	// Evidence labels have no worktree to shorten against, so they name files.
	if label := executionLabel(Execution{Tool: "functions.exec", IsWrapper: true, Command: quoted}); label != "apply_patch · A.swift, B.swift" {
		t.Fatalf("evidence label %q", label)
	}
}

// JavaScript accepts \' inside a double-quoted literal; Go's Unquote does
// not. The command must still be decoded rather than silently dropped.
func TestDoubleQuotedWrapperLiteralsWithEscapedSingleQuotesAreKept(t *testing.T) {
	wrapper := Execution{Tool: "functions.exec", IsWrapper: true, Command: `const r=await tools.exec_command({cmd:"echo \'hi\' > notes.txt"});text(r)`}
	kind, target, _ := classifyCall([]Execution{wrapper}, "")
	if kind != "write_code" || target != "echo 'hi' > notes.txt" {
		t.Fatalf("kind=%s target=%q", kind, target)
	}
}

// The dashboard records a task worktree relative to the workspace, so an
// absolute path is inside it when it passes through that directory.
func TestWorktreeRecordedRelativeStillNormalizesAbsolutePaths(t *testing.T) {
	abs := "/Users/x/ws/.worktrees/hv-ios7/HelloVietnam/A.swift"
	for _, worktree := range []string{".worktrees/hv-ios7", "/Users/x/ws/.worktrees/hv-ios7"} {
		if got := normalizeTarget(abs, worktree); got != "HelloVietnam/A.swift" {
			t.Errorf("worktree %q: %q", worktree, got)
		}
	}
	if got := normalizeTarget("/Users/x/ws/.mate/brief.md", ".worktrees/hv-ios7"); got != "/Users/x/ws/.mate/brief.md" {
		t.Errorf("outside path changed: %q", got)
	}
	if got := normalizeTarget("HelloVietnam/A.swift", ".worktrees/hv-ios7"); got != "HelloVietnam/A.swift" {
		t.Errorf("relative path changed: %q", got)
	}
	if got := normalizeTarget("/Users/x/ws/.worktrees/hv-ios7-other/A.swift", ".worktrees/hv-ios7"); got != "/Users/x/ws/.worktrees/hv-ios7-other/A.swift" {
		t.Errorf("sibling worktree matched: %q", got)
	}
	_, target, _ := classifyCall([]Execution{{Tool: "exec_command", Command: "sed -n '1,5p' " + abs}}, ".worktrees/hv-ios7")
	if target != "sed -n '1,5p' HelloVietnam/A.swift" {
		t.Errorf("command paths: %q", target)
	}
	patch := `text(await tools.apply_patch("*** Begin Patch\n*** Update File: ` + abs + `\n@@\n-a\n+b\n*** End Patch\n"))`
	if _, target, _ = classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: patch}}, ".worktrees/hv-ios7"); target != "HelloVietnam/A.swift" {
		t.Errorf("patch paths: %q", target)
	}
}

// Codex's native wait tool is a poll without the poll flag: it must join the
// process's polling chain instead of starting a segment of its own.
func TestNativeWaitToolPollsKeepTheirProcessTarget(t *testing.T) {
	wait := Execution{Tool: "wait", IsWrapper: true, ProcessID: "74527", Command: `{"cell_id":"18","yield_time_ms":1000}`}
	kind, target, label := classifyCall([]Execution{wait}, "")
	if kind != "wait" || target != "process 74527" || label != "Wait / poll · process 74527" {
		t.Fatalf("kind=%s target=%q label=%q", kind, target, label)
	}
}

// A single-quoted JavaScript literal escapes its own quotes. Left encoded,
// the \' would open no shell quote and a jq program would be read as commands.
func TestSingleQuotedWrapperLiteralsAreUnescaped(t *testing.T) {
	wrapper := Execution{Tool: "functions.exec", IsWrapper: true, Command: `const r=await tools.exec_command({cmd:'xcrun xcresulttool get test-results tests --path x.xcresult | jq -r \'.. | objects | select(.nodeType? == "Test Case")\'',workdir:'/w'});text(r.output);`}
	kind, target, _ := classifyCall([]Execution{wrapper}, "/w")
	if kind != "research" || target != `xcrun xcresulttool get test-results tests --path x.xcresult | jq -r '.. | objects | select(.nodeType? == "Test Case")'` {
		t.Fatalf("kind=%s target=%q", kind, target)
	}
	if got := executionCommand(wrapper); got != target {
		t.Fatalf("evidence command still escaped: %q", got)
	}
}

// A path that merely contains the worktree's name is not inside it. Only a
// whole absolute token that starts with the worktree (or, for a worktree
// recorded relative to the workspace, passes through that directory) is
// shortened, so operationKey never conflates unrelated commands.
func TestWorktreeAnchoringRewritesOnlyWholePathTokens(t *testing.T) {
	cases := []struct{ text, worktree, want string }{
		{"cat /data/w/config.json", "/w", "cat /data/w/config.json"},
		{"cat /w/config.json", "/w", "cat config.json"},
		{"cd /w && cat /w/a /data/w/b", "/w", "cd . && cat a /data/w/b"},
		{`--path=/w/x.xcresult workdir:'/w/sub' (/w/y) "/w/z"`, "/w", `--path=x.xcresult workdir:'sub' (y) "z"`},
		{"ls data/w/x /wx/y /w", "/w", "ls data/w/x /wx/y ."},
		{"cat /Users/x/ws/.worktrees/hv-ios7/a /Users/x/ws/.worktrees/hv-ios7-other/b", ".worktrees/hv-ios7", "cat a /Users/x/ws/.worktrees/hv-ios7-other/b"},
		{"cat /a/.worktrees/hv-ios7/x/.worktrees/hv-ios7/y.txt", ".worktrees/hv-ios7", "cat y.txt"},
		{"cat data/.worktrees/hv-ios7/y.txt", ".worktrees/hv-ios7", "cat data/.worktrees/hv-ios7/y.txt"},
	}
	for _, c := range cases {
		if got := relativeToWorktree(c.text, c.worktree); got != c.want {
			t.Errorf("relativeToWorktree(%q, %q) = %q, want %q", c.text, c.worktree, got, c.want)
		}
	}
	if got := normalizeTarget("/data/w/config.json", "/w"); got != "/data/w/config.json" {
		t.Errorf("absolute worktree matched inside a path: %q", got)
	}
	if got := normalizeTarget("/a/.worktrees/hv-ios7/x/.worktrees/hv-ios7/y.txt", ".worktrees/hv-ios7"); got != "y.txt" {
		t.Errorf("relative worktree did not take the last occurrence: %q", got)
	}
	p := projection{opts: Options{Worktree: "/w"}}
	inside := Execution{SessionID: "s", Tool: "exec_command", Command: "cat /w/config.json"}
	outside := Execution{SessionID: "s", Tool: "exec_command", Command: "cat /data/w/config.json"}
	if p.operationKey(inside) == p.operationKey(outside) {
		t.Fatal("operationKey conflated a path outside the worktree with one inside it")
	}
}

// The patch body is never scanned for commands: a line shaped like
// command: "…" inside the patched file must not become a second target.
func TestPatchBodyNeverAddsACommandTarget(t *testing.T) {
	patch := `text(await tools.apply_patch('*** Begin Patch\n*** Update File: /w/deploy.yaml\n@@\n-command: "echo hi"\n+command: "echo bye"\n*** End Patch\n'))`
	kind, target, _ := classifyCall([]Execution{{Tool: "functions.exec", IsWrapper: true, Command: patch}}, "/w")
	if kind != "edit_code" || target != "deploy.yaml" {
		t.Fatalf("kind=%s target=%q", kind, target)
	}
}

// A patch body is never scanned for commands: only an exec_command
// invocation's own argument can add a kind next to the patch's.
func TestPatchWrapperKindsComeFromThePatchAndItsOwnInvocations(t *testing.T) {
	body := `text(await tools.apply_patch('*** Begin Patch\n*** Update File: /w/ci.yaml\n@@\n-cmd: "go build ./..."\n+cmd: "go test ./..."\n*** End Patch\n'))`
	if got := executionWork(Execution{Tool: "functions.exec", IsWrapper: true, Command: body}); fmt.Sprint(got) != "[edit_code]" {
		t.Fatalf("patch body added kinds: %v", got)
	}
	both := `await tools.apply_patch("*** Begin Patch\n*** Update File: /w/a.go\n@@\n-x\n+y\n*** End Patch\n"); const r=await tools.exec_command({cmd:"go test ./...",workdir:"/w"}); text(r.output);`
	if got := executionWork(Execution{Tool: "functions.exec", IsWrapper: true, Command: both}); fmt.Sprint(got) != "[edit_code test]" {
		t.Fatalf("own invocation lost: %v", got)
	}
}

// JavaScript writes a code point above U+FFFF as a UTF-16 surrogate pair.
func TestUnescapeJSCombinesSurrogatePairs(t *testing.T) {
	if got := unescapeJS(`😀`); got != "😀" {
		t.Fatalf("pair decoded to %q", got)
	}
	if got := unescapeJS(`\uD83Dx`); got != "�x" {
		t.Fatalf("lone high surrogate decoded to %q", got)
	}
	if got := unescapeJS(`\uDE00\uD83D`); got != "��" {
		t.Fatalf("reversed pair decoded to %q", got)
	}
	for _, literal := range []string{`'a 😀 b'`, `"a 😀 b"`} {
		if got, ok := jsStringLiteral(literal, 0); !ok || got != "a 😀 b" {
			t.Fatalf("literal %s decoded to %q (%v)", literal, got, ok)
		}
	}
}

func TestExecutionKindIsTheSoleWorkKindOrMixed(t *testing.T) {
	if kind := executionKind(Execution{Tool: "functions.exec", IsWrapper: true, Command: `await tools.exec_command({cmd:"cat AGENTS.md"})`}); kind != "instructions" {
		t.Fatalf("wrapper read kind %s", kind)
	}
	if kind := executionKind(Execution{Tool: "functions.exec", IsWrapper: true, Command: `await tools.exec_command({cmd:"cat src.go"}); await tools.exec_command({cmd:"go test ./..."})`}); kind != "mixed" {
		t.Fatalf("two operations kind %s", kind)
	}
}

// Repeated-read detection follows the vocabulary rename: a wrapper whose
// only literal command is a read counts as research exactly like before.
func TestRepeatedReadsAreStillDetectedThroughWrappers(t *testing.T) {
	in := Input{}
	for i := 0; i < 3; i++ {
		c := fixtureCall(i, "p")
		in.Calls = append(in.Calls, c)
		ref := fmt.Sprintf("read%d", i)
		in.Actions = append(in.Actions, Action{ID: "s#tool#" + ref, CallID: c.ID, SessionID: "s", Tool: "functions.exec"})
		in.Facts = append(in.Facts, telemetry.Fact{ID: ref, Kind: "tool_call", SessionID: "s", SourceRef: ref, WrapperRef: ref, Tool: "functions.exec",
			Command: `const r=await tools.exec_command({cmd:"sed -n '1,10p' src.go"});text(r)`, OccurredAt: at(i * 10), SourceOffset: int64(i), Output: &telemetry.Output{SHA256: "same", Bytes: 10}})
	}
	p := Project(in, Options{Now: at(100)})
	if len(findingsOf(p, "repeated_read")) != 1 {
		t.Fatalf("wrapper reads not detected: %+v", p.Findings)
	}
	if len(p.Segments) != 1 || p.Segments[0].Kind != "research" || p.Segments[0].Label != "Research / inspect · sed -n '1,10p' src.go" {
		t.Fatalf("segments %+v", p.Segments)
	}
}
