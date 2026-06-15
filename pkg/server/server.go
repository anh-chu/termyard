package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"

	wp "github.com/SherClockHolmes/webpush-go"

	"github.com/ekristen/guppi/pkg/activity"
	"github.com/ekristen/guppi/pkg/agentcheck"
	"github.com/ekristen/guppi/pkg/auth"
	"github.com/ekristen/guppi/pkg/common"
	"github.com/ekristen/guppi/pkg/git"
	"github.com/ekristen/guppi/pkg/identity"
	"github.com/ekristen/guppi/pkg/peer"
	"github.com/ekristen/guppi/pkg/portforward"
	"github.com/ekristen/guppi/pkg/preferences"
	"github.com/ekristen/guppi/pkg/recovery"
	"github.com/ekristen/guppi/pkg/scheduler"
	"github.com/ekristen/guppi/pkg/sessionattrs"
	"github.com/ekristen/guppi/pkg/socket"
	"github.com/ekristen/guppi/pkg/state"
	"github.com/ekristen/guppi/pkg/stats"
	"github.com/ekristen/guppi/pkg/tmux"
	"github.com/ekristen/guppi/pkg/toolevents"
	"github.com/ekristen/guppi/pkg/webpush"
	"github.com/ekristen/guppi/pkg/ws"
)

type Options struct {
	Port             int
	SocketPath       string
	Client           *tmux.Client
	StateMgr         *state.Manager
	Tracker          *toolevents.Tracker
	ActivityTracker  *activity.Tracker
	PushKeys         *webpush.VAPIDKeys
	PushStore        *webpush.Store
	PrefStore        *preferences.Store
	OnPrefsChanged   func(*preferences.Preferences)
	AttrsStore       *sessionattrs.Store
	AuthEnabled      bool
	PasswordStore    *auth.PasswordStore
	SessionMgr       *auth.SessionManager
	Identity         *identity.Identity
	PeerStore        *identity.PeerStore
	PeerMgr          *peer.Manager
	PeerHandler      *peer.Handler
	PTYRelay         *peer.PTYRelay
	LinkSupervisor   *peer.LinkSupervisor
	Detector         *toolevents.Detector
	PortForwardStore *portforward.Store
	SchedulerStore   *scheduler.Store
	SchedulerRunner  *scheduler.Runner
	Hub              *ws.Hub
}

// attrsStoreAdapter bridges sessionattrs.Store to the narrow SessionAttrsSink
// interface the peer package consumes.
type attrsStoreAdapter struct {
	store *sessionattrs.Store
}

func (a attrsStoreAdapter) ApplyRemoteDelta(key string, background, hidden bool, updatedAt time.Time) (bool, error) {
	_, accepted, err := a.store.ApplyRemote(key, sessionattrs.Attr{
		Background: background, Hidden: hidden, UpdatedAt: updatedAt,
	})
	return accepted, err
}

func (a attrsStoreAdapter) ApplyRemoteSnapshot(attrs map[string]peer.SessionAttr) ([]string, error) {
	conv := make(map[string]sessionattrs.Attr, len(attrs))
	for k, v := range attrs {
		conv[k] = sessionattrs.Attr{Background: v.Background, Hidden: v.Hidden, UpdatedAt: v.UpdatedAt}
	}
	return a.store.ApplySnapshot(conv)
}

func (a attrsStoreAdapter) SnapshotAttrs() map[string]peer.SessionAttr {
	snap := a.store.Snapshot()
	out := make(map[string]peer.SessionAttr, len(snap))
	for k, v := range snap {
		out[k] = peer.SessionAttr{Background: v.Background, Hidden: v.Hidden, UpdatedAt: v.UpdatedAt}
	}
	return out
}

// pruneSessionAttrs garbage-collects session-attribute keys whose owning host
// is online but whose session is gone from the authoritative mesh list, and
// drops expired tombstones. Genuinely-gone keys are tombstoned and the removal
// is fanned out to peers + browsers. No-op when peer mode is unavailable
// (without the host list we can't prove a session is gone, so we keep keys).
func pruneSessionAttrs(opts *Options, hub *ws.Hub) {
	if opts.AttrsStore == nil || opts.PeerMgr == nil {
		return
	}
	sessions := opts.PeerMgr.GetAllSessions()
	liveKeys := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		// Global key mirrors frontend sessionKey(): "<host>/<name>".
		if s.Host != "" {
			liveKeys[s.Host+"/"+s.Name] = true
		} else {
			liveKeys[s.Name] = true
		}
	}
	online := map[string]bool{}
	for _, h := range opts.PeerMgr.GetHosts() {
		if h.Online {
			online[h.ID] = true
		}
	}
	gone, changed, err := opts.AttrsStore.Prune(liveKeys, online)
	if err != nil || !changed {
		return
	}
	for _, key := range gone {
		fanoutAttrsDeltaToPeers(opts, key, opts.AttrsStore.Get(key))
	}
	if hub != nil {
		hub.BroadcastJSON(map[string]interface{}{"type": "session-attrs-updated"})
	}
}

// fanoutAttrsDeltaToPeers broadcasts a single-key session-attribute delta to
// every connected paired peer over the control WS. Best-effort: a full Send
// queue drops the frame; the peer reconciles from the next snapshot on link-up.
func fanoutAttrsDeltaToPeers(opts *Options, key string, a sessionattrs.Attr) {
	if opts.PeerMgr == nil || opts.Identity == nil {
		return
	}
	msg, err := peer.NewMessage(peer.MsgSessionAttrsDelta, peer.SessionAttrsDeltaPayload{
		Origin: opts.Identity.Fingerprint(),
		Key:    key,
		Attr:   peer.SessionAttr{Background: a.Background, Hidden: a.Hidden, UpdatedAt: a.UpdatedAt},
	})
	if err != nil {
		return
	}
	for _, pc := range opts.PeerMgr.ConnectedPeers() {
		pc.Enqueue(msg)
	}
}

func sessionKey(host, name string) string {
	if host != "" {
		return host + "/" + name
	}
	return name
}

