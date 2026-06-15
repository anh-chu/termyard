package agentsetup

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"

	"github.com/ekristen/guppi/pkg/common"
)

//go:embed pi-extension/guppi.ts
var piExtensionTemplate string

//go:embed opencode-plugin/index.js
var openCodePluginTemplate string

type agentConfig struct {
	name     string
	key      string
	binary   string
	detected bool
	setup    func(serverURL, guppiBin string, resilient bool, extraDirs []string) error
}

func Execute(ctx context.Context, c *cli.Command) error {
	serverURL := c.String("server")

	// Parse --config-dir flags into map[agentKey][]string
	extraDirs := make(map[string][]string)
	for _, val := range c.StringSlice("config-dir") {
		parts := strings.SplitN(val, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("invalid --config-dir format %q, expected agent=path", val)
		}
		extraDirs[parts[0]] = append(extraDirs[parts[0]], parts[1])
	}

	// Find guppi binary path
	guppiBin, err := os.Executable()
	if err != nil {
		guppiBin = "guppi"
	}

	// If running `go run`, use the binary name directly
	if strings.Contains(guppiBin, "go-build") {
		guppiBin = "guppi"
	}

	agents := []agentConfig{
		{
			name:   "Claude Code",
			key:    "claude",
			binary: "claude",
			setup:  setupClaude,
		},
		{
			name:   "Codex",
			key:    "codex",
			binary: "codex",
			setup:  setupCodex,
		},
		{
			name:   "OpenCode",
			key:    "opencode",
			binary: "opencode",
			setup:  setupOpenCode,
		},
		{
			name:   "Pi",
			key:    "pi",
			binary: "pi",
			setup:  setupPi,
		},
	}

	// Detect installed agents
	fmt.Println("Detecting installed AI agents...")
	fmt.Println()
	for i := range agents {
		_, err := exec.LookPath(agents[i].binary)
		agents[i].detected = err == nil
		status := "not found"
		if agents[i].detected {
			status = "found"
		}
		fmt.Printf("  %-20s %s\n", agents[i].name, status)
	}
	fmt.Println()

	dryRun := c.Bool("dry-run")
	resilient := !c.Bool("block")

	anySetup := false
	for _, agent := range agents {
		if !agent.detected {
			continue
		}
		anySetup = true
		extras := extraDirs[agent.key]
		if dryRun {
			fmt.Printf("Would configure hooks for %s\n", agent.name)
			for _, dir := range extras {
				fmt.Printf("  Would also configure: %s\n", dir)
			}
		} else {
			fmt.Printf("Configuring hooks for %s...\n", agent.name)
			if err := agent.setup(serverURL, guppiBin, resilient, extras); err != nil {
				logrus.WithError(err).WithField("agent", agent.name).Warn("failed to configure")
				fmt.Printf("  Warning: %v\n", err)
			} else {
				fmt.Printf("  Done.\n")
			}
		}
	}

	if !anySetup {
		fmt.Println("No supported agents found. Install one of: claude, codex, opencode")
		return nil
	}

	fmt.Println()
	fmt.Println("Agent hooks configured. They will notify guppi at:", serverURL)
	return nil
}

// setupClaude configures Claude Code hooks in ~/.claude/settings.json and any extra dirs
func setupClaude(serverURL, guppiBin string, resilient bool, extraDirs []string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dirs := append([]string{filepath.Join(homeDir, ".claude")}, extraDirs...)
	for _, dir := range dirs {
		if err := setupClaudeDir(dir, guppiBin, resilient); err != nil {
			return err
		}
	}
	return nil
}

func setupClaudeDir(configDir, guppiBin string, resilient bool) error {
	settingsPath := filepath.Join(configDir, "settings.json")

	// Read existing settings
	var settings map[string]interface{}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		settings = make(map[string]interface{})
	} else {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", settingsPath, err)
		}
	}

	// Notify auto-discovers the unix socket, so no --server needed
	notifyCmd := fmt.Sprintf("'%s' notify", guppiBin)

	suffix := ""
	if resilient {
		suffix = " || true"
	}

	// Build hooks config — each hook must be {"type": "command", "command": "..."}
	makeHook := func(cmd string) []map[string]interface{} {
		return []map[string]interface{}{
			{"type": "command", "command": cmd + suffix},
		}
	}

	hooks := map[string]interface{}{
		"SessionStart": []map[string]interface{}{
			{
				"matcher": "",
				"hooks":   makeHook(notifyCmd + " -t claude -s active -m 'Session started' --stdin"),
			},
		},
		"UserPromptSubmit": []map[string]interface{}{
			{
				"matcher": "",
				"hooks":   makeHook(notifyCmd + " -t claude -s active -m 'Thinking' --stdin"),
			},
		},
		"PreToolUse": []map[string]interface{}{
			{
				"matcher": "",
				"hooks":   makeHook(notifyCmd + " -t claude --stdin"),
			},
		},
		"PostToolUse": []map[string]interface{}{
			{
				"matcher": "",
				"hooks":   makeHook(notifyCmd + " -t claude --stdin"),
			},
		},
		"Notification": []map[string]interface{}{
			{
				"matcher": "permission_prompt",
				"hooks":   makeHook(notifyCmd + " -t claude -s waiting -m 'Permission needed'"),
			},
			{
				"matcher": "elicitation_dialog",
				"hooks":   makeHook(notifyCmd + " -t claude -s waiting -m 'Needs input'"),
			},
		},
		"Stop": []map[string]interface{}{
			{
				"matcher": "",
				"hooks":   makeHook(notifyCmd + " -t claude -s completed --stdin"),
			},
		},
	}

	settings["hooks"] = hooks

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		return err
	}

	fmt.Printf("  Wrote hooks to %s\n", settingsPath)
	return nil
}

