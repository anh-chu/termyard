package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/anh-chu/termyard/pkg/peer"
	"github.com/anh-chu/termyard/pkg/portforward"
)

func servePortForwardsGet(w http.ResponseWriter, _ *http.Request, opts *Options) {
	if opts.PortForwardStore == nil {
		http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(opts.PortForwardStore.List())
}

func servePortForwardsPost(w http.ResponseWriter, r *http.Request, opts *Options) {
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
	_ = json.NewEncoder(w).Encode(opts.PortForwardStore.List())
}

func servePortForwardsDelete(w http.ResponseWriter, r *http.Request, opts *Options) {
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
}

func handleHostPortForwards(w http.ResponseWriter, r *http.Request, opts *Options, method string) {
	fp := chi.URLParam(r, "fp")
	if opts == nil {
		http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
		return
	}
	if opts.PeerMgr != nil && opts.PeerMgr.IsLocal(fp) {
		switch method {
		case http.MethodGet:
			servePortForwardsGet(w, r, opts)
		case http.MethodPost:
			servePortForwardsPost(w, r, opts)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if method != http.MethodGet && method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body []byte
	if method == http.MethodPost {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}

	status, respBody, respCT, err := relayPeerRequest(r.Context(), opts, fp, method, "/api/portforwards", r.URL.RawQuery, body, r.Header, relayUserSubject(opts))
	if err != nil {
		writeRelayError(w, err)
		return
	}

	if status >= 200 && status < 300 && len(respBody) > 0 {
		if p := opts.PeerStore.GetByFingerprint(fp); p != nil {
			if addr, err := peer.NormalizeAddress(p.Address); err == nil {
				if decorated, ok, err := decorateProxyBaseURL(respBody, "http://"+addr); err == nil && ok {
					respBody = decorated
					respCT = "application/json"
				}
			}
		}
	}

	if respCT != "" {
		w.Header().Set("Content-Type", respCT)
	}
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
}

func handleHostPortForwardDelete(w http.ResponseWriter, r *http.Request, opts *Options) {
	fp := chi.URLParam(r, "fp")
	if opts == nil {
		http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
		return
	}
	if opts.PeerMgr != nil && opts.PeerMgr.IsLocal(fp) {
		servePortForwardsDelete(w, r, opts)
		return
	}
	port := chi.URLParam(r, "port")
	status, respBody, _, err := relayPeerRequest(r.Context(), opts, fp, http.MethodDelete, "/api/portforward/"+port, "", nil, r.Header, relayUserSubject(opts))
	if err != nil {
		writeRelayError(w, err)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
}

func writeRelayError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errRelayUnknown):
		http.Error(w, "unknown host", http.StatusNotFound)
	case errors.Is(err, errRelayNoAddress):
		http.Error(w, "peer address unknown", http.StatusServiceUnavailable)
	case errors.Is(err, errRelayBadAddress):
		http.Error(w, "peer address invalid", http.StatusServiceUnavailable)
	case errors.Is(err, errRelayForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, errRelayUnavailable):
		http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
	default:
		http.Error(w, "peer unreachable", http.StatusBadGateway)
	}
}
