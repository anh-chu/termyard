package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveFilePath(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(fp, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		path string
		want int // 0 == success
	}{
		{"ok", fp, 0},
		{"relative", "hello.txt", http.StatusBadRequest},
		{"empty", "", http.StatusBadRequest},
		{"missing", filepath.Join(dir, "nope.txt"), http.StatusNotFound},
		{"dir", dir, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			_, status, _ := resolveFilePath(c.path, &Options{}, r)
			if status != c.want {
				t.Fatalf("path=%q got %d want %d", c.path, status, c.want)
			}
		})
	}
}

func TestFileGrantServe(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(fp, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	grants := newFileGrants()

	// Bad/absent token -> forbidden, never reads FS.
	r := httptest.NewRequest(http.MethodGet, "/file?token=bogus", nil)
	w := httptest.NewRecorder()
	handleFile(w, r, grants)
	if w.Code != http.StatusForbidden {
		t.Fatalf("bad token got %d want 403", w.Code)
	}

	// Granted token -> serves the file.
	tok := grants.grant(fp)
	r = httptest.NewRequest(http.MethodGet, "/file?token="+tok, nil)
	w = httptest.NewRecorder()
	handleFile(w, r, grants)
	if w.Code != http.StatusOK || w.Body.String() != "hi" {
		t.Fatalf("granted got %d body=%q", w.Code, w.Body.String())
	}

	// Expired grant -> forbidden.
	grants.byTok[tok] = fileGrant{path: fp, expires: time.Now().Add(-time.Second)}
	r = httptest.NewRequest(http.MethodGet, "/file?token="+tok, nil)
	w = httptest.NewRecorder()
	handleFile(w, r, grants)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expired got %d want 403", w.Code)
	}
}
