// Package pty defines the common Session interface for PTY-backed terminal
// sessions and the daemon-based persistence layer.
package pty

import "fmt"

// Session is a generic terminal session that can be read from, written to,
// resized, and closed. Daemon-backed sessions implement this interface,
// allowing the WebSocket bridge to treat sessions uniformly.
type Session interface {
	Read(buf []byte) (int, error)
	Write(data []byte) (int, error)
	Resize(cols, rows uint16) error
	Close()
}

// ChunkKind classifies a byte returned by a framed read.
type ChunkKind int

const (
	// ChunkReplay bytes come from the initial ring-buffer snapshot sent by
	// the daemon when a client connects. They should be written in a single
	// terminal.write so the browser paints them without a scroll flash.
	ChunkReplay ChunkKind = iota

	// ChunkLive bytes are fresh PTY output produced after the replay.
	ChunkLive

	// ChunkReplayBoundary is a zero-byte sentinel emitted exactly once after
	// the replay snapshot has been fully drained. It tells the caller it may
	// resume normal live forwarding.
	ChunkReplayBoundary
)

// String returns a human-readable ChunkKind name for logging.
func (c ChunkKind) String() string {
	switch c {
	case ChunkReplay:
		return "replay"
	case ChunkLive:
		return "live"
	case ChunkReplayBoundary:
		return "replay-boundary"
	default:
		return fmt.Sprintf("ChunkKind(%d)", c)
	}
}

// FramedReader is an optional interface implemented by DaemonSession so the
// WebSocket bridge can separate replay bytes from live output. Other Session
// implementations do not implement it; callers should type-assert.
type FramedReader interface {
	ReadFramed(buf []byte) (n int, kind ChunkKind, err error)
}
