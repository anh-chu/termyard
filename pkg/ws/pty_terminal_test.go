package ws

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"github.com/anh-chu/termyard/pkg/tmux"
)

func TestBridgePTYRoundTripAndClose(t *testing.T) {
	client := mustTmuxClient(t)
	session := mustNewSession(t, client)

	wsURL, done := mustBridgeServer(t, client.TmuxPath(), session)
	conn := mustDialBridge(t, wsURL)

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("echo PHASE2OK\n")); err != nil {
		t.Fatal(err)
	}
	mustReadBridgeOutput(t, conn, []byte("PHASE2OK"))

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	mustBridgeDone(t, done)
}

func TestBridgePTYEndsWhenTmuxSessionDies(t *testing.T) {
	client := mustTmuxClient(t)
	session := mustNewSession(t, client)

	wsURL, done := mustBridgeServer(t, client.TmuxPath(), session)
	conn := mustDialBridge(t, wsURL)
	defer conn.Close()

	if err := client.KillSession("", session); err != nil {
		t.Fatal(err)
	}
	mustBridgeDone(t, done)
}

func mustTmuxClient(t *testing.T) *tmux.Client {
	t.Helper()
	client, err := tmux.NewClient()
	if err != nil {
		t.Skip(err)
	}
	return client
}

func mustNewSession(t *testing.T, client *tmux.Client) string {
	t.Helper()
	session := fmt.Sprintf("phase2-%d", time.Now().UnixNano())
	if err := client.NewSession(session, "", ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.KillSession("", session)
	})
	return session
}

func mustBridgeServer(t *testing.T, tmuxPath, session string) (string, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		logger := logrus.New()
		logger.SetOutput(io.Discard)
		done <- BridgePTY(conn, tmuxPath, session, 80, 24, nil, logrus.NewEntry(logger))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return strings.Replace(srv.URL, "http://", "ws://", 1) + "/ws", done
}

func mustDialBridge(t *testing.T, wsURL string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func mustReadBridgeOutput(t *testing.T, conn *websocket.Conn, want []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(raw)
		if bytes.Contains(buf.Bytes(), want) {
			return buf.Bytes()
		}
	}
	t.Fatalf("did not see %q in bridge output", want)
	return nil
}

func mustBridgeDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for bridge shutdown")
	}
}

func mustSpliceDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for splice shutdown")
	}
}

func TestSpliceConns(t *testing.T) {
	t.Run("binary and text", func(t *testing.T) {
		browserClient, browserServer := mustWSConnPair(t)
		dataClient, dataServer := mustWSConnPair(t)
		done := make(chan struct{})
		logger := logrus.New()
		logger.SetOutput(io.Discard)
		go func() {
			SpliceConns(browserServer, dataServer, logrus.NewEntry(logger))
			close(done)
		}()

		if err := dataClient.WriteMessage(websocket.BinaryMessage, []byte("from-data")); err != nil {
			t.Fatal(err)
		}
		mt, got := mustReadWSMessage(t, browserClient)
		if mt != websocket.BinaryMessage || !bytes.Equal(got, []byte("from-data")) {
			t.Fatalf("browser got %d %q", mt, got)
		}

		if err := browserClient.WriteMessage(websocket.TextMessage, []byte("from-browser")); err != nil {
			t.Fatal(err)
		}
		mt, got = mustReadWSMessage(t, dataClient)
		if mt != websocket.TextMessage || !bytes.Equal(got, []byte("from-browser")) {
			t.Fatalf("data got %d %q", mt, got)
		}

		_ = browserClient.Close()
		mustSpliceDone(t, done)
		_ = dataClient.Close()
	})

	t.Run("data close unwinds browser loop", func(t *testing.T) {
		browserClient, browserServer := mustWSConnPair(t)
		dataClient, dataServer := mustWSConnPair(t)
		done := make(chan struct{})
		logger := logrus.New()
		logger.SetOutput(io.Discard)
		go func() {
			SpliceConns(browserServer, dataServer, logrus.NewEntry(logger))
			close(done)
		}()

		_ = dataClient.Close()
		mustSpliceDone(t, done)
		_ = browserClient.Close()
	})

	t.Run("ping answered locally", func(t *testing.T) {
		browserClient, browserServer := mustWSConnPair(t)
		dataClient, dataServer := mustWSConnPair(t)
		done := make(chan struct{})
		logger := logrus.New()
		logger.SetOutput(io.Discard)
		go func() {
			SpliceConns(browserServer, dataServer, logrus.NewEntry(logger))
			close(done)
		}()

		if err := browserClient.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`)); err != nil {
			t.Fatal(err)
		}
		mt, got := mustReadWSMessage(t, browserClient)
		if mt != websocket.TextMessage || !bytes.Equal(got, pongFrame) {
			t.Fatalf("browser got %d %q, want pong", mt, got)
		}
		if err := dataClient.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := dataClient.ReadMessage(); err == nil {
			t.Fatal("ping forwarded to data conn")
		}

		_ = browserClient.Close()
		mustSpliceDone(t, done)
		_ = dataClient.Close()
	})
}

func mustWSConnPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	ch := make(chan *websocket.Conn, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ch <- conn
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client, _, err := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http://", "ws://", 1)+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := <-ch
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server
}

func mustReadWSMessage(t *testing.T, conn *websocket.Conn) (int, []byte) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	mt, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	return mt, got
}
