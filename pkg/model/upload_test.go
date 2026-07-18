package model

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreUploadedFileRoundTrip(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	path, err := StoreUploadedFile(strings.NewReader("hello"), "notes.txt")
	if err != nil {
		t.Fatalf("StoreUploadedFile returned error: %v", err)
	}

	// File exists with correct content.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("contents = %q, want %q", string(got), "hello")
	}

	// Path under termyard-paste-*/pasted-*.txt
	if parent := filepath.Base(filepath.Dir(path)); !strings.HasPrefix(parent, pasteDirectoryPrefix) || !strings.HasPrefix(filepath.Base(path), pasteFilePrefix) {
		t.Fatalf("expected unique server-owned paste path, got %q", path)
	}
	if filepath.Ext(path) != ".txt" {
		t.Fatalf("expected .txt extension, got %q", filepath.Ext(path))
	}

	// Directory mode 0700
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat directory: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("expected directory mode 0700, got %o", dirInfo.Mode().Perm())
	}

	// File mode 0600
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("expected file mode 0600, got %o", fileInfo.Mode().Perm())
	}

	// Marker present with correct contents
	markerPath := filepath.Join(filepath.Dir(path), pasteMarkerName)
	markerData, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("ReadFile marker: %v", err)
	}
	if string(markerData) != pasteMarkerContents {
		t.Fatalf("marker contents = %q, want %q", string(markerData), pasteMarkerContents)
	}
}

func TestStoreUploadedFileNoSizeCap(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	// 11 MiB — exceeds the old 10 MiB paste-image/file cap.
	size := maxPastedImageBytes + 1<<20
	path, err := StoreUploadedFile(io.LimitReader(deterministicReader{}, int64(size)), "bigfile.bin")
	if err != nil {
		t.Fatalf("StoreUploadedFile returned error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != int64(size) {
		t.Fatalf("expected %d byte file, got %d", size, info.Size())
	}
}

// deterministicReader produces zero bytes without allocating.
type deterministicReader struct{}

func (d deterministicReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestStoreUploadedFileRejectsEmpty(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	before := listPasteDirs(t)

	_, err := StoreUploadedFile(strings.NewReader(""), "empty.txt")
	if err == nil {
		t.Fatal("expected error for empty upload")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("unexpected error: %v", err)
	}

	// No leftover termyard-paste-* dir created after this test.
	after := listPasteDirs(t)
	if len(after) > len(before) {
		t.Fatalf("leftover paste dirs: before=%v after=%v", before, after)
	}
}

func TestStoreUploadedFileCleansUpOnReadError(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	before := listPasteDirs(t)

	_, err := StoreUploadedFile(&errorReader{after: 3, err: io.ErrUnexpectedEOF}, "fail.bin")
	if err == nil {
		t.Fatal("expected error from failing reader")
	}

	after := listPasteDirs(t)
	if len(after) > len(before) {
		t.Fatalf("leftover paste dirs after read error: before=%v after=%v", before, after)
	}
}

type errorReader struct {
	after int
	err   error
	count int
}

func (r *errorReader) Read(p []byte) (int, error) {
	if r.count >= r.after {
		return 0, r.err
	}
	r.count++
	p[0] = 'x'
	return 1, nil
}

func TestStoreUploadedFileExtensionSanitized(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	for _, tc := range []struct {
		filename string
		wantExt  string
	}{
		{"evil.$(id)", ""},
		{"a.TXT", ".txt"},
		{"notes.日本", ""},
		{"archive.", ""},
		{"report.pdf", ".pdf"},
	} {
		t.Run(tc.filename, func(t *testing.T) {
			path, err := StoreUploadedFile(strings.NewReader("x"), tc.filename)
			if err != nil {
				t.Fatalf("StoreUploadedFile returned error: %v", err)
			}
			if ext := filepath.Ext(path); ext != tc.wantExt {
				t.Fatalf("expected extension %q, got %q", tc.wantExt, ext)
			}
		})
	}
}

func TestStoreUploadedFileCleanupEligible(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	path, err := StoreUploadedFile(strings.NewReader("cleanup-test"), "cleanup.txt")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)

	// Backdate the directory to be older than pastedDataMaxAge.
	old := time.Now().Add(-pastedDataMaxAge - time.Minute)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}

	// cleanupStalePastes should remove it.
	cleanupStalePastes(time.Now())
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stale paste directory still exists: %v", err)
	}
}

func TestShellQuoteExported(t *testing.T) {
	// Verify the exported ShellQuote matches the internal shellQuote.
	if got, want := ShellQuote("a'b"), "'a'\"'\"'b'"; got != want {
		t.Fatalf("ShellQuote(%q) = %q, want %q", "a'b", got, want)
	}
	if got, want := ShellQuote("/tmp/it's unsafe"), "'/tmp/it'\"'\"'s unsafe'"; got != want {
		t.Fatalf("ShellQuote(%q) = %q, want %q", "/tmp/it's unsafe", got, want)
	}
}

func listPasteDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), pasteDirectoryPrefix) && e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
}
