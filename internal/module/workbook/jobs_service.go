package workbook

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

// JobsKey is the core service-registry key of the mod socket's view of the workbook (modevents.WorkbookService). The
// socket module looks it up per request, so neither module depends on the other.
const JobsKey = "workbook.jobs"

// CapV2 is the capability a mod announces on its events batches once it can run turn and re-write jobs (plan D11).
const CapV2 = modevents.CapWorkbookV2

// jobsService adapts the module's engine to the socket routes. The engine exists only while the module runs, so each
// call reads it fresh: a stopped module answers "no job" and "not leased".
type jobsService struct{ m *Module }

var _ modevents.WorkbookService = jobsService{}

func (s jobsService) engine() *Engine {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	return s.m.engine
}

// Ready: the module runs. The socket answers 503 for a module that is registered but off.
func (s jobsService) Ready() bool { return s.engine() != nil }

func (s jobsService) NextJob(ctx context.Context, stream, sessionID string, wait time.Duration) (any, bool) {
	eng := s.engine()
	if eng == nil {
		return nil, false
	}
	j, ok := eng.Next(ctx, stream, sessionID, wait)
	if !ok {
		return nil, false
	}
	return j, true
}

func (s jobsService) JobResult(stream string, r modevents.WorkbookResult) (bool, error) {
	eng := s.engine()
	if eng == nil {
		return false, modevents.ErrNotLeased
	}
	more, err := eng.Result(stream, r.JobID, Result{
		Answered: r.Answered, Text: r.Text, Reason: r.Reason, Status: r.Status, Error: r.Error,
		Usage:     Usage{In: r.Usage.Input, Out: r.Usage.Output, CacheRead: r.Usage.CacheRead},
		LatencyMS: r.LatencyMS,
	})
	if err == ErrNotLeased {
		return false, modevents.ErrNotLeased
	}
	return more, err
}

func (s jobsService) JobWaiting(sessionID string) bool {
	eng := s.engine()
	return eng != nil && eng.JobWaiting(sessionID)
}

// RequestRefresh queues a refresh asked from the mod's /workbook refresh; the caller's session runs it when it can.
func (s jobsService) RequestRefresh(_, sessionID string) (int64, error) {
	eng := s.engine()
	if eng == nil {
		return 0, modevents.ErrNotLive
	}
	id, err := eng.RequestRefresh(sessionID, sessionID)
	switch err {
	case ErrNotLive:
		return 0, modevents.ErrNotLive
	case ErrRefreshPending:
		return 0, modevents.ErrRefreshPending
	}
	return id, err
}

// JobWaiting: the session's conversation has a job queued that nobody holds. Only a capable session is told, so a mod
// that cannot run the job is never asked to.
func (e *Engine) JobWaiting(sessionID string) bool {
	if !e.capable(sessionID) {
		return false
	}
	conv, err := e.convKey(sessionID)
	if err != nil {
		return false
	}
	canRefresh := e.refreshCapable(sessionID)
	e.qmu.Lock()
	defer e.qmu.Unlock()
	q := e.convs[conv]
	if e.qstopped || q == nil || q.lease != nil || len(q.waiting) == 0 {
		return false
	}
	// a refresh at the head is for a session that can run it; telling another would only make it ask for nothing
	return q.waiting[0].kind != JobRefresh || canRefresh
}
