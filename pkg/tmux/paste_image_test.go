package tmux

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorePastedImage(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	path, err := StorePastedImage(base64.StdEncoding.EncodeToString([]byte("png-bytes")), "image/png", "clipboard.png")
	if err != nil {
		t.Fatalf("StorePastedImage returned error: %v", err)
	}
	if filepath.Ext(path) != ".png" {
		t.Fatalf("expected .png extension, got %q", filepath.Ext(path))
	}
	if parent := filepath.Base(filepath.Dir(path)); !strings.HasPrefix(parent, pasteDirectoryPrefix) || !strings.HasPrefix(filepath.Base(path), pasteFilePrefix) {
		t.Fatalf("expected unique server-owned paste path, got %q", path)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(got) != "png-bytes" {
		t.Fatalf("expected written bytes to round-trip, got %q", string(got))
	}
}

func TestStorePastedImageRejectsInvalidMime(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	_, err := StorePastedImage(base64.StdEncoding.EncodeToString([]byte("not-an-image")), "text/plain", "notes.txt")
	if err == nil {
		t.Fatal("expected unsupported MIME type error")
	}
}

func TestStorePastedImageRejectsInvalidBase64(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	_, err := StorePastedImage("%%%not-base64%%%", "image/png", "broken.png")
	if err == nil {
		t.Fatal("expected invalid base64 error")
	}
}

func TestStorePastedFile(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	path, err := StorePastedFile(base64.StdEncoding.EncodeToString([]byte("document bytes")), "text/plain", "report.TXT")
	if err != nil {
		t.Fatalf("StorePastedFile returned error: %v", err)
	}
	if filepath.Ext(path) != ".txt" {
		t.Fatalf("expected .txt extension, got %q", filepath.Ext(path))
	}
	if base := filepath.Base(path); !strings.HasPrefix(base, pasteFilePrefix) || strings.Contains(base, "report") {
		t.Fatalf("expected server-owned pasted filename, got %q", base)
	}
	if parent := filepath.Base(filepath.Dir(path)); !strings.HasPrefix(parent, pasteDirectoryPrefix) {
		t.Fatalf("expected unique server-owned paste directory, got %q", parent)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(got) != "document bytes" {
		t.Fatalf("expected written bytes to round-trip, got %q", string(got))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected file mode 0600, got %o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat directory: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("expected directory mode 0700, got %o", dirInfo.Mode().Perm())
	}
}

func TestStorePastedFileAcceptsSizeLimit(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	contents := make([]byte, maxPastedImageBytes)
	contents[len(contents)-1] = 1
	encoded := base64.StdEncoding.EncodeToString(contents)
	encoded = encoded[:64] + "\r\n" + encoded[64:]
	path, err := StorePastedFile(encoded, "application/octet-stream", "archive.bin")
	if err != nil {
		t.Fatalf("StorePastedFile returned error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != maxPastedImageBytes {
		t.Fatalf("expected %d byte file, got %d", maxPastedImageBytes, info.Size())
	}
}

func TestStorePastedFileCreatesSeparateDirectories(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	data := base64.StdEncoding.EncodeToString([]byte("x"))
	first, err := StorePastedFile(data, "text/plain", "first.txt")
	if err != nil {
		t.Fatal(err)
	}
	second, err := StorePastedFile(data, "text/plain", "second.txt")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) == filepath.Dir(second) {
		t.Fatalf("pastes shared directory %q", filepath.Dir(first))
	}
}

func TestStorePastedFileCleansStaleDirectories(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	stale, err := StorePastedFile(base64.StdEncoding.EncodeToString([]byte("old")), "text/plain", "old.txt")
	if err != nil {
		t.Fatal(err)
	}
	staleDir := filepath.Dir(stale)
	old := time.Now().Add(-pastedDataMaxAge - time.Minute)
	if err := os.Chtimes(staleDir, old, old); err != nil {
		t.Fatal(err)
	}

	fresh, err := StorePastedFile(base64.StdEncoding.EncodeToString([]byte("new")), "text/plain", "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Fatalf("stale paste directory still exists: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh paste file missing: %v", err)
	}
}

func TestCleanupStalePastesSkipsUnexpectedDirectoryContents(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	dir, err := os.MkdirTemp(os.TempDir(), pasteDirectoryPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, pasteMarkerName), []byte(pasteMarkerContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unexpected"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-pastedDataMaxAge - time.Minute)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}

	cleanupStalePastes(time.Now())
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cleanup removed directory with unexpected contents: %v", err)
	}

	unmarked, err := os.MkdirTemp(os.TempDir(), pasteDirectoryPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unmarked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unmarked, "pasted-rogue"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(unmarked, old, old); err != nil {
		t.Fatal(err)
	}

	cleanupStalePastes(time.Now())
	if _, err := os.Stat(unmarked); err != nil {
		t.Fatalf("cleanup removed unmarked directory: %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	if got, want := shellQuote("/tmp/it's unsafe"), "'/tmp/it'\"'\"'s unsafe'"; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestStorePastedFileRejectsInvalidInput(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	tests := []struct {
		name string
		data string
	}{
		{name: "empty data"},
		{name: "malformed base64", data: "%%%not-base64%%%"},
		{name: "oversize", data: strings.Repeat("A", base64.StdEncoding.EncodedLen(maxPastedImageBytes)+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := StorePastedFile(test.data, "application/octet-stream", "notes.txt"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestStorePastedFileDropsUnsafeExtension(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	data := base64.StdEncoding.EncodeToString([]byte("file bytes"))
	for _, filename := range []string{"notes.$(id)", "notes.日本", "notes."} {
		t.Run(filename, func(t *testing.T) {
			path, err := StorePastedFile(data, "application/octet-stream", filename)
			if err != nil {
				t.Fatalf("StorePastedFile returned error: %v", err)
			}
			if ext := filepath.Ext(path); ext != "" {
				t.Fatalf("expected dropped extension, got %q", ext)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != "file bytes" {
				t.Fatalf("contents = %q", contents)
			}
		})
	}
}
