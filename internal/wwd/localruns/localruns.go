// Package localruns is wwd's side of the local fast lane (spec §5.2): POST /v1/local-runs on the local socket. The caller is about
// to run a task in its own working tree; this checks the host's lock table, has the queue record the run as a job that is already
// running, and answers with the claim the caller heartbeats and reports with (the ordinary claim routes). The locks are held until
// the job ends: the queue's job events release them, and a periodic comparison with the queue releases those whose event was lost.
package localruns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
)

// Queue is what the service needs of the job queue.
type Queue interface {
	// RecordLocal records a fast-lane run as a running job with local set, not pinned and without looking at locks (the service has
	// checked them), and returns the claim and its lease. worker names this host.
	RecordLocal(ctx context.Context, req wire.LocalRunRequest, worker string) (wire.LocalRunResponse, error)
	// Ended says which of ids are over: a job in a final state, or one the queue no longer has.
	Ended(ctx context.Context, ids []string) (map[string]bool, error)
	// Result ends a job with the claim RecordLocal answered with (spec 4.3): a run that was recorded and cannot be handed to its
	// caller is ended at once as failed, reason infra, instead of waiting for its lease to lapse. A stale claim is refused and
	// changes nothing.
	Result(ctx context.Context, id string, req wire.ResultRequest) (wire.Job, error)
	// Running lists the fast-lane jobs the queue holds in a state that is not final (claimed or running), with their locks. The
	// service rebuilds the lock table from it after a restart: the runner of such a job may go on renewing its lease.
	Running(ctx context.Context) ([]wire.Job, error)
}

// provisionalPrefix names the owner of keys reserved for a run the queue has not named yet; it is never a job id.
const provisionalPrefix = "local:"

// maxBody bounds a registration (a submission without a snapshot is small).
const maxBody = 1 << 20

// maxEarly bounds the ids of jobs that ended before their hold was recorded.
const maxEarly = 1024

// ReconcileEvery is how often the holds are compared with the queue.
const ReconcileEvery = 30 * time.Second

// Service is the fast-lane entry.
type Service struct {
	Locks  *locks.Table
	Queue  Queue
	Worker string // this host's alias, passed to RecordLocal
	Logf   func(format string, args ...any)

	seq atomic.Uint64

	mu    sync.Mutex
	holds map[string]*locks.Hold // by job id
	// early: jobs whose end was announced before the registration that made them returned (the event is faster than the answer).
	// It is only filled while a registration is in flight (inflight > 0) and emptied when the last one is over: an end that no
	// registration waits for needs nothing remembered (Reconcile covers a hold that outlives its job).
	early    map[string]struct{}
	inflight int
	// abandoning: ids of recorded runs being ended by abandon. A registration that gets one of them is refused: the end's final job
	// event releases by id, so a hold installed under it meanwhile would be released with it.
	abandoning map[string]struct{}
	ready      bool // the table was rebuilt from the queue after the start; registrations wait for it
}

// New returns a service over the table and the queue.
func New(t *locks.Table, q Queue, worker string) *Service {
	return &Service{Locks: t, Queue: q, Worker: worker, Logf: log.Printf, holds: map[string]*locks.Hold{}, early: map[string]struct{}{}, abandoning: map[string]struct{}{}}
}

// Handler is POST /v1/local-runs. Register it on the socket mux only: a peer never records a run here.
func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body too large or unreadable"})
			return
		}
		req, err := wire.Decode[wire.LocalRunRequest](body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		resp, status, err := s.Register(r.Context(), req)
		var busy *locks.BusyError
		switch {
		case errors.As(err, &busy):
			keys := make([]string, len(busy.Conflicts))
			holders := make([]wire.LockHolder, len(busy.Conflicts))
			provisional := false
			for i, c := range busy.Conflicts {
				keys[i], holders[i] = c.Key, wire.LockHolder{Key: c.Key, Job: c.Holder}
				provisional = provisional || strings.HasPrefix(c.Holder, provisionalPrefix)
			}
			if provisional {
				holders = nil // a run still being recorded has no job id yet: the keys alone are told
			}
			writeJSON(w, http.StatusConflict, wire.LocksBusy{Reason: wire.LocksBusyReason, Keys: keys, Holders: holders})
		case err != nil:
			writeJSON(w, status, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusOK, resp)
		}
	}
}

// Register checks the locks of req, has the queue record the run and holds the keys under the job's id. A *locks.BusyError means a
// key is held (status 409); any other error comes with the status to answer with.
func (s *Service) Register(ctx context.Context, req wire.LocalRunRequest) (wire.LocalRunResponse, int, error) {
	s.mu.Lock()
	if !s.ready {
		s.mu.Unlock()
		return wire.LocalRunResponse{}, http.StatusServiceUnavailable, errors.New("starting: the locks of the runs that were going on are not rebuilt yet")
	}
	s.inflight++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.inflight--; s.inflight == 0 {
			clear(s.early) // no registration waits for an end that was announced early
		}
		s.mu.Unlock()
	}()
	return s.register(ctx, req)
}

