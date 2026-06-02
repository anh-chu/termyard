package ws

import (
	"bytes"
	"net/http"
	"strconv"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"github.com/ekristen/guppi/pkg/activity"
	"github.com/ekristen/guppi/pkg/tmux"
)

// pongFrame is the canonical reply to a browser heartbeat ping.
var pongFrame = []byte(`{"type":"pong"}`)

// isPing reports whether a text control frame is a heartbeat ping. Cheap
// substring check avoids a JSON unmarshal on every frame.
func isPing(msg []byte) bool {
	return bytes.Contains(msg, []byte(`"ping"`))
}

// PTYTerminalHandler handles WebSocket connections backed by a PTY running tmux attach
type PTYTerminalHandler struct {
	tmuxPath        string
	activityTracker *activity.Tracker
}

// NewPTYTerminalHandler creates a new PTY-based terminal handler
func NewPTYTerminalHandler(tmuxPath string, activityTracker *activity.Tracker) *PTYTerminalHandler {
	return &PTYTerminalHandler{
		tmuxPath:        tmuxPath,
		activityTracker: activityTracker,
	}
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

	// Spawn tmux attach in a PTY
	ptySess, err := tmux.NewPTYSession(h.tmuxPath, sessionName, uint16(cols), uint16(rows))
	if err != nil {
		log.WithError(err).Error("failed to start PTY session")
		conn.WriteMessage(websocket.TextMessage, []byte("\r\n[guppi: failed to attach to session]\r\n"))
		return
	}
	defer ptySess.Close()

	// writeMu serializes WS writes between the PTY reader goroutine and the
	// heartbeat pong reply path below.
	var writeMu sync.Mutex

	// Read goroutine: PTY → WebSocket (binary messages)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, err := ptySess.Read(buf)
			if err != nil {
				return
			}
			// Track activity
			if h.activityTracker != nil {
				h.activityTracker.Record(sessionName, n)
			}
			writeMu.Lock()
			err = conn.WriteMessage(websocket.BinaryMessage, buf[:n])
			writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	// Write goroutine: WebSocket → PTY
	// Text messages = JSON control, Binary messages = terminal I/O
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
			// Heartbeat from the browser liveness watchdog. Reply so the client
			// can detect a half-open socket and reconnect.
			if isPing(message) {
				writeMu.Lock()
				err := conn.WriteMessage(websocket.TextMessage, pongFrame)
				writeMu.Unlock()
				if err != nil {
					break
				}
				continue
			}
			if err := tmux.HandlePTYControlMessage(ptySess, message); err != nil {
				log.WithError(err).Debug("control message failed")
			}

		case websocket.BinaryMessage:
			if _, err := ptySess.Write(message); err != nil {
				log.WithError(err).Debug("PTY write failed")
				break
			}
		}
	}

	<-done
	log.Info("session ws client disconnected")
}
