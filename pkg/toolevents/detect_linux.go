//go:build linux

package toolevents

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// linuxProcTable reads /proc live for each query — cheap syscalls, no need
// to snapshot the whole process table up front.
type linuxProcTable struct{}

func newProcTable() procTable {
	return linuxProcTable{}
}

func (linuxProcTable) Cmdline(pid int) []string {
	return readCmdline(pid)
}

func (linuxProcTable) Children(pid int) []int {
	return getChildPIDs(pid)
}

// getChildPIDs returns the PIDs of all direct children of the given PID.
// Uses /proc on Linux.
func getChildPIDs(pid int) []int {
	taskDir := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
	data, err := os.ReadFile(taskDir)
	if err != nil {
		// Fallback: scan /proc for processes whose PPid matches
		return getChildPIDsFallback(pid)
	}
	return parsePIDList(string(data))
}

// getChildPIDsFallback scans /proc/*/status for processes with matching PPid.
// Works on Linux when /proc/<pid>/task/<pid>/children is unavailable.
func getChildPIDsFallback(ppid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	ppidStr := strconv.Itoa(ppid)
	var children []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		statusPath := fmt.Sprintf("/proc/%d/status", pid)
		data, err := os.ReadFile(statusPath)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PPid:\t") {
				if strings.TrimPrefix(line, "PPid:\t") == ppidStr {
					children = append(children, pid)
				}
				break
			}
		}
	}
	return children
}

// readCmdline reads /proc/<pid>/cmdline and splits by null bytes.
func readCmdline(pid int) []string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(data) == 0 {
		return nil
	}
	// cmdline is null-delimited, trim trailing null
	s := strings.TrimRight(string(data), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// parsePIDList parses a space-separated list of PIDs
func parsePIDList(s string) []int {
	var pids []int
	for _, field := range strings.Fields(s) {
		pid, err := strconv.Atoi(field)
		if err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}
