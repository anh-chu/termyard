package tmux

import (
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty/v2"
	"github.com/sirupsen/logrus"
)

// PTYSession wraps a PTY running `tmux attach-session`
type PTYSession struct {
	cmd    *exec.Cmd
	ptyFd  *os.File
	closed bool
}

// NewPTYSession spawns `tmux attach-session -t <session>` in a PTY.
// It resolves the session name to its numeric ID before attaching so that
// special characters in the name (e.g. "~") are not mis-interpreted by tmux
// as target selectors.
func NewPTYSession(tmuxPath, sessionName string, cols, rows uint16) (*PTYSession, error) {
	// Resolve name → ID to avoid tmux special-target interpretation.
	client := &Client{tmuxPath: tmuxPath}
	if id := client.SessionIDByName(sessionName); id != "" {
		sessionName = id
	}
	cmd := exec.Command(tmuxPath, "attach-session", "-t", sessionName)
	cmd.Env = localeEnv()
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")

	f, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		return nil, err
	}

	logrus.WithFields(logrus.Fields{
		"session": sessionName,
		"cols":    cols,
		"rows":    rows,
	}).Info("started PTY session")

	return &PTYSession{
		cmd:   cmd,
		ptyFd: f,
	}, nil
}

// Read reads from the PTY master fd
func (p *PTYSession) Read(buf []byte) (int, error) {
	return p.ptyFd.Read(buf)
}

// Write writes to the PTY master fd
func (p *PTYSession) Write(data []byte) (int, error) {
	return p.ptyFd.Write(data)
}

// Resize changes the PTY window size
func (p *PTYSession) Resize(cols, rows uint16) error {
	return pty.Setsize(p.ptyFd, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
}

// Close closes the PTY and waits for the subprocess to exit
func (p *PTYSession) Close() {
	if p.closed {
		return
	}
	p.closed = true

	p.ptyFd.Close()
	// Wait for subprocess — tmux detaches cleanly on PTY close
	_ = p.cmd.Wait()

	logrus.Debug("PTY session closed")
}

// localeEnv returns os.Environ() with LANG, LC_ALL, and LC_CTYPE stripped
// so that the caller can set a known-good UTF-8 locale without a pre-existing
// stale entry shadowing it (getenv returns the first match on Linux/glibc).
func localeEnv() []string {
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
