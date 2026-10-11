package ccnorm

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func qop(sec float64, op string, extra ...opt) []byte {
	return line(obj{"type": "queue-operation", "operation": op, "timestamp": at(sec), "sessionId": sidA}, extra...)
}

func qin(sec float64, op, content string) []byte { return qop(sec, op, with("content", content)) }

func queuedOf(n *Normalizer) []Queued { _, _, q := n.Header(); return q }

func texts(q []Queued) []string {
	var out []string
	for _, m := range q {
		out = append(out, m.Source+":"+m.Text)
	}
	return out
}

const (
	sysMsg  = "<task-notification><task-id>t1</task-id></task-notification>"
	peerMsg = "<cross-session-message from=\"x\">hello</cross-session-message>"
)

func TestQueue_EnqueueThenDequeue(t *testing.T) {
	n := norm(t, qin(1, "enqueue", "first"))
	q := queuedOf(n)
	if len(q) != 1 || q[0] != (Queued{Text: "first", At: ms(1), Source: "user"}) {
		t.Fatalf("queued = %+v", q)
	}
	feed(t, n, qop(1.01, "dequeue"))
	if q := queuedOf(n); len(q) != 0 {
		t.Fatalf("after dequeue: %+v", q)
	}
}

func TestQueue_RemoveAbsorbedMidTurn(t *testing.T) {
	n := norm(t, qin(1, "enqueue", "a"), qin(2, "enqueue", "b"),
		qop(3, "remove", with("content", "a"), with("reason", "absorbed_mid_turn")))
	if got := texts(queuedOf(n)); !reflect.DeepEqual(got, []string{"user:b"}) {
		t.Fatalf("queued = %v", got)
	}
}

func TestQueue_UnmatchedRemoveCounted(t *testing.T) {
	n := norm(t, qin(1, "enqueue", "a"), qin(2, "remove", sysMsg), qop(3, "dequeue"), qop(4, "dequeue"))
	if got := n.Stats().Skipped; got["queue:remove-unmatched"] != 1 || got["queue:dequeue-empty"] != 1 {
		t.Fatalf("skipped = %v", got)
	}
}

func TestQueue_PopAllClears(t *testing.T) {
	n := norm(t, qin(1, "enqueue", "a"), qin(2, "enqueue", "b"), qin(3, "popAll", "b"))
	if q := queuedOf(n); len(q) != 0 {
		t.Fatalf("queued = %+v", q)
	}
}

func TestQueue_FIFOWithSystemEntriesHidden(t *testing.T) {
	n := norm(t, qin(1, "enqueue", sysMsg), qin(2, "enqueue", "mine"), qin(3, "enqueue", peerMsg))
	if got := queuedOf(n); len(got) != 2 || got[0].Source != "user" || got[1].Source != "peer" {
		t.Fatalf("header must hide the system entry, got %+v", got)
	}
	feed(t, n, qop(4, "dequeue")) // the oldest is the system entry, not "mine"
	if got := texts(queuedOf(n)); !reflect.DeepEqual(got, []string{"user:mine", "peer:hello"}) {
		t.Fatalf("queued = %v", got)
	}
	feed(t, n, qop(5, "dequeue"))
	if got := texts(queuedOf(n)); !reflect.DeepEqual(got, []string{"peer:hello"}) {
		t.Fatalf("queued = %v", got)
	}
}

func TestQueue_FreshTypedPromptClears(t *testing.T) {
	// Measured on real transcripts: an idle-typed terminal prompt is promptSource "typed", is never preceded by a dequeue
	// (1355 follow a user row, 97 a remove, 3 a popAll, 3 an enqueue), so a queue still holding entries is a leftover.
	n := norm(t, qin(1, "enqueue", "stale"), userRow("u1", 2, "typed now"))
	if q := queuedOf(n); len(q) != 0 {
		t.Fatalf("a typed prompt left %+v", q)
	}
}

func TestQueue_QueuedPromptRowDoesNotClear(t *testing.T) {
	n := norm(t, qin(1, "enqueue", "one"), qin(1.1, "enqueue", "two"), qop(2, "dequeue"),
		userRow("u1", 2.1, "one", promptSource("queued")))
	if got := texts(queuedOf(n)); !reflect.DeepEqual(got, []string{"user:two"}) {
		t.Fatalf("queued = %v", got)
	}
}

func TestQueue_CapAndTextCut(t *testing.T) {
	var lines [][]byte
	for i := 0; i < maxQueueEntries+3; i++ {
		lines = append(lines, qin(float64(i), "enqueue", fmt.Sprintf("m%d", i)))
	}
	n := norm(t, lines...)
	q := queuedOf(n)
	if len(q) != maxQueueEntries || q[0].Text != "m3" || n.Stats().Skipped["queue:overflow"] != 3 {
		t.Fatalf("len %d first %q skipped %v", len(q), q[0].Text, n.Stats().Skipped)
	}
	long := strings.Repeat("字", maxQueuedText+10)
	n = norm(t, qin(1, "enqueue", long))
	if got := []rune(queuedOf(n)[0].Text); len(got) != maxQueuedText {
		t.Fatalf("text runes = %d", len(got))
	}
	feed(t, n, qin(2, "remove", long)) // matching uses the full text, not the cut one
	if len(queuedOf(n)) != 0 {
		t.Fatal("remove did not match the full text")
	}
}

func TestQueue_NonStringContentCounted(t *testing.T) {
	n := norm(t, qop(1, "enqueue", with("content", []any{"x"})), qop(2, "enqueue"), qop(3, "bogus"))
	if len(queuedOf(n)) != 0 || n.Stats().Skipped["queue:content"] != 2 || n.Stats().Skipped["queue:op:bogus"] != 1 {
		t.Fatalf("queued %+v skipped %v", queuedOf(n), n.Stats().Skipped)
	}
}

func TestQueue_HeaderReturnsACopy(t *testing.T) {
	n := norm(t, qin(1, "enqueue", "a"))
	queuedOf(n)[0].Text = "changed"
	if queuedOf(n)[0].Text != "a" {
		t.Fatal("Header exposed internal state")
	}
}
