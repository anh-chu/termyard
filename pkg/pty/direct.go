package pty

import (
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty/v2"
	"github.com/sirupsen/logrus"
)

// DirectPTYSession spawns a shell directly in a PTY without tmux.
// It implements the Session interface.
type DirectPTYSession struct {
	cmd    *exec.Cmd
	ptyFd  *os.File
	closed bool
}

// NewDirectPTYSession creates a new direct PTY session by spawning the given
// shell directly. If shell is empty, it uses $SHELL or falls back to /bin/bash.
// cols and rows set the initial terminal size. If cwd is non-empty, the shell
// starts in that directory.
func NewDirectPTYSession(shell string, cols, rows uint16, cwd string) (*DirectPTYSession, error) {
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
	}

	cmd := exec.Command(shell)
	cmd.Env = directLocaleEnv()
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	if cwd != "" {
		cmd.Dir = cwd
	}

	f, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		return nil, err
	}

	logrus.WithFields(logrus.Fields{
		"shell": shell,
		"cols":  cols,
		"rows":  rows,
		"cwd":   cwd,
	}).Info("started direct PTY session")

	return &DirectPTYSession{
		cmd:   cmd,
		ptyFd: f,
	}, nil
}

// Read reads from the PTY master fd.
func (p *DirectPTYSession) Read(buf []byte) (int, error) {
	return p.ptyFd.Read(buf)
}

// Write writes to the PTY master fd.
func (p *DirectPTYSession) Write(data []byte) (int, error) {
	return p.ptyFd.Write(data)
}

// Resize changes the PTY window size.
func (p *DirectPTYSession) Resize(cols, rows uint16) error {
	return pty.Setsize(p.ptyFd, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
}

// Close closes the PTY and waits for the subprocess to exit.
func (p *DirectPTYSession) Close() {
	if p.closed {
		return
	}
	p.closed = true

	p.ptyFd.Close()
	_ = p.cmd.Wait()

	logrus.Debug("direct PTY session closed")
}

// directLocaleEnv returns os.Environ() with LANG, LC_ALL, and LC_CTYPE
// stripped, and C.UTF-8 set. This mirrors tmux.localeEnv but lives here
// to avoid a reverse dependency.
func directLocaleEnv() []string {
	var out []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "LANG=") ||
			strings.HasPrefix(e, "LC_ALL=") ||
			strings.HasPrefix(e, "LC_CTYPE=") {
			continue
		}
		out = append(out, e)
	}
	out = append(out, "LC_ALL=C.UTF-8", "LANG=C.UTF-8")
	return out
}
