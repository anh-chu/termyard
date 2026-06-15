package server

import "testing"

func TestEnsureUniqueSessionName(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		want     string
	}{
		{name: "codex-termyard", existing: nil, want: "codex-termyard"},
		{name: "codex-termyard", existing: []string{"codex-termyard"}, want: "codex-termyard-2"},
		{name: "codex-termyard", existing: []string{"codex-termyard", "codex-termyard-2"}, want: "codex-termyard-3"},
	}

	for _, tt := range tests {
		if got := ensureUniqueSessionName(tt.name, tt.existing); got != tt.want {
			t.Fatalf("ensureUniqueSessionName(%q, %v) = %q, want %q", tt.name, tt.existing, got, tt.want)
		}
	}
}
