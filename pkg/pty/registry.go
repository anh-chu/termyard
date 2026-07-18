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
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

// ansiRe matches ANSI escape sequences (CSI, OSC, and simple escapes).
var ansiRe = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[a-zA-Z]|\][^\x1b\x07]*(?:\x07|\x1b\\)|[()][AB012]|\[\?[0-9;]*[hl]|=|>|\x1b)`)

// ctrlRe matches carriage returns and other non-newline control chars.
var ctrlRe = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f]`)

// SessionInfo holds metadata about a running session daemon.
type SessionInfo struct {
	ID       string
	Pid      int
	ShellPid int
	Shell    string
	Cwd      string
	Created  string // RFC3339
	Cols     uint16
	Rows     uint16
	Socket   string // full path to .sock file
}

// validSessionID returns true if id is safe for use in file paths.
// Rejects empty, contains path separators, dots-only, or control chars.
func validSessionID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, c := range id {
		if c == '/' || c == '\\' || c == '\x00' {
			return false
		}
	}
	return true
}

// Registry manages session daemon lifecycle: create, list, kill, capture.
type Registry struct {
	dir       string // socket directory
	failMu    sync.Mutex
	failCount map[string]int // consecutive liveness failures per session

	recoveryMu     sync.Mutex      // serializes recover/dismiss operations
	lifecycleStore *LifecycleStore // durable lifecycle state (may be nil)
}

// NewRegistry creates a session registry using the given socket directory.
// The directory is created with 0700 if it does not exist.
func NewRegistry(dir string) *Registry {
	os.MkdirAll(dir, 0700)
	return &Registry{dir: dir, failCount: make(map[string]int)}
}

// Dir returns the registry's socket directory.
func (r *Registry) Dir() string {
	return r.dir
}

// SetLifecycleStore wires the durable lifecycle store into the registry.
// When set, the registry will differentiate crashes from clean shutdowns
// and persist session metadata for crash recovery.
func (r *Registry) SetLifecycleStore(store *LifecycleStore) {
	r.lifecycleStore = store
}

// LifecycleStore returns the durable lifecycle store, or nil if not set.
func (r *Registry) LifecycleStore() *LifecycleStore {
	return r.lifecycleStore
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

	// Pass state dir for lifecycle persistence.
	stateDir := DefaultStateDir()
	args = append(args, "--state-dir", stateDir)

	cmd := exec.Command(exe, args...)

	// Wrap in a systemd user scope if systemd-run is available.
	// This gives the daemon its own cgroup so a server OOM doesn't
	// cascade to session daemons.  Use a unique unit name (PID suffix)
	// so recovering a crashed session doesn't collide with the old scope.
	if systemdRun, err := exec.LookPath("systemd-run"); err == nil {
		unitName := fmt.Sprintf("termyard-session-%s-%d.scope", name, time.Now().UnixMilli())
		scopeArgs := []string{
			"--user", "--scope",
			"--unit", unitName,
			"--",
		}
		fullArgs := append(scopeArgs, exe)
		fullArgs = append(fullArgs, args...)
		cmd = exec.Command(systemdRun, fullArgs...)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Open /dev/null explicitly so the daemon doesn't inherit parent's
	// fds (which may be pipes that close when the server restarts).
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull

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
// Stale socket+json files (where the daemon process is confirmed dead) are removed.
func (r *Registry) List() []SessionInfo {
	entries, err := filepath.Glob(filepath.Join(r.dir, "*.sock"))
	if err != nil {
		return nil
	}

	type removal struct {
		name   string
		reason string
	}

	var (
		out      []SessionInfo
		stale    []removal
		total    = len(entries)
	)
	// Track which entries we saw for later failure-count cleanup.
	seen := make(map[string]bool, total)

	for _, sockPath := range entries {
		name := filepath.Base(sockPath[:len(sockPath)-len(".sock")])
		seen[name] = true

		conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
		if err != nil {
			r.failMu.Lock()
			r.failCount[name]++
			fails := r.failCount[name]
			r.failMu.Unlock()

			if fails < 5 {
				logrus.WithFields(logrus.Fields{
					"component": "registry",
					"name":      name,
					"fails":     fails,
				}).Debug("daemon liveness check failed, will retry")
				continue
			}

			// Threshold reached. Before removing, verify the daemon
			// process is actually dead (not just slow/overloaded).
			pid := r.readDaemonPID(name)
			if pid > 0 && processAlive(pid) {
				logrus.WithFields(logrus.Fields{
					"component": "registry",
					"name":      name,
					"pid":       pid,
					"fails":     fails,
				}).Warn("daemon process is alive but socket unreachable — keeping session")
				r.failMu.Lock()
				delete(r.failCount, name)
				r.failMu.Unlock()
				continue
			}

			// Process is dead — safe to clean up.
			stale = append(stale, removal{name: name, reason: "daemon process dead"})
			continue
		}
		conn.Close()

		// Reset failure counter on successful connect.
		r.failMu.Lock()
		delete(r.failCount, name)
		r.failMu.Unlock()

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
				info.ShellPid = meta.ShellPid
				info.Shell = meta.Shell
				info.Cwd = meta.Cwd
				info.Created = meta.Created
				info.Cols = meta.Cols
				info.Rows = meta.Rows
			}
		}

		out = append(out, info)
	}

	// Mass-removal protection: if ALL entries would be removed (stale > 0
	// and live == 0), skip staleness cleanup — this is almost certainly a
	// transient system event (load spike, tmpfs issue, etc.) and we should
	// not nuke every session in one go.
	if len(out) == 0 && len(stale) > 0 {
		logrus.WithFields(logrus.Fields{
			"component": "registry",
			"stale":     len(stale),
			"total":     total,
		}).Warn("all sessions appear stale — skipping removal (probable transient event)")
		// Keep all sessions — do not clean up.
		for _, s := range stale {
			r.failMu.Lock()
			delete(r.failCount, s.name)
			r.failMu.Unlock()
		}
	} else {
		for _, s := range stale {
			r.removeStale(s.name, s.reason)
			r.failMu.Lock()
			delete(r.failCount, s.name)
			r.failMu.Unlock()
		}
	}

	// Clean up failure counters for sessions whose sockets disappeared
	// (e.g. killed externally).
	r.failMu.Lock()
	for name := range r.failCount {
		if !seen[name] {
			delete(r.failCount, name)
		}
	}
	r.failMu.Unlock()

	return out
}

