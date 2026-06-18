package peer

import (
	"encoding/json"
	"time"

	"github.com/anh-chu/termyard/pkg/activity"
	"github.com/anh-chu/termyard/pkg/tmux"
	"github.com/anh-chu/termyard/pkg/toolevents"
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
	// MsgSessionAction forwards an API action to the peer
	MsgSessionAction = "session-action"
	// MsgRequestState asks the peer for a full state refresh
	MsgRequestState = "request-state"
	// MsgForget notifies the receiver that the sender is forgetting them.
	// Receiver should remove sender from its peer store and close the link.
	MsgForget = "forget"
	// MsgSessionAttrsSnapshot carries the full shared session-attribute map
	// (background/hidden per session key) to a freshly-connected peer for
	// per-key last-write-wins reconciliation.
	MsgSessionAttrsSnapshot = "session-attrs-snapshot"
	// MsgSessionAttrsDelta carries a single-key shared session-attribute
	// update across paired peers. Per-key LWW by UpdatedAt.
	MsgSessionAttrsDelta = "session-attrs-delta"
)

// Message types reserved for future per-terminal stream setup.
const (
	// MsgOpenTerminal asks a peer to prepare a dedicated PTY data connection,
	// correlated by one-time token. Sent over the control link.
	MsgOpenTerminal = "open-terminal"
	// MsgStreamToken is the first frame on /ws/peer-stream after auth; it
	// presents the correlation token. It does NOT authorize.
	MsgStreamToken = "stream-token"
)

// CapPerStream advertises dedicated /ws/peer-stream PTY data connections.
const CapPerStream = "per-stream"

// localCapabilities is what this build advertises in the hello.
var localCapabilities = []string{CapPerStream}

// Message is the envelope for all control WebSocket messages
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// AuthPayload is sent by the peer in response to a challenge
type AuthPayload struct {
	PublicKey    string   `json:"public_key"`
	Signature    string   `json:"signature"` // base64-encoded signature of the challenge
	Capabilities []string `json:"capabilities,omitempty"`
}

// ChallengePayload is sent by the hub to initiate auth
type ChallengePayload struct {
	Challenge string `json:"challenge"` // base64-encoded random bytes
}

// AuthOKPayload advertises the listener's capabilities on the hello.
type AuthOKPayload struct {
	Capabilities []string `json:"capabilities,omitempty"`
}

// StateUpdatePayload carries a full session snapshot from a peer
type StateUpdatePayload struct {
	Sessions []*tmux.Session `json:"sessions"`
	Version  string          `json:"version,omitempty"`
}

// StateEventPayload carries an incremental state change
type StateEventPayload struct {
	EventType string `json:"event_type"` // session-added, session-removed, session-renamed, sessions-changed
	Session   string `json:"session,omitempty"`
	Data      any    `json:"data,omitempty"`
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
	ID       string                 `json:"id"` // public key fingerprint
	Name     string                 `json:"name"`
	Version  string                 `json:"version,omitempty"`
	Local    bool                   `json:"local,omitempty"`
	Online   bool                   `json:"online"`
	Address  string                 `json:"address,omitempty"`
	Sessions []*tmux.Session        `json:"sessions"`
	Activity []*activity.Snapshot   `json:"activity,omitempty"`
	Stats    map[string]interface{} `json:"stats,omitempty"`
	LastSeen time.Time              `json:"last_seen"`
}

// PeerNotifyPayload is sent when a peer connects or disconnects
type PeerNotifyPayload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OpenTerminalPayload asks a peer to prepare a dedicated PTY data connection.
type OpenTerminalPayload struct {
	StreamID     string `json:"stream_id"`
	Session      string `json:"session"`
	Cols         uint16 `json:"cols"`
	Rows         uint16 `json:"rows"`
	Token        string `json:"token"`
	ViewerHostID string `json:"viewer_host_id"`
}

// StreamTokenPayload carries the correlation token on /ws/peer-stream.
type StreamTokenPayload struct {
	Token string `json:"token"`
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
