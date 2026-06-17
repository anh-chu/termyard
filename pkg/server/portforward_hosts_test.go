package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/anh-chu/termyard/pkg/identity"
	"github.com/anh-chu/termyard/pkg/portforward"
)

func relayHeaderForTest(t *testing.T, id *identity.Identity, method, pathQuery string, ts time.Time) string {
	t.Helper()
	tsStr := strconv.FormatInt(ts.UnixNano(), 10)
	msg := []byte(id.Fingerprint() + "." + tsStr + "." + method + " " + pathQuery)
	sig, err := id.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	return id.Fingerprint() + "." + tsStr + "." + base64.StdEncoding.EncodeToString(sig)
}

func newRelayPeerStore(t *testing.T) *identity.PeerStore {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ps, err := identity.NewPeerStore()
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestRelayPeerRequest_SignsAndStrips(t *testing.T) {
	hub, _ := identity.Generate("hub")
	remote, _ := identity.Generate("remote")
	ps := newRelayPeerStore(t)

	seen := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		if r.Method != http.MethodPost {
			t.Fatalf("method %s", r.Method)
		}
		if r.URL.Path != "/api/portforwards" {
			t.Fatalf("path %s", r.URL.Path)
		}
		hdr := r.Header.Get(relayHubHeader)
		parts := strings.SplitN(hdr, ".", 3)
		if len(parts) != 3 {
			t.Fatalf("bad hub header %q", hdr)
		}
		tsStr := parts[1]
		sig, err := base64.StdEncoding.DecodeString(parts[2])
		if err != nil {
			t.Fatal(err)
		}
		msg := []byte(hub.Fingerprint() + "." + tsStr + "." + http.MethodPost + " /api/portforwards")
		if !identity.Verify(hub.PublicKey, msg, sig) {
			t.Fatalf("bad signature")
		}
		if got := r.Header.Get(relayUserHeader); got != "dashboard@"+hub.Fingerprint() {
			t.Fatalf("user header %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Fatalf("cookie leaked: %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("authorization leaked: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"port":8080,"label":"ok","mode":"proxy"}]`))
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	if err := ps.Add(identity.Peer{Name: "remote", PublicKey: remote.PublicKey, Address: addr, Enabled: true, InitiatedByUs: true}); err != nil {
		t.Fatal(err)
	}

	opts := &Options{Identity: hub, PeerStore: ps}
	status, body, ct, err := relayPeerRequest(context.Background(), opts, remote.Fingerprint(), http.MethodPost, "/api/portforwards", "", []byte(`{"port":8080}`), http.Header{
		"Cookie":        []string{"a=b"},
		"Authorization": []string{"Bearer x"},
	}, relayUserSubject(opts))
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if ct != "application/json" {
		t.Fatalf("ct %q", ct)
	}
	if !bytes.Contains(body, []byte(`"port":8080`)) {
		t.Fatalf("body %s", body)
	}
	select {
	case <-seen:
	default:
		t.Fatal("peer request not seen")
	}
}

func TestRelayPeerRequest_PassthroughStatus(t *testing.T) {
	hub, _ := identity.Generate("hub")
	remote, _ := identity.Generate("remote")
	ps := newRelayPeerStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer srv.Close()
	if err := ps.Add(identity.Peer{Name: "remote", PublicKey: remote.PublicKey, Address: strings.TrimPrefix(srv.URL, "http://"), Enabled: true, InitiatedByUs: true}); err != nil {
		t.Fatal(err)
	}
	opts := &Options{Identity: hub, PeerStore: ps}
	status, body, _, err := relayPeerRequest(context.Background(), opts, remote.Fingerprint(), http.MethodGet, "/api/portforwards", "", nil, nil, relayUserSubject(opts))
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusBadRequest {
		t.Fatalf("status %d", status)
	}
	if string(body) != "bad request" {
		t.Fatalf("body %q", body)
	}
}

func TestPortForwardRelayMiddleware(t *testing.T) {
	hub, _ := identity.Generate("hub")
	ps := newRelayPeerStore(t)
	if err := ps.Add(identity.Peer{Name: "hub", PublicKey: hub.PublicKey, Address: "127.0.0.1:1", Enabled: true, InitiatedByUs: true}); err != nil {
		t.Fatal(err)
	}
	opts := &Options{PeerStore: ps}

	makeReq := func(header string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/portforwards", nil)
		if header != "" {
			req.Header.Set(relayHubHeader, header)
		}
		return req
	}

	t.Run("valid", func(t *testing.T) {
		opts.PeerStore = ps
		relayHdr := relayHeaderForTest(t, hub, http.MethodGet, "/api/portforwards", time.Now())
		req := makeReq(relayHdr)
		w := httptest.NewRecorder()
		called := false
		portForwardRelayMiddleware(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(w, req)
		if w.Code != http.StatusNoContent || !called {
			t.Fatalf("code=%d called=%v", w.Code, called)
		}
	})

	t.Run("unknown hub", func(t *testing.T) {
		other, _ := identity.Generate("other")
		req := makeReq(relayHeaderForTest(t, other, http.MethodGet, "/api/portforwards", time.Now()))
		w := httptest.NewRecorder()
		called := false
		portForwardRelayMiddleware(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })).ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized || called {
			t.Fatalf("code=%d called=%v", w.Code, called)
		}
	})

	t.Run("bad signature", func(t *testing.T) {
		head := relayHeaderForTest(t, hub, http.MethodGet, "/api/portforwards", time.Now())
		head = strings.TrimSuffix(head, "A") + "B"
		req := makeReq(head)
		w := httptest.NewRecorder()
		called := false
		portForwardRelayMiddleware(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })).ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized || called {
			t.Fatalf("code=%d called=%v", w.Code, called)
		}
	})

	t.Run("expired", func(t *testing.T) {
		req := makeReq(relayHeaderForTest(t, hub, http.MethodGet, "/api/portforwards", time.Now().Add(-2*relaySkew)))
		w := httptest.NewRecorder()
		called := false
		portForwardRelayMiddleware(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })).ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized || called {
			t.Fatalf("code=%d called=%v", w.Code, called)
		}
	})

	t.Run("no header falls through", func(t *testing.T) {
		req := makeReq("")
		w := httptest.NewRecorder()
		called := false
		portForwardRelayMiddleware(&Options{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)
		if w.Code != http.StatusOK || !called {
			t.Fatalf("code=%d called=%v", w.Code, called)
		}
	})
}

