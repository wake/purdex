package ccnorm

import "testing"

// K-1b: recall must know the CC queue holds ONLY what the header shows, so the hidden system entries are counted.
func TestQueue_HiddenSystemEntriesAreCounted(t *testing.T) {
	n := norm(t, qin(1, "enqueue", "a"), qin(2, "enqueue", sysMsg), qin(3, "enqueue", peerMsg))
	if got := n.HiddenQueued(); got != 1 {
		t.Fatalf("hidden = %d, want 1", got)
	}
	feed(t, n, qop(4, "dequeue"), qop(5, "dequeue"))
	if got := n.HiddenQueued(); got != 0 {
		t.Fatalf("hidden after dequeues = %d", got)
	}
}
