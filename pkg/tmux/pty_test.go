package tmux

import (
	"os"
	"testing"
)

func TestLocaleEnvStripsExistingLocaleAndPreservesLanguage(t *testing.T) {
	// Save and restore original environment.
	orig := os.Environ()
	t.Cleanup(func() {
		for _, e := range orig {
			kv := splitEnv(e)
			os.Setenv(kv[0], kv[1])
		}
	})

	// Set up a controlled environment.
	os.Clearenv()
	_ = os.Setenv("LANG", "de_DE.UTF-8")
	_ = os.Setenv("LC_ALL", "C")
	_ = os.Setenv("LC_CTYPE", "POSIX")
	_ = os.Setenv("LANGUAGE", "de:en")
	_ = os.Setenv("PATH", "/usr/bin")
	_ = os.Setenv("HOME", "/home/test")
	_ = os.Setenv("USER", "test")

	got := localeEnv()

	// Build a map for easy lookups.
	m := make(map[string]string, len(got))
	for _, e := range got {
		kv := splitEnv(e)
		m[kv[0]] = kv[1]
	}

	// Old LANG, LC_ALL, LC_CTYPE values must be stripped (keys may reappear
	// with forced C.UTF-8 values, verified below).
	for _, e := range got {
		kv := splitEnv(e)
		switch {
		case kv[0] == "LANG" && kv[1] == "de_DE.UTF-8":
			t.Errorf("old LANG=de_DE.UTF-8 not stripped from localeEnv output")
		case kv[0] == "LC_ALL" && kv[1] == "C":
			t.Errorf("old LC_ALL=C not stripped from localeEnv output")
		case kv[0] == "LC_CTYPE" && kv[1] == "POSIX":
			t.Errorf("old LC_CTYPE=POSIX not stripped from localeEnv output")
		}
	}

	// LANGUAGE must be preserved.
	if v, ok := m["LANGUAGE"]; !ok {
		t.Error("LANGUAGE missing from localeEnv output, want preserved")
	} else if v != "de:en" {
		t.Errorf("LANGUAGE = %q, want %q", v, "de:en")
	}

	// Unrelated vars preserved.
	if v, ok := m["PATH"]; !ok {
		t.Error("PATH missing from localeEnv output")
	} else if v != "/usr/bin" {
		t.Errorf("PATH = %q, want %q", v, "/usr/bin")
	}
	if v, ok := m["HOME"]; !ok {
		t.Error("HOME missing from localeEnv output")
	} else if v != "/home/test" {
		t.Errorf("HOME = %q, want %q", v, "/home/test")
	}

	// LC_ALL=C.UTF-8 must be present exactly once; last wins in exec.Cmd.Env.
	lcAllCount := 0
	for _, e := range got {
		kv := splitEnv(e)
		if kv[0] == "LC_ALL" {
			lcAllCount++
			if kv[1] != "C.UTF-8" {
				t.Errorf("LC_ALL = %q, want %q", kv[1], "C.UTF-8")
			}
		}
	}
	if lcAllCount != 1 {
		t.Errorf("LC_ALL appears %d times in localeEnv output, want exactly 1", lcAllCount)
	}

	// LANG=C.UTF-8 must be present exactly once.
	langCount := 0
	for _, e := range got {
		kv := splitEnv(e)
		if kv[0] == "LANG" {
			langCount++
			if kv[1] != "C.UTF-8" {
				t.Errorf("LANG = %q, want %q", kv[1], "C.UTF-8")
			}
		}
	}
	if langCount != 1 {
		t.Errorf("LANG appears %d times in localeEnv output, want exactly 1", langCount)
	}

	// No other locale keys should appear.
	for k := range m {
		switch k {
		case "LANG", "LC_ALL", "LC_CTYPE", "LANGUAGE", "PATH", "HOME", "USER":
			// expected
		default:
			t.Errorf("unexpected env key %q in localeEnv output", k)
		}
	}
}

// splitEnv splits "KEY=VALUE" into [key, value].
func splitEnv(e string) [2]string {
	for i := 0; i < len(e); i++ {
		if e[i] == '=' {
			return [2]string{e[:i], e[i+1:]}
		}
	}
	return [2]string{e, ""}
}
