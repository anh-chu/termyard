package pty

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// writeFrame writes a daemon wire frame to w: 1 byte type + 4 bytes BE length
// followed by the payload.
func writeFrame(t *testing.T, w io.Writer, ftype byte, payload []byte) {
	t.Helper()
	header := make([]byte, 5)
	header[0] = ftype
	binary.BigEndian.PutUint32(header[1:5], uint32(len(payload)))
	if _, err := w.Write(header); err != nil {
		t.Fatalf("write frame header: %v", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("write frame payload: %v", err)
		}
	}
}

// newTestDaemonSession wires a DaemonSession to the reader end of a net.Pipe
// and starts its frame goroutine. The caller writes frames to the returned
// writer. Close the writer to signal EOF.
func newTestDaemonSession(t *testing.T) (*DaemonSession, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	d := &DaemonSession{conn: client}
	d.cond = sync.NewCond(&d.mu)
	go d.readFrames()
	return d, server
}

func drainClose(t *testing.T, c io.Closer) {
	t.Helper()
	c.Close()
}

func TestDaemonSessionFramedRead(t *testing.T) {
	t.Run("ReplayThenLive", func(t *testing.T) {
		d, server := newTestDaemonSession(t)
		defer drainClose(t, server)
		defer d.Close()

		replay := []byte("hello replay world")
		live := []byte("live stream begins")

		writeFrame(t, server, FrameReplay, replay)
		writeFrame(t, server, FrameOutput, live)

		var gotReplay bytes.Buffer
		var sawBoundary bool
		var gotLive bytes.Buffer
		deadline := time.Now().Add(2 * time.Second)

		for time.Now().Before(deadline) {
			buf := make([]byte, 8)
			n, kind, err := d.ReadFramed(buf)
			if err == io.EOF && gotLive.Len() > 0 && gotReplay.Len() == len(replay) {
				break
			}
			if err != nil {
				t.Fatalf("ReadFramed err: %v", err)
			}
			switch kind {
			case ChunkReplay:
				if sawBoundary {
					t.Fatalf("ChunkReplay returned after boundary")
				}
				gotReplay.Write(buf[:n])
			case ChunkReplayBoundary:
				if sawBoundary {
					t.Fatalf("boundary emitted more than once")
				}
				if n != 0 {
					t.Fatalf("boundary must have n==0, got %d", n)
				}
				sawBoundary = true
			case ChunkLive:
				if !sawBoundary {
					t.Fatalf("ChunkLive before boundary")
				}
				gotLive.Write(buf[:n])
			default:
				t.Fatalf("unexpected kind %v", kind)
			}
			if gotReplay.Len() == len(replay) && sawBoundary && gotLive.Len() == len(live) {
				break
			}
		}

		if gotReplay.String() != string(replay) {
			t.Fatalf("replay mismatch: got %q, want %q", gotReplay.Bytes(), replay)
		}
		if !sawBoundary {
			t.Fatalf("boundary never emitted")
		}
		if gotLive.String() != string(live) {
			t.Fatalf("live mismatch: got %q, want %q", gotLive.Bytes(), live)
		}
	})

	t.Run("LiveOnlyNoBoundary", func(t *testing.T) {
		d, server := newTestDaemonSession(t)
		defer drainClose(t, server)
		defer d.Close()

		live := []byte("no replay here")
		writeFrame(t, server, FrameOutput, live)

		var sawBoundary bool
		var got bytes.Buffer
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			buf := make([]byte, 8)
			n, kind, err := d.ReadFramed(buf)
			if err == io.EOF && got.Len() == len(live) {
				break
			}
			if err != nil {
				t.Fatalf("ReadFramed err: %v", err)
			}
			switch kind {
			case ChunkReplayBoundary:
				sawBoundary = true
			case ChunkLive:
				got.Write(buf[:n])
			default:
				t.Fatalf("unexpected kind %v", kind)
			}
			if got.Len() == len(live) {
				break
			}
		}

		if sawBoundary {
			t.Fatalf("boundary should not be emitted when no replay frame arrived")
		}
		if got.String() != string(live) {
			t.Fatalf("live mismatch: got %q, want %q", got.Bytes(), live)
		}
	})

	t.Run("BoundaryTransitionPreservesOrdering", func(t *testing.T) {
		d, server := newTestDaemonSession(t)
		defer drainClose(t, server)
		defer d.Close()

		replay := []byte("replay-end")
		live := []byte("live-start")
		writeFrame(t, server, FrameReplay, replay)
		writeFrame(t, server, FrameOutput, live)

		var lastReplay byte
		var firstLive byte
		var lastWasReplay, sawBoundary bool
		deadline := time.Now().Add(2 * time.Second)

		for time.Now().Before(deadline) {
			buf := make([]byte, 1)
			n, kind, err := d.ReadFramed(buf)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("ReadFramed err: %v", err)
			}
			switch kind {
			case ChunkReplay:
				if sawBoundary {
					t.Fatalf("replay after boundary")
				}
				lastReplay = buf[n-1]
				lastWasReplay = true
			case ChunkReplayBoundary:
				sawBoundary = true
			case ChunkLive:
				if !sawBoundary {
					t.Fatalf("live before boundary")
				}
				if !lastWasReplay {
					t.Fatalf("expected replay immediately before boundary")
				}
				firstLive = buf[n-1]
				lastWasReplay = false
			}
			if sawBoundary && !lastWasReplay {
				break
			}
		}

		if lastReplay != replay[len(replay)-1] {
			t.Fatalf("last replay byte mismatch: got %q, want %q", lastReplay, replay[len(replay)-1])
		}
		if firstLive != live[0] {
			t.Fatalf("first live byte mismatch: got %q, want %q", firstLive, live[0])
		}
	})

	t.Run("LegacyReadFlattensReplayThenLive", func(t *testing.T) {
		d, server := newTestDaemonSession(t)
		defer drainClose(t, server)
		defer d.Close()

		replay := []byte("replay data")
		live := []byte("live data")
		writeFrame(t, server, FrameReplay, replay)
		writeFrame(t, server, FrameOutput, live)

		var got bytes.Buffer
		buf := make([]byte, 8)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			n, err := d.Read(buf)
			if n > 0 {
				got.Write(buf[:n])
			}
			if err == io.EOF && got.Len() == len(replay)+len(live) {
				break
			}
			if err != nil && err != io.EOF {
				t.Fatalf("Read err: %v", err)
			}
			if got.Len() == len(replay)+len(live) {
				break
			}
		}

		want := string(replay) + string(live)
		if got.String() != want {
			t.Fatalf("flat read mismatch: got %q, want %q", got.Bytes(), want)
		}
	})
}
