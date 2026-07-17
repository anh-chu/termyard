// Package pty defines the common Session interface shared by tmux-attached
// and direct PTY backends.
package pty

// Session is a generic terminal session that can be read from, written to,
// resized, and closed. Both tmux-attached and direct-spawn sessions implement
// this interface, allowing the WebSocket bridge to treat them uniformly.
type Session interface {
	Read(buf []byte) (int, error)
	Write(data []byte) (int, error)
	Resize(cols, rows uint16) error
	Close()
}