// setupCodex configures Codex CLI via ~/.codex/config.toml and any extra dirs
func setupCodex(serverURL, guppiBin string, resilient bool, extraDirs []string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dirs := append([]string{filepath.Join(homeDir, ".codex")}, extraDirs...)
	for _, dir := range dirs {
		if err := setupCodexDir(dir, guppiBin, resilient); err != nil {
			return err
		}
	}
	return nil
}

// setupCodexDir configures a single Codex config directory.
// Codex has two hook mechanisms:
// 1. Legacy `notify` key in config.toml (fires on agent-turn-complete)
// 2. Modern hooks system in hooks.json (supports UserPromptSubmit, Stop, PreToolUse, etc.)
// We use both: notify for backward compat, hooks.json for full lifecycle.
func setupCodexDir(configDir, guppiBin string, resilient bool) error {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}

	// Configure legacy notify hook in config.toml
	configPath := filepath.Join(configDir, "config.toml")
	if err := setupCodexNotify(configPath, guppiBin, resilient); err != nil {
		return err
	}

	// Configure modern hooks in hooks.json
	hooksPath := filepath.Join(configDir, "hooks.json")
	if err := setupCodexHooks(hooksPath, guppiBin, resilient); err != nil {
		return err
	}

	return nil
}

func setupCodexNotify(configPath, guppiBin string, resilient bool) error {
	// Codex passes the event JSON as argv[1] to the notify command.
	// guppi notify --event-data parses it natively — no bash/jq needed.
	var notifyLine string
	if resilient {
		// Wrap in bash to support || true — Codex appends event JSON as $1
		notifyLine = fmt.Sprintf(
			`notify = ["bash", "-c", "'%s' notify -t codex --event-data \"$1\" || true", "--"] # guppi-agent-hook`,
			guppiBin,
		)
	} else {
		notifyLine = fmt.Sprintf(
			`notify = ["%s", "notify", "-t", "codex", "--event-data"] # guppi-agent-hook`,
			guppiBin,
		)
	}

	// Read existing config.toml and update/insert the notify line.
	// IMPORTANT: notify must appear as a top-level key BEFORE any TOML
	// table headers (e.g. [sandbox]). Codex's TOML parser treats keys
	// after a table header as belonging to that table, so we insert
	// right after the model line (or other top-level keys at the start).
	var lines []string
	data, err := os.ReadFile(configPath)
	if err == nil {
		lines = strings.Split(string(data), "\n")
	}

	// First pass: remove any existing notify line
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "notify") && strings.Contains(trimmed, "=") {
			continue
		}
		filtered = append(filtered, line)
	}
	lines = filtered

	// Second pass: insert notify after the last top-level key-value line
	// and before the first table header ([section]).
	insertIdx := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			// Hit a table header — insert before it
			break
		}
		// It's a top-level key=value line; insert after it
		insertIdx = i + 1
	}

	// Insert the notify line
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:insertIdx]...)
	newLines = append(newLines, notifyLine)
	newLines = append(newLines, lines[insertIdx:]...)
	lines = newLines

	// Ensure hooks are enabled in [features] section
	hasFeaturesSection := false
	hooksEnabled := false
	inFeaturesSection := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[features]" {
			hasFeaturesSection = true
			inFeaturesSection = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") && trimmed != "[features]" {
			inFeaturesSection = false
			continue
		}
		if inFeaturesSection && strings.HasPrefix(trimmed, "hooks") {
			// Found hooks setting, ensure it's true
			if strings.Contains(trimmed, "=") {
				parts := strings.SplitN(trimmed, "=", 2)
				value := strings.TrimSpace(parts[1])
				if value == "true" {
					hooksEnabled = true
				} else {
					// Change to true
					lines[i] = parts[0] + "= true"
					hooksEnabled = true
				}
			}
		}
	}

	// If no features section or hooks not set, add/append it
	if !hasFeaturesSection {
		lines = append(lines, "", "[features]", "hooks = true")
	} else if !hooksEnabled {
		// Features section exists but hooks not set, add it
		for i, line := range lines {
			if strings.TrimSpace(line) == "[features]" {
				// Insert hooks = true after [features]
				newLines := make([]string, 0, len(lines)+1)
				newLines = append(newLines, lines[:i+1]...)
				newLines = append(newLines, "hooks = true")
				newLines = append(newLines, lines[i+1:]...)
				lines = newLines
				break
			}
		}
	}

	content := strings.Join(lines, "\n")
	// Ensure file ends with a newline
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return err
	}

	fmt.Printf("  Updated %s (hooks enabled)\n", configPath)
	return nil
}

