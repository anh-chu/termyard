package pty

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/creack/pty/v2"
	"github.com/sirupsen/logrus"
)

// Frame types for the daemon wire protocol.
const (
	FrameOutput = 0x01 // daemon → client: raw PTY output
	FrameInput  = 0x02 // client → daemon: raw bytes to write to PTY
	FrameResize = 0x03 // client → daemon: 4 bytes (cols u16 BE + rows u16 BE)
	FrameClose  = 0x04 // client → daemon: kill shell
	FrameReplay = 0x05 // daemon → client: ring buffer contents on connect
)

// DaemonConfig configures a session daemon.
type DaemonConfig struct {
	ID         string // unique session identifier
	Shell      string // shell to spawn (default: $SHELL or /bin/bash)
	Cols, Rows uint16 // initial terminal size
	Cwd        string // working directory
	SocketDir  string // directory for Unix sockets (default: /tmp/termyard-sessions-{uid}/)
	BufferSize int    // ring buffer size in bytes (default: 1MB)
}

// RunDaemon is the entry point for a session daemon process.
// It creates a PTY, spawns the shell, listens on a Unix socket,
// and serves clients until the shell exits or Close is received.
func RunDaemon(cfg DaemonConfig) error {
	if cfg.Shell == "" {
		cfg.Shell = os.Getenv("SHELL")
		if cfg.Shell == "" {
			cfg.Shell = "/bin/bash"
		}
	}
	if cfg.SocketDir == "" {
		cfg.SocketDir = fmt.Sprintf("/tmp/termyard-sessions-%d", os.Getuid())
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 1 << 20 // 1 MB
	}

	log := logrus.WithFields(logrus.Fields{
		"id":    cfg.ID,
		"shell": cfg.Shell,
		"cols":  cfg.Cols,
		"rows":  cfg.Rows,
	})

	// 1. Create PTY + spawn shell.
	cmd := exec.Command(cfg.Shell)
	cmd.Env = directLocaleEnv()
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}

	ptyFd, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: cfg.Cols,
		Rows: cfg.Rows,
	})
	if err != nil {
		return fmt.Errorf("start PTY: %w", err)
	}
	log.Info("started session daemon PTY")

	// 2. Ring buffer.
	ring := newRingBuffer(cfg.BufferSize)

	// 3. Listen on Unix socket.
	socketPath := filepath.Join(cfg.SocketDir, cfg.ID+".sock")
	if err := os.MkdirAll(cfg.SocketDir, 0700); err != nil {
		ptyFd.Close()
		cmd.Process.Kill()
		return fmt.Errorf("create socket dir: %w", err)
	}
	os.Remove(socketPath) // remove stale socket

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		ptyFd.Close()
		cmd.Process.Kill()
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	log.WithField("socket", socketPath).Info("daemon listening")

	// 4. Build and run the daemon.
	d := &daemon{
		config:     cfg,
		ptyFd:      ptyFd,
		cmd:        cmd,
		ring:       ring,
		ln:         ln,
		socketPath: socketPath,
		log:        log,
		clients:    make(map[net.Conn]chan []byte),
		shellDone:  make(chan struct{}),
	}
	return d.run()
}

// ringBuffer is a simple circular byte buffer.
type ringBuffer struct {
	buf  []byte
	head int // next write position
	tail int // oldest read position
	size int // number of valid bytes
	mu   sync.Mutex
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, capacity)}
}

// Write appends data to the ring buffer, overwriting oldest data if full.
func (r *ringBuffer) Write(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range data {
		r.buf[r.head] = b
		r.head = (r.head + 1) % len(r.buf)
		if r.size < len(r.buf) {
			r.size++
		} else {
			r.tail = (r.tail + 1) % len(r.buf)
		}
	}
}

// Snapshot returns a copy of all buffered bytes in order (oldest first).
func (r *ringBuffer) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == 0 {
		return nil
	}
	out := make([]byte, r.size)
	for i := 0; i < r.size; i++ {
		out[i] = r.buf[(r.tail+i)%len(r.buf)]
	}
	return out
}

