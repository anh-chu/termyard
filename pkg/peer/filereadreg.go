package peer

import (
	"sync"
	"time"
)

// FileReadResult is a file-read reply delivered to a waiting hub request.
type FileReadResult struct {
	Data        string // base64-encoded file content
	ContentType string // MIME type
	FileName    string // basename
	Error       string
}

// FileReadRegistry correlates file-read requests to their replies by token.
// Hub side only: the requester calls Register, the control-message handler that
// receives MsgFileReadResult calls Deliver.
type FileReadRegistry struct {
	mu      sync.Mutex
	pending map[string]chan FileReadResult
}

func NewFileReadRegistry() *FileReadRegistry {
	return &FileReadRegistry{pending: make(map[string]chan FileReadResult)}
}

// Register reserves token and returns its reply channel plus a cancel func.
func (r *FileReadRegistry) Register(token string) (<-chan FileReadResult, func()) {
	ch := make(chan FileReadResult, 1)
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
func (r *FileReadRegistry) Await(token string, timeout time.Duration) (FileReadResult, bool) {
	if r == nil || token == "" {
		return FileReadResult{}, false
	}
	ch, cancel := r.Register(token)
	defer cancel()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		return res, true
	case <-timer.C:
		return FileReadResult{}, false
	}
}

// Deliver hands a reply to the waiter for token, if one is still waiting.
func (r *FileReadRegistry) Deliver(token string, res FileReadResult) {
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
		ch <- res
	}
}
