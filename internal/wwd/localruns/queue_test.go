package localruns

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
)

// realService is the service over the real queue and the real lock table, wired the way cmd/wwd wires them: the queue's job events
// go to the service.
func realService(t *testing.T, dir string) (*Service, *queue.Queue, *locks.Table) {
	t.Helper()
	return realServiceAt(t, dir, nil)
}

// realServiceAt is realService with the origin's clock set (nil: the real one).
func realServiceAt(t *testing.T, dir string, now func() time.Time) (*Service, *queue.Queue, *locks.Table) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := queue.Migrate(context.Background(), st.DB()); err != nil {
		t.Fatal(err)
	}
	var svc *Service
	q, err := queue.New(st.DB(), queue.Options{Origin: "testhost", Now: now, Publish: func(ev wire.JobEvent) { svc.OnJobEvent(ev) }})
	if err != nil {
		t.Fatal(err)
	}
	tb := locks.New()
	svc = New(tb, FromQueue(q), "testhost")
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	return svc, q, tb
}

func TestARealQueueRecordsTheRunAndItsFinalEventReleasesTheKeys(t *testing.T) {
	s, q, tb := realService(t, t.TempDir())
	rec := post(s, request(t, "db"))
	if rec.Code != http.StatusOK {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	resp, err := wire.Decode[wire.LocalRunResponse](rec.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	j, err := q.Get(context.Background(), resp.ID)
	if err != nil || !j.Local || j.State != wire.StateRunning {
		t.Fatalf("the queue's job: %+v, %v", j, err)
	}
	if o, _ := tb.Holder("db"); o != resp.ID {
		t.Fatalf("the key is held by %q, want the job id", o)
	}
	if rec := post(s, request(t, "db")); rec.Code != http.StatusConflict {
		t.Fatalf("a second run for the key: %d, want 409", rec.Code)
	}
	if _, err := q.Result(context.Background(), resp.ID, wire.ResultRequest{ClaimID: resp.ClaimID, State: wire.StateFailed, Reason: "timeout"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := tb.Holder("db"); ok {
		t.Fatal("the job ended and its key is still held")
	}
	if rec := post(s, request(t, "db")); rec.Code != http.StatusOK {
		t.Fatalf("the key is free again: %d %s", rec.Code, rec.Body)
	}
}

func TestARealQueueRunningJobKeepsItsKeysAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := realService(t, dir)
	rec := post(s, request(t, "db"))
	if rec.Code != http.StatusOK {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	// a new process over the same database: the run is still running, so its key is taken again before the lane opens
	s2, _, tb2 := realService(t, dir)
	if _, ok := tb2.Holder("db"); !ok {
		t.Fatal("the key of the run that was going on was not rebuilt")
	}
	if rec := post(s2, request(t, "db")); rec.Code != http.StatusConflict {
		t.Fatalf("after the restart: %d, want 409", rec.Code)
	}
}

func TestARunWhoseLeaseLapsesIsSweptAsLostAndItsKeysAreReleased(t *testing.T) {
	clock := time.Now()
	s, q, tb := realServiceAt(t, t.TempDir(), func() time.Time { return clock })
	rec := post(s, request(t, "db"))
	if rec.Code != http.StatusOK {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	resp, _ := wire.Decode[wire.LocalRunResponse](rec.Body.Bytes())
	clock = clock.Add(time.Duration(resp.LeaseS+1) * time.Second) // the caller died: no heartbeat
	if _, err := q.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j, _ := q.Get(context.Background(), resp.ID); j.State != wire.StateFailed {
		t.Fatalf("the job is %s, want failed (lost)", j.State)
	}
	if _, ok := tb.Holder("db"); ok {
		t.Fatal("the run was swept as lost and its key is still held")
	}
}
