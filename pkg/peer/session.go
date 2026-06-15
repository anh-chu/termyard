package peer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"github.com/ekristen/guppi/pkg/activity"
	"github.com/ekristen/guppi/pkg/common"
	"github.com/ekristen/guppi/pkg/git"
	"github.com/ekristen/guppi/pkg/identity"
	"github.com/ekristen/guppi/pkg/recovery"
	"github.com/ekristen/guppi/pkg/state"
	"github.com/ekristen/guppi/pkg/stats"
	"github.com/ekristen/guppi/pkg/tmux"
	"github.com/ekristen/guppi/pkg/toolevents"
)

// Role tells the session which side it is. Affects only the initial
// peer-state push.
type Role int

const (
	RoleDialer Role = iota
	RoleListener
)

// SessionAttrsSink is the slice of pkg/sessionattrs the session loop needs in
// order to merge shared session-attribute updates received from paired peers.
// Kept narrow so pkg/peer doesn't pull pkg/sessionattrs directly.
type SessionAttrsSink interface {
	// ApplyRemoteDelta merges a single-key delta via per-key LWW. accepted=false
	// means the local copy was newer-or-equal and the delta was dropped.
	ApplyRemoteDelta(key string, background, hidden bool, updatedAt time.Time) (accepted bool, err error)
	// ApplyRemoteSnapshot merges a full peer snapshot via per-key LWW, returning
	// the keys that changed locally.
	ApplyRemoteSnapshot(attrs map[string]SessionAttr) (changed []string, err error)
	// SnapshotAttrs returns the full local attribute map to seed a fresh peer.
	SnapshotAttrs() map[string]SessionAttr
}

// BrowserBroadcaster pushes a JSON message to every connected browser. Used
// to forward session-attrs-updated events to the local UI after we accept a
// remote update from a paired peer.
type BrowserBroadcaster interface {
	BroadcastJSON(v interface{})
}

// SessionDeps groups the runtime dependencies needed by a peer session.
type SessionDeps struct {
	Manager      *Manager
	LocalMgr     *state.Manager
	Identity     *identity.Identity
	ActTracker   *activity.Tracker
	ToolTracker  *toolevents.Tracker
	PeerStore    *identity.PeerStore
	TmuxClient   *tmux.Client
	PTYManager   *PTYManager
	PTYRelay     *PTYRelay // dialer-side relay; receives MsgPTYOutput and routes to browser
	AttrsSink    SessionAttrsSink
	BrowserHub   BrowserBroadcaster
}

// connWriter serializes WebSocket writes from multiple goroutines.
type connWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *connWriter) writeJSON(msg *Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Bound the write so a stuck/half-open peer socket can't block the writer
	// goroutine indefinitely and silently back up the send queue.
	_ = w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := w.conn.WriteJSON(msg)
	_ = w.conn.SetWriteDeadline(time.Time{})
	return err
}

func (w *connWriter) writeControl(messageType int, data []byte, deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteControl(messageType, data, deadline)
}

