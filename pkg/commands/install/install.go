package install

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"text/template"

	"github.com/urfave/cli/v3"

	"github.com/anh-chu/termyard/pkg/common"
)

const systemdUnit = `[Unit]
Description=Termyard - Web dashboard for tmux sessions
Requires=termyard-tmux.service
After=default.target termyard-tmux.service

[Service]
Type=simple
ExecStart={{.ExecStart}}
Restart=on-failure
RestartSec=5
KillMode=process
OOMScoreAdjust=-600
Environment=PATH={{.Path}}

[Install]
WantedBy=default.target
`

const tmuxServerUnit = `[Unit]
Description=Termyard tmux server
After=default.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c '{{.TmuxPath}} new-session -d -s _yardkeep; {{.TmuxPath}} set-option -g exit-empty off; {{.TmuxPath}} kill-session -t _yardkeep'
KillMode=process
OOMScoreAdjust=-800
OOMPolicy=continue

[Install]
WantedBy=default.target
`

const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.termyard.server</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.BinaryPath}}</string>
		<string>server</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogDir}}/termyard.stdout.log</string>
	<key>StandardErrorPath</key>
	<string>{{.LogDir}}/termyard.stderr.log</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>{{.Path}}</string>
	</dict>
</dict>
</plist>
`

type serviceConfig struct {
	BinaryPath string
	ExecStart  string
	Path       string
	LogDir     string
	TmuxPath   string
}

func getBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not determine executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("could not resolve symlinks: %w", err)
	}
	return exe, nil
}

// resolveUnitDir returns the systemd user unit directory.
func resolveUnitDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "systemd", "user"), nil
}

// buildServiceConfig creates a serviceConfig from the current environment.
func buildServiceConfig(binPath string) (serviceConfig, error) {
	if binPath == "" {
		var err error
		binPath, err = getBinaryPath()
		if err != nil {
			return serviceConfig{}, err
		}
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return serviceConfig{}, fmt.Errorf("could not find tmux: %w", err)
	}
	return serviceConfig{
		BinaryPath: binPath,
		ExecStart:  binPath + " server",
		Path:       os.Getenv("PATH"),
		TmuxPath:   tmuxPath,
	}, nil
}

// renderUnit renders a systemd unit template with the given config.
func renderUnit(tmplSrc string, cfg serviceConfig) (string, error) {
	tmpl, err := template.New("unit").Parse(tmplSrc)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// writeRenderedUnit renders a template and writes it to path.
func writeRenderedUnit(path, tmplSrc string, cfg serviceConfig) error {
	content, err := renderUnit(tmplSrc, cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func installLinux(ctx context.Context, c *cli.Command) error {
	cfg, err := buildServiceConfig("")
	if err != nil {
		return err
	}

	unitDir, err := resolveUnitDir()
	if err != nil {
		return fmt.Errorf("could not determine config directory: %w", err)
	}

	unitPath := filepath.Join(unitDir, "termyard.service")
	tmuxUnitPath := filepath.Join(unitDir, "termyard-tmux.service")

	if err := os.MkdirAll(unitDir, 0755); err != nil {
		return fmt.Errorf("could not create systemd user directory: %w", err)
	}

	if err := writeRenderedUnit(tmuxUnitPath, tmuxServerUnit, cfg); err != nil {
		return fmt.Errorf("could not write tmux unit: %w", err)
	}
	fmt.Printf("Wrote %s\n", tmuxUnitPath)

	if err := writeRenderedUnit(unitPath, systemdUnit, cfg); err != nil {
		return fmt.Errorf("could not write unit file: %w", err)
	}
	fmt.Printf("Wrote %s\n", unitPath)

	// Reload and enable
	if err := exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w", err)
	}

	if err := exec.CommandContext(ctx, "systemctl", "--user", "enable", "--now", "termyard-tmux.service").Run(); err != nil {
		return fmt.Errorf("systemctl enable tmux failed: %w", err)
	}
	if err := exec.CommandContext(ctx, "systemctl", "--user", "enable", "--now", "termyard.service").Run(); err != nil {
		return fmt.Errorf("systemctl enable failed: %w", err)
	}

	fmt.Println("Service enabled and started (systemctl --user)")
	fmt.Println()
	fmt.Println("  Status:   systemctl --user status termyard termyard-tmux")
	fmt.Println("  Logs:     journalctl --user -u termyard -f")
	fmt.Println("  Restart:  systemctl --user restart termyard")
	fmt.Println("  Web UI:   https://localhost:7654")
	return nil
}

// RefreshUnits rewrites both systemd user units and daemon-reloads,
// but does NOT restart any services. Returns nil (not an error) when
// termyard.service is not installed or on non-Linux platforms.
func RefreshUnits(ctx context.Context, binPath string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	unitDir, err := resolveUnitDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(unitDir, "termyard.service")
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return nil // not installed
	}
	cfg, err := buildServiceConfig(binPath)
	if err != nil {
		return fmt.Errorf("refresh: %w", err)
	}
	tmuxUnitPath := filepath.Join(unitDir, "termyard-tmux.service")

	if err := writeRenderedUnit(tmuxUnitPath, tmuxServerUnit, cfg); err != nil {
		return fmt.Errorf("refresh: write tmux unit: %w", err)
	}
	if err := writeRenderedUnit(unitPath, systemdUnit, cfg); err != nil {
		return fmt.Errorf("refresh: write main unit: %w", err)
	}

	return exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()
}

func installDarwin(ctx context.Context, c *cli.Command) error {
	binPath, err := getBinaryPath()
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %w", err)
	}

	agentDir := filepath.Join(home, "Library", "LaunchAgents")
	plistPath := filepath.Join(agentDir, "com.termyard.server.plist")
	logDir := filepath.Join(home, "Library", "Logs")

	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("could not create LaunchAgents directory: %w", err)
	}

	cfg := serviceConfig{
		BinaryPath: binPath,
		Path:       os.Getenv("PATH"),
		LogDir:     logDir,
	}

	tmpl, err := template.New("launchd").Parse(launchdPlist)
	if err != nil {
		return fmt.Errorf("could not parse plist template: %w", err)
	}

	f, err := os.Create(plistPath)
	if err != nil {
		return fmt.Errorf("could not create plist file: %w", err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, cfg); err != nil {
		return fmt.Errorf("could not write plist file: %w", err)
	}

	fmt.Printf("Wrote %s\n", plistPath)

	// Load the agent
	if err := exec.CommandContext(ctx, "launchctl", "load", "-w", plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %w", err)
	}

	fmt.Println("Service loaded and started (launchctl)")
	fmt.Println()
	fmt.Println("  Status:   launchctl list com.termyard.server")
	fmt.Printf("  Logs:     tail -f %s/termyard.stderr.log\n", cfg.LogDir)
	fmt.Printf("  Restart:  launchctl kickstart -k gui/$(id -u)/com.termyard.server\n")
	fmt.Println("  Web UI:   https://localhost:7654")
	return nil
}

func installExecute(ctx context.Context, c *cli.Command) error {
	switch runtime.GOOS {
	case "linux":
		return installLinux(ctx, c)
	case "darwin":
		return installDarwin(ctx, c)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func uninstallLinux(ctx context.Context, c *cli.Command) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("could not determine config directory: %w", err)
	}

	unitDir := filepath.Join(configDir, "systemd", "user")
	unitPath := filepath.Join(unitDir, "termyard.service")
	tmuxUnitPath := filepath.Join(unitDir, "termyard-tmux.service")

	// Disable and stop
	_ = exec.CommandContext(ctx, "systemctl", "--user", "disable", "--now", "termyard.service").Run()
	_ = exec.CommandContext(ctx, "systemctl", "--user", "disable", "--now", "termyard-tmux.service").Run()

	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not remove unit file: %w", err)
	}
	if err := os.Remove(tmuxUnitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not remove tmux unit file: %w", err)
	}

	_ = exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()

	fmt.Printf("Removed %s\n", unitPath)
	fmt.Printf("Removed %s\n", tmuxUnitPath)
	fmt.Println("Service disabled and stopped")
	return nil
}

func uninstallDarwin(ctx context.Context, c *cli.Command) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %w", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.termyard.server.plist")

	// Unload the agent
	_ = exec.CommandContext(ctx, "launchctl", "unload", "-w", plistPath).Run()

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not remove plist file: %w", err)
	}

	fmt.Printf("Removed %s\n", plistPath)
	fmt.Println("Service unloaded and stopped")
	return nil
}

func uninstallExecute(ctx context.Context, c *cli.Command) error {
	switch runtime.GOOS {
	case "linux":
		return uninstallLinux(ctx, c)
	case "darwin":
		return uninstallDarwin(ctx, c)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func init() {
	cmd := &cli.Command{
		Name:  "install",
		Usage: "install termyard as a user service for auto-start",
		Description: `Install termyard to start automatically on login.

On Linux, installs a systemd user unit (~/.config/systemd/user/termyard.service).
On macOS, installs a launchd plist (~/Library/LaunchAgents/com.termyard.server.plist).

Use "termyard install" to install and enable, "termyard uninstall" to remove.`,
		Action: installExecute,
	}

	uninstallCmd := &cli.Command{
		Name:  "uninstall",
		Usage: "remove termyard user service",
		Description: `Remove the termyard auto-start service.

On Linux, disables and removes the systemd user unit.
On macOS, unloads and removes the launchd plist.`,
		Action: uninstallExecute,
	}

	common.RegisterCommand(cmd)
	common.RegisterCommand(uninstallCmd)
}