// CreateSession centralizes spawn logic for HTTP and scheduler fires.
func CreateSession(opts *Options, req scheduler.CreateSessionReq) error {
	req.Name = strings.TrimSpace(req.Name)
	req.Host = strings.TrimSpace(req.Host)
	req.Path = strings.TrimSpace(req.Path)
	req.Command = strings.TrimSpace(req.Command)
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if err := tmux.ValidateSessionName(req.Name); err != nil {
		return err
	}

	// Remote host — forward via peer connection. The peer handles worktree
	// creation locally so the worktree lands on the peer's filesystem, not ours.
	if req.Host != "" && opts.PeerMgr != nil && !opts.PeerMgr.IsLocal(req.Host) {
		peerConn := opts.PeerMgr.GetPeerConnection(req.Host)
		if peerConn == nil {
			return fmt.Errorf("peer not connected")
		}
		params, _ := json.Marshal(map[string]string{
			"name":            req.Name,
			"path":            req.Path,
			"command":         req.Command,
			"worktree_branch": req.WorktreeBranch,
			"schedule_id":     req.ScheduleID,
		})
		msg, _ := peer.NewMessage(peer.MsgSessionAction, peer.SessionActionPayload{
			Action: "new",
			Params: params,
		})
		if !peerConn.Enqueue(msg) {
			return fmt.Errorf("peer send queue full")
		}
		if opts.AttrsStore != nil && req.ScheduleID != "" {
			if _, err := opts.AttrsStore.SetScheduleID(sessionKey(req.Host, req.Name), req.ScheduleID); err != nil {
				logrus.WithError(err).Warn("failed to store schedule id")
			} else if opts.Hub != nil {
				opts.Hub.BroadcastJSON(map[string]interface{}{"type": "session-attrs-updated", "key": sessionKey(req.Host, req.Name)})
			}
		}
		return nil
	}

	// If a worktree branch is requested, create the linked worktree first and
	// redirect the session path to it.
	if req.WorktreeBranch != "" && req.Path != "" {
		expanded := req.Path
		if strings.HasPrefix(expanded, "~") {
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				expanded = home + expanded[1:]
			}
		}
		// Resolve bare relative paths (e.g. "guppi") against the home dir.
		if !filepath.IsAbs(expanded) {
			if home, err := os.UserHomeDir(); err == nil {
				expanded = filepath.Join(home, expanded)
			}
		}
		sanitized := strings.ReplaceAll(req.WorktreeBranch, "/", "-")
		worktreesDir := filepath.Join(expanded, ".worktrees")
		if err := os.MkdirAll(worktreesDir, 0755); err != nil {
			return fmt.Errorf("mkdir .worktrees: %w", err)
		}
		destPath := filepath.Join(worktreesDir, sanitized)
		if err := git.CreateWorktree(expanded, req.WorktreeBranch, destPath); err != nil {
			return fmt.Errorf("git worktree add: %w", err)
		}
		req.Path = destPath
	}

	if err := opts.Client.NewSession(req.Name, req.Path, req.Command); err != nil {
		return err
	}
	// Store explicit agent type before refresh so it survives inference.
	if req.AgentType != "" && opts.StateMgr != nil {
		opts.StateMgr.SetSessionAgentType(req.Name, req.AgentType)
	}
	if opts.AttrsStore != nil && req.ScheduleID != "" {
		if _, err := opts.AttrsStore.SetScheduleID(sessionKey(req.Host, req.Name), req.ScheduleID); err != nil {
			logrus.WithError(err).Warn("failed to store schedule id")
		} else if opts.Hub != nil {
			opts.Hub.BroadcastJSON(map[string]interface{}{"type": "session-attrs-updated", "key": sessionKey(req.Host, req.Name)})
		}
	}
	// Trigger state refresh so WebSocket clients get notified.
	if opts.StateMgr != nil {
		if fresh, err := opts.Client.ListSessions(); err == nil {
			opts.StateMgr.UpdateSessions(fresh)
		}
	}
	return nil
}

// handleRemoteSession handles a terminal session request for a remote peer.
// It tells the peer to spawn a PTY, then bridges the browser WS to the peer's PTY WS.
func handleRemoteSession(w http.ResponseWriter, r *http.Request, opts *Options, hostID string) {
	sessionName := r.URL.Query().Get("name")
	if sessionName == "" {
		http.Error(w, "missing session name", http.StatusBadRequest)
		return
	}

	cols := uint16(80)
	rows := uint16(24)
	if c := r.URL.Query().Get("cols"); c != "" {
		if v, err := strconv.ParseUint(c, 10, 16); err == nil && v > 0 {
			cols = uint16(v)
		}
	}
	if rv := r.URL.Query().Get("rows"); rv != "" {
		if v, err := strconv.ParseUint(rv, 10, 16); err == nil && v > 0 {
			rows = uint16(v)
		}
	}

	// Get the peer connection
	peerConn := opts.PeerMgr.GetPeerConnection(hostID)
	if peerConn == nil {
		http.Error(w, "peer not connected", http.StatusBadGateway)
		return
	}

	// Upgrade browser to WebSocket
	upgrader := websocket.Upgrader{
		CheckOrigin:    ws.CheckSameOrigin,
		ReadBufferSize: 1024, WriteBufferSize: 1024 * 16,
	}
	browserWS, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer browserWS.Close()

	// Generate stream ID and register the browser binding so MsgPTYOutput
	// from the peer can be routed back to this WebSocket.
	streamID := peer.GenerateStreamID()
	opts.PTYRelay.Register(streamID, hostID, browserWS)
	defer opts.PTYRelay.Remove(streamID)

	// Tell the peer to open a PTY.
	msg, _ := peer.NewMessage(peer.MsgPTYOpen, peer.PTYOpenPayload{
		StreamID: streamID,
		Session:  sessionName,
		Cols:     cols,
		Rows:     rows,
	})
	if !peerConn.Enqueue(msg) {
		return
	}

	// Pump keystrokes/control frames from browser to peer; output flows the
	// other way via MsgPTYOutput dispatched in handleSessionMessage.
	opts.PTYRelay.PumpBrowserToPeer(streamID, browserWS, peerConn)

	// Tell the peer to close the PTY.
	closeMsg, _ := peer.NewMessage(peer.MsgPTYClose, peer.PTYClosePayload{StreamID: streamID})
	peerConn.Enqueue(closeMsg)
}

// absPathRe matches HTML attribute values that begin with a single /
// (absolute paths), excluding protocol-relative URLs (//) and fragments.
// Group 1: the attribute prefix up through the opening quote.
// Group 2: the slash plus the first non-slash character of the path.
//
// This is compiled once at package init so handleProxy doesn't pay the cost
// of regexp compilation on every request.
var absPathRe = regexp.MustCompile(`((?:href|src|action|srcset|data-src|data-href)=")(/[^/])`)

// handleProxy reverse-proxies a request to a locally-bound port on the guppi
// host. WebSocket upgrade requests are tunnelled over raw TCP so that
// localhost-only dev servers remain accessible through the guppi URL.
//
// Route pattern: /proxy/{port}/{rest...}
func handleProxy(w http.ResponseWriter, r *http.Request, guppiPort int) {
	// Extract port from chi URL params
	portStr := chi.URLParam(r, "port")
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}
	if port == guppiPort {
		http.Error(w, "cannot proxy guppi's own port", http.StatusForbidden)
		return
	}

	// Strip "/proxy/{port}" prefix to get the downstream path
	rest := chi.URLParam(r, "*")
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}

	isWebSocket := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	if isWebSocket {
		proxyWebSocket(w, r, port, rest)
		return
	}

	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", port),
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Override director to rewrite the path correctly
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = rest
		if r.URL.RawQuery != "" {
			req.URL.RawQuery = r.URL.RawQuery
		}
		req.Host = fmt.Sprintf("127.0.0.1:%d", port)
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logrus.WithError(err).WithField("port", port).Debug("port forward proxy error")
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
	}
	proxy.ModifyResponse = makeHTMLRewriter(port)
	proxy.ServeHTTP(w, r)
}

