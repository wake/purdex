package pick

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A worker that reads another host's queue reads the peer's answer leniently (765): a job record that carries started_at (a peer on queue
// schema 9 sends it for a job that started) is read, not refused, and the queued jobs among them are still offered.
func TestPeerSourceReadsJobsThatCarryStartedAt(t *testing.T) {
	at := time.Date(2026, 10, 20, 8, 0, 3, 0, time.UTC)
	queued := job("q", wire.ClassMerge, time.Minute)
	started := job("s", wire.ClassMerge, time.Minute)
	started.State, started.StartedAt = wire.StateRunning, &at
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(wire.JobsListResponse{Jobs: []wire.Job{queued, started}})
	}))
	defer srv.Close()
	jobs, err := NewPeerSource("air26", srv.URL+"/", "tok", srv.Client()).Queued(context.Background())
	if err != nil {
		t.Fatalf("a record with started_at is read: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "q" {
		t.Fatalf("only the queued job is offered: %+v", jobs)
	}
}
