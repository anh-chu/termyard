package ws

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"github.com/anh-chu/termyard/pkg/tmux"
)

func TestIsPing(t *testing.T) {
	if !isPing([]byte(`{"type":"ping"}`)) {
		t.Fatal("expected ping control message")
	}
	if isPing([]byte(`{"type":"paste-file","filename":"ping"}`)) {
		t.Fatal("paste-file message misidentified as ping")
	}
}

func TestBridgePTYPasteFile(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	client := mustTmuxClient(t)
	session := mustNewSession(t, client)

	wsURL, done := mustBridgeServer(t, client.TmuxPath(), session)
	conn := mustDialBridge(t, wsURL)
	defer func() {
		_ = conn.Close()
		mustBridgeDone(t, done)
	}()

	data := base64.StdEncoding.EncodeToString([]byte("file bytes"))
	control := fmt.Sprintf(`{"type":"paste-file","data":"%s","filename":"notes.$(id)"}`, data)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(control)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("\nprintf PASTE_FILE_OK\n")); err != nil {
		t.Fatal(err)
	}
	mustReadBridgeOutput(t, conn, []byte("PASTE_FILE_OK"))

	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "termyard-paste-") {
			continue
		}
		files, err := os.ReadDir(filepath.Join(os.TempDir(), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if !strings.HasPrefix(file.Name(), "pasted-") {
				continue
			}
			path := filepath.Join(os.TempDir(), entry.Name(), file.Name())
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != "file bytes" || filepath.Ext(path) != "" {
				t.Fatalf("unexpected pasted file %q: %q", path, contents)
			}
			return
		}
	}
	t.Fatal("paste-file control did not create a stored file")
}

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
		done <- BridgePTY(conn, tmuxPath, session, 80, 24, nil, nil, nil, logrus.NewEntry(logger))
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
