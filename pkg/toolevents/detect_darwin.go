//go:build darwin

package toolevents

import (
	"os/exec"
	"strconv"
	"strings"
)

// darwinProcEntry holds one process's parent pid and tokenized argv, as
// reported by `ps`. macOS has no /proc, so unlike Linux we snapshot the
// whole process table once per detection pass instead of reading per-pid.
type darwinProcEntry struct {
	ppid int
	args []string
}

type darwinProcTable struct {
	procs map[int]darwinProcEntry
}

func newProcTable() procTable {
	pt := darwinProcTable{procs: make(map[int]darwinProcEntry)}

	out, err := exec.Command("ps", "-axww", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return pt
	}

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, errPid := strconv.Atoi(fields[0])
		ppid, errPpid := strconv.Atoi(fields[1])
		if errPid != nil || errPpid != nil {
			continue
		}
		pt.procs[pid] = darwinProcEntry{ppid: ppid, args: fields[2:]}
	}

	return pt
}

func (t darwinProcTable) Cmdline(pid int) []string {
	return t.procs[pid].args
}

func (t darwinProcTable) Children(pid int) []int {
	var children []int
	for cpid, entry := range t.procs {
		if entry.ppid == pid {
			children = append(children, cpid)
		}
	}
	return children
}
