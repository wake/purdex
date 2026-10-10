package modeventsmod

import (
	"context"
	"errors"
	"time"

	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/promptq"
)

// streamOwners says who owns a session's prompts: the live mod stream that announced prompt.v1 most recently for the
// session's current id (U3-0b).
type streamOwners struct{ reg *modevents.Registry }

func (o streamOwners) OwnerOf(sessionID string) (string, bool) {
	return o.reg.NewestCapableStream(sessionID, modevents.CapPromptV1, modevents.CapsFresh)
}

// promptSvc adapts the queue to the socket's PromptService.
type promptSvc struct{ q *promptq.Queue }

func (s promptSvc) NextPrompt(ctx context.Context, stream, sessionID string, wait time.Duration) (any, bool) {
	j, ok := s.q.Next(ctx, stream, sessionID, wait)
	if !ok {
		return nil, false
	}
	return j, true
}

func (s promptSvc) PromptResult(stream string, r modevents.PromptResult) error {
	err := s.q.Result(stream, r.JobID, promptq.Outcome{Status: r.Status, Reason: r.Reason})
	switch {
	case errors.Is(err, promptq.ErrNotLeased):
		return modevents.ErrPromptNotLeased
	case errors.Is(err, promptq.ErrNotOwner):
		return modevents.ErrPromptNotOwner
	}
	return err
}

func (m *Module) promptService() modevents.PromptService {
	if m.queue == nil {
		return nil
	}
	return promptSvc{m.queue}
}
