package peer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/anh-chu/termyard/pkg/identity"
)

func TestStreamRegistryAndPeerStream(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	_ = os.MkdirAll(filepath.Join(tmpHome, ".config", "termyard"), 0o700)

	dialerID, err := identity.Generate("dialer")
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := identity.Generate("other")
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
	defer srv.Close()

	addr := mustHostPort(t, srv.URL)

	t.Run("happy path", func(t *testing.T) {
		token := NewToken()
		ps := &PendingStream{StreamID: "S1", ExpectedPeer: dialerID.Fingerprint(), resolved: make(chan *websocket.Conn, 1)}
		reg.Register(token, ps)

		clientConn, err := dialPeerStream(context.Background(), addr, dialerID, token)
		if err != nil {
			t.Fatal(err)
		}
		defer clientConn.Close()

		serverConn := waitResolvedConn(t, ps)
		defer serverConn.Close()
		if ps.StreamID != "S1" {
			t.Fatalf("stream id = %q", ps.StreamID)
		}

		if err := clientConn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
			t.Fatal(err)
		}
		_, got, err := serverConn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "ping" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("token selects matching stream and is one-time", func(t *testing.T) {
		token1 := NewToken()
		token2 := NewToken()
		ps1 := &PendingStream{StreamID: "S2", ExpectedPeer: dialerID.Fingerprint(), resolved: make(chan *websocket.Conn, 1)}
		ps2 := &PendingStream{StreamID: "S3", ExpectedPeer: dialerID.Fingerprint(), resolved: make(chan *websocket.Conn, 1)}
		reg.Register(token1, ps1)
		reg.Register(token2, ps2)

		clientConn, err := dialPeerStream(context.Background(), addr, dialerID, token1)
		if err != nil {
			t.Fatal(err)
		}
		defer clientConn.Close()

		serverConn := waitResolvedConn(t, ps1)
		defer serverConn.Close()
		select {
		case <-ps2.resolved:
			t.Fatal("wrong stream resolved")
		case <-time.After(100 * time.Millisecond):
		}

		if err := clientConn.WriteMessage(websocket.TextMessage, []byte("select")); err != nil {
			t.Fatal(err)
		}
		_, got, err := serverConn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "select" {
			t.Fatalf("got %q", got)
		}

		clientConn.Close()
		replayConn, err := dialPeerStream(context.Background(), addr, dialerID, token1)
		if err != nil {
			t.Fatal(err)
		}
		defer replayConn.Close()
		if msg := mustReadEnvelope(t, replayConn); msg.Type != MsgAuthFail {
			t.Fatalf("type = %q, want %q", msg.Type, MsgAuthFail)
		}
	})

	t.Run("data before registration", func(t *testing.T) {
		token := NewToken()
		ps := &PendingStream{StreamID: "S4", ExpectedPeer: dialerID.Fingerprint(), resolved: make(chan *websocket.Conn, 1)}

		clientConn, err := dialPeerStream(context.Background(), addr, dialerID, token)
		if err != nil {
			t.Fatal(err)
		}
		defer clientConn.Close()

		time.Sleep(50 * time.Millisecond)
		reg.Register(token, ps)

		serverConn := waitResolvedConn(t, ps)
		defer serverConn.Close()
		if ps.StreamID != "S4" {
			t.Fatalf("stream id = %q", ps.StreamID)
		}
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

	t.Run("data after expiry", func(t *testing.T) {
		token := NewToken()
		ps := &PendingStream{StreamID: "S5", ExpectedPeer: dialerID.Fingerprint(), resolved: make(chan *websocket.Conn, 1)}
		reg.Register(token, ps)

		reg.mu.Lock()
		entry := reg.pending[token]
		reg.mu.Unlock()
		if entry == nil {
			t.Fatal("missing pending entry")
		}
		reg.expire(token, entry)

		clientConn, err := dialPeerStream(context.Background(), addr, dialerID, token)
		if err != nil {
			t.Fatal(err)
		}
		defer clientConn.Close()
		time.Sleep(50 * time.Millisecond)
		if msg := mustReadEnvelope(t, clientConn); msg.Type != MsgAuthFail {
			t.Fatalf("type = %q, want %q", msg.Type, MsgAuthFail)
		}
		select {
		case <-ps.resolved:
			t.Fatal("unexpected resolved conn after expiry")
		default:
		}
	})

	t.Run("wrong peer", func(t *testing.T) {
		token := NewToken()
		ps := &PendingStream{StreamID: "S6", ExpectedPeer: otherID.Fingerprint(), resolved: make(chan *websocket.Conn, 1)}
		reg.Register(token, ps)

		clientConn, err := dialPeerStream(context.Background(), addr, dialerID, token)
		if err != nil {
			t.Fatal(err)
		}
		defer clientConn.Close()
		time.Sleep(50 * time.Millisecond)
		if msg := mustReadEnvelope(t, clientConn); msg.Type != MsgAuthFail {
			t.Fatalf("type = %q, want %q", msg.Type, MsgAuthFail)
		}
		select {
		case <-ps.resolved:
			t.Fatal("unexpected resolved conn for wrong peer")
		default:
		}
	})
}

func mustHostPort(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func waitResolvedConn(t *testing.T, ps *PendingStream) *websocket.Conn {
	t.Helper()
	select {
	case conn := <-ps.resolved:
		return conn
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for resolved conn")
		return nil
	}
}

func mustReadEnvelope(t *testing.T, conn *websocket.Conn) Message {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	return msg
}
