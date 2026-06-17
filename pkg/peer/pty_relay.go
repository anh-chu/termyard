package peer

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"sync"

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
	defer s.writeMu.Unlock()
	if err := s.browserWS.WriteMessage(websocket.BinaryMessage, data); err != nil {
		logrus.WithField("stream", streamID).WithError(err).Debug("browser write failed")
		return false
	}
	return true
}

// PumpBrowserToPeer reads from a browser WS and forwards keystrokes/control
// messages to the peer as MsgPTYInput / MsgPTYResize / MsgPTYClose over the
// shared control channel. Blocks until the browser side closes.
func (r *PTYRelay) PumpBrowserToPeer(streamID string, browserWS *websocket.Conn, pc *PeerConnection) {
	log := logrus.WithField("stream", streamID)

	// If the underlying peer link dies (redial, role flip, transient drop), pc
	// is closed and Enqueue would silently drop every keystroke while output
	// keeps flowing over the new link — the terminal looks alive but eats no
	// input. Close the browser WS so the client's onclose fires and it
	// reconnects, picking up the fresh peer connection. Without this the user
	// has to manually switch sessions and back to recover.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-pc.Done():
			log.Debug("peer connection closed; dropping browser WS to force reconnect")
			_ = browserWS.Close()
		case <-done:
		}
	}()

	for {
		msgType, data, err := browserWS.ReadMessage()
		if err != nil {
			return
		}
		switch msgType {
		case websocket.BinaryMessage:
			// Keystrokes go out as a binary frame on the hi-priority lane: no
			// base64, no JSON, never queued behind bulky control-plane traffic.
			frame := EncodePTYFrame(FramePTYInput, streamID, data)
			if !pc.EnqueueBinaryHi(frame) {
				log.Debug("pty-input queue full, dropping")
			}
		case websocket.TextMessage:
			// Heartbeat from the browser liveness watchdog terminates here — the
			// browser↔server edge socket is what goes half-open, not the peer
			// link — so answer locally instead of round-tripping to the peer.
			if bytes.Contains(data, []byte(`"ping"`)) {
				r.mu.RLock()
				s := r.streams[streamID]
				r.mu.RUnlock()
				if s != nil {
					s.writeMu.Lock()
					err := s.browserWS.WriteMessage(websocket.TextMessage, []byte(`{"type":"pong"}`))
					s.writeMu.Unlock()
					if err != nil {
						return
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
				StreamID: streamID,
				Control:  string(data),
			}
			msg, err := NewMessage(MsgPTYControl, payload)
			if err != nil {
				continue
			}
			// Resize/control is interactive — hi lane.
			if !pc.EnqueueHi(msg) {
				log.Debug("pty-control queue full, dropping")
			}
		}
	}
}
