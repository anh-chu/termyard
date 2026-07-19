package pty

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty/v2"
	"github.com/sirupsen/logrus"
)

// Frame types for the daemon wire protocol.
const (
	FrameOutput      = 0x01 // daemon → client: raw PTY output
	FrameInput       = 0x02 // client → daemon: raw bytes to write to PTY
	FrameResize      = 0x03 // client → daemon: 4 bytes (cols u16 BE + rows u16 BE)
	FrameClose       = 0x04 // client → daemon: kill shell
	FrameReplay      = 0x05 // daemon → client: ring buffer contents on connect
	FrameQueryBuffer = 0x06 // client → daemon: request ring buffer replay (0 payload)
)

// DaemonConfig configures a session daemon.
type DaemonConfig struct {
	ID          string // unique session identifier
	Shell       string // shell to spawn (default: $SHELL or /bin/bash)
	Cols, Rows  uint16 // initial terminal size
	Cwd         string // working directory
	SocketDir   string // directory for Unix sockets (default: /tmp/termyard-sessions-{uid}/)
	StateDir    string // directory for lifecycle state (default: XDG_STATE_HOME/termyard/sessions/)
	SystemdUnit string // systemd scope unit name for cleanup (empty if not using systemd)
	BufferSize  int    // ring buffer size in bytes (default: 8MB)
}

// RunDaemon is the entry point for a session daemon process.
// It creates a PTY, spawns the shell, listens on a Unix socket,
// and serves clients until the shell exits or Close is received.
// sessionMeta is written as a JSON sidecar file alongside the socket.
type sessionMeta struct {
	ID          string `json:"id"`
	Pid         int    `json:"pid"`
	ShellPid    int    `json:"shell_pid"`
	Shell       string `json:"shell"`
	Cwd         string `json:"cwd"`
	Created     string `json:"created"`
	Cols        uint16 `json:"cols"`
	Rows        uint16 `json:"rows"`
	SystemdUnit string `json:"systemd_unit,omitempty"`
}

