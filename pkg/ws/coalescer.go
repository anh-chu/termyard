package ws

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/anh-chu/termyard/pkg/tmux"
	"github.com/anh-chu/termyard/pkg/toolevents"
)

const (
	outputQuietWindow        = 2 * time.Millisecond
	maxOutputFrameBytes      = 64 * 1024
	outputQueueCapacity      = 16
	scanQueueCapacity        = 16
	slowOutputWriteThreshold = 100 * time.Millisecond
	maxArtifactTailBytes     = 4096
)

type outputTimer interface {
	Chan() <-chan time.Time
	Reset(time.Duration)
	Stop()
}

type resettableOutputTimer struct {
	timer *time.Timer
}

func newResettableOutputTimer() *resettableOutputTimer {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	return &resettableOutputTimer{timer: timer}
}

func (t *resettableOutputTimer) Chan() <-chan time.Time {
	return t.timer.C
}

func (t *resettableOutputTimer) Reset(delay time.Duration) {
	t.timer.Reset(delay)
}

func (t *resettableOutputTimer) Stop() {
	t.timer.Stop()
}

type outputWriterFunc func(int, []byte) error

type outputWriteErrorFunc func(error)

type outputCoalescerStats struct {
	frames     int64
	bytes      int64
	maxFrame   int
	slowWrites int64
}

type outputCoalescer struct {
	write        outputWriterFunc
	onWriteError outputWriteErrorFunc
	timer        outputTimer
	quietWindow  time.Duration
	maxFrame     int

	chunks chan []byte
	pongs  chan struct{}
	done   chan struct{}

	closeOnce sync.Once
	stats     outputCoalescerStats
}

func newOutputCoalescer(write outputWriterFunc, onWriteError outputWriteErrorFunc, timer outputTimer) *outputCoalescer {
	coalescer := &outputCoalescer{
		write:        write,
		onWriteError: onWriteError,
		timer:        timer,
		quietWindow:  outputQuietWindow,
		maxFrame:     maxOutputFrameBytes,
		chunks:       make(chan []byte, outputQueueCapacity),
		pongs:        make(chan struct{}, 4),
		done:         make(chan struct{}),
	}
	go coalescer.run()
	return coalescer
}

func (c *outputCoalescer) Submit(chunk []byte) {
	c.chunks <- chunk
}

func (c *outputCoalescer) RequestPong() bool {
	select {
	case c.pongs <- struct{}{}:
		return true
	case <-c.done:
		return false
	}
}

func (c *outputCoalescer) CloseAndFlush() {
	c.closeOnce.Do(func() {
		close(c.chunks)
	})
	<-c.done
}

func (c *outputCoalescer) Done() <-chan struct{} {
	return c.done
}

func (c *outputCoalescer) Stats() outputCoalescerStats {
	return c.stats
}

func (c *outputCoalescer) run() {
	defer close(c.done)

	var buffered []byte
	writeFailed := false

	flush := func() bool {
		if len(buffered) == 0 {
			return true
		}
		payload := buffered
		buffered = nil
		return c.writeFrame(websocket.BinaryMessage, payload)
	}

	appendChunk := func(chunk []byte) bool {
		for len(chunk) > 0 {
			available := c.maxFrame - len(buffered)
			if available == 0 {
				c.timer.Stop()
				if !flush() {
					return false
				}
				available = c.maxFrame
			}
			count := min(available, len(chunk))
			buffered = append(buffered, chunk[:count]...)
			chunk = chunk[count:]
			if len(buffered) == c.maxFrame {
				c.timer.Stop()
				if !flush() {
					return false
				}
				continue
			}
			c.timer.Reset(c.quietWindow)
		}
		return true
	}

	for {
		select {
		case <-c.pongs:
			if !c.writeFrame(websocket.TextMessage, pongFrame) {
				writeFailed = true
			}
			continue
		default:
		}

		if writeFailed {
			for range c.chunks {
			}
			return
		}

		select {
		case chunk, ok := <-c.chunks:
			if !ok {
				c.timer.Stop()
				flush()
				for {
					select {
					case <-c.pongs:
						if !c.writeFrame(websocket.TextMessage, pongFrame) {
							return
						}
					default:
						return
					}
				}
			}
			if !appendChunk(chunk) {
				writeFailed = true
			}
		case <-c.timer.Chan():
			if !flush() {
				writeFailed = true
			}
		case <-c.pongs:
			if !c.writeFrame(websocket.TextMessage, pongFrame) {
				writeFailed = true
			}
		}
	}
}

