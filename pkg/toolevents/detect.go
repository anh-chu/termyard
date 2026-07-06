package toolevents

import (
	"path/filepath"
	"strings"
)

// agentPattern defines how to identify an agent from process cmdline args
type agentPattern struct {
	tool Tool
	// match returns true if the cmdline args indicate this agent
	match func(args []string) bool
}

// agentPatterns lists known agent signatures to look for in the process tree.
// Each pattern inspects the full cmdline (argv) of a process.
var agentPatterns = []agentPattern{
	{
		tool: ToolClaude,
		match: func(args []string) bool {
			return matchBinaryName(args, "claude")
		},
	},
	{
		tool: ToolCopilot,
		match: func(args []string) bool {
			// Copilot CLI runs as node — check if any arg contains "copilot"
			if matchBinaryName(args, "copilot") {
				return true
			}
			return matchNodeScript(args, "copilot")
		},
	},
	{
		tool: ToolCodex,
		match: func(args []string) bool {
			if matchBinaryName(args, "codex") {
				return true
			}
			return matchNodeScript(args, "codex")
		},
	},
	{
		tool: ToolGemini,
		match: func(args []string) bool {
			if matchBinaryName(args, "gemini") {
				return true
			}
			return matchNodeScript(args, "gemini")
		},
	},
	{
		tool: ToolOpenCode,
		match: func(args []string) bool {
			return matchBinaryName(args, "opencode")
		},
	},
	{
		tool: ToolPi,
		match: func(args []string) bool {
			return matchBinaryName(args, "pi")
		},
	},
}

// matchBinaryName checks if the first arg (the binary) has the given base name.
func matchBinaryName(args []string, name string) bool {
	if len(args) == 0 {
		return false
	}
	base := filepath.Base(args[0])
	return base == name
}

// matchNodeScript checks if this is a node process running a script whose
// path contains the given name. Handles patterns like:
//
//	node /usr/lib/node_modules/@openai/codex/bin/codex.js
//	node /home/user/.npm/bin/copilot
func matchNodeScript(args []string, name string) bool {
	if len(args) < 2 {
		return false
	}
	base := filepath.Base(args[0])
	if base != "node" && base != "nodejs" {
		return false
	}
	// Check remaining args for the tool name in the path or filename
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if strings.Contains(strings.ToLower(arg), name) {
			return true
		}
	}
	return false
}

// procTable answers process-tree questions (cmdline args, direct children)
// for a single detection pass. Platform-specific: pkg/toolevents/detect_linux.go
// reads /proc live; pkg/toolevents/detect_darwin.go snapshots via `ps` since
// macOS has no /proc.
type procTable interface {
	Cmdline(pid int) []string
	Children(pid int) []int
}

// DetectAgentInProcessTree walks the process tree rooted at pid and returns
// the first recognized agent tool found. Returns ("", false) if no agent
// is detected. Checks the pid itself (the agent may run as the pane's own
// process, e.g. after exec or when launched as the session command) plus its
// direct children and grandchildren.
func DetectAgentInProcessTree(pid int) (Tool, bool) {
	pt := newProcTable()

	// Check the root pid itself first — covers panes where the agent is the
	// foreground process (pane_pid == agent), not a shell child.
	if args := pt.Cmdline(pid); len(args) > 0 {
		for _, pat := range agentPatterns {
			if pat.match(args) {
				return pat.tool, true
			}
		}
	}

	children := pt.Children(pid)
	for _, cpid := range children {
		args := pt.Cmdline(cpid)
		if len(args) == 0 {
			continue
		}
		for _, pat := range agentPatterns {
			if pat.match(args) {
				return pat.tool, true
			}
		}
		// Also check grandchildren (shell → node → copilot)
		grandchildren := pt.Children(cpid)
		for _, gpid := range grandchildren {
			gargs := pt.Cmdline(gpid)
			if len(gargs) == 0 {
				continue
			}
			for _, pat := range agentPatterns {
				if pat.match(gargs) {
					return pat.tool, true
				}
			}
		}
	}
	return "", false
}