// readDaemonPID reads the metadata JSON sidecar and returns the daemon PID,
// or 0 if the file cannot be read or parsed.
func (r *Registry) readDaemonPID(name string) int {
	metaPath := r.metadataPath(name)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return 0
	}
	var meta sessionMeta
	if json.Unmarshal(data, &meta) != nil {
		return 0
	}
	return meta.Pid
}

// processAlive returns true if the process with the given PID exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}

// removeStale removes a socket file and its metadata sidecar.
// If the lifecycle store is configured, this function checks whether the
// daemon exited intentionally (cleanly_ended, termination_requested, dismissed)
// or crashed (active state with dead process).  Crashed sessions are NOT
// deleted — they are left on disk for recovery.
func (r *Registry) removeStale(name, reason string) {
	sockPath := r.SocketPath(name)
	metaPath := r.metadataPath(name)

	// Check lifecycle store for crash detection.
	if r.lifecycleStore != nil {
		rec, err := r.lifecycleStore.Get(name)
		if err == nil {
			switch rec.State {
			case LifecycleActive:
				// The daemon process died but the lifecycle record
				// was never transitioned out of active — this is a crash.
				// Preserve the socket and metadata for recovery.
				logrus.WithFields(logrus.Fields{
					"component": "registry",
					"name":      name,
					"pid":       rec.DaemonPID,
				}).Warn("daemon crashed — preserving session for recovery")
				if transErr := r.lifecycleStore.Transition(name, LifecycleActive, LifecycleCrashed); transErr != nil {
					logrus.WithError(transErr).WithField("name", name).Warn("failed to transition to crashed")
				}
				// Do NOT delete socket/metadata — they are needed for recovery.
				return

			case LifecycleCleanlyEnded, LifecycleTerminationRequested, LifecycleDismissed:
				// Intentionally terminated or dismissed — clean up normally.

			case LifecycleCrashed:
				// Already marked as crashed, keep preserved.
				return

			default:
				// Unknown state — clean up cautiously.
			}
		}
		// If no lifecycle record exists (pre-lifecycle daemon),
		// fall through to normal cleanup.
	}

	os.Remove(sockPath)
	os.Remove(metaPath)
	logrus.WithFields(logrus.Fields{
		"component": "registry",
		"name":      name,
		"reason":    reason,
	}).Info("removed stale session files")
}

// Kill sends a FrameClose to the daemon via its socket.
// It marks the session as intentionally terminated in the lifecycle store
// so the registry can distinguish explicit kills from crashes.
func (r *Registry) Kill(name string) error {
	// Record intentional kill before sending FrameClose.
	// If the daemon process dies without transitioning to cleanly_ended,
	// the termination_requested state tells the registry this wasn't a crash.
	if r.lifecycleStore != nil {
		// Best-effort — ignore errors (the record may not exist yet or
		// the state may already have changed).
		_ = r.lifecycleStore.Transition(name, LifecycleActive, LifecycleTerminationRequested)
	}

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

	// Strip ANSI escape sequences and control chars so callers get clean text
	// (like capture-pane).
	clean := ansiRe.ReplaceAllString(string(payload), "")
	clean = ctrlRe.ReplaceAllString(clean, "")
	return clean, nil
}

