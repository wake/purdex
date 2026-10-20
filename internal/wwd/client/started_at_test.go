package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A job that has started carries started_at (queue schema 9). The reads the Purdex daemon makes of wwd's jobs are lenient since 765, so
// they read a record that has it: the list, the cursor read and the one job.
func TestJobReadsTakeARecordThatHasStartedAt(t *testing.T) {
	at := time.Date(2026, 10, 20, 8, 0, 3, 0, time.UTC)
	j := jobIn(t, "j_run", wire.StateRunning)
	j.StartedAt = &at
	mux := http.NewServeMux()
	list := wire.JobsListResponse{Jobs: []wire.Job{j}, Cursor: "7", Epoch: strings.Repeat("b", 32)}
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(list) })
	mux.HandleFunc("GET /v1/jobs/j_run", func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(j) })
	c := New(fakeWWD(t, mux))

	jobs, err := c.Jobs(context.Background(), JobsQuery{})
	if err != nil || len(jobs) != 1 || jobs[0].StartedAt == nil || !jobs[0].StartedAt.Equal(at) {
		t.Fatalf("Jobs: %+v %v", jobs, err)
	}
	page, err := c.JobsPage(context.Background(), JobsPageQuery{})
	if err != nil || len(page.Jobs) != 1 || page.Jobs[0].StartedAt == nil {
		t.Fatalf("JobsPage: %+v %v", page, err)
	}
	one, err := c.Job(context.Background(), "j_run")
	if err != nil || one.StartedAt == nil || !one.StartedAt.Equal(at) {
		t.Fatalf("Job: %+v %v", one, err)
	}
}
