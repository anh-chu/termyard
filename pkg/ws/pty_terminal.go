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
	"github.com/anh-chu/termyard/pkg/pty"
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

// bridgeSession pumps data between a pty.Session and a WebSocket connection.
// The scanner, if non-nil, receives output chunks for artifact detection.
func bridgeSession(conn *websocket.Conn, sess pty.Session, session string, act *activity.Tracker, scanner *artifactScanner, log *logrus.Entry) {
	bridgeSessionWithCB(conn, sess, session, act, scanner, nil, log)
}

func bridgeSessionWithCB(conn *websocket.Conn, sess pty.Session, session string, act *activity.Tracker, scanner *artifactScanner, onOutput func(), log *logrus.Entry) {
	conn.SetReadLimit(tmux.MaxPTYControlMessageBytes)

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
			n, readErr := sess.Read(buffer)
			if n > 0 {
				ptyReads++
				if act != nil {
					act.Record(session, n)
				}
				if onOutput != nil {
					onOutput()
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
			if err := tmux.HandlePTYControlMessage(sess, message); err != nil {
				log.WithError(err).Debug("control message failed")
			}

		case websocket.BinaryMessage:
			if _, err := sess.Write(message); err != nil {
				log.WithError(err).Debug("PTY write failed")
				break outer
			}
		}
	}

	sess.Close()
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
}

// BridgePTY spawns a tmux PTY for session and pumps it over an already-open
// conn. It does NOT upgrade and does NOT close conn — the caller owns conn's
// single Close.
func BridgePTY(conn *websocket.Conn, tmuxPath, session string, cols, rows uint16, act *activity.Tracker, client *tmux.Client, tracker *toolevents.Tracker, log *logrus.Entry) error {
	ptySess, err := tmux.NewPTYSession(tmuxPath, session, cols, rows)
	if err != nil {
		return err
	}

	scanContext, cancelScan := context.WithCancel(context.Background())
	defer cancelScan()
	scanner := newArtifactScanner(scanContext, client, tracker, session)

	bridgeSession(conn, ptySess, session, act, scanner, log)
	return nil
}

// BridgeDirectPTY pumps a direct PTY session over an already-open WebSocket
// connection. It uses the same bridge loop as BridgePTY but without the
// tmux-specific artifact scanner. An optional onOutput callback is invoked
// on every PTY read so the silence monitor can track daemon output activity.
func BridgeDirectPTY(conn *websocket.Conn, sess pty.Session, session string, act *activity.Tracker, log *logrus.Entry, onOutput ...func()) {
	var cb func()
	if len(onOutput) > 0 {
		cb = onOutput[0]
	}
	bridgeSessionWithCB(conn, sess, session, act, nil, cb, log)
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

// HandleDirectSession handles a WebSocket connection for a direct PTY session
// (no tmux). Query params: cols=<cols>, rows=<rows>, cwd=<dir>
func (h *PTYTerminalHandler) HandleDirectSession(w http.ResponseWriter, r *http.Request) {
	cols, _ := strconv.ParseUint(r.URL.Query().Get("cols"), 10, 16)
	rows, _ := strconv.ParseUint(r.URL.Query().Get("rows"), 10, 16)
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	cwd := r.URL.Query().Get("cwd")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logrus.WithError(err).Warn("direct session ws upgrade failed")
		return
	}
	defer conn.Close()

	sess, err := pty.NewDirectPTYSession("", uint16(cols), uint16(rows), cwd)
	if err != nil {
		logrus.WithError(err).Error("failed to start direct PTY session")
		conn.WriteMessage(websocket.TextMessage, []byte("\r\n[termyard: failed to create direct PTY session]\r\n"))
		return
	}

	log := logrus.WithFields(logrus.Fields{
		"cols": cols,
		"rows": rows,
		"cwd":  cwd,
	})
	log.Info("direct session ws client connected")

	BridgeDirectPTY(conn, sess, cwd, h.activityTracker, log)
}
