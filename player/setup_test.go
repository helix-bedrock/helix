package player

import (
	"testing"

	"github.com/helix-bedrock/protocol/packet"
)

func TestInputQueue(t *testing.T) {
	var q inputQueue
	for range maxQueuedInput {
		if !q.push(&packet.Animate{}) {
			t.Fatal("queue full too early")
		}
	}
	if q.push(&packet.Animate{}) {
		t.Fatal("queue took more than its limit")
	}
	if n := len(q.take()); n != maxQueuedInput || len(q.take()) != 0 {
		t.Fatalf("took %d", n)
	}
}