func setupCodexHooks(hooksPath, guppiBin string, resilient bool) error {
	notifyCmd := fmt.Sprintf("'%s' notify", guppiBin)

	suffix := ""
	if resilient {
		suffix = " || true"
	}

	makeHook := func(cmd string) []map[string]interface{} {
		return []map[string]interface{}{
			{"type": "command", "command": cmd + suffix},
		}
	}

	// Codex hooks use the same format as Claude hooks
	// stdin JSON contains event data (user prompt, tool name, etc.)
	hooks := map[string]interface{}{
		"hooks": map[string]interface{}{
			"UserPromptSubmit": []map[string]interface{}{
				{
					"matcher": "",
					"hooks":   makeHook(notifyCmd + " -t codex -s active -m 'Thinking' --stdin"),
				},
			},
			"PreToolUse": []map[string]interface{}{
				{
					"matcher": "",
					"hooks":   makeHook(notifyCmd + " -t codex --stdin"),
				},
			},
			"PostToolUse": []map[string]interface{}{
				{
					"matcher": "",
					"hooks":   makeHook(notifyCmd + " -t codex --stdin"),
				},
			},
			"PermissionRequest": []map[string]interface{}{
				{
					"matcher": "",
					"hooks":   makeHook(notifyCmd + " -t codex -s waiting -m 'Permission needed'"),
				},
			},
			"Stop": []map[string]interface{}{
				{
					"matcher": "",
					"hooks":   makeHook(notifyCmd + " -t codex -s completed --stdin"),
				},
			},
		},
	}

	out, err := json.MarshalIndent(hooks, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(hooksPath, out, 0o644); err != nil {
		return err
	}

	fmt.Printf("  Wrote hooks to %s\n", hooksPath)
	return nil
}

// setupOpenCode configures OpenCode via native plugin in ~/.config/opencode and any extra dirs
func setupOpenCode(serverURL, guppiBin string, resilient bool, extraDirs []string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dirs := append([]string{filepath.Join(homeDir, ".config", "opencode")}, extraDirs...)
	for _, dir := range dirs {
		if err := setupOpenCodeDir(dir, guppiBin); err != nil {
			return err
		}
	}
	return nil
}

func setupOpenCodeDir(configDir, guppiBin string) error {
	pluginDir := filepath.Join(configDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return err
	}

	indexJS, err := buildOpenCodePlugin(guppiBin)
	if err != nil {
		return err
	}
	pluginFile := filepath.Join(pluginDir, "guppi.js")
	if err := os.WriteFile(pluginFile, []byte(indexJS), 0o644); err != nil {
		return err
	}

	// Clean up the previous non-canonical npm-package install: the
	// node_modules/guppi package plus its file:// entry in opencode.json.
	// OpenCode auto-loads files in plugins/ at startup, so neither is needed.
	legacyPkg := filepath.Join(configDir, "node_modules", "guppi")
	if _, err := os.Stat(legacyPkg); err == nil {
		if err := os.RemoveAll(legacyPkg); err != nil {
			fmt.Printf("  Warning: could not remove legacy plugin package %s: %v\n", legacyPkg, err)
		} else {
			fmt.Printf("  Removed legacy plugin package %s\n", legacyPkg)
		}
	}
	if err := unregisterOpenCodePlugin(filepath.Join(configDir, "opencode.json")); err != nil {
		fmt.Printf("  Warning: could not clean opencode.json: %v\n", err)
	}

	legacyHook := filepath.Join(configDir, "guppi-hook.sh")
	if _, err := os.Stat(legacyHook); err == nil {
		if err := os.Remove(legacyHook); err != nil {
			fmt.Printf("  Warning: could not remove legacy hook %s: %v\n", legacyHook, err)
		} else {
			fmt.Printf("  Removed legacy hook %s\n", legacyHook)
		}
	}

	fmt.Printf("  Wrote OpenCode plugin to %s\n", pluginFile)
	return nil
}

func buildOpenCodePlugin(guppiBin string) (string, error) {
	quotedBin, err := json.Marshal(guppiBin)
	if err != nil {
		return "", err
	}
	// Replace the __GUPPI_BIN__ token (including its surrounding quotes) with the
	// JSON-encoded binary path so the result is a valid JS string literal.
	return strings.ReplaceAll(openCodePluginTemplate, `"__GUPPI_BIN__"`, string(quotedBin)), nil
}

// unregisterOpenCodePlugin removes any guppi entry from opencode.json's plugin
// array, cleaning up the previous file:// node_modules/guppi registration. The
// plugin is now loaded canonically from the plugins/ directory instead.
func unregisterOpenCodePlugin(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}

	raw, ok := config["plugin"]
	if !ok {
		return nil
	}
	plugins, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	isGuppi := func(spec string) bool {
		return spec == "guppi" || strings.Contains(spec, "node_modules/guppi")
	}

	filtered := make([]interface{}, 0, len(plugins))
	for _, entry := range plugins {
		switch v := entry.(type) {
		case string:
			if isGuppi(v) {
				continue
			}
		case []interface{}:
			if len(v) > 0 {
				if spec, ok := v[0].(string); ok && isGuppi(spec) {
					continue
				}
			}
		}
		filtered = append(filtered, entry)
	}

	if len(filtered) == len(plugins) {
		return nil
	}

	if len(filtered) == 0 {
		delete(config, "plugin")
	} else {
		config["plugin"] = filtered
	}

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		return err
	}

	fmt.Printf("  Removed legacy plugin registration from %s\n", configPath)
	return nil
}

