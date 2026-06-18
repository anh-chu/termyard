package peer

import (
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/anh-chu/termyard/pkg/activity"
	"github.com/anh-chu/termyard/pkg/tmux"
)

// PTYManager owns local PTYs spawned on behalf of a remote browser. Output
// bytes are pushed back over the peer's control WebSocket as MsgPTYOutput
// (multiplexed by stream_id), so no inbound TCP connection from the listener
// to the dialer is ever required.
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
	cancel   chan struct{}
	once     sync.Once
}

// NewPTYManager creates a new PTY manager.
func NewPTYManager(tmuxPath string, actTracker *activity.Tracker) *PTYManager {
	return &PTYManager{
		sessions: make(map[string]*ActivePTY),
		tmuxPath: tmuxPath,
		activity: actTracker,
	}
}

// Open spawns a local PTY and starts streaming output back over pc as
// MsgPTYOutput. Browser keystrokes arrive as MsgPTYInput via the session
// dispatcher and are written through Write.
func (pm *PTYManager) Open(req PTYOpenPayload, pc *PeerConnection) {
	log := logrus.WithFields(logrus.Fields{
		"stream":  req.StreamID,
		"session": req.Session,
	})

	Trace("listener", req.StreamID, req.Session, "pty-open-received", 0, "")

	ptySess, err := tmux.NewPTYSession(pm.tmuxPath, req.Session, req.Cols, req.Rows)
	if err != nil {
		log.WithError(err).Error("failed to spawn PTY")
		Trace("listener", req.StreamID, req.Session, "pty-spawn-error", 0, err.Error())
		return
	}
	Trace("listener", req.StreamID, req.Session, "pty-spawned", 0, "")

	active := &ActivePTY{
		StreamID: req.StreamID,
		PTY:      ptySess,
		cancel:   make(chan struct{}),
	}

	pm.mu.Lock()
	pm.sessions[req.StreamID] = active
	pm.mu.Unlock()

	log.Info("PTY relay started (control-multiplexed)")

	// Reader: PTY -> control WS as MsgPTYOutput.
	go func() {
		buf := make([]byte, 32*1024)
		first := true
		var totalRead int
		var reads int
		defer func() {
			Trace("listener", req.StreamID, req.Session, "pty-reader-exit", totalRead, fmt.Sprintf("reads=%d", reads))
			pm.cleanup(req.StreamID)
		}()
		for {
			select {
			case <-active.cancel:
				return
			default:
			}
			n, err := ptySess.Read(buf)
			if err != nil {
				Trace("listener", req.StreamID, req.Session, "pty-read-error", totalRead, err.Error())
				return
			}
			totalRead += n
			reads++
			if first {
				// First bytes the PTY produced = first paint generated. Compare
				// against the viewer's deliver-first to isolate read vs deliver
				// latency.
				Trace("listener", req.StreamID, req.Session, "first-read", n, "")
				first = false
			}
			if pm.activity != nil {
				pm.activity.Record(req.Session, n)
			}
			// Binary frame on the hi-priority lane: no base64, no JSON.
			frame := EncodePTYFrame(FramePTYOutput, req.StreamID, buf[:n])
			if !pc.EnqueueBinaryHi(frame) {
				Trace("listener", req.StreamID, req.Session, "enqueue-fail", n, "hi-lane full / pc closed")
				return
			}
		}
	}()
}

// Write delivers input bytes from the remote browser to the local PTY.
func (pm *PTYManager) Write(streamID string, data []byte) {
	pm.mu.RLock()
	active, ok := pm.sessions[streamID]
	pm.mu.RUnlock()
	if !ok {
		return
	}
	if _, err := active.PTY.Write(data); err != nil {
		logrus.WithField("stream", streamID).WithError(err).Debug("pty write failed")
	}
}

// HandleControl applies a JSON text control message (e.g. resize) to a PTY.
func (pm *PTYManager) HandleControl(streamID string, data []byte) {
	pm.mu.RLock()
	active, ok := pm.sessions[streamID]
	pm.mu.RUnlock()
	if !ok {
		return
	}
	if err := tmux.HandlePTYControlMessage(active.PTY, data); err != nil {
		logrus.WithField("stream", streamID).WithError(err).Debug("control message failed")
	}
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
		active.once.Do(func() { close(active.cancel) })
		active.PTY.Close()
	}
}
