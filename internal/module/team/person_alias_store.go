package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// personAliasSchema: when a person's command approves the agent's OPEN request instead of opening its own row, the person's request id
// has no row of its own. This table maps it to the row that answered it (and the request's hash), so a retry of the same id after
// a lost response is the same request and gets the same row, not a refusal for a token that is already spent (RT-1b).
const personAliasSchema = `
	CREATE TABLE IF NOT EXISTS person_aliases (
		request_id  TEXT PRIMARY KEY,
		approval_id TEXT NOT NULL,
		hash        TEXT NOT NULL,
		created_at  INTEGER NOT NULL
	);`

// RecordPersonAlias remembers that request id was answered by approval approvalID.
func (s *Store) RecordPersonAlias(requestID, approvalID, hash string, at int64) error {
	_, err := s.db.Exec(`INSERT INTO person_aliases (request_id, approval_id, hash, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (request_id) DO NOTHING`, requestID, approvalID, hash, at)
	if err != nil {
		return fmt.Errorf("record person alias %s: %w", requestID, err)
	}
	return nil
}

// MarkLeadPerson marks the open lead request id as a person's card request (LeadPayload.Person), so the unattended sweep leaves it
// for the person; it returns the row as it is now. The request's hash is not part of the payload and does not change.
func (s *Store) MarkLeadPerson(id string) (team.Approval, error) {
	if _, err := s.db.Exec(`UPDATE approval_requests SET payload_json = json_set(payload_json, '$.person', json('true'))
		WHERE id = ? AND kind = 'lead' AND state = 'open'`, id); err != nil {
		return team.Approval{}, fmt.Errorf("mark lead person %s: %w", id, err)
	}
	row, ok, err := s.Get(id)
	if err == nil && !ok {
		err = ErrNoSuchApproval
	}
	return row, err
}

// PersonAlias is the approval that answered request id and the hash it was asked with; ok false when there is none.
func (s *Store) PersonAlias(requestID string) (approvalID, hash string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT approval_id, hash FROM person_aliases WHERE request_id = ?`, requestID).Scan(&approvalID, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("person alias %s: %w", requestID, err)
	}
	return approvalID, hash, true, nil
}