// runSession owns the post-auth lifetime of one peer connection. It blocks
// until conn is closed or ctx is canceled. Same code runs on both ends.
func runSession(
	ctx context.Context,
	role Role,
	conn *websocket.Conn,
	peerInfo identity.Peer,
	address string,
	deps SessionDeps,
) error {
	peerID := peerInfo.Fingerprint()
	log := logrus.WithFields(logrus.Fields{"peer": peerInfo.Name, "id": peerID})

	cw := &connWriter{conn: conn}

	pc := NewPeerConnection(peerID, 64)
	if !deps.Manager.TryRegisterPeer(peerID, peerInfo.Name, peerInfo.PublicKey, address, pc) {
		return fmt.Errorf("peer already connected")
	}

	sessionCtx, cancel := context.WithCancel(ctx)

	// Teardown order is crucial:
	//   1. cancel session ctx — stops background producers
	//   2. close websocket — unblocks read loop on the other goroutine, drains pings
	//   3. unregister from manager — stops new HTTP-side producers from finding pc
	//   4. close pc — ends writer loop
	//   5. wait for writer to drain
	writerDone := make(chan struct{})
	defer func() {
		cancel()
		_ = conn.Close()
		deps.Manager.UnregisterPeer(peerID)
		pc.Close()
		<-writerDone
	}()

	// If parent ctx is canceled while we're in ReadJSON, close conn to unblock.
	go func() {
		select {
		case <-sessionCtx.Done():
			_ = conn.Close()
		}
	}()

	// Liveness: ping/pong with 15s ping, 30s read deadline.
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		return nil
	})
	conn.SetPingHandler(func(data string) error {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		return cw.writeControl(websocket.PongMessage, []byte(data), time.Now().Add(5*time.Second))
	})
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Writer goroutine drains pc.Recv() to conn. Any write failure cancels the
	// session so the read loop unblocks.
	go func() {
		defer close(writerDone)
		for msg := range pc.Recv() {
			if err := cw.writeJSON(msg); err != nil {
				log.WithError(err).Debug("session write failed")
				cancel()
				return
			}
		}
	}()

	// Initial pushes — both sides advertise themselves.
	sendStateUpdate(pc, deps)
	sendInitialPeerState(pc, deps, peerID)
	sendInitialSessionAttrs(pc, deps)

	// Background loops.
	go pingLoop(sessionCtx, cw)
	go periodicActivity(sessionCtx, pc, deps)
	go periodicStats(sessionCtx, pc, deps)
	go forwardStateEvents(sessionCtx, pc, deps)
	go forwardToolEvents(sessionCtx, pc, deps, peerID)
	go forwardPeerStateChanges(sessionCtx, pc, deps, peerID)

	_ = role

	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.WithError(err).Debug("session read error")
			}
			return err
		}
		if msg.Type == MsgForget {
			log.Info("peer sent forget — removing")
			if err := deps.PeerStore.RemoveByPublicKey(peerInfo.PublicKey); err != nil {
				log.WithError(err).Debug("forget remove failed")
			}
			deps.Manager.RemoveHost(peerID)
			return fmt.Errorf("peer forgot us")
		}
		handleSessionMessage(peerID, &msg, pc, deps, log)
	}
}

// sendInitialSessionAttrs pushes our full shared session-attribute map once on
// link-up so the remote can reconcile via per-key LWW.
func sendInitialSessionAttrs(pc *PeerConnection, deps SessionDeps) {
	if deps.AttrsSink == nil {
		return
	}
	attrs := deps.AttrsSink.SnapshotAttrs()
	if len(attrs) == 0 {
		return
	}
	msg, err := NewMessage(MsgSessionAttrsSnapshot, SessionAttrsSnapshotPayload{
		Origin: deps.Identity.Fingerprint(),
		Attrs:  attrs,
	})
	if err != nil {
		return
	}
	pc.Enqueue(msg)
}

// sendInitialPeerState pushes a snapshot containing only the local host
// (transitivity off, see plan §3.5).
func sendInitialPeerState(pc *PeerConnection, deps SessionDeps, remotePeerID string) {
	hosts := deps.Manager.GetHostsForPeer(remotePeerID)
	msg, err := NewMessage(MsgPeerState, PeerStatePayload{Hosts: hosts})
	if err != nil {
		return
	}
	pc.Enqueue(msg)
}

func sendStateUpdate(pc *PeerConnection, deps SessionDeps) {
	sessions := deps.LocalMgr.GetSessions()
	msg, err := NewMessage(MsgStateUpdate, StateUpdatePayload{Sessions: sessions, Version: common.VERSION})
	if err != nil {
		return
	}
	pc.Enqueue(msg)
}

func pingLoop(ctx context.Context, cw *connWriter) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := cw.writeControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

func periodicActivity(ctx context.Context, pc *PeerConnection, deps SessionDeps) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	localID := deps.Manager.LocalID()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			snapshots := deps.ActTracker.GetAll()
			for _, s := range snapshots {
				if s.Host == "" {
					s.Host = localID
				}
			}
			msg, err := NewMessage(MsgActivityUpdate, ActivityUpdatePayload{Snapshots: snapshots})
			if err != nil {
				continue
			}
			pc.Enqueue(msg)
		}
	}
}

