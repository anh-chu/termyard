package peer

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// PTYRelay sits on the dialer side. For each remote-session terminal opened
// in the browser, it tracks a stream_id -> browser WebSocket mapping. PTY
// bytes arriving from the peer (as MsgPTYOutput) are routed to the right
// browser WS; browser keystrokes are sent back to the peer as MsgPTYInput
// over the control channel.
type PTYRelay struct {
	mu      sync.RWMutex
	streams map[string]*streamBinding
}

type streamBinding struct {
	browserWS *websocket.Conn
	hostID    string
	writeMu   sync.Mutex // serialize writes to browserWS
}

// PumpSpec groups the data needed to re-open a remote PTY on a fresh peer link.
type PumpSpec struct {
	StreamID string
	HostID   string
	Session  string
	Cols     uint16
	Rows     uint16
	Mgr      *Manager
}

// NewPTYRelay creates a new PTY relay.
func NewPTYRelay() *PTYRelay {
	return &PTYRelay{streams: make(map[string]*streamBinding)}
}

// GenerateStreamID creates a random stream ID.
func GenerateStreamID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Register binds a stream_id to a browser WS connection. The dialer keeps the
// connection here for the lifetime of the terminal.
func (r *PTYRelay) Register(streamID, hostID string, browserWS *websocket.Conn) {
	r.mu.Lock()
	r.streams[streamID] = &streamBinding{browserWS: browserWS, hostID: hostID}
	r.mu.Unlock()
}

// Remove drops a stream binding.
func (r *PTYRelay) Remove(streamID string) {
	r.mu.Lock()
	delete(r.streams, streamID)
	r.mu.Unlock()
}

// HostFor returns the remote host ID bound to a stream, or "" if unknown.
func (r *PTYRelay) HostFor(streamID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.streams[streamID]; ok {
		return s.hostID
	}
	return ""
}

// DeliverOutput writes PTY output bytes received from the peer to the
// matching browser WebSocket. Returns true if a stream binding existed.
func (r *PTYRelay) DeliverOutput(streamID string, data []byte) bool {
	r.mu.RLock()
	s, ok := r.streams[streamID]
	r.mu.RUnlock()
	if !ok {
		return false
	}

	s.writeMu.Lock()
	_ = s.browserWS.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := s.browserWS.WriteMessage(websocket.BinaryMessage, data)
	_ = s.browserWS.SetWriteDeadline(time.Time{})
	s.writeMu.Unlock()
	if err != nil {
		logrus.WithField("stream", streamID).WithError(err).Debug("browser write failed")
		r.Remove(streamID)
		_ = s.browserWS.Close()
		return false
	}
	return true
}

// PumpBrowserToPeer reads from a browser WS and forwards keystrokes/control
// messages to the peer as MsgPTYInput / MsgPTYResize / MsgPTYClose over the
// shared control channel. If the peer link drops briefly, it waits for a fresh
// peer connection and re-opens the same stream instead of closing the browser.
func (r *PTYRelay) PumpBrowserToPeer(spec PumpSpec, browserWS *websocket.Conn, pc *PeerConnection) *PeerConnection {
	log := logrus.WithField("stream", spec.StreamID)

	var (
		stateMu sync.Mutex
		cur     = pc
	)
	done := make(chan struct{})
	defer close(done)

	waitForReattach := func(watched *PeerConnection) bool {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()

		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()

		for {
			select {
			case <-done:
				return false
			case <-timer.C:
				stateMu.Lock()
				cur = nil
				stateMu.Unlock()
				_ = browserWS.Close()
				return false
			case <-ticker.C:
				cand := spec.Mgr.GetPeerConnection(spec.HostID)
				if cand == nil || cand == watched {
					continue
				}
				select {
				case <-cand.Done():
					continue
				default:
				}

				stateMu.Lock()
				select {
				case <-done:
					stateMu.Unlock()
					return false
				default:
				}
				msg, _ := NewMessage(MsgPTYOpen, PTYOpenPayload{
					StreamID: spec.StreamID,
					Session:  spec.Session,
					Cols:     spec.Cols,
					Rows:     spec.Rows,
				})
				if !cand.Enqueue(msg) {
					stateMu.Unlock()
					continue
				}
				cur = cand
				stateMu.Unlock()
				return true
			}
		}
	}

	go func() {
		for {
			stateMu.Lock()
			watched := cur
			stateMu.Unlock()
			if watched == nil {
				return
			}
			select {
			case <-watched.Done():
				if !waitForReattach(watched) {
					return
				}
			case <-done:
				return
			}
		}
	}()

	for {
		msgType, data, err := browserWS.ReadMessage()
		if err != nil {
			stateMu.Lock()
			c := cur
			stateMu.Unlock()
			return c
		}
		stateMu.Lock()
		c := cur
		stateMu.Unlock()
		if c == nil {
			return nil
		}
		switch msgType {
		case websocket.BinaryMessage:
			// Keystrokes go out as a binary frame on the hi-priority lane: no
			// base64, no JSON, never queued behind bulky control-plane traffic.
			frame := EncodePTYFrame(FramePTYInput, spec.StreamID, data)
			if !c.EnqueueBinaryHi(frame) {
				log.Debug("pty-input queue full, dropping")
			}
		case websocket.TextMessage:
			// Heartbeat from the browser liveness watchdog terminates here — the
			// browser↔server edge socket is what goes half-open, not the peer
			// link — so answer locally instead of round-tripping to the peer.
			if bytes.Contains(data, []byte(`"ping"`)) {
				r.mu.RLock()
				s := r.streams[spec.StreamID]
				r.mu.RUnlock()
				if s != nil {
					s.writeMu.Lock()
					err := s.browserWS.WriteMessage(websocket.TextMessage, []byte(`{"type":"pong"}`))
					s.writeMu.Unlock()
					if err != nil {
						stateMu.Lock()
						c := cur
						stateMu.Unlock()
						return c
					}
				}
				continue
			}
			// Forward as a resize/control message. The remote side uses
			// HandlePTYControlMessage which parses JSON, so wrap it in a
			// MsgPTYInput envelope tagged as control via a magic prefix is
			// overkill — easier: parse the resize JSON locally and forward
			// MsgPTYResize. To keep the wire small, just send the raw text
			// as MsgPTYInput with a separate Control flag.
			payload := PTYControlPayload{
				StreamID: spec.StreamID,
				Control:  string(data),
			}
			msg, err := NewMessage(MsgPTYControl, payload)
			if err != nil {
				continue
			}
			// Resize/control is interactive — hi lane.
			if !c.EnqueueHi(msg) {
				log.Debug("pty-control queue full, dropping")
			}
		}
	}
}
