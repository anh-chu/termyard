package peer

import (
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"github.com/ekristen/guppi/pkg/activity"
	"github.com/ekristen/guppi/pkg/tmux"
)

// PTYManager manages local PTY sessions spawned on behalf of remote browsers.
type PTYManager struct {
	mu       sync.RWMutex
	sessions map[string]*ActivePTY
	tmuxPath string
	activity *activity.Tracker
}

// ActivePTY is a local PTY session being relayed to a remote browser.
type ActivePTY struct {
	StreamID string
	PTY      *tmux.PTYSession
	HubWS    *websocket.Conn
}

// NewPTYManager creates a new PTY manager.
func NewPTYManager(tmuxPath string, actTracker *activity.Tracker) *PTYManager {
	return &PTYManager{
		sessions: make(map[string]*ActivePTY),
		tmuxPath: tmuxPath,
		activity: actTracker,
	}
}

// Open spawns a local PTY and connects it to the peer via /ws/peer-pty.
// peerAddr is the remote node's reachable host:port for back-dial.
func (pm *PTYManager) Open(req PTYOpenPayload, peerAddr string) {
	log := logrus.WithFields(logrus.Fields{
		"stream":  req.StreamID,
		"session": req.Session,
	})

	ptySess, err := tmux.NewPTYSession(pm.tmuxPath, req.Session, req.Cols, req.Rows)
	if err != nil {
		log.WithError(err).Error("failed to spawn PTY")
		return
	}

	hubWS, err := pm.connectPTYWebSocket(req.StreamID, peerAddr)
	if err != nil {
		log.WithError(err).Error("failed to connect PTY WebSocket to peer")
		ptySess.Close()
		return
	}

	active := &ActivePTY{
		StreamID: req.StreamID,
		PTY:      ptySess,
		HubWS:    hubWS,
	}

	pm.mu.Lock()
	pm.sessions[req.StreamID] = active
	pm.mu.Unlock()

	log.Info("PTY relay started")

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 32*1024)
		for {
			n, err := ptySess.Read(buf)
			if err != nil {
				return
			}
			if pm.activity != nil {
				pm.activity.Record(req.Session, n)
			}
			if err := hubWS.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				return
			}
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			msgType, data, err := hubWS.ReadMessage()
			if err != nil {
				return
			}
			switch msgType {
			case websocket.BinaryMessage:
				if _, err := ptySess.Write(data); err != nil {
					return
				}
			case websocket.TextMessage:
				if err := tmux.HandlePTYControlMessage(ptySess, data); err != nil {
					log.WithError(err).Debug("control message failed")
				}
			}
		}
	}()

	<-done
	pm.cleanup(req.StreamID)
	<-done
	log.Info("PTY relay stopped")
}

// Close closes a PTY session by stream ID.
func (pm *PTYManager) Close(streamID string) {
	pm.cleanup(streamID)
}

// Resize resizes a PTY session.
func (pm *PTYManager) Resize(streamID string, cols, rows uint16) {
	pm.mu.RLock()
	active, ok := pm.sessions[streamID]
	pm.mu.RUnlock()

	if ok {
		if err := active.PTY.Resize(cols, rows); err != nil {
			logrus.WithField("stream", streamID).WithError(err).Debug("resize failed")
		}
	}
}

func (pm *PTYManager) cleanup(streamID string) {
	pm.mu.Lock()
	active, ok := pm.sessions[streamID]
	if ok {
		delete(pm.sessions, streamID)
	}
	pm.mu.Unlock()

	if ok {
		active.PTY.Close()
		active.HubWS.Close()
	}
}

func (pm *PTYManager) connectPTYWebSocket(streamID, peerAddr string) (*websocket.Conn, error) {
	if peerAddr == "" {
		return nil, fmtErrNoPeerAddr
	}
	u := &url.URL{Scheme: "ws", Host: peerAddr, Path: "/ws/peer-pty"}
	q := u.Query()
	q.Set("stream", streamID)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// fmtErrNoPeerAddr is the sentinel error when we can't determine a peer
// address for PTY relay back-dial.
var fmtErrNoPeerAddr = &peerAddrErr{msg: "no peer address available for PTY back-dial"}

type peerAddrErr struct{ msg string }

func (e *peerAddrErr) Error() string { return e.msg }
