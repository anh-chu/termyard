package namer

import "testing"

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"Fix Auth Token":       "fix-auth-token",
		"  db migration  ":     "db-migration",
		"\"docker-logs\"":      "docker-logs",
		"feature/branch:thing": "featurebranchthing",
		"a..b::c~d!e":          "abcde",
		"UPPER_snake_case":     "upper-snake-case",
		"multi   space\tname":  "multi-space-name",
		"---trim---":           "trim",
		"café résumé":          "caf-rsum",
		"":                     "",
		"!!!":                  "",
		"verylongnamethatexceedsthefortycharacterlimitfortmux": "verylongnamethatexceedsthefortycharacter",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDedup(t *testing.T) {
	taken := map[string]bool{"build": true, "build-2": true}
	if got := Dedup("build", taken); got != "build-3" {
		t.Errorf("Dedup collision = %q, want build-3", got)
	}
	if got := Dedup("fresh", taken); got != "fresh" {
		t.Errorf("Dedup no-collision = %q, want fresh", got)
	}
	if got := Dedup("", taken); got != "" {
		t.Errorf("Dedup empty = %q, want empty", got)
	}
}

func TestExtractContent(t *testing.T) {
	json := []byte(`{"choices":[{"message":{"role":"assistant","content":"auth-bug-fix"}}]}`)
	if got, err := extractContent(json); err != nil || got != "auth-bug-fix" {
		t.Fatalf("json: got %q err %v", got, err)
	}
	sse := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"auth\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"-bug-\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"fix\"}}]}\n\n" +
		"data: [DONE]\n")
	if got, err := extractContent(sse); err != nil || got != "auth-bug-fix" {
		t.Fatalf("sse: got %q err %v", got, err)
	}
}

func TestLastLine(t *testing.T) {
	cases := map[string]string{
		"fix-auth":                       "fix-auth",
		"reasoning...\n\nfinal: db-sync": "final: db-sync",
		"  db-sync  ":                    "db-sync",
		"line1\nline2\n":                 "line2",
		"":                              "",
	}
	for in, want := range cases {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", in, got, want)
		}
	}
}
