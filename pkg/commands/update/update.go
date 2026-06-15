// Package update implements `termyard update`: in-place self-update from
// GitHub Releases. Channel-aware (stable / nightly), arch-aware, and
// binary-path aware (resolves symlinks, swaps atomically).
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"

	"github.com/anh-chu/termyard/pkg/common"
)

const defaultRepo = "anh-chu/termyard"

// Channel is the release stream to track.
type Channel string

const (
	ChannelStable  Channel = "stable"
	ChannelNightly Channel = "nightly"
)

// release mirrors the fields we use from the GitHub Releases API.
type release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// detectChannel infers the current channel from the running binary version.
// Nightly tags follow `v0.0.0-nightly.<ts>.<sha>` (see .github/workflows/nightly.yml).
func detectChannel(currentVersion string) Channel {
	if strings.Contains(currentVersion, "nightly") {
		return ChannelNightly
	}
	return ChannelStable
}

// resolveBinaryPath returns the canonical path of the currently-running
// executable, resolving any symlinks. We update the underlying file, not the
// symlink target name.
func resolveBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate self: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil // fall back to the unresolved path
	}
	return real, nil
}

// pickRelease fetches the release for the requested channel or pinned tag.
func pickRelease(ctx context.Context, repo string, ch Channel, pinnedTag string) (*release, error) {
	api := "https://api.github.com/repos/" + repo
	client := &http.Client{Timeout: 20 * time.Second}

	doGet := func(url string, out interface{}) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "termyard-update/"+common.VERSION)
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("github api: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("not found: %s", url)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("github api: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}

	if pinnedTag != "" {
		var rel release
		if err := doGet(api+"/releases/tags/"+pinnedTag, &rel); err != nil {
			return nil, err
		}
		return &rel, nil
	}

	if ch == ChannelStable {
		var rel release
		if err := doGet(api+"/releases/latest", &rel); err != nil {
			return nil, err
		}
		return &rel, nil
	}

	// Nightly: scan recent releases for the first prerelease.
	var releases []release
	if err := doGet(api+"/releases?per_page=20", &releases); err != nil {
		return nil, err
	}
	for i := range releases {
		r := &releases[i]
		if r.Draft {
			continue
		}
		if r.Prerelease {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no nightly prerelease found in last 20 releases")
}

// expectedAssetName matches the goreleaser archives.name_template:
// "{{ .ProjectName }}-v{{ .Version }}-{{ .Os }}-{{ .Arch }}{{ .Arm }}".
// The tag already has a leading 'v', and Version has it stripped, so we
// just substitute the bare tag back into the same shape.
func expectedAssetName(tag, goos, goarch string) string {
	v := strings.TrimPrefix(tag, "v")
	return fmt.Sprintf("termyard-v%s-%s-%s.tar.gz", v, goos, goarch)
}

// findAsset returns the URL + size of the archive matching this host's
// OS/arch, plus the checksums.txt URL.
func findAsset(rel *release, goos, goarch string) (archiveURL, checksumURL, archiveName string, archiveSize int64, err error) {
	wantArchive := expectedAssetName(rel.TagName, goos, goarch)
	for _, a := range rel.Assets {
		switch a.Name {
		case wantArchive:
			archiveURL = a.BrowserDownloadURL
			archiveName = a.Name
			archiveSize = a.Size
		case "checksums.txt":
			checksumURL = a.BrowserDownloadURL
		}
	}
	if archiveURL == "" {
		return "", "", "", 0, fmt.Errorf("no asset %q in release %s", wantArchive, rel.TagName)
	}
	if checksumURL == "" {
		return "", "", "", 0, fmt.Errorf("no checksums.txt in release %s", rel.TagName)
	}
	return
}

// downloadToTemp streams url to a temp file under dir and returns its path
// and SHA256 hex digest. Caller is responsible for removing the file.
func downloadToTemp(ctx context.Context, url, dir, prefix string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "termyard-update/"+common.VERSION)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	tmp, err := os.CreateTemp(dir, prefix+"-*")
	if err != nil {
		return "", "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", "", err
	}
	return tmp.Name(), hex.EncodeToString(hash.Sum(nil)), nil
}

