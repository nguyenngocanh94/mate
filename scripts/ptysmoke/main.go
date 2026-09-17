// Command ptysmoke runs a command under a pseudo-terminal of a fixed size
// and prints the frame it had drawn after a moment.
//
// It is a terminal, not a tape recorder: the pty output is fed through a
// real emulator and the emulator's own replies are written back to the
// program. Bubble Tea blocks on those replies (it queries the background
// colour and the cursor position before its first render), so a harness
// that only records bytes captures nothing at all.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// script is the optional -in flag: a semicolon-separated list of
// `<delay-ms>:<bytes>` steps typed into the pty before the frame is taken.
// The bytes go through strconv.Unquote, so an escape is written the way Go
// writes it: `\r` for Enter, `\x1b` for Esc. Without it ptysmoke keeps its
// original behaviour - wait a moment, capture, quit.
//
// A step whose byte half starts with `@` is a mouse gesture instead, written
// `@<verb>:<col>,<row>` in 0-based cell coordinates - the same coordinates
// Bubble Tea reports on a tea.MouseMsg, so a step can be written straight
// from a frame ptysmoke itself printed. The verbs are click, dblclick,
// press, release, move, drag, wheelup and wheeldown, and each expands to the
// SGR sequences (`ESC [ < Cb ; Cx ; Cy M`, trailing `m` for a release) a
// real terminal in SGR mouse mode sends. Writing those by hand is possible
// but unreadable, and a driver that has to spell out `\x1b[<0;41;7M` twice
// per click is a driver nobody will keep in step with the layout.
var (
	script = flag.String("in", "", "input script: `<ms>:<bytes>|@<verb>:<col>,<row>[;...]`, bytes Go-quoted (\\r, \\x1b)")
	settle = flag.Duration("settle", 1500*time.Millisecond, "how long to wait after the last step before capturing")
	noQuit = flag.Bool("no-quit", false, "do not send q after the capture; kill the child instead")
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ptysmoke [-in script] [-settle d] <cmd> [args...]")
		os.Exit(2)
	}
	steps, err := parseScript(*script)
	if err != nil {
		fmt.Fprintln(os.Stderr, "-in:", err)
		os.Exit(2)
	}
	const cols, rows = 120, 36

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(1)
	}
	defer f.Close()

	var mu sync.Mutex
	emu := vt.NewEmulator(cols, rows)

	// The emulator's reader carries its answers to the program's own
	// queries. Feed them back or nothing is ever drawn.
	go io.Copy(f, emu)
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := f.Read(chunk)
			if n > 0 {
				mu.Lock()
				emu.Write(chunk[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	// Type the script, then snapshot while the program is still drawing:
	// the console runs on the alternate screen, and quitting restores the
	// primary one, so a capture taken after the exit would be blank.
	for _, s := range steps {
		time.Sleep(s.delay)
		if _, err := f.Write([]byte(s.bytes)); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
	}
	time.Sleep(*settle)
	mu.Lock()
	emu.Flush()
	frame := make([]string, rows)
	for y := 0; y < rows; y++ {
		var sb strings.Builder
		for x := 0; x < cols; {
			c := emu.CellAt(x, y)
			if c == nil || c.Width <= 0 {
				x++
				continue
			}
			if s := c.String(); s == "" {
				sb.WriteString(strings.Repeat(" ", c.Width))
			} else {
				sb.WriteString(s)
			}
			x += c.Width
		}
		frame[y] = strings.TrimRight(sb.String(), " ")
	}
	mu.Unlock()

	if *noQuit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	} else if _, err := f.Write([]byte("q")); err == nil {
		waited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(waited) }()
		select {
		case <-waited:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
	fmt.Println(strings.Join(frame, "\n"))
}

type step struct {
	delay time.Duration
	bytes string
}

// parseScript turns `1500:\r;500:s` into timed writes. The byte half is
// Go-quoted rather than taken literally so a control character can be
// written at all: the keys that matter here - Enter, Esc, Ctrl+b - have no
// printable spelling.
func parseScript(s string) ([]step, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []step
	for _, raw := range strings.Split(s, ";") {
		ms, bytes, ok := strings.Cut(raw, ":")
		if !ok {
			return nil, fmt.Errorf("step %q is not <ms>:<bytes>", raw)
		}
		d, err := strconv.Atoi(strings.TrimSpace(ms))
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", raw, err)
		}
		payload := ""
		if strings.HasPrefix(bytes, "@") {
			payload, err = mouseSequence(bytes[1:])
		} else {
			payload, err = strconv.Unquote(`"` + bytes + `"`)
		}
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", raw, err)
		}
		out = append(out, step{delay: time.Duration(d) * time.Millisecond, bytes: payload})
	}
	return out, nil
}

// mouseSequence expands one `@<verb>:<col>,<row>` gesture into the SGR
// bytes a terminal in mouse mode would have sent for it. Coordinates are
// 0-based cells on the way in and 1-based in the wire format, which is the
// one off-by-one this whole encoding has.
func mouseSequence(spec string) (string, error) {
	verb, where, ok := strings.Cut(spec, ":")
	if !ok {
		return "", fmt.Errorf("mouse step %q is not <verb>:<col>,<row>", spec)
	}
	colText, rowText, ok := strings.Cut(where, ",")
	if !ok {
		return "", fmt.Errorf("mouse step %q names no <col>,<row>", spec)
	}
	col, err := strconv.Atoi(strings.TrimSpace(colText))
	if err != nil {
		return "", fmt.Errorf("mouse step %q: %w", spec, err)
	}
	row, err := strconv.Atoi(strings.TrimSpace(rowText))
	if err != nil {
		return "", fmt.Errorf("mouse step %q: %w", spec, err)
	}
	press := fmt.Sprintf("\x1b[<0;%d;%dM", col+1, row+1)
	release := fmt.Sprintf("\x1b[<0;%d;%dm", col+1, row+1)
	switch strings.TrimSpace(verb) {
	case "click":
		return press + release, nil
	case "dblclick":
		return press + release + press + release, nil
	case "press":
		return press, nil
	case "release":
		return release, nil
	case "move":
		// Button field 3 is "no button held", the bare-motion report.
		return fmt.Sprintf("\x1b[<35;%d;%dM", col+1, row+1), nil
	case "drag":
		// The motion bit (32) with the left button still held.
		return fmt.Sprintf("\x1b[<32;%d;%dM", col+1, row+1), nil
	case "wheelup":
		return fmt.Sprintf("\x1b[<64;%d;%dM", col+1, row+1), nil
	case "wheeldown":
		return fmt.Sprintf("\x1b[<65;%d;%dM", col+1, row+1), nil
	default:
		return "", fmt.Errorf("mouse step %q: unknown verb %q", spec, verb)
	}
}
