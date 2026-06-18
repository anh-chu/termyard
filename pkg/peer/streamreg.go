package peer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const streamSetupTimeout = 20 * time.Second

// ponytail: capability negotiation stays out of Phase 1; Phase 3 adds it when the path branches.

type PendingStream struct {
	StreamID     string
	Session      string
	Cols, Rows   uint16
	HostID       string
	ViewerHostID string
	ExpectedPeer string
	resolved     chan *websocket.Conn
}

func NewPendingStream(streamID, session string, cols, rows uint16, hostID, viewerHostID, expectedPeer string) *PendingStream {
	return &PendingStream{
		StreamID:     streamID,
		Session:      session,
		Cols:         cols,
		Rows:         rows,
		HostID:       hostID,
		ViewerHostID: viewerHostID,
		ExpectedPeer: expectedPeer,
		resolved:     make(chan *websocket.Conn, 1),
	}
}

func (ps *PendingStream) WaitResolved(d time.Duration) (*websocket.Conn, bool) {
	if ps == nil {
		return nil, false
	}
	if d < 0 {
		d = 0
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case conn, ok := <-ps.resolved:
		if !ok || conn == nil {
			return nil, false
		}
		return conn, true
	case <-timer.C:
		return nil, false
	}
}

func StreamSetupTimeout() time.Duration { return streamSetupTimeout }

type pendingEntry struct {
	ps *PendingStream
}

type pendingWaiter struct {
	fingerprint string
	ch          chan *PendingStream
}

type StreamRegistry struct {
	mu      sync.Mutex
	pending map[string]*pendingEntry
	waiters map[string][]*pendingWaiter
	expired map[string]struct{}
}

var (
	errTokenNotFound = errors.New("stream token not found")
	errWrongPeer     = errors.New("stream token bound to different peer")
)

func NewStreamRegistry() *StreamRegistry {
	return &StreamRegistry{
		pending: make(map[string]*pendingEntry),
		waiters: make(map[string][]*pendingWaiter),
		expired: make(map[string]struct{}),
	}
}

func NewToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// GenerateStreamID creates a random stream ID.
func GenerateStreamID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func streamDeadline(ctx context.Context, fallback time.Duration) time.Time {
	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			return deadline
		}
	}
	return time.Now().Add(fallback)
}

func (s *StreamRegistry) Register(token string, ps *PendingStream) {
	if s == nil || token == "" || ps == nil {
		return
	}

	if ps.resolved == nil {
		ps.resolved = make(chan *websocket.Conn, 1)
	}

	s.mu.Lock()
	if _, dead := s.expired[token]; dead {
		s.mu.Unlock()
		return
	}

	waiters := s.waiters[token]
	if len(waiters) > 0 {
		match := -1
		for i, waiter := range waiters {
			if waiter != nil && waiter.fingerprint == ps.ExpectedPeer {
				match = i
				break
			}
		}
		if match >= 0 {
			delete(s.waiters, token)
			s.expired[token] = struct{}{}
			matched := waiters[match]
			s.mu.Unlock()

			select {
			case matched.ch <- ps:
			default:
			}
			close(matched.ch)
			for i, waiter := range waiters {
				if i != match {
					close(waiter.ch)
				}
			}
			return
		}
	}

	entry := &pendingEntry{ps: ps}
	s.pending[token] = entry
	s.mu.Unlock()

	time.AfterFunc(streamSetupTimeout, func() {
		s.expire(token, entry)
	})
}

func (s *StreamRegistry) Claim(ctx context.Context, token, fingerprint string) (*PendingStream, error) {
	if s == nil || token == "" {
		return nil, errTokenNotFound
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if _, dead := s.expired[token]; dead {
		s.mu.Unlock()
		return nil, errTokenNotFound
	}
	if entry, ok := s.pending[token]; ok {
		ps := entry.ps
		if ps.ExpectedPeer != fingerprint {
			s.mu.Unlock()
			return nil, errWrongPeer
		}
		delete(s.pending, token)
		s.expired[token] = struct{}{}
		waiters := s.waiters[token]
		delete(s.waiters, token)
		s.mu.Unlock()

		for _, waiter := range waiters {
			close(waiter.ch)
		}
		return ps, nil
	}

	waiter := &pendingWaiter{
		fingerprint: fingerprint,
		ch:          make(chan *PendingStream, 1),
	}
	s.waiters[token] = append(s.waiters[token], waiter)
	timeout := streamSetupTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	s.mu.Unlock()

	if timeout < 0 {
		timeout = 0
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case ps, ok := <-waiter.ch:
		if !ok || ps == nil {
			return nil, errTokenNotFound
		}
		if ps.ExpectedPeer != fingerprint {
			return nil, errWrongPeer
		}
		return ps, nil
	case <-timer.C:
		s.removeWaiter(token, waiter)
		return nil, errTokenNotFound
	case <-ctx.Done():
		s.removeWaiter(token, waiter)
		return nil, errTokenNotFound
	}
}

func (s *StreamRegistry) Resolve(ps *PendingStream, conn *websocket.Conn) {
	if conn == nil {
		return
	}
	if ps == nil {
		_ = conn.Close()
		return
	}
	if ps.resolved == nil {
		ps.resolved = make(chan *websocket.Conn, 1)
	}
	select {
	case ps.resolved <- conn:
	default:
		_ = conn.Close()
	}
}

func (s *StreamRegistry) expire(token string, entry *pendingEntry) {
	s.mu.Lock()
	cur, ok := s.pending[token]
	if !ok || cur != entry {
		s.mu.Unlock()
		return
	}
	delete(s.pending, token)
	s.expired[token] = struct{}{}
	waiters := s.waiters[token]
	delete(s.waiters, token)
	s.mu.Unlock()

	if entry.ps != nil && entry.ps.resolved != nil {
		select {
		case conn := <-entry.ps.resolved:
			_ = conn.Close()
		default:
		}
	}
	for _, waiter := range waiters {
		close(waiter.ch)
	}
}

func (s *StreamRegistry) removeWaiter(token string, target *pendingWaiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiters := s.waiters[token]
	if len(waiters) == 0 {
		return
	}
	kept := waiters[:0]
	for _, waiter := range waiters {
		if waiter != target {
			kept = append(kept, waiter)
		}
	}
	if len(kept) == 0 {
		delete(s.waiters, token)
		return
	}
	s.waiters[token] = kept
}
