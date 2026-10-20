package testcmd

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/localrec"
	"github.com/wake/purdex/internal/wwd/localruns"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
)

// recordingQueue is the job queue as the fast lane's service needs it, and nothing more.
type recordingQueue struct {
	mu   sync.Mutex
	n    int
	reqs []wire.LocalRunRequest
}

func (q *recordingQueue) RecordLocal(_ context.Context, req wire.LocalRunRequest, _ string) (wire.LocalRunResponse, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.n++
	q.reqs = append(q.reqs, req)
	return wire.LocalRunResponse{ID: "j_e2e" + string(rune('0'+q.n)), ClaimID: "0123456789abcdef0123456789abcdef", LeaseS: 60, RenewS: 15}, nil
}
func (q *recordingQueue) Ended(_ context.Context, ids []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (q *recordingQueue) Result(_ context.Context, id string, _ wire.ResultRequest) (wire.Job, error) {
	return wire.Job{ID: id}, nil
}
func (q *recordingQueue) Running(context.Context) ([]wire.Job, error) { return nil, nil }

// wwdOnASocket serves the fast lane's service and the ordinary claim routes on a unix socket, as the daemon does for `pdx test`.
func wwdOnASocket(t *testing.T, svc *localruns.Service) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wwde2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "w.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/local-runs", svc.Handler())
	mux.HandleFunc("POST /v1/jobs/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"cancel":false}`)) })
	mux.HandleFunc("PUT /v1/jobs/{id}/log", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /v1/jobs/{id}/result", func(w http.ResponseWriter, r *http.Request) {
		svc.OnJobEvent(wire.JobEvent{Job: wire.Job{ID: r.PathValue("id"), Local: true, State: wire.StateSucceeded}})
		_, _ = w.Write([]byte(`{}`))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return sock
}

// The fast lane end to end over the socket: a declared task asks wwd for its light slot, is told 429 while there is none, asks again
// every poll, and runs (with no gate slot of its own) once wwd has room; the slot is wwd's, counted while the command runs.
func TestTheFastLaneTakesAWwdSlotAndRetriesAfterA429(t *testing.T) {
	q := &recordingQueue{}
	svc := localruns.New(locks.New(), q, "mlab")
	svc.Logf = func(string, ...any) {}
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	var refused atomic.Int32
	svc.Room = func(heavy bool, running wire.Slots) []string {
		if heavy {
			t.Errorf("a declared task asked for the heavy slot")
		}
		if refused.Add(1) <= 2 {
			return []string{"zone_danger"}
		}
		return nil
	}
	g := &fakeGate{}
	var during wire.Slots
	g.onRun = func() { during = svc.Running() }
	o := Options{Args: args(t, "one-file", "a.test.ts"), API: &fakeAPI{}, Gate: g, Recorder: localrec.Recorder{API: client.New(wwdOnASocket(t, svc))},
		Env: os.Environ(), LockPoll: time.Millisecond, LockWait: time.Minute}
	code, _, errOut := run(t, o)
	if code != 0 || g.calls != 1 {
		t.Fatalf("code %d, the command ran %d times\n%s", code, g.calls, errOut)
	}
	if refused.Load() != 3 {
		t.Fatalf("wwd was asked %d times, want 3 (two 429s, then the slot)", refused.Load())
	}
	if !strings.Contains(errOut, "zone_danger") {
		t.Fatalf("the wait does not say why: %q", errOut)
	}
	if during != (wire.Slots{Light: 1}) {
		t.Fatalf("while the command ran wwd counted %+v, want one light run", during)
	}
	if got := svc.Running(); got != (wire.Slots{}) {
		t.Fatalf("after the result wwd still counts %+v", got)
	}
	if len(q.reqs) != 1 || q.reqs[0].AdHoc {
		t.Fatalf("wwd recorded %d runs, adhoc=%v", len(q.reqs), len(q.reqs) > 0 && q.reqs[0].AdHoc)
	}
}
