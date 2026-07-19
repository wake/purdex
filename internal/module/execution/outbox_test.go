package execution

import (
	"testing"
)

func newTestOutbox(t *testing.T) *Outbox {
	t.Helper()
	return openTestStore(t).Outbox()
}

func outboxRec(execID string, seq int, status string) OutboxRecord {
	return OutboxRecord{
		ExecutionID: execID,
		DispatchID:  "dsp_" + execID,
		Seq:         seq,
		Status:      status,
		Payload:     []byte(`{"schema_version":1}`),
	}
}

func TestOutbox_EnqueueIdempotentOnExecSeq(t *testing.T) {
	o := newTestOutbox(t)

	ins, err := o.Enqueue(outboxRec("exc_1", 1, "accepted"))
	if err != nil || !ins {
		t.Fatalf("first enqueue: inserted=%v err=%v", ins, err)
	}
	// Same (execution_id, seq) → no-op, inserted=false.
	ins, err = o.Enqueue(outboxRec("exc_1", 1, "accepted"))
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if ins {
		t.Fatalf("re-enqueue of same (exec,seq) should be a no-op")
	}

	got, ok, err := o.RecordBySeq("exc_1", 1)
	if err != nil || !ok {
		t.Fatalf("RecordBySeq: ok=%v err=%v", ok, err)
	}
	if got.Status != "accepted" || got.Acked || got.Permanent || got.Attempts != 0 {
		t.Fatalf("unexpected record %+v", got)
	}
}

func TestOutbox_EnqueueRejectsInvalid(t *testing.T) {
	o := newTestOutbox(t)
	bad := OutboxRecord{ExecutionID: "", DispatchID: "d", Seq: 1, Payload: []byte("{}")}
	if _, err := o.Enqueue(bad); err == nil {
		t.Fatal("want error for empty execution_id")
	}
	if _, err := o.Enqueue(OutboxRecord{ExecutionID: "e", DispatchID: "d", Seq: 0, Payload: []byte("{}")}); err == nil {
		t.Fatal("want error for seq < 1")
	}
}

func TestOutbox_UnackedRecordsOrderedBySeq(t *testing.T) {
	o := newTestOutbox(t)
	// Insert out of order; UnackedRecords must return ascending seq.
	o.Enqueue(outboxRec("exc_1", 3, "completed"))
	o.Enqueue(outboxRec("exc_1", 1, "accepted"))
	o.Enqueue(outboxRec("exc_1", 2, "running"))
	o.Enqueue(outboxRec("exc_2", 1, "accepted")) // different execution — excluded

	recs, err := o.UnackedRecords("exc_1")
	if err != nil {
		t.Fatalf("UnackedRecords: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d", len(recs))
	}
	for i, want := range []int{1, 2, 3} {
		if recs[i].Seq != want {
			t.Errorf("recs[%d].Seq = %d, want %d", i, recs[i].Seq, want)
		}
	}
}

func TestOutbox_MarkAckedExcludesFromUnacked(t *testing.T) {
	o := newTestOutbox(t)
	o.Enqueue(outboxRec("exc_1", 1, "accepted"))
	r, _, _ := o.RecordBySeq("exc_1", 1)

	if err := o.MarkAcked(r.ID); err != nil {
		t.Fatalf("MarkAcked: %v", err)
	}
	recs, _ := o.UnackedRecords("exc_1")
	if len(recs) != 0 {
		t.Fatalf("acked record should not appear as unacked, got %d", len(recs))
	}
}

func TestOutbox_MarkAttemptBumpsAndBacksOff(t *testing.T) {
	o := newTestOutbox(t)
	o.Enqueue(outboxRec("exc_1", 1, "accepted"))
	r, _, _ := o.RecordBySeq("exc_1", 1)

	if err := o.MarkAttempt(r.ID, 5000, "boom"); err != nil {
		t.Fatalf("MarkAttempt: %v", err)
	}
	got, _, _ := o.RecordBySeq("exc_1", 1)
	if got.Attempts != 1 || got.NextAttemptAt != 5000 || got.LastError != "boom" {
		t.Fatalf("after attempt: %+v", got)
	}
	// A future next_attempt_at hides the execution from a DueExecutions(now<next).
	due, _ := o.DueExecutions(4999)
	if len(due) != 0 {
		t.Fatalf("record in backoff should not be due, got %v", due)
	}
	due, _ = o.DueExecutions(5000)
	if len(due) != 1 || due[0] != "exc_1" {
		t.Fatalf("record past backoff should be due, got %v", due)
	}
}

func TestOutbox_MarkPermanentExcludesFromDue(t *testing.T) {
	o := newTestOutbox(t)
	o.Enqueue(outboxRec("exc_1", 1, "accepted"))
	r, _, _ := o.RecordBySeq("exc_1", 1)

	if err := o.MarkPermanent(r.ID, "401"); err != nil {
		t.Fatalf("MarkPermanent: %v", err)
	}
	got, _, _ := o.RecordBySeq("exc_1", 1)
	if !got.Permanent {
		t.Fatal("record should be permanent")
	}
	due, _ := o.DueExecutions(1 << 40)
	if len(due) != 0 {
		t.Fatalf("permanent record must never be due, got %v", due)
	}
	if recs, _ := o.UnackedRecords("exc_1"); len(recs) != 0 {
		t.Fatalf("permanent record excluded from unacked, got %d", len(recs))
	}
}

func TestOutbox_CursorMonotonic(t *testing.T) {
	o := newTestOutbox(t)

	if seq, _ := o.Cursor("exc_1"); seq != 0 {
		t.Fatalf("initial cursor = %d, want 0", seq)
	}
	if err := o.AdvanceCursor("exc_1", 2); err != nil {
		t.Fatalf("AdvanceCursor: %v", err)
	}
	if seq, _ := o.Cursor("exc_1"); seq != 2 {
		t.Fatalf("cursor = %d, want 2", seq)
	}
	// A stale/out-of-order lower ack must not regress the cursor.
	o.AdvanceCursor("exc_1", 1)
	if seq, _ := o.Cursor("exc_1"); seq != 2 {
		t.Fatalf("cursor regressed to %d, want 2", seq)
	}
	o.AdvanceCursor("exc_1", 3)
	if seq, _ := o.Cursor("exc_1"); seq != 3 {
		t.Fatalf("cursor = %d, want 3", seq)
	}
}

func TestOutbox_DueExecutionsDistinct(t *testing.T) {
	o := newTestOutbox(t)
	o.Enqueue(outboxRec("exc_1", 1, "accepted"))
	o.Enqueue(outboxRec("exc_1", 2, "running"))
	o.Enqueue(outboxRec("exc_2", 1, "accepted"))

	due, err := o.DueExecutions(0)
	if err != nil {
		t.Fatalf("DueExecutions: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("want 2 distinct executions, got %v", due)
	}
}
