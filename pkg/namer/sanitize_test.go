package namer

import "testing"

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"Fix Auth Token":        "fix-auth-token",
		"  db migration  ":      "db-migration",
		"\"docker-logs\"":       "docker-logs",
		"feature/branch:thing":  "featurebranchthing",
		"a..b::c~d!e":           "abcde",
		"UPPER_snake_case":      "upper-snake-case",
		"multi   space\tname":   "multi-space-name",
		"---trim---":            "trim",
		"café résumé":           "caf-rsum",
		"":                      "",
		"!!!":                   "",
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