// makeHTMLRewriter returns a ModifyResponse function that rewrites absolute
// paths in HTML responses so browsers route asset requests back through the
// guppi proxy rather than directly to the host root.
//
// For example, a Next.js app served at /proxy/8377/ generates:
//
//	<script src="/_next/static/chunks/main.js">
//
// which the browser resolves to devvm:7654/_next/... (a guppi 404). The
// rewriter turns it into src="/proxy/8377/_next/...", which routes correctly.
//
// It also patches the assetPrefix/basePath fields in Next.js __NEXT_DATA__ so
// that client-side navigation and code-splitting also use the proxy prefix.
func makeHTMLRewriter(port int) func(*http.Response) error {
	prefix := fmt.Sprintf("/proxy/%d", port)
	// Replacement: group1 stays, prefix is inserted before the leading slash of group2.
	// Example: href="/foo" → href="/proxy/8377/foo"
	// Example: href="/"   → href="/proxy/8377/"
	repl := []byte("${1}" + prefix + "${2}")

	// Next.js embeds {"assetPrefix":"","basePath":""} in __NEXT_DATA__.
	// Rewriting these makes the React runtime also use the proxy for all
	// dynamically loaded chunks and API calls.
	nextAssetReplace := []byte(`"assetPrefix":""`)
	nextAssetWith := []byte(`"assetPrefix":"` + prefix + `"`)
	nextBaseReplace := []byte(`"basePath":""`)
	nextBaseWith := []byte(`"basePath":"` + prefix + `"`)

	return func(resp *http.Response) error {
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			return nil
		}

		// Decompress gzip if needed so we can inspect the body bytes.
		var reader io.Reader = resp.Body
		encoded := strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip")
		if encoded {
			gr, err := gzip.NewReader(resp.Body)
			if err != nil {
				return nil // can't decompress — leave response untouched
			}
			defer gr.Close()
			reader = gr
		}

		body, err := io.ReadAll(reader)
		resp.Body.Close()
		if err != nil {
			return err
		}

		// Rewrite absolute-path attribute values.
		body = absPathRe.ReplaceAll(body, repl)

		// Patch Next.js runtime metadata.
		body = bytes.Replace(body, nextAssetReplace, nextAssetWith, -1)
		body = bytes.Replace(body, nextBaseReplace, nextBaseWith, -1)

		if encoded {
			// We decoded gzip; remove the header so the browser doesn't try to decode.
			resp.Header.Del("Content-Encoding")
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return nil
	}
}

