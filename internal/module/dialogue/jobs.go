package dialogue

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

// jobsKey is the core service-registry key of the mod socket's view of the job channel (modevents.WorkbookService). The
// name is the session workbook's, kept so the deployed mod and the socket module need no change (spec section 8): the
// socket module looks "workbook.jobs" up per request.
const jobsKey = "workbook.jobs"

// jobsService adapts the module's channel to the socket routes. The channel exists only while the module runs, so each
// call reads it fresh: a stopped module answers "no job" and "not leased", and Ready is false (the socket answers 503).
type jobsService struct{ m *Module }

var _ modevents.WorkbookService = jobsService{}

func (s jobsService) channel() *channel {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	return s.m.jobs
}

// Ready: the module runs. The socket answers 503 for a module that is registered but off.
func (s jobsService) Ready() bool { return s.channel() != nil }

func (s jobsService) NextJob(ctx context.Context, stream, sessionID string, wait time.Duration) (any, bool) {
	ch := s.channel()
	if ch == nil {
		return nil, false
	}
	j, ok := ch.Next(ctx, stream, sessionID, wait)
	if !ok {
		return nil, false
	}
	return j, true
}

func (s jobsService) JobResult(stream string, r modevents.WorkbookResult) (bool, error) {
	ch := s.channel()
	if ch == nil {
		return false, modevents.ErrNotLeased
	}
	return ch.Result(stream, r.JobID, result{
		Answered: r.Answered, Text: r.Text, Reason: r.Reason, Status: r.Status, Error: r.Error,
		Usage:     usage{In: r.Usage.Input, Out: r.Usage.Output, CacheRead: r.Usage.CacheRead},
		LatencyMS: r.LatencyMS,
	})
}

// CompactContext is the text a compaction hands the agent (compact_context.go); false: nothing to hand over.
func (s jobsService) CompactContext(ctx context.Context, sid string) (string, bool) {
	return s.m.compactContext(ctx, sid, compactRecapWait)
}

func (s jobsService) JobWaiting(sessionID string) bool {
	ch := s.channel()
	return ch != nil && ch.JobWaiting(sessionID)
}
