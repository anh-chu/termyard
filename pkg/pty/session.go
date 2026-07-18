// Package pty defines the common Session interface for PTY-backed terminal
// sessions and the daemon-based persistence layer.
package pty

// Session is a generic terminal session that can be read from, written to,
// resized, and closed. Daemon-backed sessions implement this interface,
// allowing the WebSocket bridge to treat sessions uniformly.
type Session interface {
	Read(buf []byte) (int, error)
	Write(data []byte) (int, error)
	Resize(cols, rows uint16) error
	Close()
}