// setupPi configures Pi extension in ~/.pi/agent/extensions/ and any extra dirs
func setupPi(serverURL, guppiBin string, resilient bool, extraDirs []string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dirs := append([]string{filepath.Join(homeDir, ".pi", "agent", "extensions")}, extraDirs...)
	for _, dir := range dirs {
		if err := setupPiDir(dir, guppiBin, resilient); err != nil {
			return err
		}
	}

	// Register extension in settings.json
	settingsPath := filepath.Join(homeDir, ".pi", "agent", "settings.json")
	if err := registerPiExtension(settingsPath, "extensions/guppi.ts"); err != nil {
		fmt.Printf("  Warning: could not register extension in settings.json: %v\n", err)
	} else {
		fmt.Printf("  Registered extension in %s\n", settingsPath)
	}

	return nil
}

func setupPiDir(configDir, guppiBin string, resilient bool) error {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}

	// Replace placeholder with actual guppi binary path
	content := strings.ReplaceAll(piExtensionTemplate, "__GUPPI_BIN__", guppiBin)

	pluginFile := filepath.Join(configDir, "guppi.ts")
	if err := os.WriteFile(pluginFile, []byte(content), 0o644); err != nil {
		return err
	}

	fmt.Printf("  Wrote extension to %s\n", pluginFile)
	return nil
}

// registerPiExtension adds an extension path to the packages array in settings.json
func registerPiExtension(settingsPath, extensionPath string) error {
	// Read existing settings
	var settings map[string]interface{}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		settings = make(map[string]interface{})
	} else {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", settingsPath, err)
		}
	}

	// Get or create packages array
	var packages []interface{}
	if pkgRaw, ok := settings["packages"]; ok {
		if pkgSlice, ok := pkgRaw.([]interface{}); ok {
			packages = pkgSlice
		}
	}

	// Check if already registered
	for _, pkg := range packages {
		if pkgStr, ok := pkg.(string); ok && pkgStr == extensionPath {
			return nil // Already registered
		}
	}

	// Add extension
	packages = append(packages, extensionPath)
	settings["packages"] = packages

	// Write back
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(settingsPath, out, 0o644)
}

func init() {
	flags := []cli.Flag{
		&cli.StringFlag{
			Name:    "server",
			Usage:   "guppi server URL",
			Sources: cli.EnvVars("GUPPI_URL"),
			Value:   "http://localhost:7654",
		},
		&cli.BoolFlag{
			Name:  "dry-run",
			Usage: "show what would be configured without making changes",
		},
		&cli.BoolFlag{
			Name:  "block",
			Usage: "allow hook failures to block agents (by default hooks append '|| true' so failures are ignored)",
		},
		&cli.StringSliceFlag{
			Name:  "config-dir",
			Usage: "additional config directory for an agent (format: agent=path, repeatable)",
		},
	}

	cmd := &cli.Command{
		Name:  "agent-setup",
		Usage: "configure AI agent hooks to notify guppi",
		Description: `Detects installed AI coding tools and configures their hooks
to send status notifications to the guppi server.

Supported agents:
  - Claude Code (claude)
  - Codex (codex)
  - OpenCode (opencode)

Use --dry-run to preview changes without writing files.`,
		Flags:  flags,
		Action: Execute,
	}

	common.RegisterCommand(cmd)
}
