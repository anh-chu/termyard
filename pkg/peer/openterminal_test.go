package peer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/anh-chu/termyard/pkg/identity"
)

func TestPlanStream(t *testing.T) {
	tests := []struct {
		name                 string
		amHost, dialer       bool
		wantDial, wantBridge bool
	}{
		{name: "viewer-dialer", amHost: false, dialer: true, wantDial: true},
		{name: "viewer-listener", amHost: false, dialer: false},
		{name: "host-dialer", amHost: true, dialer: true, wantDial: true, wantBridge: true},
		{name: "host-listener", amHost: true, dialer: false, wantBridge: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dial, bridge := PlanStream(tt.amHost, tt.dialer)
			if dial != tt.wantDial || bridge != tt.wantBridge {
				t.Fatalf("PlanStream(%v, %v) = (%v, %v), want (%v, %v)", tt.amHost, tt.dialer, dial, bridge, tt.wantDial, tt.wantBridge)
			}
		})
	}
}

func TestOpenTerminalTransport(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	_ = os.MkdirAll(filepath.Join(tmpHome, ".config", "termyard"), 0o700)

	dialerID, err := identity.Generate("dialer")
	if err != nil {
		t.Fatal(err)
	}
	store, err := identity.NewPeerStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(identity.Peer{Name: "dialer", PublicKey: dialerID.PublicKey, Enabled: true, InitiatedByUs: true}); err != nil {
		t.Fatal(err)
	}
	reg := NewStreamRegistry()
	handler := NewHandler(SessionDeps{PeerStore: store}, reg)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/peer-stream", handler.HandlePeerStream)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	addr := mustHostPort(t, srv.URL)

	t.Run("register-before-dial", func(t *testing.T) {
		token := NewToken()
		ps := NewPendingStream("S1", "sess", 80, 24, "host-a", "viewer-a", dialerID.Fingerprint())
		reg.Register(token, ps)

		clientConn, err := DialPeerStream(context.Background(), addr, dialerID, token)
		if err != nil {
			t.Fatal(err)
		}
		defer clientConn.Close()

		serverConn := waitResolvedConn(t, ps)
		defer serverConn.Close()

		if err := clientConn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
			t.Fatal(err)
		}
		_, got, err := serverConn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "hello" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("dial-before-register", func(t *testing.T) {
		token := NewToken()
		ps := NewPendingStream("S2", "sess", 80, 24, "host-b", "viewer-b", dialerID.Fingerprint())

		clientConn, err := DialPeerStream(context.Background(), addr, dialerID, token)
		if err != nil {
			t.Fatal(err)
		}
		defer clientConn.Close()

		time.Sleep(50 * time.Millisecond)
		reg.Register(token, ps)

		serverConn := waitResolvedConn(t, ps)
		defer serverConn.Close()

		if err := clientConn.WriteMessage(websocket.TextMessage, []byte("late")); err != nil {
			t.Fatal(err)
		}
		_, got, err := serverConn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "late" {
			t.Fatalf("got %q", got)
		}
	})
}
