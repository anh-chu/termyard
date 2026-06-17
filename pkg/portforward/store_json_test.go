package portforward

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestForwardBaseURLOmitEmpty(t *testing.T) {
	b, err := json.Marshal(Forward{Port: 3000, Mode: ModeProxy})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "base_url") {
		t.Fatalf("unexpected base_url in %s", b)
	}
	b, err = json.Marshal(Forward{Port: 3000, Mode: ModeProxy, BaseURL: "http://peer:7654"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "base_url") {
		t.Fatalf("missing base_url in %s", b)
	}
}
