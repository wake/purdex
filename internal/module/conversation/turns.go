package conversation

import (
	"context"
	"errors"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
)

// LastTurns implements convturns.Reader: acquire the session's entry, refresh it from the transcript, take the newest n
// turns and release. The turns are the model's own (items are not size-capped here; the caller picks what it needs).
func (m *Module) LastTurns(ctx context.Context, provider, sessionID string, n int) ([]convmodel.Turn, error) {
	if provider != "claude" {
		return nil, convturns.ErrUnsupportedProvider
	}
	if n < 1 {
		return nil, errors.New("conversation: LastTurns needs n >= 1")
	}
	entry, release, err := m.cache.Acquire(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := entry.Exclusive(ctx, func() error { return m.refresh(ctx, entry, sessionID) }); err != nil {
		return nil, err
	}
	win := entry.Window(n, -1, func([]byte) bool { return true })
	return win.Turns, nil
}
