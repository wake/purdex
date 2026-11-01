// Package convturns is the narrow in-process read of a conversation's last turns, for modules that cannot import the
// conversation module (the session workbook). The conversation module registers a Reader under Key at Init.
package convturns

import (
	"context"
	"errors"

	"github.com/wake/purdex/internal/convmodel"
)

// Key is the service-registry key of the Reader.
const Key = "conversation.turns"

// ErrUnsupportedProvider: only Claude Code transcripts are readable.
var ErrUnsupportedProvider = errors.New("convturns: unsupported provider")

// Reader reads a session's newest turns from its transcript.
type Reader interface {
	// LastTurns returns the newest n turns (1 ≤ n), oldest first. A turn still running has Outcome running. The errors
	// are convfeed's: ErrNotFound (no transcript), ErrBusy (the cache is full of pinned entries), or a read failure.
	LastTurns(ctx context.Context, provider, sessionID string, n int) ([]convmodel.Turn, error)

	// TurnsFrom returns the turn whose id is fromTurnID and every turn after it, oldest first, with Found=true and the
	// transcript's resolved path. An empty fromTurnID, or one the conversation no longer has, returns every turn with
	// Found=false. A turn still running has Outcome running. The turns are a deep copy. The errors are LastTurns':
	// ErrUnsupportedProvider (not claude), convfeed's ErrNotFound (no transcript), ErrBusy (the cache is full of pinned
	// entries), or a read failure.
	TurnsFrom(ctx context.Context, provider, sessionID, fromTurnID string) (TurnsFromResult, error)
}

// TurnsFromResult is the answer of Reader.TurnsFrom.
type TurnsFromResult struct {
	Turns []convmodel.Turn
	Found bool   // fromTurnID was found among the turns (false: Turns is the whole conversation)
	Path  string // the transcript file the turns were read from
}
