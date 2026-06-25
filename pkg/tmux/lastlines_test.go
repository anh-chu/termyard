package tmux

import "testing"

func TestLastLines(t *testing.T) {
	cases := []struct {
		name, in string
		n        int
		want     string
	}{
		{"empty", "", 40, ""},
		{"fewer than n", "a\nb", 40, "a\nb"},
		{"exactly n", "a\nb\nc", 3, "a\nb\nc"},
		{"more than n", "a\nb\nc\nd", 2, "c\nd"},
		{"trailing newline ignored", "a\nb\nc\n", 2, "b\nc"},
		{"n zero returns input", "a\nb", 0, "a\nb"},
	}
	for _, c := range cases {
		if got := LastLines(c.in, c.n); got != c.want {
			t.Errorf("%s: LastLines(%q,%d)=%q want %q", c.name, c.in, c.n, got, c.want)
		}
	}
}