func RunDaemon(cfg DaemonConfig) error {
	// Ensure we're a session leader so we survive parent/server restarts.
	// Registry.Create sets Setsid on the child, but if someone starts us
	// directly (e.g. systemd, manual invocation) we need our own session.
	syscall.Setsid() // no-op if already a session leader
	signal.Ignore(syscall.SIGHUP)

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
		cfg.BufferSize = 8 << 20 // 8 MB
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
	cmd.Env = append(cmd.Env, "TERMYARD_SESSION="+cfg.ID)
	cmd.Env = append(cmd.Env, "TERMYARD_PANE="+cfg.ID+":0.0")
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
	metadataPath := filepath.Join(cfg.SocketDir, cfg.ID+".json")
	if err := os.MkdirAll(cfg.SocketDir, 0700); err != nil {
		ptyFd.Close()
		cmd.Process.Kill()
		return fmt.Errorf("create socket dir: %w", err)
	}

	// Check whether a live daemon is already bound to this socket.
	// If we can connect to it, refuse to start — we must not steal an
	// active session.  If the socket file exists but nobody is listening,
	// it is a stale leftover and we can safely remove it.
	if isDaemonAlive(socketPath) {
		ptyFd.Close()
		killProcessGroup(cmd.Process.Pid)
		return fmt.Errorf("session %q is already active (socket %s is live)", cfg.ID, socketPath)
	}
	os.Remove(socketPath)   // remove stale socket
	os.Remove(metadataPath) // remove stale metadata

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		ptyFd.Close()
		killProcessGroup(cmd.Process.Pid)
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	defer os.Remove(socketPath)
	defer os.Remove(metadataPath)

	// Write metadata sidecar so Registry.List can discover sessions.
	meta := sessionMeta{
		ID:          cfg.ID,
		Pid:         os.Getpid(),
		ShellPid:    cmd.Process.Pid,
		Shell:       cfg.Shell,
		Cwd:         cfg.Cwd,
		Created:     time.Now().Format(time.RFC3339),
		Cols:        cfg.Cols,
		Rows:        cfg.Rows,
		SystemdUnit: cfg.SystemdUnit,
	}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(metadataPath, metaBytes, 0600); err != nil {
		ptyFd.Close()
		killProcessGroup(cmd.Process.Pid)
		ln.Close()
		return fmt.Errorf("write metadata: %w", err)
	}

	// 3b. Set up durable lifecycle state so crashes can be detected.
	stateDir := cfg.StateDir
	if stateDir == "" {
		stateDir = DefaultStateDir()
	}
	lifecycleStore, lcErr := NewLifecycleStore(stateDir)
	if lcErr != nil {
		log.WithError(lcErr).Warn("cannot create lifecycle store — crash detection disabled")
	} else {
		pid := os.Getpid()
		lr := LifecycleRecord{
			ID:            cfg.ID,
			Shell:         cfg.Shell,
			Cwd:           cfg.Cwd,
			Cols:          cfg.Cols,
			Rows:          cfg.Rows,
			DaemonPID:     pid,
			SystemdUnit:   cfg.SystemdUnit,
			Generation:    NewGeneration(),
			ProcStartTime: procStartTime(pid),
		}
		if err := lifecycleStore.RecordActive(lr); err != nil {
			log.WithError(err).Warn("failed to write lifecycle record")
		} else {
			log.WithField("state_dir", stateDir).Debug("lifecycle store ready")
		}
	}

	log.WithField("socket", socketPath).Info("daemon listening")

	// 4. Build and run the daemon.
	d := &daemon{
		config:         cfg,
		ptyFd:          ptyFd,
		cmd:            cmd,
		ring:           ring,
		ln:             ln,
		socketPath:     socketPath,
		log:            log,
		lifecycleStore: lifecycleStore,
		clients:        make(map[net.Conn]chan []byte),
		shellDone:      make(chan struct{}),
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

	lifecycleStore *LifecycleStore // durable lifecycle state

	clientsMu sync.RWMutex
	clients   map[net.Conn]chan []byte // per-client write channel

	shellDone chan struct{}
	closeOnce sync.Once
}

func (d *daemon) run() error {
	defer func() {
		if r := recover(); r != nil {
			d.log.WithFields(logrus.Fields{
				"panic": r,
				"stack": string(debug.Stack()),
			}).Error("daemon run accept loop panicked")
		}
	}()

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
	defer func() {
		if r := recover(); r != nil {
			d.log.WithFields(logrus.Fields{
				"panic": r,
				"stack": string(debug.Stack()),
			}).Error("daemon pumpPTY panicked — shutting down")
			d.shutdown()
		}
	}()

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
	defer func() {
		if r := recover(); r != nil {
			d.log.WithFields(logrus.Fields{
				"panic": r,
				"stack": string(debug.Stack()),
			}).Error("daemon handleClient panicked — closing connection")
			conn.Close()
		}
	}()

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
		case FrameQueryBuffer:
			// Send current ring buffer contents without disconnecting.
			replay := d.ring.Snapshot()
			if len(replay) > 0 {
				frame := encodeFrame(FrameReplay, replay)
				select {
				case writeCh <- frame:
				default:
				}
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
// It also transitions the lifecycle record to cleanly_ended so the
// registry knows this was intentional, not a crash.
func (d *daemon) shutdown() {
	d.closeOnce.Do(func() {
		// Transition lifecycle before tearing down.
		if d.lifecycleStore != nil {
			if err := d.lifecycleStore.Transition(d.config.ID, LifecycleActive, LifecycleCleanlyEnded); err != nil {
				d.log.WithError(err).Debug("lifecycle transition to cleanly_ended failed (may already be ended)")
			}
		}
		d.ptyFd.Close()
		// Kill the shell's entire process group so child processes
		// (background jobs, pipelines, etc.) are not leaked as orphans.
		// The shell is a session leader (pty.StartWithSize uses Setsid),
		// so its PID equals its process group ID.  Negative PID targets
		// the process group.  If the shell already exited this is a no-op.
		killProcessGroup(d.cmd.Process.Pid)
		d.ln.Close()
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

// isDaemonAlive returns true if a daemon is currently listening on socketPath.
// Used by RunDaemon to avoid stealing a live daemon's socket.
func isDaemonAlive(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// killProcessGroup sends SIGKILL to the process group identified by pgid.
// If the process group no longer exists (e.g. shell already exited),
// this is silently ignored.
func killProcessGroup(pgid int) {
	if pgid <= 0 {
		return
	}
	// Negative PID sends the signal to the entire process group.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
