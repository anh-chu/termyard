package peer

import (
	"sync"
	"time"
)

// CaptureResult is a pane-capture reply delivered to a waiting hub request.
type CaptureResult struct {
	Text  string
	Error string
}

// CaptureRegistry correlates capture-pane requests to their replies by token.
// Hub side only: the requester calls Await, the control-message handler that
// receives MsgCapturePaneResult calls Deliver.
type CaptureRegistry struct {
	mu      sync.Mutex
	pending map[string]chan CaptureResult
}

func NewCaptureRegistry() *CaptureRegistry {
	return &CaptureRegistry{pending: make(map[string]chan CaptureResult)}
}

// Register reserves token and returns its reply channel plus a cancel func to
// release it. Register before sending the request so a fast reply cannot race
// ahead of the waiter.
func (r *CaptureRegistry) Register(token string) (<-chan CaptureResult, func()) {
	ch := make(chan CaptureResult, 1)
	r.mu.Lock()
	r.pending[token] = ch
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.pending, token)
		r.mu.Unlock()
	}
}

// Await registers token, blocks for its reply or timeout, then unregisters.
// Convenience wrapper over Register for callers that send before waiting is
// acceptable (and for tests).
func (r *CaptureRegistry) Await(token string, timeout time.Duration) (CaptureResult, bool) {
	if r == nil || token == "" {
		return CaptureResult{}, false
	}
	ch, cancel := r.Register(token)
	defer cancel()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		return res, true
	case <-timer.C:
		return CaptureResult{}, false
	}
}

// Deliver hands a reply to the waiter for token, if one is still waiting.
func (r *CaptureRegistry) Deliver(token string, res CaptureResult) {
	if r == nil || token == "" {
		return
	}
	r.mu.Lock()
	ch, ok := r.pending[token]
	if ok {
		delete(r.pending, token)
	}
	r.mu.Unlock()
	if ok {
		ch <- res // buffered (cap 1), never blocks
	}
}
