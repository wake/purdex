package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// conversationNameMaxRunes caps a stored registry name.
const conversationNameMaxRunes = 120

// ConversationNameStore persists the registry name (Claude Code's session
// name) seen for a conversation while it was alive. It is a separate table
// from conversation_index on purpose: scan rewrites index rows whole, which
// would wipe a name kept there. Rows are never deleted (same lifetime as the
// index).
type ConversationNameStore struct{ db *sql.DB }

// ConversationNames returns the conversation-name store backed by this MetaStore's DB.
func (m *MetaStore) ConversationNames() *ConversationNameStore {
	return &ConversationNameStore{db: m.db}
}

func migrateConversationNames(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS conversation_names (
			session_id TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			seen_at    INTEGER NOT NULL
		)`)
	return err
}

// Upsert records name for sessionID (lowercased), refreshing seen_at. The
// name is trimmed and cut to 120 runes; an empty id or name is rejected.
func (s *ConversationNameStore) Upsert(ctx context.Context, sessionID, name string, nowMs int64) error {
	sid := strings.ToLower(strings.TrimSpace(sessionID))
	if sid == "" {
		return errors.New("conversation name: empty session id")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("conversation name: empty name")
	}
	if r := []rune(name); len(r) > conversationNameMaxRunes {
		name = string(r[:conversationNameMaxRunes])
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_names (session_id, name, seen_at) VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET name = excluded.name, seen_at = excluded.seen_at`,
		sid, name, nowMs)
	return err
}

// All returns session id -> name for every row.
func (s *ConversationNameStore) All(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, name FROM conversation_names`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var sid, name string
		if err := rows.Scan(&sid, &name); err != nil {
			return nil, err
		}
		out[sid] = name
	}
	return out, rows.Err()
}

// seenAt is a test-only accessor for the seen_at column.
func (s *ConversationNameStore) seenAt(ctx context.Context, sessionID string) (int64, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT seen_at FROM conversation_names WHERE session_id = ?`,
		strings.ToLower(sessionID)).Scan(&at)
	return at, err
}
