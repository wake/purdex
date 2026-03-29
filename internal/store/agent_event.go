// internal/store/agent_event.go
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

// AgentEvent is a single hook event stored per tmux session.
type AgentEvent struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
	AgentType   string          `json:"agent_type"`
}

// AgentEventStore persists the latest agent hook event per tmux session.
type AgentEventStore struct{ db *sql.DB }

// OpenAgentEvent opens (or creates) an AgentEventStore DB at path, runs
// migration, and enables WAL mode. Use ":memory:" for tests.
func OpenAgentEvent(path string) (*AgentEventStore, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open agent event db: %w", err)
	}
	if err := migrateAgentEventDB(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate agent event db: %w", err)
	}
	return &AgentEventStore{db: db}, nil
}

func migrateAgentEventDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_events (
			tmux_session TEXT PRIMARY KEY,
			event_name   TEXT NOT NULL,
			raw_event    TEXT NOT NULL,
			updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}
	// Idempotent migration: add agent_type column if not exists.
	_, _ = db.Exec(`ALTER TABLE agent_events ADD COLUMN agent_type TEXT NOT NULL DEFAULT ''`)
	return nil
}

// Close closes the underlying DB connection.
func (s *AgentEventStore) Close() error { return s.db.Close() }

// Set upserts the latest agent hook event for a tmux session.
func (s *AgentEventStore) Set(tmuxSession, eventName string, rawEvent json.RawMessage, agentType string) error {
	_, err := s.db.Exec(`
		INSERT INTO agent_events (tmux_session, event_name, raw_event, agent_type, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tmux_session) DO UPDATE SET
			event_name = excluded.event_name,
			raw_event  = excluded.raw_event,
			agent_type = excluded.agent_type,
			updated_at = CURRENT_TIMESTAMP
	`, tmuxSession, eventName, string(rawEvent), agentType)
	return err
}

// Get returns the latest AgentEvent for tmuxSession, or nil if not found.
func (s *AgentEventStore) Get(tmuxSession string) (*AgentEvent, error) {
	var ev AgentEvent
	var raw string
	err := s.db.QueryRow(`
		SELECT tmux_session, event_name, raw_event, agent_type
		FROM agent_events WHERE tmux_session = ?
	`, tmuxSession).Scan(&ev.TmuxSession, &ev.EventName, &raw, &ev.AgentType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ev.RawEvent = json.RawMessage(raw)
	return &ev, nil
}

// ListAll returns all stored agent events ordered by tmux_session.
func (s *AgentEventStore) ListAll() ([]AgentEvent, error) {
	rows, err := s.db.Query(`
		SELECT tmux_session, event_name, raw_event, agent_type
		FROM agent_events ORDER BY tmux_session
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentEvent
	for rows.Next() {
		var ev AgentEvent
		var raw string
		if err := rows.Scan(&ev.TmuxSession, &ev.EventName, &raw, &ev.AgentType); err != nil {
			return nil, err
		}
		ev.RawEvent = json.RawMessage(raw)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// Delete removes the event for tmuxSession (no-op if not found).
func (s *AgentEventStore) Delete(tmuxSession string) error {
	_, err := s.db.Exec("DELETE FROM agent_events WHERE tmux_session = ?", tmuxSession)
	return err
}