// daemon manages the PTY, ring buffer, and client fan-out.
type daemon struct {
	config     DaemonConfig
	ptyFd      *os.File
	cmd        *exec.Cmd
	ring       *ringBuffer
	ln         net.Listener
	socketPath string
	log        *logrus.Entry

	clientsMu sync.RWMutex
	clients   map[net.Conn]chan []byte // per-client write channel

	shellDone chan struct{}
	closeOnce sync.Once
}

func (d *daemon) run() error {
	// Goroutine: read PTY → ring buffer → broadcast.
	go d.pumpPTY()

	// Goroutine: wait for shell exit.
	go d.waitShell()

	// Accept loop.
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			select {
			case <-d.shellDone:
				return nil
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		go d.handleClient(conn)
	}
}

// pumpPTY reads from the PTY and fans out output to all clients.
func (d *daemon) pumpPTY() {
	buf := make([]byte, 64*1024)
	for {
		n, err := d.ptyFd.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			d.ring.Write(data)
			d.broadcast(data)
		}
		if err != nil {
			if err != io.EOF {
				d.log.WithError(err).Debug("PTY read error")
			}
			d.shutdown()
			return
		}
	}
}

// waitShell waits for the shell process to exit, then shuts down.
func (d *daemon) waitShell() {
	_ = d.cmd.Wait()
	d.log.Debug("shell process exited")
	d.shutdown()
}

// broadcast sends an Output frame to all connected clients.
// Uses per-client goroutines with buffered channels so a slow client
// never blocks the PTY pump.
func (d *daemon) broadcast(data []byte) {
	frame := encodeFrame(FrameOutput, data)

	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, ch := range d.clients {
		select {
		case ch <- frame:
		default:
			// Client is too slow; drop this frame.
		}
	}
}

// handleClient serves a single client connection.
func (d *daemon) handleClient(conn net.Conn) {
	d.log.Debug("client connected")

	writeCh := make(chan []byte, 256)
	d.addClient(conn, writeCh)
	defer func() {
		d.removeClient(conn)
		conn.Close()
		d.log.Debug("client disconnected")
	}()

	// Start writer goroutine (reads from writeCh, writes to conn).
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for data := range writeCh {
			if _, err := conn.Write(data); err != nil {
				return
			}
		}
	}()

	// 1. Send replay on connect.
	replay := d.ring.Snapshot()
	if len(replay) > 0 {
		frame := encodeFrame(FrameReplay, replay)
		select {
		case writeCh <- frame:
		default:
			// Client write buffer full on connect; disconnect.
			return
		}
	}

	// 2. Read frames from client.
	header := make([]byte, 5)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		ftype := header[0]
		plen := binary.BigEndian.Uint32(header[1:5])

		if plen > 10*1024*1024 { // sanity: max 10 MiB per frame
			return
		}

		var payload []byte
		if plen > 0 {
			payload = make([]byte, plen)
			if _, err := io.ReadFull(conn, payload); err != nil {
				return
			}
		}

		switch ftype {
		case FrameInput:
			d.ptyFd.Write(payload)
		case FrameResize:
			if len(payload) == 4 {
				cols := binary.BigEndian.Uint16(payload[0:2])
				rows := binary.BigEndian.Uint16(payload[2:4])
				_ = pty.Setsize(d.ptyFd, &pty.Winsize{Cols: cols, Rows: rows})
			}
		case FrameClose:
			d.shutdown()
			return
		}
	}
}

func (d *daemon) addClient(conn net.Conn, ch chan []byte) {
	d.clientsMu.Lock()
	d.clients[conn] = ch
	d.clientsMu.Unlock()
}

func (d *daemon) removeClient(conn net.Conn) {
	d.clientsMu.Lock()
	if ch, ok := d.clients[conn]; ok {
		delete(d.clients, conn)
		close(ch)
	}
	d.clientsMu.Unlock()
}

// shutdown closes the PTY, kills the shell, and closes the listener.
func (d *daemon) shutdown() {
	d.closeOnce.Do(func() {
		d.ptyFd.Close()
		d.cmd.Process.Kill()
		d.ln.Close()
		os.Remove(d.socketPath)
		close(d.shellDone)
		d.log.Debug("daemon shutdown complete")
	})
}

// encodeFrame builds a wire-protocol frame.
func encodeFrame(ftype byte, payload []byte) []byte {
	frame := make([]byte, 5+len(payload))
	frame[0] = ftype
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	return frame
}
