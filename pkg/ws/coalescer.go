package ws

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	outputQuietWindow        = 2 * time.Millisecond
	maxOutputFrameBytes      = 64 * 1024
	outputQueueCapacity      = 16
	slowOutputWriteThreshold = 100 * time.Millisecond
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
