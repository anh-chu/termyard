package namer

import (
	"fmt"
	"strings"
	"unicode"
)

// Sanitize converts arbitrary model output into a safe, kebab-case slug.
//
// Session names treat '.' and ':' as window/pane target separators and reserves
// '~' and '!'; whitespace is also problematic for target syntax. We reduce to
// [a-z0-9-], collapse repeats, and trim hyphens. Returns "" if nothing usable
// remains.
func Sanitize(s string) string {
	s = strings.TrimSpace(s)
	// Strip surrounding quotes/backticks the model may add.
	s = strings.Trim(s, "\"'`")
	s = strings.ToLower(s)

	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case r == '-' || r == '_' || unicode.IsSpace(r):
			if !lastHyphen && b.Len() > 0 {
				b.WriteRune('-')
				lastHyphen = true
			}
		default:
			// drop everything else (incl. . : ~ ! and non-ASCII)
		}
	}
	out := strings.Trim(b.String(), "-")

	const maxLen = 40
	if len(out) > maxLen {
		out = strings.Trim(out[:maxLen], "-")
	}
	return out
}

// Dedup returns name, or name with a numeric suffix, such that it does not
// collide with any entry in taken. taken keys are existing session names.
func Dedup(name string, taken map[string]bool) string {
	if name == "" {
		return name
	}
	if !taken[name] {
		return name
	}
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d", name, i)
		if !taken[cand] {
			return cand
		}
	}
	return name
}