func TestHostPortForwards_LocalShortCircuit(t *testing.T) {
	opts := newTestOpts(t)
	opts.PortForwardStore = portforward.NewStore()
	localID := opts.PeerMgr.LocalID()

	router := chi.NewRouter()
	router.Post("/api/hosts/{fp}/portforwards", func(w http.ResponseWriter, r *http.Request) {
		handleHostPortForwards(w, r, opts, http.MethodPost)
	})

	body := `{"port":8123,"label":"local","mode":"proxy"}`
	req := httptest.NewRequest(http.MethodPost, "/api/hosts/"+localID+"/portforwards", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("code %d body=%s", w.Code, w.Body.String())
	}
	fs := opts.PortForwardStore.List()
	if len(fs) != 1 || fs[0].Port != 8123 || fs[0].BaseURL != "" {
		t.Fatalf("store %+v", fs)
	}
}

func TestHostPortForwards_RemoteDecoratesProxyBaseURL(t *testing.T) {
	opts := newTestOpts(t)
	remote, _ := identity.Generate("remote")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method %s", r.Method)
		}
		if r.URL.Path != "/api/portforwards" {
			t.Fatalf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]portforward.Forward{
			{Port: 3000, Label: "proxy", Mode: portforward.ModeProxy},
			{Port: 4000, Label: "tcp", Mode: portforward.ModeSocat, ExternalPort: 4000},
		})
	}))
	defer srv.Close()
	if err := opts.PeerStore.Add(identity.Peer{Name: "remote", PublicKey: remote.PublicKey, Address: strings.TrimPrefix(srv.URL, "http://"), Enabled: true, InitiatedByUs: true}); err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	router.Get("/api/hosts/{fp}/portforwards", func(w http.ResponseWriter, r *http.Request) {
		handleHostPortForwards(w, r, opts, http.MethodGet)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/hosts/"+remote.Fingerprint()+"/portforwards", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d body=%s", w.Code, w.Body.String())
	}
	var got []portforward.Forward
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len %d", len(got))
	}
	if got[0].BaseURL != "http://"+strings.TrimPrefix(srv.URL, "http://") {
		t.Fatalf("proxy base %q", got[0].BaseURL)
	}
	if got[1].BaseURL != "" {
		t.Fatalf("socat base %q", got[1].BaseURL)
	}
}

func TestHostPortForwardDelete_RemotePassesThrough(t *testing.T) {
	opts := newTestOpts(t)
	remote, _ := identity.Generate("remote")
	seen := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method %s", r.Method)
		}
		if r.URL.Path != "/api/portforward/8080" {
			t.Fatalf("path %s", r.URL.Path)
		}
		seen <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := opts.PeerStore.Add(identity.Peer{Name: "remote", PublicKey: remote.PublicKey, Address: strings.TrimPrefix(srv.URL, "http://"), Enabled: true, InitiatedByUs: true}); err != nil {
		t.Fatal(err)
	}

	router := chi.NewRouter()
	router.Delete("/api/hosts/{fp}/portforward/{port}", func(w http.ResponseWriter, r *http.Request) {
		handleHostPortForwardDelete(w, r, opts)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/hosts/"+remote.Fingerprint()+"/portforward/8080", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("code %d body=%s", w.Code, w.Body.String())
	}
	select {
	case <-seen:
	default:
		t.Fatal("remote delete not seen")
	}
}

