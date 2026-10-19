package main

import (
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"

	"github.com/creack/pty"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// terminal is the web terminal: one shell (or one command) in a PTY. Opening a
// new one closes the old one. Terminal data is never logged.
type terminal struct {
	send func(*bandav1.ConnectRequest) error

	mu  sync.Mutex
	cmd *exec.Cmd
	pty *os.File
}

func (t *terminal) open(o *bandav1.TerminalOpen) {
	t.close()
	args := []string{"-l"}
	if o.Command != "" {
		args = []string{"-lc", o.Command}
	}
	cmd := exec.Command("bash", args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if _, err := os.Stat(work); err == nil {
		cmd.Dir = work
	} else {
		cmd.Dir = os.Getenv("HOME")
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(o.Cols), Rows: uint16(o.Rows)})
	if err != nil {
		t.send(output(&bandav1.TerminalOutput{Data: []byte("could not start the terminal: " + err.Error() + "\r\n"), Exited: true}))
		return
	}
	t.mu.Lock()
	t.cmd, t.pty = cmd, f
	t.mu.Unlock()

	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				t.send(output(&bandav1.TerminalOutput{Data: slices.Clone(buf[:n])}))
			}
			if err != nil { // EIO once the process has exited
				break
			}
		}
		cmd.Wait()
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.cmd == cmd { // not closed or replaced meanwhile
			t.cmd, t.pty = nil, nil
			f.Close()
			t.send(output(&bandav1.TerminalOutput{Exited: true}))
		}
	}()
}

func output(o *bandav1.TerminalOutput) *bandav1.ConnectRequest {
	return &bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_TerminalOutput{TerminalOutput: o}}
}

func (t *terminal) input(data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pty != nil {
		t.pty.Write(data)
	}
}

func (t *terminal) resize(cols, rows uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pty != nil {
		pty.Setsize(t.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	}
}

// close hangs up the process group, without announcing an exit.
func (t *terminal) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd != nil {
		syscall.Kill(-t.cmd.Process.Pid, syscall.SIGHUP)
		t.pty.Close()
		t.cmd, t.pty = nil, nil
	}
}
