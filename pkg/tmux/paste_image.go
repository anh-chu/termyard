package tmux

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxPastedImageBytes = 10 << 20

	// MaxPTYControlMessageBytes permits a 10 MiB raw file after base64 encoding
	// plus JSON metadata, while bounding WebSocket memory use.
	MaxPTYControlMessageBytes int64 = 14 << 20

	pasteDirectoryPrefix = "termyard-paste-"
	pasteFilePrefix      = "pasted-"
	pasteMarkerName      = ".termyard-paste"
	pasteMarkerContents  = "termyard-paste-v1\n"
	pastedDataMaxAge     = 24 * time.Hour
)

var (
	pasteStoreMu      sync.Mutex
	pasteCleanupTimer *time.Timer
)

type PTYControlMessage struct {
	Type     string `json:"type"`
	Cols     uint16 `json:"cols,omitempty"`
	Rows     uint16 `json:"rows,omitempty"`
	Data     string `json:"data,omitempty"`
	Mime     string `json:"mime,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// HandlePTYControlMessage applies a websocket control message to a PTY-backed tmux session.
func HandlePTYControlMessage(ptySess *PTYSession, raw []byte) error {
	var msg PTYControlMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return err
	}

	switch msg.Type {
	case "resize":
		if msg.Cols == 0 || msg.Rows == 0 {
			return nil
		}
		return ptySess.Resize(msg.Cols, msg.Rows)
	case "paste-image":
		path, err := StorePastedImage(msg.Data, msg.Mime, msg.Filename)
		if err != nil {
			return err
		}
		_, err = ptySess.Write([]byte(shellQuote(path)))
		return err
	case "paste-file":
		path, err := StorePastedFile(msg.Data, msg.Mime, msg.Filename)
		if err != nil {
			return err
		}
		_, err = ptySess.Write([]byte(shellQuote(path)))
		return err
	default:
		return fmt.Errorf("unknown PTY control message type %q", msg.Type)
	}
}

func StorePastedImage(data, mimeType, filename string) (string, error) {
	if mimeType != "" && !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return "", fmt.Errorf("unsupported pasted image MIME type %q", mimeType)
	}

	imageBytes, err := decodePastedBytes(data, "image")
	if err != nil {
		return "", err
	}
	return storePastedBytes(imageBytes, pastedImageExt(mimeType, filename))
}

// StorePastedFile stores arbitrary base64-encoded file content using a
// server-owned filename. MIME type is informational; unlike paste-image it is
// intentionally unrestricted.
func StorePastedFile(data, _ string, filename string) (string, error) {
	ext := pastedFileExt(filename)
	fileBytes, err := decodePastedBytes(data, "file")
	if err != nil {
		return "", err
	}
	return storePastedBytes(fileBytes, ext)
}

func decodePastedBytes(data, kind string) ([]byte, error) {
	if strings.TrimSpace(data) == "" {
		return nil, fmt.Errorf("missing pasted %s data", kind)
	}
	encodedLen := len(data) - strings.Count(data, "\r") - strings.Count(data, "\n")
	if encodedLen > base64.StdEncoding.EncodedLen(maxPastedImageBytes) {
		return nil, fmt.Errorf("pasted %s exceeds %d byte limit", kind, maxPastedImageBytes)
	}

	contents, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("decode pasted %s: %w", kind, err)
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("empty pasted %s payload", kind)
	}
	if len(contents) > maxPastedImageBytes {
		return nil, fmt.Errorf("pasted %s exceeds %d byte limit", kind, maxPastedImageBytes)
	}
	return contents, nil
}

func storePastedBytes(contents []byte, extension string) (string, error) {
	pasteStoreMu.Lock()
	defer pasteStoreMu.Unlock()

	cleanupStalePastes(time.Now())

	dir, err := os.MkdirTemp(os.TempDir(), pasteDirectoryPrefix)
	if err != nil {
		return "", fmt.Errorf("create paste directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("secure paste directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pasteMarkerName), []byte(pasteMarkerContents), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("mark paste directory: %w", err)
	}

	file, err := os.CreateTemp(dir, pasteFilePrefix+"*"+extension)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("create pasted file: %w", err)
	}
	path := file.Name()
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = os.Remove(path)
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("secure pasted file: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = os.Remove(path)
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write pasted file: %w", err)
	}
	if err := file.Close(); err != nil {
		file = nil
		_ = os.Remove(path)
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("close pasted file: %w", err)
	}
	file = nil
	schedulePastedCleanup()
	return path, nil
}

// schedulePastedCleanup keeps one delayed cleanup for an idle server. Caller holds pasteStoreMu.
func schedulePastedCleanup() {
	if pasteCleanupTimer != nil {
		pasteCleanupTimer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(pastedDataMaxAge+time.Minute, func() {
		pasteStoreMu.Lock()
		defer pasteStoreMu.Unlock()
		if pasteCleanupTimer != timer {
			return
		}
		cleanupStalePastes(time.Now())
		pasteCleanupTimer = nil
	})
	pasteCleanupTimer = timer
}

// cleanupStalePastes removes only old, complete paste directories. Caller must
// hold pasteStoreMu, which keeps an in-process write from becoming a cleanup target.
func cleanupStalePastes(now time.Time) {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}

	cutoff := now.Add(-pastedDataMaxAge)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), pasteDirectoryPrefix) {
			continue
		}
		path := filepath.Join(os.TempDir(), entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !info.ModTime().Before(cutoff) {
			continue
		}
		files, ok := pastedDirectoryFiles(path)
		if !ok {
			continue
		}
		removePastedDirectory(path, files)
	}
}

// pastedDirectoryFiles verifies the complete, server-owned layout before cleanup.
func pastedDirectoryFiles(dir string) ([]string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}

	files := make([]string, 0, 2)
	markerFound := false
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, false
		}
		switch entry.Name() {
		case pasteMarkerName:
			if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				return nil, false
			}
			markerFound = true
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != pasteMarkerContents {
				return nil, false
			}
		default:
			if !strings.HasPrefix(entry.Name(), pasteFilePrefix) || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				return nil, false
			}
			files = append(files, entry.Name())
		}
	}
	if !markerFound || len(files) != 1 {
		return nil, false
	}
	return files, true
}

// removePastedDirectory never recurses, so an unexpected entry leaves the directory intact.
func removePastedDirectory(dir string, files []string) {
	for _, name := range files {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return
		}
	}
	if err := os.Remove(filepath.Join(dir, pasteMarkerName)); err != nil {
		return
	}
	_ = os.Remove(dir)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func pastedFileExt(filename string) string {
	ext := filepath.Ext(filename)
	if len(ext) < 2 || len(ext) > 33 {
		return ""
	}
	for _, char := range ext[1:] {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')) {
			return ""
		}
	}
	return strings.ToLower(ext)
}

func pastedImageExt(mimeType, filename string) string {
	switch strings.ToLower(mimeType) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/heic":
		return ".heic"
	case "image/heif":
		return ".heif"
	}

	switch ext := strings.ToLower(filepath.Ext(filename)); ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".heic", ".heif":
		if ext == ".jpeg" {
			return ".jpg"
		}
		return ext
	}

	return ".png"
}
