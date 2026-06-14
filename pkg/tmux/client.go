package tmux

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Client wraps tmux CLI commands
type Client struct {
	tmuxPath string
}

// NewClient creates a new tmux client
func NewClient() (*Client, error) {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return nil, fmt.Errorf("tmux not found in PATH: %w", err)
	}
	return &Client{tmuxPath: path}, nil
}

// TmuxPath returns the path to the tmux binary
func (c *Client) TmuxPath() string {
	return c.tmuxPath
}

// Exec runs a tmux command and returns stdout
func (c *Client) Exec(args ...string) (string, error) {
	cmd := exec.Command(c.tmuxPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

// ListSessions returns all tmux sessions
func (c *Client) ListSessions() ([]*Session, error) {
	out, err := c.Exec("list-sessions", "-F", "#{session_id}:#{session_name}:#{session_created}:#{session_attached}:#{session_activity}")
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") {
			return nil, nil
		}
		return nil, err
	}

	var sessions []*Session
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 5)
		if len(parts) < 5 {
			continue
		}

		created, _ := strconv.ParseInt(parts[2], 10, 64)
		attached := parts[3] != "0"
		activity, _ := strconv.ParseInt(parts[4], 10, 64)

		sessions = append(sessions, &Session{
			ID:           parts[0],
			Name:         parts[1],
			Created:      time.Unix(created, 0),
			Attached:     attached,
			LastActivity: time.Unix(activity, 0),
		})
	}
	return sessions, nil
}

// ListWindows returns windows for a session
func (c *Client) ListWindows(sessionName string) ([]*Window, error) {
	out, err := c.Exec("list-windows", "-t", sessionName, "-F",
		"#{window_id}:#{session_id}:#{window_name}:#{window_index}:#{window_active}:#{window_layout}")
	if err != nil {
		return nil, err
	}

	var windows []*Window
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 6)
		if len(parts) < 6 {
			continue
		}
		idx, _ := strconv.Atoi(parts[3])
		windows = append(windows, &Window{
			ID:        parts[0],
			SessionID: parts[1],
			Name:      parts[2],
			Index:     idx,
			Active:    parts[4] == "1",
			Layout:    parts[5],
		})
	}
	return windows, nil
}

// ListPanes returns panes for a window
func (c *Client) ListPanes(target string) ([]*Pane, error) {
	out, err := c.Exec("list-panes", "-t", target, "-F",
		"#{pane_id}:#{window_id}:#{session_id}:#{pane_index}:#{pane_active}:#{pane_width}:#{pane_height}:#{pane_current_command}:#{pane_current_path}:#{pane_pid}")
	if err != nil {
		return nil, err
	}

	var panes []*Pane
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 10)
		if len(parts) < 10 {
			continue
		}
		idx, _ := strconv.Atoi(parts[3])
		w, _ := strconv.Atoi(parts[5])
		h, _ := strconv.Atoi(parts[6])
		pid, _ := strconv.Atoi(parts[9])
		panes = append(panes, &Pane{
			ID:             parts[0],
			WindowID:       parts[1],
			SessionID:      parts[2],
			Index:          idx,
			Active:         parts[4] == "1",
			Width:          w,
			Height:         h,
			CurrentCommand: parts[7],
			CurrentPath:    parts[8],
			PID:            pid,
		})
	}
	return panes, nil
}

// ListAllPanesDetailed returns all panes with session name and window index
// resolved by tmux (avoids extra ListSessions/ListWindows calls).
func (c *Client) ListAllPanesDetailed() ([]*PaneDetailed, error) {
	out, err := c.Exec("list-panes", "-a", "-F",
		"#{pane_id}:#{session_name}:#{window_index}:#{pane_pid}")
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") {
			return nil, nil
		}
		return nil, err
	}

	var panes []*PaneDetailed
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 4)
		if len(parts) < 4 {
			continue
		}
		winIdx, _ := strconv.Atoi(parts[2])
		pid, _ := strconv.Atoi(parts[3])
		panes = append(panes, &PaneDetailed{
			ID:      parts[0],
			Session: parts[1],
			Window:  winIdx,
			PID:     pid,
		})
	}
	return panes, nil
}

// ListAllPanes returns all panes across all sessions
func (c *Client) ListAllPanes() ([]*Pane, error) {
	out, err := c.Exec("list-panes", "-a", "-F",
		"#{pane_id}:#{window_id}:#{session_id}:#{pane_index}:#{pane_active}:#{pane_width}:#{pane_height}:#{pane_current_command}:#{pane_current_path}:#{pane_pid}")
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") {
			return nil, nil
		}
		return nil, err
	}

	var panes []*Pane
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 10)
		if len(parts) < 10 {
			continue
		}
		idx, _ := strconv.Atoi(parts[3])
		w, _ := strconv.Atoi(parts[5])
		h, _ := strconv.Atoi(parts[6])
		pid, _ := strconv.Atoi(parts[9])
		panes = append(panes, &Pane{
			ID:             parts[0],
			WindowID:       parts[1],
			SessionID:      parts[2],
			Index:          idx,
			Active:         parts[4] == "1",
			Width:          w,
			Height:         h,
			CurrentCommand: parts[7],
			CurrentPath:    parts[8],
			PID:            pid,
		})
	}
	return panes, nil
}