func collectStats(deps SessionDeps) map[string]interface{} {
	s := stats.SystemStats()
	sessions := deps.LocalMgr.GetSessions()
	s["processes"] = stats.ProcessCountsFromSessions(sessions)
	return s
}

func periodicStats(ctx context.Context, pc *PeerConnection, deps SessionDeps) {
	// Send immediately.
	if msg, err := NewMessage(MsgStats, StatsPayload{Stats: collectStats(deps)}); err == nil {
		pc.Enqueue(msg)
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if msg, err := NewMessage(MsgStats, StatsPayload{Stats: collectStats(deps)}); err == nil {
				pc.Enqueue(msg)
			}
		}
	}
}

func forwardStateEvents(ctx context.Context, pc *PeerConnection, deps SessionDeps) {
	ch := deps.LocalMgr.Subscribe()
	defer deps.LocalMgr.Unsubscribe(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			msg, err := NewMessage(MsgStateEvent, StateEventPayload{
				EventType: evt.Type,
				Session:   evt.Session,
			})
			if err != nil {
				continue
			}
			pc.Enqueue(msg)
			sendStateUpdate(pc, deps)
		}
	}
}

func forwardToolEvents(ctx context.Context, pc *PeerConnection, deps SessionDeps, remotePeerID string) {
	ch := deps.ToolTracker.Subscribe()
	defer deps.ToolTracker.Unsubscribe(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			// Don't echo the peer's own events back — this would create a
			// ping-pong loop: peer A's event arrives, gets stamped Host=A,
			// records locally, broadcasts to subscribers, and would forward
			// straight back to A, which re-stamps and re-forwards forever.
			if evt.Host == remotePeerID {
				continue
			}
			// Only forward our own local-origin events; we don't transitively
			// relay other peers' events.
			if evt.Host != "" && evt.Host != deps.Manager.LocalID() {
				continue
			}
			msg, err := NewMessage(MsgToolEvent, ToolEventPayload{Event: evt})
			if err != nil {
				continue
			}
			pc.Enqueue(msg)
		}
	}
}

// forwardPeerStateChanges pushes a peer-state snapshot whenever local state
// changes, so the remote sees our updated session list / activity / stats.
func forwardPeerStateChanges(ctx context.Context, pc *PeerConnection, deps SessionDeps, remotePeerID string) {
	ch := deps.Manager.Subscribe()
	defer deps.Manager.Unsubscribe(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			// Don't echo the peer's own events back.
			if evt.Host == remotePeerID {
				continue
			}
			// Only push peer-state when our local host changed; we don't
			// transitively expose other peers.
			if evt.Host != "" && evt.Host != deps.Manager.LocalID() {
				continue
			}
			msg, err := NewMessage(MsgPeerState, PeerStatePayload{
				Hosts: deps.Manager.GetHostsForPeer(remotePeerID),
			})
			if err != nil {
				continue
			}
			pc.Enqueue(msg)
		}
	}
}

