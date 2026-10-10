// Package workbooklines is the narrow view of the session workbook that the push module needs: wait for the entry of a
// Stop and read its push line. It lives apart from both modules so neither imports the other (push depends on team and
// agent; the workbook depends on agent, team and the conversation module).
package workbooklines

import (
	"context"
	"time"
)

// Key is the service-registry key under which the workbook module publishes its Lines.
const Key = "workbook.push-lines"

// Line is what a Stop push may use once the entry's push line is final.
type Line struct {
	Thing   string // the name of the thing worked on, the title's subject
	Push    string // the lock-screen line; never empty in a Line
	ConvKey string
	EntryID int64
}

// Lines waits for the push line of a turn.
type Lines interface {
	// Await returns the push line of the newest entry of sessionID whose turn ended at or after sinceMs-2000 (unix ms),
	// as soon as it is final. false at once when that entry failed, was skipped, or has no push line, or when the
	// session's turn was looked at and has no entry; false at the deadline or when ctx ends.
	Await(ctx context.Context, sessionID string, sinceMs int64, deadline time.Time) (Line, bool)
}
