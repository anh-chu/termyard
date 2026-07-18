package pty

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/sirupsen/logrus"
)

const (
	// maxClientBuffer caps the internal read buffer to prevent the server
	// from growing unbounded memory when a daemon produces output faster
	// than the WebSocket can drain it.
	maxClientBuffer = 4 * 1024 * 1024 // 4 MiB
)

// DaemonSession connects to a running session daemon via Unix socket
// and implements the Session interface (Read/Write/Resize/Close).
type DaemonSession struct {
	conn net.Conn

	// Internal buffering for Read() calls.
	mu   sync.Mutex
	cond *sync.Cond
	buf  bytes.Buffer
	done bool // set when connection is closed or daemon exits
}

// NewDaemonSession connects to a session daemon at the given socket path.
// It receives the initial Replay message and makes it available via Read().
func NewDaemonSession(socketPath string) (*DaemonSession, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial daemon socket %s: %w", socketPath, err)
	}

	d := &DaemonSession{conn: conn}
	d.cond = sync.NewCond(&d.mu)

	// Start goroutine reading frames from the daemon.
	go d.readFrames()

	return d, nil
}

// readFrames reads frames from the socket and appends Output/Replay data
// to the internal buffer, signaling any blocked Read() callers.
func (d *DaemonSession) readFrames() {
	header := make([]byte, 5)
	for {
		if _, err := io.ReadFull(d.conn, header); err != nil {
			d.signalDone()
			return
		}
		ftype := header[0]
		plen := binary.BigEndian.Uint32(header[1:5])

		if plen > 10*1024*1024 { // sanity
			d.signalDone()
			return
		}

		var payload []byte
		if plen > 0 {
			payload = make([]byte, plen)
			if _, err := io.ReadFull(d.conn, payload); err != nil {
				d.signalDone()
				return
			}
		}

		switch ftype {
		case FrameOutput, FrameReplay:
			d.appendData(payload)
		case FrameClose:
			// Daemon is shutting down.
			d.signalDone()
			return
		}
	}
}

// appendData adds data to the internal buffer and wakes blocked readers.
// If the buffer would exceed maxClientBuffer, the oldest data is discarded.
func (d *DaemonSession) appendData(data []byte) {
	d.mu.Lock()
	if d.buf.Len()+len(data) > maxClientBuffer {
		// Discard the oldest buffered data to make room.
		// If the incoming data alone exceeds the cap, keep only the tail.
		d.buf.Reset()
		if len(data) > maxClientBuffer {
			data = data[len(data)-maxClientBuffer:]
		}
	}
	d.buf.Write(data)
	d.mu.Unlock()
	d.cond.Broadcast()
}

// signalDone marks the session as done and wakes all readers.
func (d *DaemonSession) signalDone() {
	d.mu.Lock()
	d.done = true
	d.mu.Unlock()
	d.cond.Broadcast()
}

// Read implements Session — returns PTY output bytes.
// Blocks until data is available or the session is closed.
func (d *DaemonSession) Read(buf []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Wait until there is data or we're done.
	for d.buf.Len() == 0 && !d.done {
		d.cond.Wait()
	}

	if d.buf.Len() == 0 && d.done {
		return 0, io.EOF
	}

	n, err := d.buf.Read(buf)
	// If we drained the buffer but there's more data coming, return what we have.
	// Only signal EOF when both buffer is empty AND done.
	if err == io.EOF {
		err = nil // buffer EOF is not a real error
	}
	return n, err
}

// Write implements Session — sends an Input frame to the daemon.
func (d *DaemonSession) Write(data []byte) (int, error) {
	frame := encodeFrame(FrameInput, data)
	_, err := d.conn.Write(frame)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// Resize implements Session — sends a Resize frame to the daemon.
func (d *DaemonSession) Resize(cols, rows uint16) error {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint16(payload[0:2], cols)
	binary.BigEndian.PutUint16(payload[2:4], rows)
	frame := encodeFrame(FrameResize, payload)
	_, err := d.conn.Write(frame)
	return err
}

// Close implements Session — disconnects from the daemon WITHOUT
// killing it.  The daemon keeps running so other clients (or a
// later reconnect) can attach.  To actually terminate the daemon,
// use Registry.Kill() which sends FrameClose explicitly.
func (d *DaemonSession) Close() {
	// Do NOT send FrameClose — that would shut down the daemon.
	// Just close the local socket connection.
	d.conn.Close()
	d.signalDone()
	logrus.Debug("daemon session client disconnected (daemon still running)")
}
