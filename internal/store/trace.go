package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxChains = 10000
	maxSteps  = 100000
)

// TraceChain is the lightweight summary row for a trace chain.
type TraceChain struct {
	ChainID     string `json:"chain_id"`
	TmuxSession string `json:"tmux_session"`
	PaneID      string `json:"pane_id"`
	AgentType   string `json:"agent_type"`
	EventName   string `json:"event_name"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	StepCount   int    `json:"step_count"`
}

// TraceStep is a single ordered step within a trace chain.
type TraceStep struct {
	StepID       string          `json:"step_id"`
	ChainID      string          `json:"chain_id"`
	ParentStepID string          `json:"parent_step_id,omitempty"`
	StepName     string          `json:"step_name"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	StepIndex    int             `json:"step_index"`
	CreatedAt    int64           `json:"created_at"`
}

// TraceRecord combines the chain summary with its ordered steps.
type TraceRecord struct {
	Chain TraceChain  `json:"chain"`
	Steps []TraceStep `json:"steps"`
}

// TraceListFilter filters and paginates chain summaries.
type TraceListFilter struct {
	TmuxSession string
	PaneID      string
	AgentType   string
	EventName   string
	Limit       int
	Cursor      string
	Before      bool
}

// TraceChainPage is a page of trace chain summaries.
type TraceChainPage struct {
	Chains     []TraceChain
	NextCursor string
}

// TraceStore persists trace chains and their ordered steps.
type TraceStore struct{ db *sql.DB }

func migrateTraceDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_trace_chains (
			chain_id     TEXT PRIMARY KEY,
			tmux_session TEXT NOT NULL,
			pane_id      TEXT NOT NULL,
			agent_type   TEXT NOT NULL,
			event_name   TEXT NOT NULL,
			created_at   INTEGER NOT NULL,
			updated_at   INTEGER NOT NULL
		)
	`)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_trace_steps (
			step_id        TEXT PRIMARY KEY,
			chain_id       TEXT NOT NULL,
			parent_step_id TEXT,
			step_name      TEXT NOT NULL,
			payload        TEXT NOT NULL DEFAULT 'null',
			step_index     INTEGER NOT NULL,
			created_at     INTEGER NOT NULL,
			FOREIGN KEY (chain_id) REFERENCES agent_trace_chains(chain_id) ON DELETE CASCADE,
			FOREIGN KEY (parent_step_id) REFERENCES agent_trace_steps(step_id) ON DELETE SET NULL
		)
	`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_chains_session_created ON agent_trace_chains(tmux_session, created_at DESC, chain_id DESC)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_chains_pane_created ON agent_trace_chains(pane_id, created_at DESC, chain_id DESC)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_chains_agent_event_created ON agent_trace_chains(agent_type, event_name, created_at DESC, chain_id DESC)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_chains_created ON agent_trace_chains(created_at DESC, chain_id DESC)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_steps_chain_index ON agent_trace_steps(chain_id, step_index ASC, created_at ASC, step_id ASC)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_steps_created ON agent_trace_steps(created_at DESC, step_id DESC)`); err != nil {
		return err
	}
	return nil
}

// SaveChain stores a chain summary and its ordered steps in a single
// transaction, replacing any prior steps for the same chain_id.
func (s *TraceStore) SaveChain(record TraceRecord) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	rollback := func() error {
		if rbErr := tx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
			return rbErr
		}
		return nil
	}

	chain := record.Chain
	if chain.ChainID == "" {
		chain.ChainID = uuid.NewString()
	}
	if chain.CreatedAt == 0 {
		chain.CreatedAt = time.Now().UnixNano()
	}
	if chain.UpdatedAt == 0 {
		chain.UpdatedAt = chain.CreatedAt
	}

	_, err = tx.Exec(`
		INSERT INTO agent_trace_chains (
			chain_id, tmux_session, pane_id, agent_type, event_name, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chain_id) DO UPDATE SET
			tmux_session = excluded.tmux_session,
			pane_id      = excluded.pane_id,
			agent_type   = excluded.agent_type,
			event_name   = excluded.event_name,
			updated_at   = excluded.updated_at
	`, chain.ChainID, chain.TmuxSession, chain.PaneID, chain.AgentType, chain.EventName, chain.CreatedAt, chain.UpdatedAt)
	if err != nil {
		if rbErr := rollback(); rbErr != nil {
			return rbErr
		}
		return err
	}

	if _, err = tx.Exec(`DELETE FROM agent_trace_steps WHERE chain_id = ?`, chain.ChainID); err != nil {
		if rbErr := rollback(); rbErr != nil {
			return rbErr
		}
		return err
	}

	for i := range record.Steps {
		step := record.Steps[i]
		if step.StepID == "" {
			step.StepID = uuid.NewString()
		}
		step.ChainID = chain.ChainID
		if step.CreatedAt == 0 {
			step.CreatedAt = chain.CreatedAt + int64(i)
		}
		payload := string(step.Payload)
		if payload == "" {
			payload = "null"
		}
		_, err = tx.Exec(`
			INSERT INTO agent_trace_steps (
				step_id, chain_id, parent_step_id, step_name, payload, step_index, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`, step.StepID, step.ChainID, nullString(step.ParentStepID), step.StepName, payload, step.StepIndex, step.CreatedAt)
		if err != nil {
			if rbErr := rollback(); rbErr != nil {
				return rbErr
			}
			return err
		}
	}

	if err := pruneTraceChains(tx); err != nil {
		if rbErr := rollback(); rbErr != nil {
			return rbErr
		}
		return err
	}
	if err := pruneTraceSteps(tx); err != nil {
		if rbErr := rollback(); rbErr != nil {
			return rbErr
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		if rbErr := rollback(); rbErr != nil {
			return rbErr
		}
		return err
	}
	return nil
}

// ListChains returns chain summaries ordered newest-first, with cursor-based
// pagination that can move either before or after the provided cursor.
func (s *TraceStore) ListChains(filter TraceListFilter) (TraceChainPage, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	limit++

	query, args, err := buildTraceChainListQuery(filter, limit)
	if err != nil {
		return TraceChainPage{}, err
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return TraceChainPage{}, err
	}
	defer rows.Close()

	chains, err := collectTraceChains(rows)
	if err != nil {
		return TraceChainPage{}, err
	}

	page := TraceChainPage{Chains: chains}
	if len(page.Chains) > limit-1 {
		last := page.Chains[limit-2]
		page.NextCursor = encodeTraceCursor(last.CreatedAt, last.ChainID)
		page.Chains = page.Chains[:limit-1]
	}
	return page, nil
}

// GetChainRecord returns a chain summary and its steps in step_index order.
func (s *TraceStore) GetChainRecord(chainID string) (*TraceRecord, error) {
	var chain TraceChain
	err := s.db.QueryRow(`
		SELECT c.chain_id, c.tmux_session, c.pane_id, c.agent_type, c.event_name,
		       c.created_at, c.updated_at,
		       (
		         SELECT COUNT(*)
		         FROM agent_trace_steps s
		         WHERE s.chain_id = c.chain_id
		       ) AS step_count
		FROM agent_trace_chains c
		WHERE c.chain_id = ?
	`, chainID).Scan(
		&chain.ChainID,
		&chain.TmuxSession,
		&chain.PaneID,
		&chain.AgentType,
		&chain.EventName,
		&chain.CreatedAt,
		&chain.UpdatedAt,
		&chain.StepCount,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`
		SELECT step_id, chain_id, parent_step_id, step_name, payload, step_index, created_at
		FROM agent_trace_steps
		WHERE chain_id = ?
		ORDER BY step_index ASC, created_at ASC, step_id ASC
	`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	steps, err := collectTraceSteps(rows)
	if err != nil {
		return nil, err
	}
	chain.StepCount = len(steps)
	return &TraceRecord{Chain: chain, Steps: steps}, nil
}

func pruneTraceChains(tx *sql.Tx) error {
	_, err := tx.Exec(`
		DELETE FROM agent_trace_chains
		WHERE chain_id IN (
			SELECT chain_id
			FROM agent_trace_chains
			ORDER BY created_at DESC, chain_id DESC
			LIMIT -1 OFFSET ?
		)
	`, maxChains)
	return err
}

func pruneTraceSteps(tx *sql.Tx) error {
	_, err := tx.Exec(`
		DELETE FROM agent_trace_steps
		WHERE step_id IN (
			SELECT step_id
			FROM agent_trace_steps
			ORDER BY created_at DESC, step_id DESC
			LIMIT -1 OFFSET ?
		)
	`, maxSteps)
	return err
}

func buildTraceChainListQuery(filter TraceListFilter, limit int) (string, []any, error) {
	var clauses []string
	var args []any

	if filter.TmuxSession != "" {
		clauses = append(clauses, "c.tmux_session = ?")
		args = append(args, filter.TmuxSession)
	}
	if filter.PaneID != "" {
		clauses = append(clauses, "c.pane_id = ?")
		args = append(args, filter.PaneID)
	}
	if filter.AgentType != "" {
		clauses = append(clauses, "c.agent_type = ?")
		args = append(args, filter.AgentType)
	}
	if filter.EventName != "" {
		clauses = append(clauses, "c.event_name = ?")
		args = append(args, filter.EventName)
	}
	if filter.Cursor != "" {
		createdAt, chainID, err := decodeTraceCursor(filter.Cursor)
		if err != nil {
			return "", nil, err
		}
		op := "<"
		if !filter.Before {
			op = ">"
		}
		clauses = append(clauses, fmt.Sprintf("(c.created_at %s ? OR (c.created_at = ? AND c.chain_id %s ?))", op, op))
		args = append(args, createdAt, createdAt, chainID)
	}

	query := `
		SELECT c.chain_id, c.tmux_session, c.pane_id, c.agent_type, c.event_name,
		       c.created_at, c.updated_at,
		       COUNT(s.step_id) AS step_count
		FROM agent_trace_chains c
		LEFT JOIN agent_trace_steps s ON s.chain_id = c.chain_id
	`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += `
		GROUP BY c.chain_id
		ORDER BY c.created_at DESC, c.chain_id DESC
		LIMIT ?
	`
	args = append(args, limit)
	return query, args, nil
}

func collectTraceChains(rows *sql.Rows) ([]TraceChain, error) {
	var chains []TraceChain
	for rows.Next() {
		var chain TraceChain
		if err := rows.Scan(
			&chain.ChainID,
			&chain.TmuxSession,
			&chain.PaneID,
			&chain.AgentType,
			&chain.EventName,
			&chain.CreatedAt,
			&chain.UpdatedAt,
			&chain.StepCount,
		); err != nil {
			return nil, err
		}
		chains = append(chains, chain)
	}
	return chains, rows.Err()
}

func collectTraceSteps(rows *sql.Rows) ([]TraceStep, error) {
	var steps []TraceStep
	for rows.Next() {
		var step TraceStep
		var parent sql.NullString
		var payload string
		if err := rows.Scan(
			&step.StepID,
			&step.ChainID,
			&parent,
			&step.StepName,
			&payload,
			&step.StepIndex,
			&step.CreatedAt,
		); err != nil {
			return nil, err
		}
		step.ParentStepID = parent.String
		step.Payload = json.RawMessage(payload)
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

func encodeTraceCursor(createdAt int64, chainID string) string {
	return strconv.FormatInt(createdAt, 10) + "|" + chainID
}

func decodeTraceCursor(cursor string) (int64, string, error) {
	createdAtText, chainID, ok := strings.Cut(cursor, "|")
	if !ok {
		return 0, "", fmt.Errorf("invalid trace cursor %q", cursor)
	}
	createdAt, err := strconv.ParseInt(createdAtText, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("invalid trace cursor %q: %w", cursor, err)
	}
	if chainID == "" {
		return 0, "", fmt.Errorf("invalid trace cursor %q", cursor)
	}
	return createdAt, chainID, nil
}