func TestRelayAllowed(t *testing.T) {
	if !relayAllowed("/api/portforwards") {
		t.Fatal("expected /api/portforwards allowed")
	}
	if !relayAllowed("/api/portforward/8080") {
		t.Fatal("expected /api/portforward/8080 allowed")
	}
	if relayAllowed("/api/portforward") {
		t.Fatal("expected /api/portforward forbidden")
	}
	if relayAllowed("/api/hosts/abc/portforwards") {
		t.Fatal("expected /api/hosts/abc/portforwards forbidden")
	}
}

func TestCopyRelayHeaders_StripsConnectionNominatedHeaders(t *testing.T) {
	dst := http.Header{}
	src := http.Header{
		"Connection":        {"X-Leak, keep-alive"},
		"X-Leak":            {"secret"},
		"Keep-Alive":        {"timeout=5"},
		"Transfer-Encoding": {"chunked"},
		"Cookie":            {"a=b"},
		"Authorization":     {"Bearer x"},
		"X-Termyard-Hub":    {"signed"},
		"X-Termyard-User":   {"dashboard"},
		"X-Good":            {"ok"},
	}
	copyRelayHeaders(dst, src)
	if got := dst.Get("X-Leak"); got != "" {
		t.Fatalf("X-Leak leaked %q", got)
	}
	if got := dst.Get("Keep-Alive"); got != "" {
		t.Fatalf("Keep-Alive leaked %q", got)
	}
	if got := dst.Get("Transfer-Encoding"); got != "" {
		t.Fatalf("Transfer-Encoding leaked %q", got)
	}
	if got := dst.Get("Cookie"); got != "" {
		t.Fatalf("Cookie leaked %q", got)
	}
	if got := dst.Get("Authorization"); got != "" {
		t.Fatalf("Authorization leaked %q", got)
	}
	if got := dst.Get("X-Termyard-Hub"); got != "" {
		t.Fatalf("hub header leaked %q", got)
	}
	if got := dst.Get("X-Termyard-User"); got != "" {
		t.Fatalf("user header leaked %q", got)
	}
	if got := dst.Get("X-Good"); got != "ok" {
		t.Fatalf("X-Good %q", got)
	}
}

func TestRelayPeerRequest_Errors(t *testing.T) {
	hub, _ := identity.Generate("hub")
	ps := newRelayPeerStore(t)
	opts := &Options{Identity: hub, PeerStore: ps}

	t.Run("unknown host", func(t *testing.T) {
		_, _, _, err := relayPeerRequest(context.Background(), opts, "deadbeef", http.MethodGet, "/api/portforwards", "", nil, nil, relayUserSubject(opts))
		if !errors.Is(err, errRelayUnknown) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("no address", func(t *testing.T) {
		remote, _ := identity.Generate("remote")
		if err := ps.Add(identity.Peer{Name: "remote", PublicKey: remote.PublicKey, Enabled: true, InitiatedByUs: true}); err != nil {
			t.Fatal(err)
		}
		_, _, _, err := relayPeerRequest(context.Background(), opts, remote.Fingerprint(), http.MethodGet, "/api/portforwards", "", nil, nil, relayUserSubject(opts))
		if !errors.Is(err, errRelayNoAddress) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("bad address", func(t *testing.T) {
		remote, _ := identity.Generate("remote-bad")
		if err := ps.Add(identity.Peer{Name: "remote-bad", PublicKey: remote.PublicKey, Address: "http://", Enabled: true, InitiatedByUs: true}); err != nil {
			t.Fatal(err)
		}
		_, _, _, err := relayPeerRequest(context.Background(), opts, remote.Fingerprint(), http.MethodGet, "/api/portforwards", "", nil, nil, relayUserSubject(opts))
		if !errors.Is(err, errRelayBadAddress) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		remote, _ := identity.Generate("remote-down")
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}
		if err := ps.Add(identity.Peer{Name: "remote-down", PublicKey: remote.PublicKey, Address: addr, Enabled: true, InitiatedByUs: true}); err != nil {
			t.Fatal(err)
		}
		_, _, _, err = relayPeerRequest(context.Background(), opts, remote.Fingerprint(), http.MethodGet, "/api/portforwards", "", nil, nil, relayUserSubject(opts))
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
