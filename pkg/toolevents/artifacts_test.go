package toolevents

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScanArtifactPaths(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "plain path in sentence",
			text: "See ./cmd/tool/main.go in docs",
			want: []string{"./cmd/tool/main.go"},
		},
		{
			name: "path at end of line",
			text: "Wrote /tmp/result.json",
			want: []string{"/tmp/result.json"},
		},
		{
			name: "path followed by comma and colon",
			text: "Saved ./docs/spec.md, then ./notes/todo.txt:",
			want: []string{"./docs/spec.md", "./notes/todo.txt"},
		},
		{
			name: "ansi stripped",
			text: "\x1b[31m./pkg/server/server.go\x1b[0m",
			want: []string{"./pkg/server/server.go"},
		},
		{
			name: "identifier suffix rejected",
			text: "Ignore foo.go2 and bar.tsx1",
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanArtifactPaths(tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestParseOSC8FilePaths(t *testing.T) {
	host, _ := os.Hostname()
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "percent decoded",
			text: "\x1b]8;;file://localhost/tmp/hello%20world.pdf\x1b\\",
			want: []string{"/tmp/hello world.pdf"},
		},
		{
			name: "close alone no phantom path",
			text: "\x1b]8;;\x1b\\",
			want: nil,
		},
		{
			name: "ansi noise ignored",
			text: "\x1b[31m\x1b]8;;file://localhost/tmp/noisy%20file.txt\x1b\\\x1b[0m",
			want: []string{"/tmp/noisy file.txt"},
		},
		{
			name: "http ignored",
			text: "\x1b]8;;http://example.com/a.txt\x1b\\",
			want: nil,
		},
		{
			name: "https ignored",
			text: "\x1b]8;;https://example.com/a.txt\x1b\\",
			want: nil,
		},
		{
			name: "mailto ignored",
			text: "\x1b]8;;mailto:test@example.com\x1b\\",
			want: nil,
		},
		{
			name: "real host accepted",
			text: fmt.Sprintf("\x1b]8;;file://%s/tmp/host.pdf\x1b\\", host),
			want: []string{"/tmp/host.pdf"},
		},
		{
			name: "wrong host rejected",
			text: "\x1b]8;;file://wrong-host/tmp/host.pdf\x1b\\",
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseOSC8FilePaths(tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestParseOSC7CWD(t *testing.T) {
	host, _ := os.Hostname()
	tests := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{
			name: "latest wins",
			text: "\x1b]7;file://localhost/first%20path\x1b\\ hello \x1b]7;file://localhost/second%20path\x1b\\",
			want: "/second path",
			ok:   true,
		},
		{
			name: "real host accepted",
			text: fmt.Sprintf("\x1b]7;file://%s/home/real\x1b\\", host),
			want: "/home/real",
			ok:   true,
		},
		{
			name: "wrong host rejected",
			text: "\x1b]7;file://wrong-host/home/real\x1b\\",
			ok:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseOSC7CWD(tc.text)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("got ok=%v cwd=%q want ok=%v cwd=%q", ok, got, tc.ok, tc.want)
			}
		})
	}
}

func TestEnrichArtifactRequiresExistingFile(t *testing.T) {
	cwd := t.TempDir()
	missing := filepath.Join(cwd, "later.txt")
	if got := EnrichArtifact(missing, cwd, ToolPi, "regex"); got != nil {
		t.Fatalf("missing file enriched: %#v", got)
	}

	path := filepath.Join(cwd, "later.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := EnrichArtifact(path, cwd, ToolPi, "regex")
	if got == nil {
		t.Fatal("existing file not enriched")
	}
	if got.Path != path || got.Name != "later.txt" || got.Tool != ToolPi || got.Source != "regex" {
		t.Fatalf("bad artifact: %#v", got)
	}
}