func (s *Service) register(ctx context.Context, req wire.LocalRunRequest) (wire.LocalRunResponse, int, error) {
	// The keys are reserved before the queue names the job and are renamed to its id after: no other start can take them while the
	// run is being recorded, and a cancel that names the job finds the keys.
	if err := locks.ValidateKeys(req.Locks); err != nil {
		return wire.LocalRunResponse{}, http.StatusBadRequest, err // the request is malformed; the queue is not the problem
	}
	owner := fmt.Sprintf("%s%d", provisionalPrefix, s.seq.Add(1))
	var resp wire.LocalRunResponse
	hold, err := s.Locks.Acquire(owner, req.Locks, func() error {
		var rerr error
		resp, rerr = s.Queue.RecordLocal(ctx, req, s.Worker)
		return rerr
	})
	var busy *locks.BusyError
	switch {
	case errors.As(err, &busy):
		return wire.LocalRunResponse{}, http.StatusConflict, err
	case err != nil:
		return wire.LocalRunResponse{}, http.StatusServiceUnavailable, fmt.Errorf("the queue could not record the run: %w", err)
	}
	if err := resp.Validate(); err != nil {
		// The queue has the run as running and the caller will not get its claim.
		s.abandon(ctx, resp, fmt.Sprintf("its claim is unusable (%v)", err))
		hold.Release()
		return wire.LocalRunResponse{}, http.StatusBadGateway, fmt.Errorf("the queue answered with an unusable claim: %w", err)
	}
	if err := hold.Rename(resp.ID); err != nil {
		// The id is already the owner of keys: the queue named two runs alike, which it never does. The run is NOT ended here: its
		// final job event would release, by that id, the keys of the other run that holds it.
		s.Logf("localruns: ERROR the run %s was recorded but its locks could not be named (%v); it is not ended (that would release the keys of the run that already has this id), its lease will lapse as lost", resp.ID, err)
		hold.Release()
		return wire.LocalRunResponse{}, http.StatusInternalServerError, fmt.Errorf("the locks of %s: %w", resp.ID, err)
	}
	s.mu.Lock()
	if _, ending := s.abandoning[resp.ID]; ending {
		s.mu.Unlock()
		s.Logf("localruns: ERROR the run %s was recorded but its id is being ended here as an abandoned run; its keys are not held, its lease will lapse as lost", resp.ID)
		hold.Release()
		return wire.LocalRunResponse{}, http.StatusInternalServerError, fmt.Errorf("the locks of %s: the id is being ended as another run", resp.ID)
	}
	if _, over := s.early[resp.ID]; over {
		delete(s.early, resp.ID)
		s.mu.Unlock()
		hold.Release() // the job ended while it was being recorded: nothing to hold
		return resp, http.StatusOK, nil
	}
	s.holds[resp.ID] = hold
	s.mu.Unlock()
	return resp, http.StatusOK, nil
}

// abandon ends a run that was recorded and cannot be handed to its caller: failed, reason infra, with the claim the queue answered
// with, so it does not stay running until its lease lapses. When that is not possible (no usable claim, or the queue refuses) the job
// id is logged as an error and the lease lapse is left to mark it lost.
func (s *Service) abandon(ctx context.Context, resp wire.LocalRunResponse, why string) {
	if resp.ID == "" || !wire.ValidClaimID(resp.ClaimID) {
		s.Logf("localruns: ERROR the run %q was recorded but %s, and it cannot be ended (no usable claim); its lease will lapse as lost", resp.ID, why)
		return
	}
	if !s.beginAbandon(resp.ID) {
		// The queue gave this run the id of one that holds keys here. Ending it would publish a final job event, and the event
		// releases by id: the keys of the run that really has the id.
		s.Logf("localruns: ERROR the run %s was recorded but %s, and its id is already in use here; it is not ended (that would release the keys of the run that has this id), its lease will lapse as lost", resp.ID, why)
		return
	}
	defer s.endAbandon(resp.ID)
	// the caller's context may be the very reason for the failure: the end of the run gets its own short one
	ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := s.Queue.Result(ectx, resp.ID, wire.ResultRequest{ClaimID: resp.ClaimID, State: wire.StateFailed, Reason: "infra"}); err != nil {
		s.Logf("localruns: ERROR the run %s was recorded but %s, and ending it failed (%v); its lease will lapse as lost", resp.ID, why, err)
		return
	}
	s.Logf("localruns: the run %s was recorded but %s; ended as failed (infra)", resp.ID, why)
}