// proxyWebSocket tunnels a WebSocket upgrade through a raw TCP connection to
// the downstream port, allowing WebSocket-based dev servers to work through
// the guppi port-forward proxy.
func proxyWebSocket(w http.ResponseWriter, r *http.Request, port int, path string) {
	backend, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		logrus.WithError(err).WithField("port", port).Debug("ws port forward: dial failed")
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer backend.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		http.Error(w, "hijack failed", http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Forward the original WS upgrade request to the backend with the rewritten path
	upgradeReq := r.Clone(r.Context())
	upgradeReq.URL.Path = path
	upgradeReq.URL.Host = fmt.Sprintf("127.0.0.1:%d", port)
	upgradeReq.Host = fmt.Sprintf("127.0.0.1:%d", port)
	if err := upgradeReq.Write(backend); err != nil {
		return
	}

	// Flush any buffered data from the hijacked connection to the backend
	if buf.Reader.Buffered() > 0 {
		buffered := make([]byte, buf.Reader.Buffered())
		_, _ = buf.Read(buffered)
		_, _ = backend.Write(buffered)
	}

	// Tunnel bidirectionally
	done := make(chan struct{}, 2)
	go func() { io.Copy(backend, conn); done <- struct{}{} }() //nolint:errcheck
	go func() { io.Copy(conn, backend); done <- struct{}{} }() //nolint:errcheck
	<-done
}

func Run(ctx context.Context, opts *Options) error {
	logger := logrus.WithField("component", "server")

	secureCookies := false

	// Build the events hub up front so routes can broadcast layout changes.
	hub := ws.NewHub(opts.StateMgr, opts.Tracker)
	opts.Hub = hub
	if opts.ActivityTracker != nil {
		var peerActivity ws.ActivitySource
		localHostID := ""
		if opts.PeerMgr != nil {
			peerActivity = opts.PeerMgr
			localHostID = opts.PeerMgr.LocalID()
		}
		hub.SetActivityTracker(opts.ActivityTracker, peerActivity, localHostID, false)
	}

	// Wire cross-machine session-attribute sync. The peer subsystem applies
	// inbound MsgSessionAttrs{Snapshot,Delta} to our server-authoritative store
	// (per-key LWW) and bounces session-attrs-updated through the browser hub.
	// Keys are global and host-qualified on every node — no translation layer.
	if opts.AttrsStore != nil {
		sink := attrsStoreAdapter{store: opts.AttrsStore}
		if opts.LinkSupervisor != nil {
			opts.LinkSupervisor.SetAttrsSink(sink)
			opts.LinkSupervisor.SetBrowserHub(hub)
		}
		if opts.PeerHandler != nil {
			opts.PeerHandler.SetAttrsSink(sink)
			opts.PeerHandler.SetBrowserHub(hub)
		}
	}

	r := chi.NewRouter()
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.StripSlashes)
	r.Use(chimiddleware.RequestID)

	// API routes
	r.Route("/api", func(r chi.Router) {
		// Public auth endpoints (no middleware)
		r.Get("/auth/status", auth.StatusHandler(opts.AuthEnabled, opts.PasswordStore))
		if opts.AuthEnabled {
			r.Post("/auth/setup", auth.SetupHandler(opts.PasswordStore, opts.SessionMgr, secureCookies))
			r.Post("/auth/login", auth.LoginHandler(opts.PasswordStore, opts.SessionMgr, secureCookies))
			r.Post("/auth/logout", auth.LogoutHandler(opts.SessionMgr))
			r.Get("/auth/check", auth.CheckHandler(opts.SessionMgr))
		}

		// Peer bootstrap endpoint — password-authenticated (no session cookie).
		// Lets two nodes establish mutual trust via the dashboard password.
		r.Post("/peers/bootstrap", func(w http.ResponseWriter, r *http.Request) {
			handlePeersBootstrap(w, r, opts)
		})

		// Version endpoint — public, no auth required
		r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"version": common.VERSION,
				"commit":  common.COMMIT,
			})
		})

		// Tool event ingest — no auth required (used by local CLI via unix socket)
		r.Post("/tool-event", func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			logrus.WithField("raw_body", string(body)).Trace("tool-event API: received request")

			var evt toolevents.Event
			if err := json.Unmarshal(body, &evt); err != nil {
				logrus.WithError(err).WithField("raw_body", string(body)).Trace("tool-event API: JSON parse failed")
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}

			if evt.Tool == "" || evt.Status == "" || evt.Session == "" {
				logrus.WithFields(logrus.Fields{
					"tool": evt.Tool, "status": evt.Status, "session": evt.Session,
				}).Trace("tool-event API: missing required fields")
				http.Error(w, "tool, status, and session are required", http.StatusBadRequest)
				return
			}

			// Stamp local host identity when running in multi-host mode
			if opts.PeerMgr != nil && evt.Host == "" {
				evt.Host = opts.PeerMgr.LocalID()
				evt.HostName = opts.PeerMgr.LocalName()
			}

			logrus.WithFields(logrus.Fields{
				"tool":    evt.Tool,
				"status":  evt.Status,
				"session": evt.Session,
				"window":  evt.Window,
				"pane":    evt.Pane,
				"message": evt.Message,
				"host":    evt.Host,
			}).Debug("received tool event via API")

			opts.Tracker.Record(&evt)
			if opts.StateMgr != nil {
				opts.StateMgr.UpdateSessionMetadataFromEvent(&evt)
			}
			w.WriteHeader(http.StatusNoContent)
		})

		// Protected API routes
		r.Group(func(r chi.Router) {
			if opts.AuthEnabled {
				r.Use(auth.Middleware(opts.SessionMgr))
			}

			// Agent status — check which agents are installed/configured
			r.Get("/agent-status", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(agentcheck.CheckAgents())
			})

			r.Get("/sessions", func(w http.ResponseWriter, r *http.Request) {
				var sessions []*tmux.Session
				if opts.PeerMgr != nil {
					sessions = opts.PeerMgr.GetAllSessions()
				} else {
					sessions = opts.StateMgr.GetSessions()
				}
				enrichSessionsFromTracker(sessions, opts.Tracker)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(sessions)
			})

			r.Get("/hosts", func(w http.ResponseWriter, r *http.Request) {
				if opts.PeerMgr != nil {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(opts.PeerMgr.GetHosts())
				} else {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode([]interface{}{})
				}
			})

			r.Post("/session/new", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Name           string `json:"name"`
					Host           string `json:"host,omitempty"`
					Path           string `json:"path,omitempty"`
					Command        string `json:"command,omitempty"`
					AgentType      string `json:"agent_type,omitempty"`
					WorktreeBranch string `json:"worktree_branch,omitempty"`
					ScheduleID     string `json:"schedule_id,omitempty"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				req.Name = strings.TrimSpace(req.Name)
				req.Path = strings.TrimSpace(req.Path)
				req.Command = strings.TrimSpace(req.Command)
				req.Name = resolveNewSessionName(opts, req.Host, req.Name, req.Command, req.Path)
				if req.Name == "" {
					http.Error(w, "name or path is required", http.StatusBadRequest)
					return
				}
				if err := tmux.ValidateSessionName(req.Name); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if err := CreateSession(opts, scheduler.CreateSessionReq{
					Name:           req.Name,
					Host:           req.Host,
					Path:           req.Path,
					Command:        req.Command,
					AgentType:      req.AgentType,
					WorktreeBranch: req.WorktreeBranch,
					ScheduleID:     req.ScheduleID,
				}); err != nil {
					switch err.Error() {
					case "peer not connected", "peer send queue full":
						http.Error(w, err.Error(), http.StatusBadGateway)
					default:
						http.Error(w, err.Error(), http.StatusInternalServerError)
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"name": req.Name})
			})

			r.Post("/session/display-name", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Session     string `json:"session"`
					DisplayName string `json:"display_name"`
					Clear       bool   `json:"clear,omitempty"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Session == "" {
					http.Error(w, "session is required", http.StatusBadRequest)
					return
				}
				if opts.StateMgr == nil {
					http.Error(w, "state manager unavailable", http.StatusInternalServerError)
					return
				}
				// clear=true resets to AI/auto naming; otherwise mark user-set.
				opts.StateMgr.SetDisplayName(req.Session, req.DisplayName, !req.Clear)
				w.WriteHeader(http.StatusNoContent)
			})

			// AI-name a layout group from its member session labels. Groups are
			// a frontend-only concept, so this is stateless: it returns a name,
			// the client persists it.
			r.Post("/group/name", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Members []string `json:"members"`
					Current string   `json:"current,omitempty"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Members) == 0 {
					http.Error(w, "members is required", http.StatusBadRequest)
					return
				}
				if opts.StateMgr == nil {
					http.Error(w, "state manager unavailable", http.StatusInternalServerError)
					return
				}
				name, err := opts.StateMgr.GenerateGroupName(req.Members, req.Current)
				if err != nil {
					http.Error(w, err.Error(), http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"name": name})
			})

			r.Post("/session/rename", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					OldName string `json:"old_name"`
					NewName string `json:"new_name"`
					Host    string `json:"host,omitempty"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OldName == "" || req.NewName == "" {
					http.Error(w, "old_name and new_name are required", http.StatusBadRequest)
					return
				}
				if err := tmux.ValidateSessionName(req.NewName); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}

				// Remote host — forward via peer connection
				if req.Host != "" && opts.PeerMgr != nil && !opts.PeerMgr.IsLocal(req.Host) {
					peerConn := opts.PeerMgr.GetPeerConnection(req.Host)
					if peerConn == nil {
						http.Error(w, "peer not connected", http.StatusBadGateway)
						return
					}
					params, _ := json.Marshal(map[string]string{
						"old_name": req.OldName,
						"new_name": req.NewName,
					})
					msg, _ := peer.NewMessage(peer.MsgSessionAction, peer.SessionActionPayload{
						Action: "rename",
						Params: params,
					})
					if peerConn.Enqueue(msg) {
						w.WriteHeader(http.StatusNoContent)
					} else {
						http.Error(w, "peer send queue full", http.StatusBadGateway)
					}
					return
				}

				if err := opts.Client.RenameSession(req.OldName, req.NewName); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if fresh, err := opts.Client.ListSessions(); err == nil {
					opts.StateMgr.UpdateSessions(fresh)
				}
				w.WriteHeader(http.StatusNoContent)
			})

			r.Post("/session/select-window", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Session string `json:"session"`
					Window  int    `json:"window"`
					Host    string `json:"host,omitempty"`
					Pane    string `json:"pane,omitempty"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Session == "" {
					http.Error(w, "session and window are required", http.StatusBadRequest)
					return
				}

				// Remote host — forward via peer connection
				if req.Host != "" && opts.PeerMgr != nil && !opts.PeerMgr.IsLocal(req.Host) {
					peerConn := opts.PeerMgr.GetPeerConnection(req.Host)
					if peerConn == nil {
						http.Error(w, "peer not connected", http.StatusBadGateway)
						return
					}
					params, _ := json.Marshal(map[string]interface{}{
						"session": req.Session,
						"window":  req.Window,
						"pane":    req.Pane,
					})
					msg, _ := peer.NewMessage(peer.MsgSessionAction, peer.SessionActionPayload{
						Action: "select-window",
						Params: params,
					})
					if peerConn.Enqueue(msg) {
						w.WriteHeader(http.StatusNoContent)
					} else {
						http.Error(w, "peer send queue full", http.StatusBadGateway)
					}
					return
				}

				index := fmt.Sprintf("%d", req.Window)
				if err := opts.Client.SelectWindow(req.Session, index); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if req.Pane != "" {
					if err := opts.Client.SelectPane(req.Pane); err != nil {
						logrus.WithError(err).Warn("failed to select pane")
					}
				}
				w.WriteHeader(http.StatusNoContent)
			})

			r.Post("/session/kill", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID             string `json:"id,omitempty"`
					Name           string `json:"name"`
					Host           string `json:"host,omitempty"`
					RemoveWorktree bool   `json:"remove_worktree,omitempty"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
					http.Error(w, "name is required", http.StatusBadRequest)
					return
				}

				// Remote host — forward via peer connection
				if req.Host != "" && opts.PeerMgr != nil && !opts.PeerMgr.IsLocal(req.Host) {
					peerConn := opts.PeerMgr.GetPeerConnection(req.Host)
					if peerConn == nil {
						http.Error(w, "peer not connected", http.StatusBadGateway)
						return
					}
					params, _ := json.Marshal(map[string]string{"id": req.ID, "name": req.Name})
					msg, _ := peer.NewMessage(peer.MsgSessionAction, peer.SessionActionPayload{
						Action: "kill",
						Params: params,
					})
					if peerConn.Enqueue(msg) {
						w.WriteHeader(http.StatusNoContent)
					} else {
						http.Error(w, "peer send queue full", http.StatusBadGateway)
					}
					return
				}

				// Capture worktree path before state is cleared.
				var worktreePath string
				if req.RemoveWorktree && opts.StateMgr != nil {
					worktreePath = opts.StateMgr.GetSessionProjectPath(req.Name)
				}

				// Kill by ID first (avoids tmux special-target interpretation of names
				// like '~'); fall back to name. Always clean up state regardless.
				if err := opts.Client.KillSession(req.ID, req.Name); err != nil {
					logrus.WithError(err).WithField("session", req.Name).Warn("tmux kill-session failed, removing from state")
				}
				if opts.StateMgr != nil {
					opts.StateMgr.RemoveSession(req.Name)
				}
				// Drop from crash-recovery manifest synchronously so the
				// rebuilder cannot resurrect an intentionally-killed session.
				if err := recovery.ForgetSession(req.Name); err != nil {
					logrus.WithError(err).WithField("session", req.Name).Warn("failed to remove session from recovery manifest")
				}

				// Remove the linked worktree if requested. Non-fatal — session is
				// already gone; log and continue.
				if req.RemoveWorktree && worktreePath != "" {
					if err := git.RemoveWorktree(worktreePath); err != nil {
						logrus.WithError(err).WithField("path", worktreePath).Warn("git worktree remove failed")
					}
				}

				w.WriteHeader(http.StatusNoContent)
			})

			// Tool event query/management (auth-protected)
			r.Get("/tool-events", func(w http.ResponseWriter, r *http.Request) {
				session := r.URL.Query().Get("session")
				var events []*toolevents.Event
				if session != "" {
					events = opts.Tracker.GetForSession(session)
				} else {
					events = opts.Tracker.GetAll()
				}

				// Merge in auto-detected agents that don't have a tracked event.
				// These are "active" agents found via process-tree inspection
				// (e.g. codex/copilot running as node).
				if opts.Detector != nil {
					tracked := make(map[string]bool)
					for _, evt := range events {
						if evt.Pane != "" {
							tracked[evt.Pane] = true
						}
					}
					for paneID, tool := range opts.Detector.DetectedPanes() {
						if tracked[paneID] {
							continue
						}
						info := opts.Detector.PaneInfo(paneID)
						if session != "" && info.Session != session {
							continue
						}
						evt := &toolevents.Event{
							Tool:         tool,
							Status:       toolevents.StatusActive,
							Session:      info.Session,
							Window:       info.Window,
							Pane:         paneID,
							Message:      "auto-detected",
							AutoDetected: true,
						}
						// Stamp local host identity so frontend session key matching works
						if opts.PeerMgr != nil {
							evt.Host = opts.PeerMgr.LocalID()
							evt.HostName = opts.PeerMgr.LocalName()
						}
						events = append(events, evt)
					}
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(events)
			})

			r.Delete("/tool-events", func(w http.ResponseWriter, r *http.Request) {
				opts.Tracker.ClearAll()
				w.WriteHeader(http.StatusNoContent)
			})

			r.Delete("/tool-event", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Host    string `json:"host"`
					Session string `json:"session"`
					Window  int    `json:"window"`
					Pane    string `json:"pane"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Session == "" {
					http.Error(w, "session is required", http.StatusBadRequest)
					return
				}
				opts.Tracker.Clear(req.Host, req.Session, req.Window, req.Pane)
				w.WriteHeader(http.StatusNoContent)
			})

			// Stats endpoint — aggregate overview data
			r.Get("/stats", func(w http.ResponseWriter, r *http.Request) {
				sessions := opts.StateMgr.GetSessions()
				allPanes, _ := opts.Client.ListAllPanes()

				agentCommands := map[string]bool{
					"claude": true, "codex": true, "copilot": true, "opencode": true,
				}
				totalWindows := 0
				attachedSessions := 0
				agentPanes := 0
				for _, s := range sessions {
					if s.Attached {
						attachedSessions++
					}
					totalWindows += len(s.Windows)
				}

				// Build a set of panes with known agent tool events (from hooks
				// or process-tree detection). This catches agents like codex and
				// copilot that show up as "node" in pane_current_command.
				toolEvents := opts.Tracker.GetAll()
				agentEventPanes := make(map[string]bool)
				for _, evt := range toolEvents {
					if evt.Pane != "" {
						agentEventPanes[evt.Pane] = true
					}
				}
				// Also include panes detected via process tree inspection
				if opts.Detector != nil {
					for paneID := range opts.Detector.DetectedPanes() {
						agentEventPanes[paneID] = true
					}
				}

				for _, p := range allPanes {
					if agentCommands[p.CurrentCommand] || agentEventPanes[p.ID] {
						agentPanes++
					}
				}
				waitingAgents := 0
				errorAgents := 0
				stuckAgents := 0
				for _, evt := range toolEvents {
					switch evt.Status {
					case "waiting":
						waitingAgents++
					case "error":
						errorAgents++
					case "stuck":
						stuckAgents++
					}
				}

				result := map[string]interface{}{
					"sessions": map[string]int{
						"total":    len(sessions),
						"attached": attachedSessions,
						"detached": len(sessions) - attachedSessions,
					},
					"windows":     totalWindows,
					"panes":       len(allPanes),
					"agent_panes": agentPanes,
					"agents": map[string]int{
						"active":  agentPanes,
						"waiting": waitingAgents,
						"stuck":   stuckAgents,
						"error":   errorAgents,
					},
					"processes": stats.ProcessCountsFromSessions(sessions),
					"system":    stats.SystemStats(),
				}

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(result)
			})

			// Activity endpoints
			r.Get("/activity", func(w http.ResponseWriter, r *http.Request) {
				session := r.URL.Query().Get("session")
				w.Header().Set("Content-Type", "application/json")
				if session != "" {
					snap := opts.ActivityTracker.Get(session)
					// Stamp host on local snapshot in multi-host mode
					if snap != nil && opts.PeerMgr != nil && snap.Host == "" {
						snap.Host = opts.PeerMgr.LocalID()
					}
					json.NewEncoder(w).Encode(snap)
				} else {
					snapshots := opts.ActivityTracker.GetAll()
					// Stamp host on local snapshots in multi-host mode
					if opts.PeerMgr != nil {
						localID := opts.PeerMgr.LocalID()
						for _, s := range snapshots {
							if s.Host == "" {
								s.Host = localID
							}
						}
						peerActivity := opts.PeerMgr.GetAllActivity()
						snapshots = append(snapshots, peerActivity...)
					}
					json.NewEncoder(w).Encode(snapshots)
				}
			})

			// Push notification endpoints
			r.Get("/push/vapid-key", func(w http.ResponseWriter, r *http.Request) {
				if opts.PushKeys == nil {
					http.Error(w, "push notifications not configured", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"public_key": opts.PushKeys.PublicKey,
				})
			})

			r.Post("/push/subscribe", func(w http.ResponseWriter, r *http.Request) {
				if opts.PushStore == nil {
					http.Error(w, "push notifications not configured", http.StatusServiceUnavailable)
					return
				}
				var sub wp.Subscription
				if err := json.NewDecoder(r.Body).Decode(&sub); err != nil || sub.Endpoint == "" {
					http.Error(w, "invalid subscription", http.StatusBadRequest)
					return
				}
				opts.PushStore.Add(&sub)
				w.WriteHeader(http.StatusNoContent)
			})

			r.Post("/push/unsubscribe", func(w http.ResponseWriter, r *http.Request) {
				if opts.PushStore == nil {
					http.Error(w, "push notifications not configured", http.StatusServiceUnavailable)
					return
				}
				var req struct {
					Endpoint string `json:"endpoint"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" {
					http.Error(w, "endpoint is required", http.StatusBadRequest)
					return
				}
				opts.PushStore.Remove(req.Endpoint)
				w.WriteHeader(http.StatusNoContent)
			})

			// Preferences endpoints
			r.Get("/preferences", func(w http.ResponseWriter, r *http.Request) {
				var prefs *preferences.Preferences
				if opts.PrefStore != nil {
					prefs = opts.PrefStore.Get()
				} else {
					prefs = preferences.Default()
				}
				// Never leak the stored API key to the browser; send a mask
				// placeholder when one is set.
				if prefs.AINaming.APIKey != "" {
					prefs.AINaming.APIKey = preferences.APIKeyMask
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(prefs)
			})

			r.Put("/preferences", func(w http.ResponseWriter, r *http.Request) {
				if opts.PrefStore == nil {
					http.Error(w, "preferences not available", http.StatusServiceUnavailable)
					return
				}
				var prefs preferences.Preferences
				if err := json.NewDecoder(r.Body).Decode(&prefs); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				// A masked API key means "keep the existing one"; restore it from
				// the store so the masked placeholder is never persisted.
				if prefs.AINaming.APIKey == preferences.APIKeyMask {
					prefs.AINaming.APIKey = opts.PrefStore.Get().AINaming.APIKey
				}
				// Defensive: never persist the mask itself, even if the store was
				// previously corrupted with it.
				if prefs.AINaming.APIKey == preferences.APIKeyMask {
					prefs.AINaming.APIKey = ""
				}
				if err := opts.PrefStore.Update(&prefs); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if opts.OnPrefsChanged != nil {
					opts.OnPrefsChanged(&prefs)
				}
				// Echo a masked COPY; never mutate the stored struct (Update keeps
				// the pointer, so mutating prefs here would corrupt the store).
				echo := prefs
				if echo.AINaming.APIKey != "" {
					echo.AINaming.APIKey = preferences.APIKeyMask
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(&echo)
			})

			// Session-attribute endpoints — server-authoritative, mesh-wide shared
			// per-session UI bits (backgrounded / hidden). Keys are global and
			// host-qualified ("<owner-fp>/<name>"), identical to the frontend's
			// sessionKey(). No localStorage source of truth, no namespace
			// translation, no whole-blob writes.
			r.Get("/session-attrs", func(w http.ResponseWriter, r *http.Request) {
				if opts.AttrsStore == nil {
					http.Error(w, "session attrs not available", http.StatusServiceUnavailable)
					return
				}
				// Opportunistically GC sessions that are genuinely gone (owner
				// online but session absent) before returning the live sets.
				pruneSessionAttrs(opts, hub)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(opts.AttrsStore.Sets())
			})

			r.Post("/session-attrs", func(w http.ResponseWriter, r *http.Request) {
				if opts.AttrsStore == nil {
					http.Error(w, "session attrs not available", http.StatusServiceUnavailable)
					return
				}
				var body struct {
					Key        string `json:"key"`
					Background bool   `json:"background"`
					Hidden     bool   `json:"hidden"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
					http.Error(w, "key is required", http.StatusBadRequest)
					return
				}
				a, err := opts.AttrsStore.Set(body.Key, body.Background, body.Hidden)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				hub.BroadcastJSON(map[string]interface{}{
					"type": "session-attrs-updated",
					"key":  body.Key,
				})
				fanoutAttrsDeltaToPeers(opts, body.Key, a)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(opts.AttrsStore.Sets())
			})

			// Schedule registry
			r.Get("/schedules", func(w http.ResponseWriter, r *http.Request) {
				if opts.SchedulerStore == nil {
					http.Error(w, "scheduler not available", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(opts.SchedulerStore.List())
			})
			r.Post("/schedules", func(w http.ResponseWriter, r *http.Request) {
				if opts.SchedulerStore == nil {
					http.Error(w, "scheduler not available", http.StatusServiceUnavailable)
					return
				}
				var job scheduler.Job
				if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				created, err := opts.SchedulerStore.Add(job)
				if err != nil {
					if strings.Contains(err.Error(), "invalid cron spec") || strings.Contains(err.Error(), "cron spec is required") {
						http.Error(w, err.Error(), http.StatusBadRequest)
					} else {
						http.Error(w, err.Error(), http.StatusInternalServerError)
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(created)
			})
			r.Put("/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
				if opts.SchedulerStore == nil {
					http.Error(w, "scheduler not available", http.StatusServiceUnavailable)
					return
				}
				id := chi.URLParam(r, "id")
				cur, ok := opts.SchedulerStore.Get(id)
				if !ok {
					http.Error(w, "job not found", http.StatusNotFound)
					return
				}
				var job scheduler.Job
				if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				if job.ID != "" && job.ID != id {
					http.Error(w, "id mismatch", http.StatusBadRequest)
					return
				}
				job.ID = id
				job.CreatedAt = cur.CreatedAt
				job.LastRun = cur.LastRun
				job.RunCount = cur.RunCount
				if job.SessionNamePrefix == "" {
					job.SessionNamePrefix = cur.SessionNamePrefix
				}
				if job.Enabled {
					schedule, err := cron.ParseStandard(job.CronSpec)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					job.NextRun = schedule.Next(time.Now())
				} else {
					job.NextRun = time.Time{}
				}
				updated, err := opts.SchedulerStore.Update(job)
				if err != nil {
					if strings.Contains(err.Error(), "invalid cron spec") || strings.Contains(err.Error(), "cron spec is required") {
						http.Error(w, err.Error(), http.StatusBadRequest)
					} else {
						http.Error(w, err.Error(), http.StatusInternalServerError)
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(updated)
			})
			r.Delete("/schedules/{id}", func(w http.ResponseWriter, r *http.Request) {
				if opts.SchedulerStore == nil {
					http.Error(w, "scheduler not available", http.StatusServiceUnavailable)
					return
				}
				if err := opts.SchedulerStore.Remove(chi.URLParam(r, "id")); err != nil {
					if strings.Contains(err.Error(), "not found") {
						http.Error(w, err.Error(), http.StatusNotFound)
					} else {
						http.Error(w, err.Error(), http.StatusInternalServerError)
					}
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			r.Post("/schedules/{id}/run", func(w http.ResponseWriter, r *http.Request) {
				if opts.SchedulerStore == nil || opts.SchedulerRunner == nil {
					http.Error(w, "scheduler not available", http.StatusServiceUnavailable)
					return
				}
				id := chi.URLParam(r, "id")
				job, ok := opts.SchedulerStore.Get(id)
				if !ok {
					http.Error(w, "job not found", http.StatusNotFound)
					return
				}
				req := scheduler.CreateSessionReq{
					Name:           job.SessionNamePrefix,
					Host:           job.Host,
					Path:           job.Path,
					Command:        job.Command,
					AgentType:      job.AgentType,
					WorktreeBranch: job.WorktreeBranch,
					ScheduleID:     job.ID,
				}
				if req.Name == "" {
					req.Name = job.Name
				}
				if req.Name == "" {
					req.Name = "schedule"
				}
				req.Name = fmt.Sprintf("%s-%d", req.Name, time.Now().Unix())
				if err := CreateSession(opts, req); err != nil {
					if err.Error() == "peer not connected" {
						http.Error(w, err.Error(), http.StatusBadGateway)
					} else {
						http.Error(w, err.Error(), http.StatusInternalServerError)
					}
					return
				}
				nextRun := job.NextRun
				if _, err := opts.SchedulerStore.MarkRan(job.ID, time.Now(), nextRun); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				updated, _ := opts.SchedulerStore.Get(job.ID)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(updated)
			})

			// Port forward registry
			r.Get("/portforwards", func(w http.ResponseWriter, r *http.Request) {
				if opts.PortForwardStore == nil {
					http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(opts.PortForwardStore.List())
			})
			r.Post("/portforwards", func(w http.ResponseWriter, r *http.Request) {
				if opts.PortForwardStore == nil {
					http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
					return
				}
				var req struct {
					Port         int              `json:"port"`
					Label        string           `json:"label"`
					Mode         portforward.Mode `json:"mode"`
					ExternalPort int              `json:"external_port"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Port < 1 || req.Port > 65535 {
					http.Error(w, "port (1-65535) required", http.StatusBadRequest)
					return
				}
				if req.Mode == "" {
					req.Mode = portforward.ModeProxy
				}
				if err := opts.PortForwardStore.Add(req.Port, req.Label, req.Mode, req.ExternalPort); err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(opts.PortForwardStore.List())
			})
			r.Delete("/portforward/{port}", func(w http.ResponseWriter, r *http.Request) {
				if opts.PortForwardStore == nil {
					http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
					return
				}
				port, err := strconv.Atoi(chi.URLParam(r, "port"))
				if err != nil {
					http.Error(w, "invalid port", http.StatusBadRequest)
					return
				}
				opts.PortForwardStore.Remove(port)
				w.WriteHeader(http.StatusNoContent)
			})

			// Peers management endpoints (auth-protected)
			r.Get("/peers", func(w http.ResponseWriter, r *http.Request) {
				handleGetPeers(w, r, opts)
			})
			r.Post("/peers", func(w http.ResponseWriter, r *http.Request) {
				handlePostPeers(w, r, opts)
			})
			r.Patch("/peers/{fp}", func(w http.ResponseWriter, r *http.Request) {
				handlePatchPeer(w, r, opts)
			})
			r.Post("/peers/{fp}/reconnect", func(w http.ResponseWriter, r *http.Request) {
				handleReconnectPeer(w, r, opts)
			})
			r.Delete("/peers/{fp}", func(w http.ResponseWriter, r *http.Request) {
				handleDeletePeer(w, r, opts)
			})
		})
	})

	// WebSocket routes (protected by auth if enabled)
	go hub.Run()

	ptyHandler := ws.NewPTYTerminalHandler(opts.Client.TmuxPath(), opts.ActivityTracker)

	if opts.AuthEnabled {
		authMw := auth.Middleware(opts.SessionMgr)
		r.With(authMw).Get("/ws/events", hub.HandleEvents)
		r.With(authMw).Get("/ws/session", func(w http.ResponseWriter, req *http.Request) {
			// Route remote sessions through PTY relay
			hostID := req.URL.Query().Get("host")
			if opts.PeerMgr != nil && hostID != "" && !opts.PeerMgr.IsLocal(hostID) {
				handleRemoteSession(w, req, opts, hostID)
				return
			}
			ptyHandler.HandleSession(w, req)
		})
	} else {
		r.Get("/ws/events", hub.HandleEvents)
		r.Get("/ws/session", func(w http.ResponseWriter, req *http.Request) {
			hostID := req.URL.Query().Get("host")
			if opts.PeerMgr != nil && hostID != "" && !opts.PeerMgr.IsLocal(hostID) {
				handleRemoteSession(w, req, opts, hostID)
				return
			}
			ptyHandler.HandleSession(w, req)
		})
	}

	// Peer WebSocket routes (no browser auth — peers use their own challenge-response)
	if opts.PeerHandler != nil {
		r.Get("/ws/peer", opts.PeerHandler.HandlePeer)
	}

	// Port-forward proxy — exposes localhost-bound services through guppi's URL.
	// Requires auth (same rule as other protected routes) so remote users can't
	// reach internal services without a valid session.
	if opts.PortForwardStore != nil {
		proxyHandler := func(w http.ResponseWriter, r *http.Request) {
			handleProxy(w, r, opts.Port)
		}
		if opts.AuthEnabled {
			authMw := auth.Middleware(opts.SessionMgr)
			r.With(authMw).Get("/proxy/{port}", proxyHandler)
			r.With(authMw).Get("/proxy/{port}/*", proxyHandler)
		} else {
			r.Get("/proxy/{port}", proxyHandler)
			r.Get("/proxy/{port}/*", proxyHandler)
		}
	}

	// Serve embedded frontend
	sub, err := fs.Sub(frontendFS, "dist")
	if err != nil {
		return fmt.Errorf("frontend fs: %w", err)
	}
	fileServer := http.FileServer(http.FS(sub))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		f, err := sub.Open(r.URL.Path[1:])
		if err != nil {
			r.URL.Path = "/"
		} else {
			f.Close()
		}
		fileServer.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Handler:           r,
		ErrorLog:          log.New(logger.WriterLevel(logrus.WarnLevel), "", 0),
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		// Note: ReadTimeout and WriteTimeout are intentionally omitted.
		// They apply to the underlying net.Conn and would kill long-lived
		// WebSocket connections after the timeout period.
	}

	serverErr := make(chan error, 2)

	// Start TCP listener for browser connections
	tcpAddr := fmt.Sprintf(":%d", opts.Port)
	tcpListener, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return fmt.Errorf("tcp listen: %w", err)
	}

	go func() {
		serveErr := srv.Serve(tcpListener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.WithError(serveErr).Error("tcp listen error")
			serverErr <- serveErr
		}
	}()

	logger.WithField("port", opts.Port).Info("starting guppi server")
	logger.Infof("open http://localhost:%d in your browser", opts.Port)
	if opts.AuthEnabled {
		logger.Info("authentication is enabled")
	}

	// Start Unix socket listener for local notify CLI
	var unixListener net.Listener
	socketPath := opts.SocketPath
	if socketPath == "" {
		socketPath = socket.DefaultPath()
	}
	if err := socket.EnsureDir(socketPath); err != nil {
		logger.WithError(err).Warn("failed to create socket directory, notify via socket will be unavailable")
	} else {
		// Remove stale socket file from a previous run
		_ = socket.Cleanup(socketPath)

		unixListener, err = net.Listen("unix", socketPath)
		if err != nil {
			logger.WithError(err).Warn("failed to listen on unix socket, notify via socket will be unavailable")
		} else {
			go func() {
				if err := srv.Serve(unixListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.WithError(err).Error("unix socket listen error")
					serverErr <- err
				}
			}()
			logger.WithField("socket", socketPath).Info("listening on unix socket")
		}
	}

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	}

	logger.Info("shutting down server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.WithError(err).Error("unable to shutdown gracefully")
		return err
	}

	// Clean up socket file
	if unixListener != nil {
		_ = socket.Cleanup(socketPath)
	}

	// Stop any socat processes
	if opts.PortForwardStore != nil {
		opts.PortForwardStore.StopAll()
	}

	return nil
}

