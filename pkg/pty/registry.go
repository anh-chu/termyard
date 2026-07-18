package pty

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

// SessionInfo holds metadata about a running session daemon.
type SessionInfo struct {
	ID      string
	Pid     int
	Shell   string
	Cwd     string
	Created string // RFC3339
	Cols    uint16
	Rows    uint16
	Socket  string // full path to .sock file
}

// Registry manages session daemon lifecycle: create, list, kill, capture.
type Registry struct {
	dir string // socket directory
}

// NewRegistry creates a session registry using the given socket directory.
// The directory is created with 0700 if it does not exist.
func NewRegistry(dir string) *Registry {
	os.MkdirAll(dir, 0700)
	return &Registry{dir: dir}
}

// Dir returns the registry's socket directory.
func (r *Registry) Dir() string {
	return r.dir
}

// SocketPath returns the full path to a session's Unix socket.
func (r *Registry) SocketPath(name string) string {
	return filepath.Join(r.dir, name+".sock")
}

// metadataPath returns the full path to a session's metadata JSON file.
func (r *Registry) metadataPath(name string) string {
	return filepath.Join(r.dir, name+".json")
}

// Create spawns a session daemon as a fully detached subprocess.
// It waits up to 2s for the socket file to appear, then returns.
func (r *Registry) Create(name, shell, cwd string, cols, rows uint16) error {
	log := logrus.WithFields(logrus.Fields{
		"component": "registry",
		"name":      name,
		"shell":     shell,
		"cwd":       cwd,
	})

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}

	// Derive defaults in-process so the daemon gets explicit values.
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
	}
	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 40
	}

	args := []string{
		"session-daemon",
		"--id", name,
		"--shell", shell,
		"--cols", fmt.Sprintf("%d", cols),
		"--rows", fmt.Sprintf("%d", rows),
		"--cwd", cwd,
		"--socket-dir", r.dir,
	}

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon process: %w", err)
	}

	// Release the process handle so the daemon is fully independent.
	if err := cmd.Process.Release(); err != nil {
		log.WithError(err).Warn("failed to release daemon process handle")
	}

	// Wait up to 2s for the socket file to appear.
	socketPath := r.SocketPath(name)
	for i := 0; i < 40; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			log.WithField("socket", socketPath).Info("session daemon created")
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	return fmt.Errorf("daemon socket %s did not appear within 2s", socketPath)
}

// List scans the socket directory for *.sock files, reads their sidecar JSON,
// and checks liveness by attempting a connection.
// Stale socket+json files (where the socket is dead) are removed.
func (r *Registry) List() []SessionInfo {
	entries, err := filepath.Glob(filepath.Join(r.dir, "*.sock"))
	if err != nil {
		return nil
	}

	var out []SessionInfo
	for _, sockPath := range entries {
		name := sockPath[:len(sockPath)-len(".sock")]
		name = filepath.Base(name)

		// Check liveness.
		conn, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
		if err != nil {
			// Dead socket — clean up.
			r.removeStale(name)
			continue
		}
		conn.Close()

		info := SessionInfo{
			ID:     name,
			Socket: sockPath,
		}

		// Read metadata sidecar.
		metaPath := r.metadataPath(name)
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta sessionMeta
			if json.Unmarshal(data, &meta) == nil {
				info.Pid = meta.Pid
				info.Shell = meta.Shell
				info.Cwd = meta.Cwd
				info.Created = meta.Created
				info.Cols = meta.Cols
				info.Rows = meta.Rows
			}
		}

		out = append(out, info)
	}
	return out
}

// removeStale removes a socket file and its metadata sidecar.
func (r *Registry) removeStale(name string) {
	sockPath := r.SocketPath(name)
	metaPath := r.metadataPath(name)
	os.Remove(sockPath)
	os.Remove(metaPath)
	logrus.WithField("name", name).Debug("removed stale session files")
}

// Kill sends a FrameClose to the daemon via its socket.
func (r *Registry) Kill(name string) error {
	socketPath := r.SocketPath(name)
	conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
	if err != nil {
		return fmt.Errorf("dial daemon socket %s: %w", socketPath, err)
	}
	defer conn.Close()

	frame := encodeFrame(FrameClose, nil)
	if _, err := conn.Write(frame); err != nil {
		return fmt.Errorf("send close frame: %w", err)
	}
	return nil
}

// Capture connects to the daemon, sends FrameQueryBuffer, reads the
// FrameReplay response, and returns the text content.
func (r *Registry) Capture(name string) (string, error) {
	socketPath := r.SocketPath(name)
	conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
	if err != nil {
		return "", fmt.Errorf("dial daemon socket %s: %w", socketPath, err)
	}
	defer conn.Close()

	// Send query.
	frame := encodeFrame(FrameQueryBuffer, nil)
	if _, err := conn.Write(frame); err != nil {
		return "", fmt.Errorf("send query buffer frame: %w", err)
	}

	// Read the response header.
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", fmt.Errorf("read response header: %w", err)
	}

	ftype := header[0]
	plen := binary.BigEndian.Uint32(header[1:5])

	if plen > 10*1024*1024 { // sanity: max 10 MiB
		return "", fmt.Errorf("response too large: %d bytes", plen)
	}

	payload := make([]byte, plen)
	if plen > 0 {
		if _, err := io.ReadFull(conn, payload); err != nil {
			return "", fmt.Errorf("read response payload: %w", err)
		}
	}

	if ftype != FrameReplay {
		return "", fmt.Errorf("unexpected frame type: %02x", ftype)
	}

	return string(payload), nil
}