// beginAbandon reserves id for the end of an abandoned run, atomically with the check that no hold here has it; false: it is in use.
// A registration that is given the id from now until endAbandon is refused (register).
func (s *Service) beginAbandon(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idInUse(id) {
		return false
	}
	s.abandoning[id] = struct{}{}
	return true
}

func (s *Service) endAbandon(id string) {
	s.mu.Lock()
	delete(s.abandoning, id)
	s.mu.Unlock()
}

// idInUse says whether id is the owner of keys held here, or of a hold of this service. s.mu is held.
func (s *Service) idInUse(id string) bool {
	if _, ok := s.holds[id]; ok {
		return true
	}
	for _, owner := range s.Locks.Held() {
		if owner == id {
			return true
		}
	}
	return false
}

// OnJobEvent is the queue's Publish: when a fast-lane job reaches a final state its keys are released.
func (s *Service) OnJobEvent(ev wire.JobEvent) {
	j := ev.Job
	if !j.Local || !j.State.Terminal() {
		return
	}
	s.end(j.ID)
}

func (s *Service) end(id string) {
	s.mu.Lock()
	hold, ok := s.holds[id]
	delete(s.holds, id)
	if !ok && s.inflight > 0 { // a registration may be about to return this job
		if len(s.early) >= maxEarly { // bounded; the next comparison covers a hold that is missed
			for k := range s.early {
				delete(s.early, k)
				break
			}
		}
		s.early[id] = struct{}{}
	}
	s.mu.Unlock()
	if ok {
		hold.Release()
	}
}

// Held lists the job ids whose keys are held for a fast-lane run (sorted).
func (s *Service) Held() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.holds))
	for id := range s.holds {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Reconcile compares the holds with the queue and releases those of jobs that are over: an event can be lost (a subscriber that fell
// behind, a restart), and keys held for a job that has ended would keep every other start away.
func (s *Service) Reconcile(ctx context.Context) error {
	ids := s.Held()
	if len(ids) == 0 {
		return nil
	}
	ended, err := s.Queue.Ended(ctx, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ended[id] {
			s.mu.Lock()
			hold, ok := s.holds[id]
			delete(s.holds, id)
			s.mu.Unlock()
			if ok {
				hold.Release()
			}
		}
	}
	return nil
}

// Recover rebuilds the lock table after a start from the fast-lane jobs the queue still has as not final: their runners may go on
// renewing their leases, and without their keys a new run could overlap them. Registrations are refused until every one of them is
// accounted for: a job whose keys cannot be taken (another running job already has one of them, the keys are malformed) is an
// error, and the lane stays closed — Run asks again, and it opens when that job's lease has lapsed. It may be called again; a job
// already held is skipped.
func (s *Service) Recover(ctx context.Context) error {
	// A registration-like window: an end announced while the list is read and installed is remembered, so the lock of a job that
	// has already ended is not built.
	s.mu.Lock()
	s.inflight++
	s.ready = false // closed for the whole rebuild, also when it repeats an earlier success; it opens again only when complete
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.inflight--; s.inflight == 0 {
			clear(s.early)
		}
		s.mu.Unlock()
	}()
	jobs, err := s.Queue.Running(ctx)
	if err != nil {
		return err
	}
	var failed []string
	for _, j := range jobs {
		if !j.Local || j.State.Terminal() {
			continue
		}
		s.mu.Lock()
		_, have := s.holds[j.ID]
		_, over := s.early[j.ID]
		s.mu.Unlock()
		if have || over {
			continue
		}
		hold, err := s.Locks.Acquire(j.ID, j.Locks, nil)
		if err != nil {
			s.Logf("localruns: the locks of the running job %s were not rebuilt: %v", j.ID, err)
			failed = append(failed, j.ID)
			continue
		}
		s.mu.Lock()
		if _, over := s.early[j.ID]; over { // it ended while its keys were being taken
			s.mu.Unlock()
			hold.Release()
			continue
		}
		s.holds[j.ID] = hold
		s.mu.Unlock()
	}
	if len(failed) > 0 {
		return fmt.Errorf("the locks of %d running jobs could not be rebuilt (%v): the fast lane stays closed", len(failed), failed)
	}
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	return nil
}

// Run rebuilds the locks (retrying until the queue answers), then reconciles every ReconcileEvery until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := s.Recover(ctx)
		if err == nil {
			break
		}
		s.Logf("localruns: rebuilding the locks from the queue: %v", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	t := time.NewTicker(ReconcileEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
				s.Logf("localruns: compare with the queue: %v", err)
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