// HasSession checks if a session exists
func (c *Client) HasSession(name string) bool {
	_, err := c.Exec("has-session", "-t", name)
	return err == nil
}

// SessionIDByName returns the tmux session ID (e.g. "$3") for the given
// session name, or an empty string if not found. Using the numeric ID as the
// -t target avoids tmux interpreting special characters in the name (e.g. "~"
// is the last-marked-pane selector).
func (c *Client) SessionIDByName(name string) string {
	out, err := c.Exec("list-sessions", "-F", "#{session_id}:#{session_name}")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && parts[1] == name {
			return parts[0]
		}
	}
	return ""
}

// ValidateSessionName returns an error if name contains characters that have
// special meaning in tmux target syntax and would prevent reliable targeting.
func ValidateSessionName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	// These characters are reserved in tmux target syntax:
	//   ~  last marked pane  (causes "no marked target" error)
	//   !  last active session
	//   :  window separator  (foo:1 targets window 1 of session foo)
	const reserved = "~!:"
	for _, r := range reserved {
		if strings.ContainsRune(name, r) {
			return fmt.Errorf("session name cannot contain %q (reserved by tmux target syntax)", r)
		}
	}
	return nil
}

// SelectWindow switches the active window in a session
func (c *Client) SelectWindow(session, index string) error {
	_, err := c.Exec("select-window", "-t", fmt.Sprintf("%s:%s", session, index))
	return err
}

// SelectPane switches the active pane in a session window
func (c *Client) SelectPane(target string) error {
	_, err := c.Exec("select-pane", "-t", target)
	return err
}

// SelectLayout applies a tmux window layout string to a target.
func (c *Client) SelectLayout(target, layout string) error {
	_, err := c.Exec("select-layout", "-t", target, layout)
	return err
}

// NewWindow creates a new detached tmux window.
func (c *Client) NewWindow(session, name, projectPath, command string) error {
	args := []string{"new-window", "-d", "-t", session}
	if name != "" {
		args = append(args, "-n", name)
	}
	if projectPath = expandSessionPath(projectPath); projectPath != "" {
		args = append(args, "-c", projectPath)
	}
	if command != "" {
		args = append(args, wrapSessionCommand(command)...)
	}
	_, err := c.Exec(args...)
	return err
}

// SplitWindow splits the active pane in a target window.
func (c *Client) SplitWindow(target, projectPath, command string) error {
	args := []string{"split-window", "-d", "-t", target}
	if projectPath = expandSessionPath(projectPath); projectPath != "" {
		args = append(args, "-c", projectPath)
	}
	if command != "" {
		args = append(args, wrapSessionCommand(command)...)
	}
	_, err := c.Exec(args...)
	return err
}

// ServerAlive reports whether tmux server is responding.
func (c *Client) ServerAlive() bool {
	_, err := c.Exec("list-sessions")
	if err == nil {
		return true
	}
	return !strings.Contains(err.Error(), "no server running")
}

// NewSession creates a new tmux session with the given name (detached).
// Optional projectPath sets the initial working directory, and command starts
// the requested agent or shell process inside the session.
func (c *Client) NewSession(name, projectPath, command string) error {
	args := []string{"new-session", "-d", "-s", name}
	if projectPath = expandSessionPath(projectPath); projectPath != "" {
		args = append(args, "-c", projectPath)
	}
	if command != "" {
		args = append(args, wrapSessionCommand(command)...)
	}
	_, err := c.Exec(args...)
	return err
}

func expandSessionPath(projectPath string) string {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || projectPath[0] != '~' {
		return projectPath
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return projectPath
	}

	switch {
	case projectPath == "~":
		return home
	case strings.HasPrefix(projectPath, "~/"):
		return filepath.Join(home, strings.TrimPrefix(projectPath, "~/"))
	default:
		return projectPath
	}
}

func wrapSessionCommand(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}

	// Run the requested command in a login shell, then hand control back to an
	// interactive shell when it exits so the pane stays alive.
	script := command + "; exec " + shell + " -i"
	return []string{shell, "-lc", script}
}

// RenameSession renames a tmux session
func (c *Client) RenameSession(oldName, newName string) error {
	_, err := c.Exec("rename-session", "-t", oldName, newName)
	return err
}

// KillSession kills a tmux session, preferring the numeric ID over name.
// tmux uses special target syntax where characters like '~' have meaning,
// so name-based targeting is unreliable for sessions with unusual names.
// Pass id as the tmux session ID (e.g. "$15"); name is the fallback.
func (c *Client) KillSession(id, name string) error {
	if id != "" {
		if _, err := c.Exec("kill-session", "-t", id); err == nil {
			return nil
		}
	}
	_, err := c.Exec("kill-session", "-t", name)
	return err
}

// CapturePaneContent returns the visible text content of a pane
func (c *Client) CapturePaneContent(paneID string) (string, error) {
	return c.Exec("capture-pane", "-t", paneID, "-p")
}

// CapturePaneHistory returns pane content including recent scrollback.
func (c *Client) CapturePaneHistory(paneID string, startLine int) (string, error) {
	args := []string{"capture-pane", "-t", paneID}
	if startLine != 0 {
		args = append(args, "-S", strconv.Itoa(startLine))
	}
	args = append(args, "-p")
	return c.Exec(args...)
}
