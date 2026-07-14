package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"github.com/anh-chu/termyard/pkg/activity"
	"github.com/anh-chu/termyard/pkg/tmux"
	"github.com/anh-chu/termyard/pkg/toolevents"
)

// pongFrame is the canonical reply to a browser heartbeat ping.
var pongFrame = []byte(`{"type":"pong"}`)

// isPing reports whether a text control frame is a heartbeat ping.
func isPing(msg []byte) bool {
	var control struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(msg, &control) == nil && control.Type == "ping"
}

// PTYTerminalHandler handles WebSocket connections backed by a PTY running tmux attach
type PTYTerminalHandler struct {
	tmuxPath        string
	activityTracker *activity.Tracker
	tmuxClient      *tmux.Client
	tracker         *toolevents.Tracker
}

// NewPTYTerminalHandler creates a new PTY-based terminal handler
func NewPTYTerminalHandler(tmuxPath string, activityTracker *activity.Tracker, tmuxClient *tmux.Client, tracker *toolevents.Tracker) *PTYTerminalHandler {
	return &PTYTerminalHandler{
		tmuxPath:        tmuxPath,
		activityTracker: activityTracker,
		tmuxClient:      tmuxClient,
		tracker:         tracker,
	}
}

// BridgePTY spawns a tmux PTY for session and pumps it over an already-open
// conn: PTY->conn (binary) in a goroutine, conn->PTY (control+binary) in the
// caller's goroutine, answering browser heartbeat pings locally. It does NOT
// upgrade and does NOT close conn — the caller owns conn's single Close.
func BridgePTY(conn *websocket.Conn, tmuxPath, session string, cols, rows uint16, act *activity.Tracker, client *tmux.Client, tracker *toolevents.Tracker, log *logrus.Entry) error {
	conn.SetReadLimit(tmux.MaxPTYControlMessageBytes)

	ptySess, err := tmux.NewPTYSession(tmuxPath, session, cols, rows)
	if err != nil {
		return err
	}
	defer ptySess.Close()

	scanContext, cancelScan := context.WithCancel(context.Background())
	scanner := newArtifactScanner(scanContext, client, tracker, session)
	output := newOutputCoalescer(func(mt int, payload []byte) error {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		err := conn.WriteMessage(mt, payload)
		_ = conn.SetWriteDeadline(time.Time{})
		return err
	}, func(error) {
		_ = conn.SetReadDeadline(time.Now())
	}, newResettableOutputTimer())
	ptyDone := make(chan struct{})
	var ptyReads int64

	go func() {
		defer close(ptyDone)
		defer output.CloseAndFlush()
		if scanner != nil {
			defer scanner.Close()
		}

		buffer := make([]byte, 32*1024)
		for {
			n, readErr := ptySess.Read(buffer)
			if n > 0 {
				ptyReads++
				if act != nil {
					act.Record(session, n)
				}
				chunk := append([]byte(nil), buffer[:n]...)
				output.Submit(chunk)
				if scanner != nil {
					scanner.Submit(chunk)
				}
			}
			if readErr != nil {
				_ = conn.SetReadDeadline(time.Now())
				return
			}
		}
	}()

outer:
	for {
		msgType, message, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.WithError(err).Debug("session ws read error")
			}
			break
		}

		switch msgType {
		case websocket.TextMessage:
			if isPing(message) {
				if !output.RequestPong() {
					break outer
				}
				continue
			}
			if err := tmux.HandlePTYControlMessage(ptySess, message); err != nil {
				log.WithError(err).Debug("control message failed")
			}

		case websocket.BinaryMessage:
			if _, err := ptySess.Write(message); err != nil {
				log.WithError(err).Debug("PTY write failed")
				break outer
			}
		}
	}

	cancelScan()
	ptySess.Close()
	<-ptyDone
	stats := output.Stats()
	fields := logrus.Fields{
		"pty_reads":           ptyReads,
		"output_bytes":        stats.bytes,
		"output_frames":       stats.frames,
		"max_output_frame":    stats.maxFrame,
		"slow_writes":         stats.slowWrites,
		"dropped_scan_chunks": int64(0),
	}
	if scanner != nil {
		fields["dropped_scan_chunks"] = scanner.DroppedChunks()
	}
	log.WithFields(fields).Info("session ws client disconnected")
	return nil
}

// SpliceConns pumps bytes between an upgraded browser WS and a peer data conn.
// Teardown nudges both read deadlines and callers own Close.
func SpliceConns(browser, data *websocket.Conn, log *logrus.Entry) {
	browser.SetReadLimit(tmux.MaxPTYControlMessageBytes)
	data.SetReadLimit(tmux.MaxPTYControlMessageBytes)

	var browserMu sync.Mutex
	var once sync.Once
	done := make(chan struct{})
	closeBoth := func() {
		once.Do(func() {
			_ = browser.SetReadDeadline(time.Now())
			_ = data.SetReadDeadline(time.Now())
		})
	}

	go func() {
		defer close(done)
		for {
			mt, msg, err := data.ReadMessage()
			if err != nil {
				closeBoth()
				return
			}
			browserMu.Lock()
			_ = browser.SetWriteDeadline(time.Now().Add(10 * time.Second))
			werr := browser.WriteMessage(mt, msg)
			_ = browser.SetWriteDeadline(time.Time{})
			browserMu.Unlock()
			if werr != nil {
				closeBoth()
				return
			}
		}
	}()

	for {
		mt, msg, err := browser.ReadMessage()
		if err != nil {
			closeBoth()
			break
		}
		if mt == websocket.TextMessage && isPing(msg) {
			browserMu.Lock()
			_ = browser.SetWriteDeadline(time.Now().Add(10 * time.Second))
			werr := browser.WriteMessage(websocket.TextMessage, pongFrame)
			_ = browser.SetWriteDeadline(time.Time{})
			browserMu.Unlock()
			if werr != nil {
				closeBoth()
				break
			}
			continue
		}
		_ = data.SetWriteDeadline(time.Now().Add(10 * time.Second))
		werr := data.WriteMessage(mt, msg)
		_ = data.SetWriteDeadline(time.Time{})
		if werr != nil {
			closeBoth()
			break
		}
	}
	closeBoth()
	<-done
	log.Info("peer-stream splice ended")
}

// HandleSession handles a WebSocket connection for an entire tmux session via PTY.
// Query params: name=<session>, cols=<cols>, rows=<rows>
func (h *PTYTerminalHandler) HandleSession(w http.ResponseWriter, r *http.Request) {
	sessionName := r.URL.Query().Get("name")
	if sessionName == "" {
		http.Error(w, "missing session name", http.StatusBadRequest)
		return
	}

	cols, _ := strconv.ParseUint(r.URL.Query().Get("cols"), 10, 16)
	rows, _ := strconv.ParseUint(r.URL.Query().Get("rows"), 10, 16)
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logrus.WithError(err).Warn("session ws upgrade failed")
		return
	}
	defer conn.Close()

	log := logrus.WithField("session", sessionName)
	log.Info("session ws client connected")

	if err := BridgePTY(conn, h.tmuxPath, sessionName, uint16(cols), uint16(rows), h.activityTracker, h.tmuxClient, h.tracker, log); err != nil {
		log.WithError(err).Error("failed to start PTY session")
		conn.WriteMessage(websocket.TextMessage, []byte("\r\n[termyard: failed to attach to session]\r\n"))
	}
}
