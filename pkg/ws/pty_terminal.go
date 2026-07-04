package ws

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
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

// isPing reports whether a text control frame is a heartbeat ping. Cheap
// substring check avoids a JSON unmarshal on every frame.
func isPing(msg []byte) bool {
	return bytes.Contains(msg, []byte(`"ping"`))
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
	// Spawn tmux attach in a PTY.
	ptySess, err := tmux.NewPTYSession(tmuxPath, session, cols, rows)
	if err != nil {
		return err
	}
	defer ptySess.Close()

	// writeMu serializes WS writes between the PTY reader goroutine and the
	// heartbeat pong reply path below.
	var writeMu sync.Mutex

	// Read goroutine: PTY → WebSocket (binary messages)
	done := make(chan struct{})
	artifactTail := ""
	lastOSC7CWD := ""
	seen := make(map[string]bool)
	const maxArtifactTail = 4096
	// ponytail: PTY exit or write error nudges conn.ReadMessage out so either side closing unwinds both loops.
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, err := ptySess.Read(buf)
			if err != nil {
				_ = conn.SetReadDeadline(time.Now())
				return
			}
			// Track activity
			if act != nil {
				act.Record(session, n)
			}
			writeMu.Lock()
			err = conn.WriteMessage(websocket.BinaryMessage, buf[:n])
			writeMu.Unlock()
			if err != nil {
				_ = conn.SetReadDeadline(time.Now())
				return
			}
			if client != nil && tracker != nil {
				combined := artifactTail + string(buf[:n])
				lastNewline := strings.LastIndex(combined, "\n")
				scanText := combined
				if lastNewline >= 0 {
					scanText = combined[:lastNewline+1]
					artifactTail = combined[lastNewline+1:]
				} else if len(combined) > maxArtifactTail {
					artifactTail = combined[len(combined)-maxArtifactTail:]
				} else {
					artifactTail = combined
				}
				if cwd, ok := toolevents.ParseOSC7CWD(scanText); ok {
					lastOSC7CWD = cwd
				}
				paths := toolevents.ScanArtifactPaths(scanText)
				osc8Paths := toolevents.ParseOSC8FilePaths(scanText)
				if len(paths) == 0 && len(osc8Paths) == 0 {
					continue
				}
				type artifactCandidate struct {
					path   string
					source string
				}
				candidates := make([]artifactCandidate, 0, len(paths)+len(osc8Paths))
				for _, p := range paths {
					candidates = append(candidates, artifactCandidate{path: p, source: "regex"})
				}
				for _, p := range osc8Paths {
					candidates = append(candidates, artifactCandidate{path: p, source: "osc8"})
				}
				batchSeen := make(map[string]struct{}, len(candidates))
				cwd := lastOSC7CWD
				if cwd == "" {
					cwd = toolevents.ResolveSessionCWD(client, session)
				}
				arts := make([]*toolevents.FileArtifact, 0, len(candidates))
				for _, cand := range candidates {
					if cand.path == "" {
						continue
					}
					if _, ok := batchSeen[cand.path]; ok {
						continue
					}
					batchSeen[cand.path] = struct{}{}
					if seen[cand.path] {
						continue
					}
					if art := toolevents.EnrichArtifact(cand.path, cwd, "", cand.source); art != nil {
						arts = append(arts, art)
						seen[cand.path] = true
					}
				}
				if len(arts) > 0 {
					tracker.RecordArtifacts(session, arts)
				}
			}
		}
	}()

	// Write goroutine: WebSocket → PTY
	// Text messages = JSON control, Binary messages = terminal I/O
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
			// Heartbeat from the browser liveness watchdog. Reply so the client
			// can detect a half-open socket and reconnect.
			if isPing(message) {
				writeMu.Lock()
				err := conn.WriteMessage(websocket.TextMessage, pongFrame)
				writeMu.Unlock()
				if err != nil {
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

	ptySess.Close()
	<-done
	log.Info("session ws client disconnected")
	return nil
}

// SpliceConns pumps bytes between an upgraded browser WS and a peer data conn.
// Teardown nudges both read deadlines and callers own Close.
func SpliceConns(browser, data *websocket.Conn, log *logrus.Entry) {
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
