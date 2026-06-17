package update

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"github.com/anh-chu/termyard/pkg/common"
)

type Status struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	PendingRestart  bool   `json:"pending_restart,omitempty"`
	Channel         string `json:"channel"`
}

func normalizeVersion(version string) string {
	if version == "" || version == "0.1.1-beta.2" {
		return ""
	}
	return version
}

func currentChannelRelease(ctx context.Context) (current string, ch Channel, rel *release, err error) {
	current = normalizeVersion(common.VERSION)
	ch = detectChannel(common.VERSION)
	rel, err = pickRelease(ctx, defaultRepo, ch, "")
	return
}

func CheckLatest(ctx context.Context) (Status, error) {
	current, ch, rel, err := currentChannelRelease(ctx)
	if err != nil {
		return Status{CurrentVersion: current, Channel: string(ch)}, err
	}
	return Status{
		CurrentVersion:  current,
		LatestVersion:   rel.TagName,
		UpdateAvailable: current == "" || !matches(current, rel.TagName),
		Channel:         string(ch),
	}, nil
}

func Apply(ctx context.Context) (string, error) {
	current, _, rel, err := currentChannelRelease(ctx)
	if err != nil {
		return "", err
	}
	if current != "" && matches(current, rel.TagName) {
		return rel.TagName, nil
	}
	archiveURL, checksumURL, archiveName, _, err := findAsset(rel, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	newVersion, _, _, _, err := applyRelease(ctx, rel, archiveURL, checksumURL, archiveName)
	return newVersion, err
}

func ServiceManaged() bool {
	switch runtime.GOOS {
	case "linux":
		if out, err := exec.Command("systemctl", "--user", "is-active", "termyard.service").Output(); err == nil && strings.TrimSpace(string(out)) == "active" {
			return true
		}
	case "darwin":
		if out, err := exec.Command("launchctl", "list", "com.termyard.server").Output(); err == nil && len(out) > 0 {
			return true
		}
	}
	return false
}

func RestartManaged() error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("sh", "-c", "sleep 1; systemctl --user restart termyard.service")
	case "darwin":
		cmd = exec.Command("sh", "-c", "sleep 1; launchctl kickstart -k gui/$(id -u)/com.termyard.server")
	default:
		return fmt.Errorf("restart not supported on %s", runtime.GOOS)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
