package peer

import (
	"testing"
	"time"
)

func TestCaptureRegistryAwaitDeliver(t *testing.T) {
	r := NewCaptureRegistry()
	ch, cancel := r.Register("tok")
	defer cancel()
	r.Deliver("tok", CaptureResult{Text: "hello"})
	select {
	case res := <-ch:
		if res.Text != "hello" {
			t.Fatalf("got %q want hello", res.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}
}

func TestCaptureRegistryTimeout(t *testing.T) {
	r := NewCaptureRegistry()
	if _, ok := r.Await("missing", 10*time.Millisecond); ok {
		t.Fatal("expected timeout, got delivery")
	}
}

func TestCaptureRegistryDeliverUnknownTokenNoPanic(t *testing.T) {
	r := NewCaptureRegistry()
	r.Deliver("nobody", CaptureResult{Text: "x"}) // must not block or panic
}