// expectedChecksum scans a `checksums.txt` body (sha256sum-style, two columns)
// for the line matching archiveName and returns its hex digest.
func expectedChecksum(checksumsBody, archiveName string) (string, error) {
	for _, line := range strings.Split(checksumsBody, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == archiveName {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksum for %q not found", archiveName)
}

// extractTermyardBinary pulls the `termyard` (or `termyard.exe`) file out of the
// tarball at archivePath and writes it to outPath. Returns the bytes written.
func extractTermyardBinary(archivePath, outPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		base := filepath.Base(h.Name)
		if base != "termyard" && base != "termyard.exe" {
			continue
		}
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("termyard binary not found in archive")
}

// suggestRestart inspects the OS for a known service manager and prints a
// hint after a successful update. Best-effort, never fatal.
func suggestRestart(log *logrus.Entry) {
	switch runtime.GOOS {
	case "linux":
		if out, err := exec.Command("systemctl", "--user", "is-active", "termyard.service").Output(); err == nil && strings.TrimSpace(string(out)) == "active" {
			log.Info("restart with: systemctl --user restart termyard")
			return
		}
	case "darwin":
		if out, err := exec.Command("launchctl", "list", "com.termyard.server").Output(); err == nil && len(out) > 0 {
			log.Info("restart with: launchctl kickstart -k gui/$(id -u)/com.termyard.server")
			return
		}
	}
	log.Info("if termyard is currently running, restart it for the update to take effect")
}

// run performs the update flow. dryRun=true means: check + report only.
func run(ctx context.Context, repo string, ch Channel, pinnedTag string, dryRun, force bool) error {
	log := logrus.WithField("component", "update")

	current := common.VERSION
	if current == "" || current == "0.1.1-beta.2" {
		// `0.1.1-beta.2` is the default in pkg/common when ldflags didn't run
		// (e.g. `go run .`). Treat as "unknown".
		current = ""
	}

	log.WithFields(logrus.Fields{
		"current": current,
		"channel": ch,
		"repo":    repo,
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
	}).Info("checking for updates")

	rel, err := pickRelease(ctx, repo, ch, pinnedTag)
	if err != nil {
		return err
	}
	log.WithFields(logrus.Fields{
		"tag":        rel.TagName,
		"prerelease": rel.Prerelease,
		"published":  rel.PublishedAt.Format(time.RFC3339),
	}).Info("found release")

	if !force && current != "" {
		if matches(current, rel.TagName) {
			log.Info("already up to date")
			return nil
		}
	}

	archiveURL, checksumURL, archiveName, archiveSize, err := findAsset(rel, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	log.WithFields(logrus.Fields{
		"asset": archiveName,
		"size":  archiveSize,
	}).Info("downloading")

	if dryRun {
		fmt.Printf("update available: %s -> %s\n", current, rel.TagName)
		fmt.Printf("would download: %s (%d bytes)\n", archiveName, archiveSize)
		return nil
	}

	binPath, err := resolveBinaryPath()
	if err != nil {
		return err
	}
	binDir := filepath.Dir(binPath)
	log.WithField("path", binPath).Info("target binary")

	// Download checksums first, then archive.
	tmpDir, err := os.MkdirTemp("", "termyard-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	checksumsPath, _, err := downloadToTemp(ctx, checksumURL, tmpDir, "checksums")
	if err != nil {
		return err
	}
	checksumsBody, err := os.ReadFile(checksumsPath)
	if err != nil {
		return err
	}
	wantSum, err := expectedChecksum(string(checksumsBody), archiveName)
	if err != nil {
		return err
	}

	archivePath, gotSum, err := downloadToTemp(ctx, archiveURL, tmpDir, "archive")
	if err != nil {
		return err
	}
	if !strings.EqualFold(gotSum, wantSum) {
		return fmt.Errorf("checksum mismatch: want %s got %s", wantSum, gotSum)
	}
	log.Info("checksum verified")

	// Extract directly into a sibling of the target so the final rename is
	// atomic (same filesystem).
	newPath := binPath + ".new"
	if err := extractTermyardBinary(archivePath, newPath); err != nil {
		return err
	}

	// Preserve perms of the existing binary if it exists.
	if info, err := os.Stat(binPath); err == nil {
		_ = os.Chmod(newPath, info.Mode().Perm())
	}

	// Backup the old binary, then swap. We keep the backup in the same dir so
	// the user can roll back manually if they hit issues.
	backupPath := binPath + ".bak"
	_ = os.Remove(backupPath)
	if err := os.Rename(binPath, backupPath); err != nil {
		// Old binary couldn't be moved (e.g. running with locked text segment
		// on macOS). On most unixes the running file can be renamed away. If
		// we hit this, surface the error so the user can rerun with sudo.
		os.Remove(newPath)
		return fmt.Errorf("move old binary aside: %w (you may need sudo, or your filesystem is read-only)", err)
	}
	if err := os.Rename(newPath, binPath); err != nil {
		// Try to restore the backup before bailing.
		_ = os.Rename(backupPath, binPath)
		return fmt.Errorf("install new binary: %w", err)
	}

	log.WithFields(logrus.Fields{
		"from":   current,
		"to":     rel.TagName,
		"binary": binPath,
		"backup": backupPath,
		"dir":    binDir,
	}).Info("update installed")

	suggestRestart(log)
	return nil
}

// matches reports whether the running binary version is the same as the
// release tag. Handles "v" prefix differences and the ldflags-injected forms.
func matches(currentVersion, tag string) bool {
	a := strings.TrimPrefix(currentVersion, "v")
	b := strings.TrimPrefix(tag, "v")
	return a == b
}

func init() {
	cmd := &cli.Command{
		Name:  "update",
		Usage: "update termyard to the latest release",
		Description: `Checks GitHub Releases for a newer build of termyard targeting this
machine's OS/arch and swaps the running binary in place.

Channels:
  stable   /releases/latest (default for stable installs)
  nightly  most recent prerelease (default for nightly installs)

The current channel is auto-detected from the running version (anything
containing "nightly" defaults to the nightly channel).`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "channel",
				Usage:   "release channel: stable or nightly (default: auto-detect from running version)",
				Sources: cli.EnvVars("TERMYARD_UPDATE_CHANNEL"),
			},
			&cli.StringFlag{
				Name:  "version",
				Usage: "pin to a specific release tag (e.g. v0.2.0-beta.3); overrides --channel",
			},
			&cli.StringFlag{
				Name:    "repo",
				Usage:   "GitHub repo to pull releases from",
				Sources: cli.EnvVars("TERMYARD_UPDATE_REPO"),
				Value:   defaultRepo,
			},
			&cli.BoolFlag{
				Name:  "check",
				Usage: "check for an update without installing",
			},
			&cli.BoolFlag{
				Name:  "force",
				Usage: "reinstall even if the running version already matches the latest",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			repo := c.String("repo")
			pinned := c.String("version")
			channel := Channel(strings.ToLower(c.String("channel")))
			if pinned == "" && channel == "" {
				channel = detectChannel(common.VERSION)
			}
			switch channel {
			case ChannelStable, ChannelNightly, "":
			default:
				return fmt.Errorf("invalid channel %q (use stable or nightly)", channel)
			}
			return run(ctx, repo, channel, pinned, c.Bool("check"), c.Bool("force"))
		},
	}
	common.RegisterCommand(cmd)
}
