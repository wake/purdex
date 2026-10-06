package conversations

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wake/purdex/internal/store"
)

// Index is the conversation index Scan keeps up to date
// (store.ConversationStore).
type Index interface {
	All(ctx context.Context) ([]store.ConversationIndexRow, error)
	UpsertBatch(ctx context.Context, rows []store.ConversationIndexRow) error
}

// ScanResult is what one Scan saw.
type ScanResult struct {
	Present        map[string]Entry // the listing, by session id; nil when RootErr != nil
	UnreadableDirs []string         // slug dirs skipped this round (R-4-7), as ListRoot gave them
	RootErr        error            // the root could not be listed; nothing was written
	ScannedAt      int64            // Unix ms, the now() of the scan
	Files          int              // entries listed
	Reread         int              // entries whose head or tail was read into a written row
	BytesRead      int64            // every byte read, a read that failed included
}

// openTranscript is OpenTranscript; a var only so tests can act between the
// listing and the open.
var openTranscript = OpenTranscript

// Scan lists root and brings the index up to date. Each listed file is
// opened and its fstat compared with its index row, in this order (§13.6
// "How the index is kept"; transcripts are append-only):
//
//   - no row, a different inode, or a smaller size: the file was rewritten
//     (or is new), so the head is scanned from byte 0 with every head field
//     reset, and the tail is read;
//   - the same size and mtime: nothing is read, only LastSeenAt (and
//     TranscriptPath, for a renamed slug dir) is updated;
//   - otherwise it grew (or only its mtime moved): the head fields are kept
//     when the head is done, or the head scan resumes from HeadOffset with
//     them, and the tail is read.
//
// Size, MtimeMs and Inode come from the opened file's fstat, so the row
// describes the bytes read against. The head scan is bounded by the 16 MB
// cap, not by that size, so on a file growing during the scan HeadOffset may
// pass the stored Size; the next scan sees a larger file and resumes there.
//
// A root failure returns RootErr and writes nothing. A failure to open or
// read one file skips that file this round (it stays Present; its row is
// unchanged). ctx is checked between files. Every written row (re-read or
// only seen) goes into one UpsertBatch. The returned error is only an index
// error or ctx.Err(); with it, the result is the zero ScanResult.
func Scan(ctx context.Context, root string, idx Index, now func() time.Time) (ScanResult, error) {
	res := ScanResult{ScannedAt: now().UnixMilli()}
	entries, unreadable, err := ListRoot(root)
	if err != nil {
		res.RootErr = err
		return res, nil
	}
	res.UnreadableDirs = unreadable
	res.Files = len(entries)
	res.Present = make(map[string]Entry, len(entries))
	for _, e := range entries {
		res.Present[e.SessionID] = e
	}

	rows, err := idx.All(ctx)
	if err != nil {
		return ScanResult{}, fmt.Errorf("conversations: read index: %w", err)
	}
	byID := make(map[string]store.ConversationIndexRow, len(rows))
	for _, r := range rows {
		byID[strings.ToLower(r.SessionID)] = r
	}

	var batch []store.ConversationIndexRow
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return ScanResult{}, err
		}
		prev, has := byID[e.SessionID]
		row, reread, n, ok := scanFile(e, prev, has, res.ScannedAt)
		res.BytesRead += n
		if !ok {
			continue
		}
		if reread {
			res.Reread++
		}
		batch = append(batch, row)
	}
	if err := idx.UpsertBatch(ctx, batch); err != nil {
		return ScanResult{}, fmt.Errorf("conversations: write index: %w", err)
	}
	return res, nil
}

// scanFile brings one listed file's row up to date (see Scan). prev is its
// index row when has. ok is false when the file could not be opened or
// read: no row is written for it this round. reread tells whether the head
// or the tail was read; n counts the bytes read, a failed read included.
func scanFile(e Entry, prev store.ConversationIndexRow, has bool, nowMs int64) (row store.ConversationIndexRow, reread bool, n int64, ok bool) {
	f, fe, err := openTranscript(e.Path)
	if err != nil {
		return row, false, 0, false
	}
	defer f.Close()

	var head Head
	switch {
	case !has || fe.Inode != prev.Inode || fe.Size < prev.Size:
		row = store.ConversationIndexRow{FirstSeenAt: nowMs}
		if has {
			row.FirstSeenAt = prev.FirstSeenAt
		}
	case fe.Size == prev.Size && fe.MtimeMs == prev.MtimeMs:
		row = prev
		row.SessionID, row.TranscriptPath, row.LastSeenAt = e.SessionID, fe.Path, nowMs
		return row, false, 0, true
	default:
		row = prev
		head = Head{Cwd: prev.Cwd, FirstEntrypoint: prev.FirstEntrypoint, FirstPrompt: prev.FirstPrompt,
			Offset: prev.HeadOffset, Done: prev.HeadDone} // a done head reads nothing
	}

	h, hn, err := ScanHead(f, head)
	n += hn
	if err != nil {
		return row, false, n, false
	}
	t, tn, err := ReadTail(f, fe.Size)
	n += tn
	if err != nil {
		return row, false, n, false
	}

	row.SessionID, row.TranscriptPath, row.LastSeenAt = e.SessionID, fe.Path, nowMs
	row.Size, row.MtimeMs, row.Inode = fe.Size, fe.MtimeMs, fe.Inode
	row.Cwd, row.FirstEntrypoint, row.FirstPrompt = h.Cwd, h.FirstEntrypoint, h.FirstPrompt
	row.HeadOffset, row.HeadDone = h.Offset, h.Done
	row.LastEntrypoint, row.CustomTitle, row.AITitle = t.LastEntrypoint, t.CustomTitle, t.AITitle
	return row, true, n, true
}