// CrashedSessions returns lifecycle records for all sessions that crashed
// (state == "crashed").  Returns nil if no lifecycle store is configured.
func (r *Registry) CrashedSessions() []LifecycleRecord {
	if r.lifecycleStore == nil {
		return nil
	}
	return r.lifecycleStore.ListByState(LifecycleCrashed)
}

// RecoverSession re-spawns a daemon for a previously crashed session.
// It reads the saved shell/cwd from the lifecycle record, starts a new daemon,
// and transitions the state to "recovered".  The old stale socket and metadata
// files are cleaned up before the new daemon is spawned.
// Optional shellOverride and cwdOverride allow the user to choose a different
// shell or working directory at recovery time.
func (r *Registry) RecoverSession(id string, shellOverride ...string) error {
	if !validSessionID(id) {
		return fmt.Errorf("invalid session id: %q", id)
	}
	r.recoveryMu.Lock()
	defer r.recoveryMu.Unlock()
	if r.lifecycleStore == nil {
		return fmt.Errorf("no lifecycle store configured")
	}

	rec, err := r.lifecycleStore.Get(id)
	if err != nil {
		return fmt.Errorf("get lifecycle record for %s: %w", id, err)
	}
	if rec.State != LifecycleCrashed {
		return fmt.Errorf("session %s is in state %q, not crashed", id, rec.State)
	}

	shell := rec.Shell
	cwd := rec.Cwd
	if len(shellOverride) > 0 && shellOverride[0] != "" {
		shell = shellOverride[0]
	}
	if len(shellOverride) > 1 && shellOverride[1] != "" {
		cwd = shellOverride[1]
	}

	// Transition to recovered BEFORE spawning — the new daemon will
	// overwrite the lifecycle record with a fresh "active" state on
	// startup, which is the correct final state.
	if err := r.lifecycleStore.Transition(id, LifecycleCrashed, LifecycleRecovered); err != nil {
		return fmt.Errorf("transition to recovered: %w", err)
	}

	// Clean up old stale files so the new daemon can claim the socket.
	os.Remove(r.SocketPath(id))
	os.Remove(r.metadataPath(id))

	// Spawn a new daemon with the saved (or overridden) configuration.
	if err := r.Create(id, shell, cwd, rec.Cols, rec.Rows); err != nil {
		// Rollback lifecycle state on spawn failure.
		_ = r.lifecycleStore.Transition(id, LifecycleRecovered, LifecycleCrashed)
		return fmt.Errorf("re-spawn daemon for %s: %w", id, err)
	}

	logrus.WithFields(logrus.Fields{
		"component": "registry",
		"id":        id,
		"shell":     shell,
		"cwd":       cwd,
	}).Info("recovered crashed session")

	return nil
}

// DismissSession marks a crashed session as dismissed and cleans up its files.
func (r *Registry) DismissSession(id string) error {
	if !validSessionID(id) {
		return fmt.Errorf("invalid session id: %q", id)
	}
	r.recoveryMu.Lock()
	defer r.recoveryMu.Unlock()
	return r.dismissSessionLocked(id)
}

// dismissSessionLocked is the inner implementation; caller must hold recoveryMu.
func (r *Registry) dismissSessionLocked(id string) error {
	if r.lifecycleStore == nil {
		os.Remove(r.SocketPath(id))
		os.Remove(r.metadataPath(id))
		return nil
	}

	rec, err := r.lifecycleStore.Get(id)
	if err != nil {
		os.Remove(r.SocketPath(id))
		os.Remove(r.metadataPath(id))
		return nil
	}

	if rec.State != LifecycleCrashed {
		return fmt.Errorf("session %s is in state %q, not crashed", id, rec.State)
	}

	if err := r.lifecycleStore.Transition(id, LifecycleCrashed, LifecycleDismissed); err != nil {
		return fmt.Errorf("transition to dismissed: %w", err)
	}

	os.Remove(r.SocketPath(id))
	os.Remove(r.metadataPath(id))
	return nil
}

// DismissAll marks all crashed sessions as dismissed and cleans up their files.
func (r *Registry) DismissAll() error {
	r.recoveryMu.Lock()
	defer r.recoveryMu.Unlock()
	crashed := r.CrashedSessions()
	for _, rec := range crashed {
		_ = r.dismissSessionLocked(rec.ID)
	}
	return nil
}

// CleanupCrashedIfDead removes crash-preserved files for a session if the
// daemon process is confirmed dead and the user hasn't chosen recovery.
// This is called as a fallback when a session transitions from crashed to
// cleanly_ended (e.g. the daemon's shell finally exits after a crash).
func (r *Registry) CleanupCrashedIfDead(id string) {
	if r.lifecycleStore == nil {
		return
	}
	rec, err := r.lifecycleStore.Get(id)
	if err != nil || rec.State != LifecycleCrashed {
		return
	}
	if !processAlive(rec.DaemonPID) {
		// Process long dead — safe to clean up.
		os.Remove(r.SocketPath(id))
		os.Remove(r.metadataPath(id))
	}
}
