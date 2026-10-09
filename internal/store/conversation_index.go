// internal/store/conversation_index.go
package store

import (
	"context"
	"database/sql"
	"errors"
)

// ConversationIndexRow is one conversation_index row: what the scanner has
// learned about one Claude Code transcript. Rows are never deleted.
type ConversationIndexRow struct {
	SessionID       string // lowercase UUID, primary key
	TranscriptPath  string
	Cwd             string
	FirstEntrypoint string
	LastEntrypoint  string
	CustomTitle     string
	AITitle         string
	FirstPrompt     string // ≤ 500 bytes, cut on a UTF-8 boundary
	Size            int64
	MtimeMs         int64
	Inode           uint64 // a different inode means the file was rewritten
	HeadOffset      int64  // byte where the head scan stopped (always a line boundary)
	HeadDone        bool   // the first prompt was found, or 16 MB were examined
	FirstSeenAt     int64  // Unix ms
	LastSeenAt      int64  // Unix ms
}

// ConversationStore persists the conversation index on the shared meta DB.
type ConversationStore struct{ db *sql.DB }

// Conversations returns the conversation index store backed by this MetaStore's DB.
func (m *MetaStore) Conversations() *ConversationStore { return &ConversationStore{db: m.db} }

func migrateConversationIndex(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS conversation_index (
			session_id        TEXT PRIMARY KEY,
			transcript_path   TEXT NOT NULL,
			cwd               TEXT NOT NULL DEFAULT '',
			first_entrypoint  TEXT NOT NULL DEFAULT '',
			last_entrypoint   TEXT NOT NULL DEFAULT '',
			custom_title      TEXT NOT NULL DEFAULT '',
			ai_title          TEXT NOT NULL DEFAULT '',
			first_prompt      TEXT NOT NULL DEFAULT '',
			size              INTEGER NOT NULL DEFAULT 0,
			mtime_ms          INTEGER NOT NULL DEFAULT 0,
			inode             INTEGER NOT NULL DEFAULT 0,
			head_offset       INTEGER NOT NULL DEFAULT 0,
			head_done         INTEGER NOT NULL DEFAULT 0,
			first_seen_at     INTEGER NOT NULL,
			last_seen_at      INTEGER NOT NULL
		)`)
	return err
}

// All returns every row ordered by session_id.
func (s *ConversationStore) All(ctx context.Context) ([]ConversationIndexRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, transcript_path, cwd, first_entrypoint, last_entrypoint,
		       custom_title, ai_title, first_prompt, size, mtime_ms, inode,
		       head_offset, head_done, first_seen_at, last_seen_at
		FROM conversation_index ORDER BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ConversationIndexRow, 0)
	for rows.Next() {
		var (
			r        ConversationIndexRow
			inode    int64
			headDone int64
		)
		if err := rows.Scan(&r.SessionID, &r.TranscriptPath, &r.Cwd, &r.FirstEntrypoint,
			&r.LastEntrypoint, &r.CustomTitle, &r.AITitle, &r.FirstPrompt, &r.Size,
			&r.MtimeMs, &inode, &r.HeadOffset, &headDone, &r.FirstSeenAt, &r.LastSeenAt); err != nil {
			return nil, err
		}
		// sqlite INTEGER is signed 64-bit; the inode was stored as int64(inode).
		r.Inode = uint64(inode)
		r.HeadDone = headDone != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// TranscriptPath is the transcript path the index recorded for sessionID (lowercase UUID); ok is false when there is
// no row.
func (s *ConversationStore) TranscriptPath(ctx context.Context, sessionID string) (path string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT transcript_path FROM conversation_index WHERE session_id = ?`, sessionID).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return path, true, nil
}

// UpsertBatch writes rows in one transaction. On conflict it updates every
// column except first_seen_at, which keeps its original value.
func (s *ConversationStore) UpsertBatch(ctx context.Context, rows []ConversationIndexRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit succeeds
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO conversation_index (
			session_id, transcript_path, cwd, first_entrypoint, last_entrypoint,
			custom_title, ai_title, first_prompt, size, mtime_ms, inode,
			head_offset, head_done, first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			transcript_path  = excluded.transcript_path,
			cwd              = excluded.cwd,
			first_entrypoint = excluded.first_entrypoint,
			last_entrypoint  = excluded.last_entrypoint,
			custom_title     = excluded.custom_title,
			ai_title         = excluded.ai_title,
			first_prompt     = excluded.first_prompt,
			size             = excluded.size,
			mtime_ms         = excluded.mtime_ms,
			inode            = excluded.inode,
			head_offset      = excluded.head_offset,
			head_done        = excluded.head_done,
			last_seen_at     = excluded.last_seen_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		headDone := 0
		if r.HeadDone {
			headDone = 1
		}
		if _, err := stmt.ExecContext(ctx, r.SessionID, r.TranscriptPath, r.Cwd,
			r.FirstEntrypoint, r.LastEntrypoint, r.CustomTitle, r.AITitle, r.FirstPrompt,
			r.Size, r.MtimeMs, int64(r.Inode), r.HeadOffset, headDone,
			r.FirstSeenAt, r.LastSeenAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
