package convfeed

import "testing"

func queueRow(op, content string) []byte {
	o := obj{"type": "queue-operation", "operation": op, "sessionId": sidA}
	if content != "" {
		o["content"] = content
	}
	return row(o)
}

func TestEntry_QueueOnlyChangeBumpsRevisionAndHeader(t *testing.T) {
	m := newMem(idle(1)...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	rev := e.Revision()
	m.Append(queueRow("enqueue", "wait for me"))
	refresh(t, e, src(m, "f1", false))
	if e.Revision() != rev+1 || !e.HeaderChangedSince(rev) {
		t.Fatalf("rev %d (want %d), headerChanged %v", e.Revision(), rev+1, e.HeaderChangedSince(rev))
	}
	if q := e.Header().Queued; len(q) != 1 || q[0].Text != "wait for me" || q[0].Source != "user" {
		t.Fatalf("queued = %+v", q)
	}
	if ch := e.ChangesSince(rev); len(ch) != 0 {
		t.Errorf("a queue row changes no turn, got %+v", ch)
	}
	// a system entry is invisible: no revision bump
	rev = e.Revision()
	m.Append(queueRow("enqueue", "<task-notification>x</task-notification>"))
	refresh(t, e, src(m, "f1", false))
	if e.Revision() != rev {
		t.Fatalf("a system entry bumped the revision to %d", e.Revision())
	}
	m.Append(queueRow("dequeue", "")) // the oldest is "wait for me"
	refresh(t, e, src(m, "f1", false))
	if e.Revision() != rev+1 || len(e.Header().Queued) != 0 {
		t.Fatalf("rev %d queued %+v", e.Revision(), e.Header().Queued)
	}
}
