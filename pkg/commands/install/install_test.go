package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRenderUnit_systemdUnit_containsKillModeProcess(t *testing.T) {
	cfg := serviceConfig{
		BinaryPath: "/usr/local/bin/termyard",
		ExecStart:  "/usr/local/bin/termyard server",
		Path:       "/usr/local/bin:/usr/bin",
		TmuxPath:   "/usr/bin/tmux",
	}
	out, err := renderUnit(systemdUnit, cfg)
	if err != nil {
		t.Fatalf("renderUnit(systemdUnit) = error: %v", err)
	}
	if !strings.Contains(out, "KillMode=process") {
		t.Errorf("systemdUnit output missing KillMode=process:\n%s", out)
	}
	if !strings.Contains(out, "ExecStart=/usr/local/bin/termyard server") {
		t.Errorf("systemdUnit output missing ExecStart:\n%s", out)
	}
	if !strings.Contains(out, "[Unit]") {
		t.Errorf("systemdUnit output missing [Unit] section:\n%s", out)
	}
	if !strings.Contains(out, "[Install]") {
		t.Errorf("systemdUnit output missing [Install] section:\n%s", out)
	}
}

func TestRenderUnit_tmuxServerUnit_containsKillModeProcess(t *testing.T) {
	cfg := serviceConfig{
		BinaryPath: "/usr/local/bin/termyard",
		ExecStart:  "/usr/local/bin/termyard server",
		Path:       "/usr/local/bin:/usr/bin",
		TmuxPath:   "/opt/homebrew/bin/tmux",
	}
	out, err := renderUnit(tmuxServerUnit, cfg)
	if err != nil {
		t.Fatalf("renderUnit(tmuxServerUnit) = error: %v", err)
	}
	if !strings.Contains(out, "KillMode=process") {
		t.Errorf("tmuxServerUnit output missing KillMode=process:\n%s", out)
	}
	if !strings.Contains(out, "OOMPolicy=continue") {
		t.Errorf("tmuxServerUnit output missing OOMPolicy=continue:\n%s", out)
	}
	if !strings.Contains(out, "/opt/homebrew/bin/tmux new-session") {
		t.Errorf("tmuxServerUnit output missing correct tmux path:\n%s", out)
	}
}

func TestRenderUnit_templateInjection(t *testing.T) {
	cfg := serviceConfig{
		BinaryPath: "/usr/local/bin/termyard",
		ExecStart:  "/usr/local/bin/termyard server",
		Path:       "/usr/local/bin:/usr/bin",
		TmuxPath:   "/usr/bin/tmux",
	}
	out, err := renderUnit(systemdUnit, cfg)
	if err != nil {
		t.Fatalf("renderUnit = error: %v", err)
	}

	if strings.Contains(out, "{{") {
		t.Errorf("output contains unreplaced template markers:\n%s", out)
	}
}

func TestRenderUnit_emptyTmuxPath(t *testing.T) {
	cfg := serviceConfig{
		BinaryPath: "/usr/local/bin/termyard",
		ExecStart:  "/usr/local/bin/termyard server",
		Path:       "/usr/local/bin:/usr/bin",
		TmuxPath:   "",
	}
	out, err := renderUnit(tmuxServerUnit, cfg)
	if err != nil {
		t.Fatalf("renderUnit = error: %v", err)
	}
	if !strings.Contains(out, " new-session") {
		t.Errorf("tmuxServerUnit should render even with empty TmuxPath:\n%s", out)
	}
}

func TestRenderUnit_invalidTemplate(t *testing.T) {
	_, err := renderUnit("{{.MissingField}}", serviceConfig{})
	if err == nil {
		t.Error("expected error for missing template field, got nil")
	}
}

func TestSystemdUnit_hasNoControlGroupKillMode(t *testing.T) {
	// The whole point: systemdUnit must use KillMode=process, not
	// the default control-group which would reap tmux on restart.
	cfg := testCfg()
	out, err := renderUnit(systemdUnit, cfg)
	if err != nil {
		t.Fatalf("renderUnit = error: %v", err)
	}
	if strings.Contains(out, "KillMode=control-group") {
		t.Errorf("systemdUnit must NOT use KillMode=control-group:\n%s", out)
	}
	if !strings.Contains(out, "KillMode=process") {
		t.Errorf("systemdUnit must use KillMode=process:\n%s", out)
	}
}

func TestSystemdUnit_requiresTmuxBeforeStart(t *testing.T) {
	cfg := testCfg()
	out, err := renderUnit(systemdUnit, cfg)
	if err != nil {
		t.Fatalf("renderUnit = error: %v", err)
	}
	if !strings.Contains(out, "Requires=termyard-tmux.service") {
		t.Errorf("systemdUnit missing Requires=termyard-tmux.service:\n%s", out)
	}
	if !strings.Contains(out, "After=default.target termyard-tmux.service") {
		t.Errorf("systemdUnit missing After directive:\n%s", out)
	}
}

func TestRefreshUnits_rewritesInstalledUnits(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only systemd units")
	}

	configDir := t.TempDir()
	binDir := t.TempDir()
	unitDir := filepath.Join(configDir, "systemd", "user")
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "termyard.service"), []byte("legacy"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "termyard-tmux.service"), []byte("Type=forking"), 0644); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	for name, script := range map[string]string{
		"tmux":      "#!/bin/sh\nexit 0\n",
		"systemctl": "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$TERMYARD_TEST_SYSTEMCTL_LOG\"\n",
	} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("TERMYARD_TEST_SYSTEMCTL_LOG", logPath)

	newBinary := "/opt/termyard/termyard"
	if err := RefreshUnits(t.Context(), newBinary); err != nil {
		t.Fatalf("RefreshUnits() error: %v", err)
	}

	mainUnit, err := os.ReadFile(filepath.Join(unitDir, "termyard.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainUnit), "KillMode=process") {
		t.Errorf("main unit was not refreshed:\n%s", mainUnit)
	}
	if !strings.Contains(string(mainUnit), "ExecStart="+newBinary+" server") {
		t.Errorf("main unit did not use the installed binary:\n%s", mainUnit)
	}
	tmuxUnit, err := os.ReadFile(filepath.Join(unitDir, "termyard-tmux.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tmuxUnit), "Type=oneshot") {
		t.Errorf("tmux unit was not refreshed:\n%s", tmuxUnit)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(log) != "--user daemon-reload\n" {
		t.Errorf("systemctl arguments = %q, want daemon reload", log)
	}
}

func testCfg() serviceConfig {
	return serviceConfig{
		BinaryPath: "/usr/local/bin/termyard",
		ExecStart:  "/usr/local/bin/termyard server",
		Path:       "/usr/local/bin:/usr/bin:/bin",
		TmuxPath:   "/usr/bin/tmux",
	}
}
