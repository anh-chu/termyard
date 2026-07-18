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

func TestRenderUnit_templateInjection(t *testing.T) {
	cfg := serviceConfig{
		BinaryPath: "/usr/local/bin/termyard",
		ExecStart:  "/usr/local/bin/termyard server",
		Path:       "/usr/local/bin:/usr/bin",
	}
	out, err := renderUnit(systemdUnit, cfg)
	if err != nil {
		t.Fatalf("renderUnit = error: %v", err)
	}

	if strings.Contains(out, "{{") {
		t.Errorf("output contains unreplaced template markers:\n%s", out)
	}
}

func TestSystemdUnit_hasNoControlGroupKillMode(t *testing.T) {
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

func TestSystemdUnit_noTmuxDependency(t *testing.T) {
	cfg := testCfg()
	out, err := renderUnit(systemdUnit, cfg)
	if err != nil {
		t.Fatalf("renderUnit = error: %v", err)
	}
	if strings.Contains(out, "tmux") {
		t.Errorf("systemdUnit must NOT reference tmux:\n%s", out)
	}
}

func TestRefreshUnits_rewritesInstalledUnits(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only systemd units")
	}

	configDir := t.TempDir()
	unitDir := filepath.Join(configDir, "systemd", "user")
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "termyard.service"), []byte("legacy"), 0644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TERMYARD_TEST_SYSTEMCTL_LOG\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
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
	if strings.Contains(string(mainUnit), "tmux") {
		t.Errorf("main unit should not reference tmux:\n%s", mainUnit)
	}
}

func testCfg() serviceConfig {
	return serviceConfig{
		BinaryPath: "/usr/local/bin/termyard",
		ExecStart:  "/usr/local/bin/termyard server",
		Path:       "/usr/local/bin:/usr/bin:/bin",
	}
}
