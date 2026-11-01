package conversation

import (
	"context"
	"errors"

	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convturns"
)

// LastTurns implements convturns.Reader: acquire the session's entry, refresh it from the transcript, take the newest n
// turns and release. The turns are a deep copy of the model's (items are not size-capped here; the caller picks what it needs and may change them).
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
	return entry.CopyLastTurns(n), nil // a deep copy: the cache's own model never leaves the package
}

// TurnsFrom implements convturns.Reader: acquire the session's entry, refresh it from the transcript, take the turn with
// fromTurnID and the turns after it (all of them when it is empty or gone, Found=false) and release. The turns are a deep
// copy, and Path is the transcript file the refresh read.
func (m *Module) TurnsFrom(ctx context.Context, provider, sessionID, fromTurnID string) (convturns.TurnsFromResult, error) {
	if provider != "claude" {
		return convturns.TurnsFromResult{}, convturns.ErrUnsupportedProvider
	}
	entry, release, err := m.cache.Acquire(ctx, sessionID)
	if err != nil {
		return convturns.TurnsFromResult{}, err
	}
	defer release()
	var path string
	if err := entry.Exclusive(ctx, func() error {
		var rerr error
		path, rerr = m.refreshPath(ctx, entry, sessionID)
		return rerr
	}); err != nil {
		return convturns.TurnsFromResult{}, err
	}
	turns, found := entry.CopyTurnsFrom(fromTurnID)
	return convturns.TurnsFromResult{Turns: turns, Found: found, Path: path}, nil
}
