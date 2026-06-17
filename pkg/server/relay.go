package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anh-chu/termyard/pkg/auth"
	"github.com/anh-chu/termyard/pkg/identity"
	"github.com/anh-chu/termyard/pkg/peer"
	"github.com/anh-chu/termyard/pkg/portforward"
)

const (
	relayHubHeader  = "X-Termyard-Hub"
	relayUserHeader = "X-Termyard-User"
	relayTimeout    = 15 * time.Second
	relaySkew       = 30 * time.Second
)

var (
	errRelayUnavailable  = errors.New("peer relay unavailable")
	errRelayUnknown      = errors.New("unknown host")
	errRelayNoAddress    = errors.New("peer address unknown")
	errRelayBadAddress   = errors.New("peer address invalid")
	errRelayForbidden    = errors.New("relay forbidden")
	errRelayUnauthorized = errors.New("unauthorized")

	relayHTTPClient = &http.Client{
		Timeout: relayTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		},
	}
)

func relayAllowed(path string) bool {
	if path == "/api/portforwards" {
		return true
	}
	return strings.HasPrefix(path, "/api/portforward/")
}

func relayUserSubject(opts *Options) string {
	if opts == nil || opts.Identity == nil {
		return "dashboard"
	}
	return "dashboard@" + opts.Identity.Fingerprint()
}

func relaySkippedHeaders(src http.Header) map[string]struct{} {
	skip := map[string]struct{}{
		"Connection":        {},
		"Proxy-Connection":  {},
		"Keep-Alive":        {},
		"Transfer-Encoding": {},
		"Te":                {},
		"Trailer":           {},
		"Upgrade":           {},
		"Cookie":            {},
		"Set-Cookie":        {},
		"Authorization":     {},
		relayHubHeader:      {},
		relayUserHeader:     {},
	}
	for _, value := range src.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			skip[http.CanonicalHeaderKey(token)] = struct{}{}
		}
	}
	return skip
}

func copyRelayHeaders(dst, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	skip := relaySkippedHeaders(src)
	for k, values := range src {
		if _, ok := skip[http.CanonicalHeaderKey(k)]; ok {
			continue
		}
		for _, v := range values {
			dst.Add(k, v)
		}
	}
}

func signedRelayHeader(id *identity.Identity, method, pathQuery string) (string, error) {
	ts := strconv.FormatInt(time.Now().UnixNano(), 10)
	msg := []byte(id.Fingerprint() + "." + ts + "." + method + " " + pathQuery)
	sig, err := id.Sign(msg)
	if err != nil {
		return "", err
	}
	return id.Fingerprint() + "." + ts + "." + base64.StdEncoding.EncodeToString(sig), nil
}

func relayPeerRequest(
	ctx context.Context,
	opts *Options,
	fp string,
	method string,
	downstreamPath string,
	rawQuery string,
	body []byte,
	srcHeaders http.Header,
	userSubject string,
) (status int, respBody []byte, respCT string, err error) {
	if !relayAllowed(downstreamPath) {
		return 0, nil, "", errRelayForbidden
	}
	if opts == nil || opts.Identity == nil || opts.PeerStore == nil {
		return 0, nil, "", errRelayUnavailable
	}

	p := opts.PeerStore.GetByFingerprint(fp)
	if p == nil {
		return 0, nil, "", errRelayUnknown
	}
	if p.Address == "" {
		return 0, nil, "", errRelayNoAddress
	}
	addr, err := peer.NormalizeAddress(p.Address)
	if err != nil {
		return 0, nil, "", fmt.Errorf("%w: %v", errRelayBadAddress, err)
	}

	pathQuery := downstreamPath
	if rawQuery != "" {
		pathQuery += "?" + rawQuery
	}

	ctx, cancel := context.WithTimeout(ctx, relayTimeout)
	defer cancel()

	url := "http://" + addr + pathQuery
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, "", err
	}
	copyRelayHeaders(req.Header, srcHeaders)
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "termyard")
	}
	if userSubject != "" {
		req.Header.Set(relayUserHeader, userSubject)
	}
	signed, err := signedRelayHeader(opts.Identity, method, pathQuery)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set(relayHubHeader, signed)

	resp, err := relayHTTPClient.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, "", err
	}
	return resp.StatusCode, data, resp.Header.Get("Content-Type"), nil
}

func verifyRelayHeader(opts *Options, r *http.Request) (string, error) {
	if opts == nil || opts.PeerStore == nil {
		return "", errRelayUnavailable
	}
	hdr := strings.TrimSpace(r.Header.Get(relayHubHeader))
	if hdr == "" {
		return "", errRelayUnauthorized
	}
	parts := strings.SplitN(hdr, ".", 3)
	if len(parts) != 3 {
		return "", errRelayUnauthorized
	}
	hubFP, tsStr, sigB64 := parts[0], parts[1], parts[2]
	p := opts.PeerStore.GetByFingerprint(hubFP)
	if p == nil {
		return "", errRelayUnauthorized
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return "", errRelayUnauthorized
	}
	if d := time.Since(time.Unix(0, ts)); d < -relaySkew || d > relaySkew {
		return "", errRelayUnauthorized
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return "", errRelayUnauthorized
	}
	pathQuery := r.URL.Path
	if r.URL.RawQuery != "" {
		pathQuery += "?" + r.URL.RawQuery
	}
	msg := []byte(hubFP + "." + tsStr + "." + r.Method + " " + pathQuery)
	if !identity.Verify(p.PublicKey, msg, sig) {
		return "", errRelayUnauthorized
	}
	return hubFP, nil
}

func portForwardRelayMiddleware(opts *Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(relayHubHeader) != "" {
				if _, err := verifyRelayHeader(opts, r); err != nil {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if opts != nil && opts.AuthEnabled {
				if opts.SessionMgr == nil {
					http.Error(w, "auth unavailable", http.StatusServiceUnavailable)
					return
				}
				auth.Middleware(opts.SessionMgr)(next).ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func decorateProxyBaseURL(body []byte, baseURL string) ([]byte, bool, error) {
	var forwards []portforward.Forward
	if err := json.Unmarshal(body, &forwards); err != nil {
		return nil, false, err
	}
	changed := false
	for i := range forwards {
		if forwards[i].Mode == portforward.ModeProxy {
			forwards[i].BaseURL = baseURL
			changed = true
		}
	}
	if !changed {
		return body, false, nil
	}
	out, err := json.Marshal(forwards)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}
