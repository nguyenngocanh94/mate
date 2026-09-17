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
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ptysmoke <cmd> [args...]")
		os.Exit(2)
	}
	const cols, rows = 120, 36

	cmd := exec.Command(os.Args[1], os.Args[2:]...)
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

	// Snapshot while the program is still drawing: the console runs on the
	// alternate screen, and quitting restores the primary one, so a capture
	// taken after the exit would be blank.
	time.Sleep(1500 * time.Millisecond)
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

	if _, err := f.Write([]byte("q")); err == nil {
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
