package model

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// ErrEmptyUpload is returned by StoreUploadedFile when the request body
// contains zero bytes. Callers should map it to HTTP 400 Bad Request.
var ErrEmptyUpload = errors.New("empty upload payload")

// IsEmptyUploadMessage returns true when msg contains the sentinel empty
// upload error. Use this when the error arrives as a string from a remote
// peer's JSON reply rather than as a Go error.
func IsEmptyUploadMessage(msg string) bool {
	return strings.Contains(msg, ErrEmptyUpload.Error())
}

// StoreUploadedFile streams contents into a private termyard-paste directory
// (0700 dir, 0600 file, marker, 24h TTL — same layout cleanupStalePastes owns).
// No size limit: infrastructure/disk limits surface as write errors.
// Returns the stored absolute path.
func StoreUploadedFile(contents io.Reader, filename string) (string, error) {
	ext := pastedFileExt(filename)

	// Create the paste directory and file while holding the mutex so
	// cleanupStalePastes cannot race the dir creation.
	pasteStoreMu.Lock()
	cleanupStalePastes(time.Now())
	dir, file, err := createPasteFile(ext)
	pasteStoreMu.Unlock()
	if err != nil {
		return "", err
	}
	path := file.Name()

	n, copyErr := io.Copy(file, contents)
	closeErr := file.Close()

	if copyErr != nil {
		_ = os.Remove(path)
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("store uploaded file: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("close uploaded file: %w", closeErr)
	}
	if n == 0 {
		_ = os.Remove(path)
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("%w", ErrEmptyUpload)
	}

	// Re-acquire mutex to schedule cleanup safely.
	pasteStoreMu.Lock()
	schedulePastedCleanup()
	pasteStoreMu.Unlock()
	return path, nil
}

// ShellQuote exports the existing single-quote POSIX quoting used by the
// paste-image and paste-file paths.
func ShellQuote(value string) string {
	return shellQuote(value)
}
