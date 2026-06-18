package peer

import (
	"sync"
	"time"
)

// TraceEvent is one timestamped point on a remote PTY stream's lifecycle.
// Events are correlated across hosts by Stream (the stream id is generated on
// the viewer/dialer and echoed to the listener in pty-open), and to the
// browser by Session + wall clock.
type TraceEvent struct {
	UnixUs  int64  `json:"unix_us"`           // wall clock, microseconds (cross-host diffable; assumes NTP)
	ISO     string `json:"iso"`               // human-readable timestamp
	Host    string `json:"host"`              // which process emitted it
	Side    string `json:"side"`              // "viewer" | "listener" | "browser"
	Stream  string `json:"stream,omitempty"`  // backend stream id (viewer/listener correlation key)
	Session string `json:"session,omitempty"` // tmux session name (browser correlation key)
	Event   string `json:"event"`
	Bytes   int    `json:"bytes,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ponytail: single global ring buffer, fixed cap. A real per-stream tracer
// only if the 4000-event window proves too small under a firehose.
type traceRing struct {
	mu  sync.Mutex
	buf []TraceEvent
	max int
}

var (
	relayTrace = &traceRing{max: 4000}
	traceHost  string
)

// SetTraceHost records this process's host name for trace attribution.
func SetTraceHost(name string) { traceHost = name }

// Trace records one lifecycle event. O(1) append under a short lock.
func Trace(side, stream, session, event string, nbytes int, detail string) {
	now := time.Now()
	e := TraceEvent{
		UnixUs:  now.UnixMicro(),
		ISO:     now.Format("15:04:05.000"),
		Host:    traceHost,
		Side:    side,
		Stream:  stream,
		Session: session,
		Event:   event,
		Bytes:   nbytes,
		Detail:  detail,
	}
	relayTrace.mu.Lock()
	if len(relayTrace.buf) >= relayTrace.max {
		// Drop the oldest quarter so we amortize the shift.
		relayTrace.buf = append(relayTrace.buf[:0], relayTrace.buf[relayTrace.max/4:]...)
	}
	relayTrace.buf = append(relayTrace.buf, e)
	relayTrace.mu.Unlock()
}

// TraceSnapshot returns a copy of buffered events, optionally filtered to a
// session name or stream id substring match.
func TraceSnapshot(filter string) []TraceEvent {
	relayTrace.mu.Lock()
	defer relayTrace.mu.Unlock()
	out := make([]TraceEvent, 0, len(relayTrace.buf))
	for _, e := range relayTrace.buf {
		if filter != "" && e.Session != filter && e.Stream != filter {
			continue
		}
		out = append(out, e)
	}
	return out
}

// TraceAppendExternal lets the frontend push browser-side events into the same
// buffer so one snapshot carries the full viewer/browser timeline.
func TraceAppendExternal(e TraceEvent) {
	if e.UnixUs == 0 {
		now := time.Now()
		e.UnixUs = now.UnixMicro()
		e.ISO = now.Format("15:04:05.000")
	}
	if e.Host == "" {
		e.Host = traceHost
	}
	if e.Side == "" {
		e.Side = "browser"
	}
	relayTrace.mu.Lock()
	relayTrace.buf = append(relayTrace.buf, e)
	relayTrace.mu.Unlock()
}

// TraceClear empties the buffer (debug endpoint reset before a fresh repro).
func TraceClear() {
	relayTrace.mu.Lock()
	relayTrace.buf = relayTrace.buf[:0]
	relayTrace.mu.Unlock()
}