// handleSessionMessage dispatches messages received from the remote peer.
func handleSessionMessage(peerID string, msg *Message, pc *PeerConnection, deps SessionDeps, log *logrus.Entry) {
	switch msg.Type {
	case MsgStateUpdate:
		var p StateUpdatePayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			log.WithError(err).Debug("invalid state-update")
			return
		}
		deps.Manager.UpdatePeerSessions(peerID, p.Sessions)
		if p.Version != "" {
			deps.Manager.UpdatePeerVersion(peerID, p.Version)
		}

	case MsgStateEvent:
		var p StateEventPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		deps.Manager.UpdatePeerSessions(peerID, getPeerSessions(deps.Manager, peerID))

	case MsgToolEvent:
		var p ToolEventPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if p.Event != nil {
			p.Event.Host = peerID
			p.Event.HostName = deps.Manager.GetHostName(peerID)
			deps.ToolTracker.Record(p.Event)
		}

	case MsgActivityUpdate:
		var p ActivityUpdatePayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		for _, s := range p.Snapshots {
			s.Host = peerID
		}
		deps.Manager.UpdatePeerActivity(peerID, p.Snapshots)

	case MsgStats:
		var p StatsPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		deps.Manager.UpdatePeerStats(peerID, p.Stats)

	case MsgPeerState:
		var p PeerStatePayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		for _, host := range p.Hosts {
			if deps.Manager.IsLocal(host.ID) {
				continue
			}
			deps.Manager.UpdatePeerSessions(host.ID, host.Sessions)
			if host.Online && !deps.Manager.HasHost(host.ID) {
				deps.Manager.RegisterPeer(host.ID, host.Name, "", nil)
			}
		}

	case MsgPeerConnected:
		var p PeerNotifyPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		deps.Manager.RegisterPeer(p.ID, p.Name, "", nil)

	case MsgPeerDisconnected:
		var p PeerNotifyPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		deps.Manager.UnregisterPeer(p.ID)

	case MsgPTYOpen:
		var p PTYOpenPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if deps.PTYManager != nil {
			go deps.PTYManager.Open(p, pc)
		}

	case MsgPTYInput:
		var p PTYDataPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if deps.PTYManager != nil {
			data, derr := base64.StdEncoding.DecodeString(p.Data)
			if derr == nil {
				deps.PTYManager.Write(p.StreamID, data)
			}
		}

	case MsgPTYControl:
		var p PTYControlPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if deps.PTYManager != nil {
			deps.PTYManager.HandleControl(p.StreamID, []byte(p.Control))
		}

	case MsgPTYOutput:
		var p PTYDataPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if deps.PTYRelay != nil {
			data, derr := base64.StdEncoding.DecodeString(p.Data)
			if derr == nil {
				deps.PTYRelay.DeliverOutput(p.StreamID, data)
			}
		}

	case MsgPTYClose:
		var p PTYClosePayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if deps.PTYManager != nil {
			deps.PTYManager.Close(p.StreamID)
		}

	case MsgPTYResize:
		var p PTYResizePayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if deps.PTYManager != nil {
			deps.PTYManager.Resize(p.StreamID, p.Cols, p.Rows)
		}

	case MsgSessionAction:
		var p SessionActionPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		handleSessionAction(&p, pc, deps, log)

	case MsgRequestState:
		sendStateUpdate(pc, deps)

	case MsgSessionAttrsSnapshot:
		var p SessionAttrsSnapshotPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			log.WithError(err).Debug("invalid session-attrs-snapshot")
			return
		}
		if p.Origin == deps.Identity.Fingerprint() || deps.AttrsSink == nil {
			return
		}
		changed, err := deps.AttrsSink.ApplyRemoteSnapshot(p.Attrs)
		if err != nil {
			log.WithError(err).Warn("apply remote session-attrs snapshot failed")
			return
		}
		if len(changed) > 0 && deps.BrowserHub != nil {
			deps.BrowserHub.BroadcastJSON(map[string]interface{}{
				"type":   "session-attrs-updated",
				"origin": p.Origin,
			})
		}
		log.WithField("origin", p.Origin).WithField("changed", len(changed)).Debug("applied remote session-attrs snapshot")

	case MsgSessionAttrsDelta:
		var p SessionAttrsDeltaPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			log.WithError(err).Debug("invalid session-attrs-delta")
			return
		}
		if p.Origin == deps.Identity.Fingerprint() || deps.AttrsSink == nil {
			return
		}
		accepted, err := deps.AttrsSink.ApplyRemoteDelta(p.Key, p.Attr.Background, p.Attr.Hidden, p.Attr.UpdatedAt)
		if err != nil {
			log.WithError(err).Warn("apply remote session-attrs delta failed")
			return
		}
		if !accepted {
			return
		}
		if deps.BrowserHub != nil {
			deps.BrowserHub.BroadcastJSON(map[string]interface{}{
				"type":   "session-attrs-updated",
				"origin": p.Origin,
				"key":    p.Key,
			})
		}
		log.WithField("origin", p.Origin).WithField("key", p.Key).Debug("applied remote session-attrs delta")

	default:
		log.WithField("type", msg.Type).Debug("unknown session message")
	}
}