func (c *outputCoalescer) writeFrame(messageType int, payload []byte) bool {
	started := time.Now()
	err := c.write(messageType, payload)
	if time.Since(started) >= slowOutputWriteThreshold {
		c.stats.slowWrites++
	}
	if err != nil {
		c.onWriteError(err)
		return false
	}
	if messageType == websocket.BinaryMessage {
		c.stats.frames++
		c.stats.bytes += int64(len(payload))
		c.stats.maxFrame = max(c.stats.maxFrame, len(payload))
	}
	return true
}

type artifactScanner struct {
	ctx     context.Context
	client  *tmux.Client
	tracker *toolevents.Tracker
	session string

	chunks chan []byte
	done   chan struct{}

	closeOnce     sync.Once
	droppedChunks atomic.Int64
}

func newArtifactScanner(ctx context.Context, client *tmux.Client, tracker *toolevents.Tracker, session string) *artifactScanner {
	if client == nil || tracker == nil {
		return nil
	}

	scanner := &artifactScanner{
		ctx:     ctx,
		client:  client,
		tracker: tracker,
		session: session,
		chunks:  make(chan []byte, scanQueueCapacity),
		done:    make(chan struct{}),
	}
	go scanner.run()
	return scanner
}

func (s *artifactScanner) Submit(chunk []byte) {
	select {
	case <-s.ctx.Done():
		s.droppedChunks.Add(1)
		return
	default:
	}

	select {
	case s.chunks <- chunk:
	default:
		s.droppedChunks.Add(1)
	}
}

func (s *artifactScanner) Close() {
	s.closeOnce.Do(func() {
		close(s.chunks)
	})
}

func (s *artifactScanner) Done() <-chan struct{} {
	return s.done
}

func (s *artifactScanner) DroppedChunks() int64 {
	return s.droppedChunks.Load()
}

func (s *artifactScanner) run() {
	defer close(s.done)
	cancelled := true
	defer func() {
		if cancelled {
			s.closeOnce.Do(func() {
				close(s.chunks)
			})
			for range s.chunks {
				s.droppedChunks.Add(1)
			}
		}
	}()

	artifactTail := ""
	lastOSC7CWD := ""
	seen := make(map[string]bool)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		var chunk []byte
		select {
		case <-s.ctx.Done():
			return
		case next, ok := <-s.chunks:
			if !ok {
				cancelled = false
				return
			}
			chunk = next
		}

		combined := artifactTail + string(chunk)
		lastNewline := strings.LastIndex(combined, "\n")
		scanText := combined
		if lastNewline >= 0 {
			scanText = combined[:lastNewline+1]
			artifactTail = combined[lastNewline+1:]
		} else if len(combined) > maxArtifactTailBytes {
			artifactTail = combined[len(combined)-maxArtifactTailBytes:]
		} else {
			artifactTail = combined
		}

		if s.ctx.Err() != nil {
			return
		}
		if cwd, ok := toolevents.ParseOSC7CWD(scanText); ok {
			lastOSC7CWD = cwd
		}
		if s.ctx.Err() != nil {
			return
		}
		paths := toolevents.ScanArtifactPaths(scanText)
		if s.ctx.Err() != nil {
			return
		}
		osc8Paths := toolevents.ParseOSC8FilePaths(scanText)
		if len(paths) == 0 && len(osc8Paths) == 0 {
			continue
		}

		type artifactCandidate struct {
			path   string
			source string
		}
		candidates := make([]artifactCandidate, 0, len(paths)+len(osc8Paths))
		for _, path := range paths {
			candidates = append(candidates, artifactCandidate{path: path, source: "regex"})
		}
		for _, path := range osc8Paths {
			candidates = append(candidates, artifactCandidate{path: path, source: "osc8"})
		}

		cwd := lastOSC7CWD
		if cwd == "" {
			if s.ctx.Err() != nil {
				return
			}
			cwd = toolevents.ResolveSessionCWD(s.client, s.session)
		}
		batchSeen := make(map[string]struct{}, len(candidates))
		artifacts := make([]*toolevents.FileArtifact, 0, len(candidates))
		for _, candidate := range candidates {
			if s.ctx.Err() != nil {
				return
			}
			if candidate.path == "" {
				continue
			}
			if _, ok := batchSeen[candidate.path]; ok {
				continue
			}
			batchSeen[candidate.path] = struct{}{}
			if seen[candidate.path] {
				continue
			}
			if artifact := toolevents.EnrichArtifact(candidate.path, cwd, "", candidate.source); artifact != nil {
				artifacts = append(artifacts, artifact)
				seen[candidate.path] = true
			}
		}
		if len(artifacts) > 0 {
			if s.ctx.Err() != nil {
				return
			}
			s.tracker.RecordArtifacts(s.session, artifacts)
		}
	}
}