func enrichSessionsFromTracker(sessions []*tmux.Session, tracker *toolevents.Tracker) {
	if tracker == nil {
		return
	}
	for _, session := range sessions {
		meta := tracker.SessionMetaFor(session.Host, session.Name)
		if session.AgentType == "" && meta.Tool != "" {
			session.AgentType = string(meta.Tool)
		}
		if session.ProjectPath == "" && meta.CWD != "" {
			session.ProjectPath = meta.CWD
		}
		if session.PromptPreview == "" && meta.Message != "" {
			session.PromptPreview = meta.Message
		}
		if session.AgentSessionID == "" && meta.AgentSessionID != "" {
			session.AgentSessionID = meta.AgentSessionID
		}
		if session.UserPrompt == "" && meta.UserPrompt != "" {
			session.UserPrompt = meta.UserPrompt
		}
		if session.LastAgentMessage == "" && meta.LastAgentMessage != "" {
			session.LastAgentMessage = meta.LastAgentMessage
		}
	}
}

func defaultSessionName(command, projectPath string) string {
	base := strings.TrimSpace(command)
	if idx := strings.IndexByte(base, ' '); idx >= 0 {
		base = base[:idx]
	}
	base = strings.Trim(base, `"'`)
	base = strings.TrimSpace(base)
	if base == "" {
		base = "session"
	}
	if projectPath == "" {
		return ""
	}

	projectBase := strings.TrimSpace(projectPath)
	projectBase = strings.TrimRight(projectBase, "/")
	if idx := strings.LastIndex(projectBase, "/"); idx >= 0 {
		projectBase = projectBase[idx+1:]
	}
	projectBase = sanitizeSessionSegment(projectBase)
	if projectBase == "" {
		projectBase = "workspace"
	}

	return sanitizeSessionSegment(base) + "-" + projectBase
}

func sanitizeSessionSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-', r == '_', r == '.', r == '/', r == ' ':
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func resolveNewSessionName(opts *Options, host, name, command, projectPath string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultSessionName(command, projectPath)
	}
	if name == "" {
		return ""
	}
	return ensureUniqueSessionName(name, existingSessionNames(opts, host))
}

func existingSessionNames(opts *Options, host string) []string {
	if opts == nil {
		return nil
	}

	if host != "" && opts.PeerMgr != nil && !opts.PeerMgr.IsLocal(host) {
		sessions := opts.PeerMgr.GetAllSessions()
		names := make([]string, 0, len(sessions))
		for _, session := range sessions {
			if session != nil && session.Host == host {
				names = append(names, session.Name)
			}
		}
		return names
	}

	if opts.Client != nil {
		if sessions, err := opts.Client.ListSessions(); err == nil {
			names := make([]string, 0, len(sessions))
			for _, session := range sessions {
				if session != nil {
					names = append(names, session.Name)
				}
			}
			return names
		}
	}

	if opts.StateMgr != nil {
		sessions := opts.StateMgr.GetSessions()
		names := make([]string, 0, len(sessions))
		for _, session := range sessions {
			if session != nil {
				names = append(names, session.Name)
			}
		}
		return names
	}

	return nil
}

func ensureUniqueSessionName(name string, existing []string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	used := make(map[string]struct{}, len(existing))
	for _, candidate := range existing {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			used[candidate] = struct{}{}
		}
	}

	if _, exists := used[name]; !exists {
		return name
	}

	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}