func handleSessionAction(payload *SessionActionPayload, pc *PeerConnection, deps SessionDeps, log *logrus.Entry) {
	if deps.TmuxClient == nil {
		log.Warn("no tmux client available for session action")
		return
	}
	switch payload.Action {
	case "new":
		var params struct {
			Name            string `json:"name"`
			Path            string `json:"path,omitempty"`
			Command         string `json:"command,omitempty"`
			WorktreeBranch  string `json:"worktree_branch,omitempty"`
		}
		if err := json.Unmarshal(payload.Params, &params); err != nil || params.Name == "" {
			return
		}
		// Create worktree locally if requested. Same logic as the local
		// session create path in pkg/server.
		if params.WorktreeBranch != "" && params.Path != "" {
			expanded := params.Path
			if strings.HasPrefix(expanded, "~") {
				if home, err := os.UserHomeDir(); err == nil && home != "" {
					expanded = home + expanded[1:]
				}
			}
			if !filepath.IsAbs(expanded) {
				if home, err := os.UserHomeDir(); err == nil {
					expanded = filepath.Join(home, expanded)
				}
			}
			sanitized := strings.ReplaceAll(params.WorktreeBranch, "/", "-")
			worktreesDir := filepath.Join(expanded, ".worktrees")
			if err := os.MkdirAll(worktreesDir, 0755); err != nil {
				log.WithError(err).Warn("mkdir .worktrees failed on peer")
				return
			}
			destPath := filepath.Join(worktreesDir, sanitized)
			if err := git.CreateWorktree(expanded, params.WorktreeBranch, destPath); err != nil {
				log.WithError(err).Warn("git worktree add failed on peer")
				return
			}
			params.Path = destPath
		}
		if err := deps.TmuxClient.NewSession(params.Name, params.Path, params.Command); err != nil {
			log.WithError(err).Warn("new session via peer failed")
			return
		}
		sendStateUpdate(pc, deps)

	case "rename":
		var params struct {
			OldName string `json:"old_name"`
			NewName string `json:"new_name"`
		}
		if err := json.Unmarshal(payload.Params, &params); err != nil {
			return
		}
		if err := deps.TmuxClient.RenameSession(params.OldName, params.NewName); err != nil {
			log.WithError(err).Warn("rename session via peer failed")
			return
		}
		sendStateUpdate(pc, deps)

	case "select-window":
		var params struct {
			Session string `json:"session"`
			Window  int    `json:"window"`
			Pane    string `json:"pane,omitempty"`
		}
		if err := json.Unmarshal(payload.Params, &params); err != nil {
			return
		}
		deps.TmuxClient.SelectWindow(params.Session, fmt.Sprintf("%d", params.Window))
		if params.Pane != "" {
			deps.TmuxClient.SelectPane(params.Pane)
		}

	case "kill":
		var params struct {
			ID   string `json:"id,omitempty"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(payload.Params, &params); err != nil || params.Name == "" {
			return
		}
		if err := deps.TmuxClient.KillSession(params.ID, params.Name); err != nil {
			log.WithError(err).Warn("kill session via peer failed")
		}
		if err := recovery.ForgetSession(params.Name); err != nil {
			log.WithError(err).Warn("failed to remove session from recovery manifest")
		}
		sendStateUpdate(pc, deps)

	case "regenerate-name":
		var params struct {
			Session string `json:"session"`
		}
		if err := json.Unmarshal(payload.Params, &params); err != nil || params.Session == "" {
			return
		}
		if deps.LocalMgr == nil {
			log.Warn("no state manager available for regenerate-name action")
			return
		}
		if _, err := deps.LocalMgr.RegenerateName(params.Session); err != nil {
			log.WithError(err).Warn("regenerate name via peer failed")
			return
		}
		sendStateUpdate(pc, deps)

	default:
		log.WithField("action", payload.Action).Debug("unknown session action")
	}
}

func getPeerSessions(m *Manager, peerID string) []*tmux.Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if h, ok := m.hosts[peerID]; ok {
		return h.Sessions
	}
	return nil
}
