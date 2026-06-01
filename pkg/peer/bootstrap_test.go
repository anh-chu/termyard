package peer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeAddress(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"host.example", "host.example:7654", false},
		{"host:1234", "host:1234", false},
		{"https://host:8080", "host:8080", false},
		{"http://host", "host:7654", false},
		{"  trim.me  ", "trim.me:7654", false},
		{"[::1]:9000", "[::1]:9000", false},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeAddress(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("NormalizeAddress(%q) err=%v want err=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("NormalizeAddress(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestSendBootstrap_OK(t *testing.T) {
	var got BootstrapRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/peers/bootstrap" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(BootstrapResponse{
			Name: "remote", PublicKey: "pk-remote", Fingerprint: "fp-remote",
		})
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	resp, err := SendBootstrap(context.Background(), addr, BootstrapRequest{
		Password: "pw", Name: "me", PublicKey: "pk-me", Fingerprint: "fp-me",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Name != "remote" || resp.PublicKey != "pk-remote" {
		t.Errorf("got %+v", resp)
	}
	if got.Password != "pw" || got.Name != "me" {
		t.Errorf("server got %+v", got)
	}
}

func TestSendBootstrap_StatusCodesAreTyped(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{http.StatusUnauthorized, http.StatusUnauthorized},
		{http.StatusServiceUnavailable, http.StatusServiceUnavailable},
		{http.StatusBadRequest, http.StatusBadRequest},
		{http.StatusTeapot, http.StatusBadGateway},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "x", c.status)
		}))
		addr := strings.TrimPrefix(srv.URL, "http://")
		_, err := SendBootstrap(context.Background(), addr, BootstrapRequest{Password: "pw"})
		srv.Close()
		if err == nil {
			t.Errorf("status %d: expected error", c.status)
			continue
		}
		var bErr *BootstrapError
		if !errors.As(err, &bErr) {
			t.Errorf("status %d: not BootstrapError: %v", c.status, err)
			continue
		}
		if bErr.Status != c.want {
			t.Errorf("status %d: got bErr.Status=%d want=%d", c.status, bErr.Status, c.want)
		}
	}
}
