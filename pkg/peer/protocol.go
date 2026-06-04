package peer

import (
	"encoding/json"
	"time"

	"github.com/ekristen/guppi/pkg/activity"
	"github.com/ekristen/guppi/pkg/tmux"
	"github.com/ekristen/guppi/pkg/toolevents"
)

// Message types sent from peer to hub over control WebSocket
const (
	// MsgAuth is the challenge-response auth message
	MsgAuth = "auth"
	// MsgStateUpdate is a full session state snapshot
	MsgStateUpdate = "state-update"
	// MsgStateEvent is an incremental state change
	MsgStateEvent = "state-event"
	// MsgToolEvent forwards a local tool event
	MsgToolEvent = "tool-event"
	// MsgActivityUpdate sends periodic sparkline data
	MsgActivityUpdate = "activity-update"
	// MsgStats sends system stats
	MsgStats = "stats"
)

// Message types sent from hub to peer over control WebSocket
const (
	// MsgChallenge is the auth challenge from hub
	MsgChallenge = "challenge"
	// MsgAuthOK indicates successful authentication
	MsgAuthOK = "auth-ok"
	// MsgAuthFail indicates failed authentication
	MsgAuthFail = "auth-fail"
	// MsgPeerState is aggregated state from all other peers
	MsgPeerState = "peer-state"
	// MsgPeerConnected notifies that a new peer joined
	MsgPeerConnected = "peer-connected"
	// MsgPeerDisconnected notifies that a peer left
	MsgPeerDisconnected = "peer-disconnected"
	// MsgPTYOpen requests the peer to spawn a PTY
	MsgPTYOpen = "pty-open"
	// MsgPTYClose requests the peer to close a PTY
	MsgPTYClose = "pty-close"
	// MsgPTYResize requests the peer to resize a PTY
	MsgPTYResize = "pty-resize"
	// MsgSessionAction forwards an API action to the peer
	MsgSessionAction = "session-action"
	// MsgRequestState asks the peer for a full state refresh
	MsgRequestState = "request-state"
	// MsgForget notifies the receiver that the sender is forgetting them.
	// Receiver should remove sender from its peer store and close the link.
	MsgForget = "forget"
	// MsgPTYOutput carries PTY output bytes from the listener (where the
	// session lives) back to the dialer (where the browser is). Multiplexed
	// over the control WS so we never need a back-dial.
	MsgPTYOutput = "pty-output"
	// MsgPTYInput carries keystroke/input bytes from the dialer (browser) to
	// the listener (PTY).
	MsgPTYInput = "pty-input"
	// MsgPTYControl carries a JSON control frame (e.g. resize) for a PTY.
	MsgPTYControl = "pty-control"
	// MsgSessionAttrsSnapshot carries the full shared session-attribute map
	// (background/hidden per session key) to a freshly-connected peer for
	// per-key last-write-wins reconciliation.
	MsgSessionAttrsSnapshot = "session-attrs-snapshot"
	// MsgSessionAttrsDelta carries a single-key shared session-attribute
	// update across paired peers. Per-key LWW by UpdatedAt.
	MsgSessionAttrsDelta = "session-attrs-delta"
)

// Message is the envelope for all control WebSocket messages
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// AuthPayload is sent by the peer in response to a challenge
type AuthPayload struct {
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"` // base64-encoded signature of the challenge
}

// ChallengePayload is sent by the hub to initiate auth
type ChallengePayload struct {
	Challenge string `json:"challenge"` // base64-encoded random bytes
}

// StateUpdatePayload carries a full session snapshot from a peer
type StateUpdatePayload struct {
	Sessions []*tmux.Session `json:"sessions"`
	Version  string          `json:"version,omitempty"`
}

// StateEventPayload carries an incremental state change
type StateEventPayload struct {
	EventType string `json:"event_type"` // session-added, session-removed, sessions-changed
	Session   string `json:"session,omitempty"`
}

// ToolEventPayload wraps a tool event from a peer
type ToolEventPayload struct {
	Event *toolevents.Event `json:"event"`
}

// ActivityUpdatePayload carries sparkline data from a peer
type ActivityUpdatePayload struct {
	Snapshots []*activity.Snapshot `json:"snapshots"`
}

// StatsPayload carries system stats from a peer
type StatsPayload struct {
	Stats map[string]interface{} `json:"stats"`
}

// PeerStatePayload is the aggregated state sent from hub to peers
type PeerStatePayload struct {
	Hosts []HostInfo `json:"hosts"`
}

// HostInfo represents a peer's state as seen by the hub
type HostInfo struct {
	ID        string                 `json:"id"` // public key fingerprint
	Name      string                 `json:"name"`
	Version   string                 `json:"version,omitempty"`
	Local     bool                   `json:"local,omitempty"`
	Online    bool                   `json:"online"`
	Address   string                 `json:"address,omitempty"`
	Sessions  []*tmux.Session        `json:"sessions"`
	Activity  []*activity.Snapshot   `json:"activity,omitempty"`
	Stats     map[string]interface{} `json:"stats,omitempty"`
	LastSeen  time.Time              `json:"last_seen"`
}

// PeerNotifyPayload is sent when a peer connects or disconnects
type PeerNotifyPayload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PTYOpenPayload requests a peer to spawn a PTY session
type PTYOpenPayload struct {
	StreamID string `json:"stream_id"`
	Session  string `json:"session"`
	Cols     uint16 `json:"cols"`
	Rows     uint16 `json:"rows"`
}

// PTYClosePayload requests a peer to close a PTY session
type PTYClosePayload struct {
	StreamID string `json:"stream_id"`
}

// PTYResizePayload requests a peer to resize a PTY session
type PTYResizePayload struct {
	StreamID string `json:"stream_id"`
	Cols     uint16 `json:"cols"`
	Rows     uint16 `json:"rows"`
}

// PTYDataPayload carries opaque bytes for one PTY stream over the control WS.
// Used both directions: MsgPTYOutput (listener→dialer) and MsgPTYInput
// (dialer→listener). Data is base64-encoded so it fits in the JSON envelope.
type PTYDataPayload struct {
	StreamID string `json:"stream_id"`
	Data     string `json:"data"`
}

// PTYControlPayload carries a JSON control frame (e.g. resize) for a PTY.
type PTYControlPayload struct {
	StreamID string `json:"stream_id"`
	Control  string `json:"control"`
}

// SessionAttr is the shared attribute set for one global session key. Mirrors
// sessionattrs.Attr without importing that package into pkg/peer.
type SessionAttr struct {
	Background bool      `json:"background"`
	Hidden     bool      `json:"hidden"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SessionAttrsSnapshotPayload carries the full attribute map to a peer.
type SessionAttrsSnapshotPayload struct {
	Origin string                 `json:"origin"` // node fingerprint that produced the change
	Attrs  map[string]SessionAttr `json:"attrs"`
}

// SessionAttrsDeltaPayload carries a single-key attribute update.
type SessionAttrsDeltaPayload struct {
	Origin string      `json:"origin"`
	Key    string      `json:"key"`
	Attr   SessionAttr `json:"attr"`
}

// SessionActionPayload forwards a session API action to a peer
type SessionActionPayload struct {
	Action string          `json:"action"` // new, rename, select-window
	Params json.RawMessage `json:"params"`
}

// NewMessage creates a Message with a typed payload
func NewMessage(msgType string, payload interface{}) (*Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Message{
		Type:    msgType,
		Payload: json.RawMessage(data),
	}, nil
}
