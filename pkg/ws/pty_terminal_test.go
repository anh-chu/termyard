package ws

import (
	"bytes"
	"compress/flate"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

func TestIsPing(t *testing.T) {
	if !isPing([]byte(`{"type":"ping"}`)) {
		t.Fatal("expected ping control message")
	}
	if isPing([]byte(`{"type":"paste-file","filename":"ping"}`)) {
		t.Fatal("paste-file message misidentified as ping")
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

	t.Run("ping answered locally and forwarded", func(t *testing.T) {
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
		// Fast local ack reaches the browser immediately.
		mt, got := mustReadWSMessage(t, browserClient)
		if mt != websocket.TextMessage || !bytes.Equal(got, pongFrame) {
			t.Fatalf("browser got %d %q, want pong", mt, got)
		}
		// The ping is also forwarded to the peer data conn so the host echoes
		// a pong back, keeping the hub<->host link alive on idle terminals.
		mt, got = mustReadWSMessage(t, dataClient)
		if mt != websocket.TextMessage || !bytes.Equal(got, []byte(`{"type":"ping"}`)) {
			t.Fatalf("data conn got %d %q, want forwarded ping", mt, got)
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

// compressedUpgrader is a WebSocket upgrader with EnableCompression.
var compressedUpgrader = websocket.Upgrader{
	CheckOrigin:       CheckSameOrigin,
	ReadBufferSize:    1024,
	WriteBufferSize:   1024 * 16,
	EnableCompression: true,
}

// mustWSConnPairCompressed returns a client/server pair where both sides
// negotiate permessage-deflate. The server enables write compression; the
// client leaves it disabled (viewer role). The returned response is from the
// client dial handshake and can be used to verify compression negotiation.
func mustWSConnPairCompressed(t *testing.T) (*websocket.Conn, *websocket.Conn, *http.Response) {
	t.Helper()
	ch := make(chan *websocket.Conn, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := compressedUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.EnableWriteCompression(false)
		ch <- conn
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	dialer := &websocket.Dialer{HandshakeTimeout: 5 * time.Second, EnableCompression: true}
	client, resp, err := dialer.Dial(strings.Replace(srv.URL, "http://", "ws://", 1)+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	client.EnableWriteCompression(false)
	server := <-ch
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server, resp
}

func TestSpliceConnsCompression(t *testing.T) {
	t.Run("large compressed frame traverses splice losslessly", func(t *testing.T) {
		browserClient, browserServer := mustWSConnPair(t)
		dataClient, dataServer, resp := mustWSConnPairCompressed(t)

		// Prove permessage-deflate was negotiated in the handshake response.
		if ext := resp.Header.Get("Sec-WebSocket-Extensions"); !strings.Contains(ext, "permessage-deflate") {
			t.Fatalf("Sec-WebSocket-Extensions missing permessage-deflate: %q", ext)
		}

		done := make(chan struct{})
		logger := logrus.New()
		logger.SetOutput(io.Discard)
		go func() {
			SpliceConns(browserServer, dataServer, logrus.NewEntry(logger))
			close(done)
		}()

		// Host side (dataClient) enables write compression with BestSpeed.
		// PTY output flows host→viewer, so this is the compressed direction.
		// Set compression level before EnableWriteCompression per gorilla/websocket API.
		if err := dataClient.SetCompressionLevel(flate.BestSpeed); err != nil {
			t.Fatalf("set compression level: %v", err)
		}
		dataClient.EnableWriteCompression(true)

		// Send a large repetitive frame from the host through the compressed channel.
		payload := bytes.Repeat([]byte("X"), 65536)
		if err := dataClient.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			t.Fatal(err)
		}
		mt, got := mustReadWSMessage(t, browserClient)
		if mt != websocket.BinaryMessage || !bytes.Equal(got, payload) {
			t.Fatalf("browser got mt=%d len=%d", mt, len(got))
		}

		// Browser reply (keystrokes) flows uncompressed through the viewer.
		reply := []byte("keystroke")
		if err := browserClient.WriteMessage(websocket.TextMessage, reply); err != nil {
			t.Fatal(err)
		}
		mt, got = mustReadWSMessage(t, dataClient)
		if mt != websocket.TextMessage || !bytes.Equal(got, reply) {
			t.Fatalf("data client got mt=%d len=%d", mt, len(got))
		}

		_ = browserClient.Close()
		mustSpliceDone(t, done)
	})

	t.Run("viewer writes stay uncompressed", func(t *testing.T) {
		browserClient, browserServer := mustWSConnPair(t)
		dataClient, dataServer, _ := mustWSConnPairCompressed(t)
		done := make(chan struct{})
		logger := logrus.New()
		logger.SetOutput(io.Discard)
		go func() {
			SpliceConns(browserServer, dataServer, logrus.NewEntry(logger))
			close(done)
		}()

		// DataServer (viewer) explicitly disables write compression,
		// matching serveViewerPerStream behavior.
		dataServer.EnableWriteCompression(false)

		// Browser input flows through to host uncompressed.
		reply := []byte("ls -la\n")
		if err := browserClient.WriteMessage(websocket.TextMessage, reply); err != nil {
			t.Fatal(err)
		}
		mt, got := mustReadWSMessage(t, dataClient)
		if mt != websocket.TextMessage || !bytes.Equal(got, reply) {
			t.Fatalf("data client got mt=%d len=%d", mt, len(got))
		}

		_ = browserClient.Close()
		mustSpliceDone(t, done)
	})

	t.Run("compression-negotiated data close unwinds browser loop", func(t *testing.T) {
		browserClient, browserServer := mustWSConnPair(t)
		dataClient, dataServer, _ := mustWSConnPairCompressed(t)
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
}
