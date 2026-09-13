// internal/store/peer_message.go
package store

import (
	"database/sql"
	"time"
)

// Direction values for PeerMessage.Direction.
const (
	DirOut   = "out"
	DirIn    = "in"
	DirReply = "reply"
)

// PeerMessage is a single cross-host message delivery attempt recorded for
// audit purposes.
type PeerMessage struct {
	ID            int64
	MsgID         string
	NativeMsgID   string
	Direction     string
	TS            time.Time
	FromHostID    string
	FromSessionID string
	ToHostID      string
	ToSessionID   string
	DeclaredMode  string
	EffectiveMode string
	Bytes         int
	Result        string
	Error         string
}

// PeerMessageStore persists peer_messages audit rows on the shared meta DB.
type PeerMessageStore struct{ db *sql.DB }

// PeerMessages returns the audit store backed by this MetaStore's DB.
func (m *MetaStore) PeerMessages() *PeerMessageStore {
	return &PeerMessageStore{db: m.db}
}

// Insert writes the row and returns its id. A repeated (msg_id, direction)
// is allowed (D10): dedup is the caller's in-memory window, never the DB.
func (s *PeerMessageStore) Insert(p PeerMessage) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO peer_messages (
			msg_id, native_msg_id, direction, ts,
			from_host_id, from_session_id, to_host_id, to_session_id,
			declared_mode, effective_mode, bytes, result, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		p.MsgID, p.NativeMsgID, p.Direction, p.TS.UnixMilli(),
		p.FromHostID, p.FromSessionID, p.ToHostID, p.ToSessionID,
		p.DeclaredMode, p.EffectiveMode, p.Bytes, p.Result, p.Error,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetResult updates result and error; effectiveMode is written too when
// non-empty (the sender learns it only from the remote's answer).
func (s *PeerMessageStore) SetResult(id int64, effectiveMode, result, errText string) error {
	if effectiveMode == "" {
		_, err := s.db.Exec(`
			UPDATE peer_messages SET result = ?, error = ? WHERE id = ?
		`, result, errText, id)
		return err
	}
	_, err := s.db.Exec(`
		UPDATE peer_messages SET effective_mode = ?, result = ?, error = ? WHERE id = ?
	`, effectiveMode, result, errText, id)
	return err
}

// Tail returns the newest n rows, oldest first.
func (s *PeerMessageStore) Tail(n int) ([]PeerMessage, error) {
	out := make([]PeerMessage, 0)
	if n <= 0 {
		return out, nil
	}

	rows, err := s.db.Query(`
		SELECT id, msg_id, native_msg_id, direction, ts,
			from_host_id, from_session_id, to_host_id, to_session_id,
			declared_mode, effective_mode, bytes, result, error
		FROM peer_messages
		ORDER BY id DESC
		LIMIT ?
	`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var newestFirst []PeerMessage
	for rows.Next() {
		var p PeerMessage
		var tsMs int64
		if err := rows.Scan(
			&p.ID, &p.MsgID, &p.NativeMsgID, &p.Direction, &tsMs,
			&p.FromHostID, &p.FromSessionID, &p.ToHostID, &p.ToSessionID,
			&p.DeclaredMode, &p.EffectiveMode, &p.Bytes, &p.Result, &p.Error,
		); err != nil {
			return nil, err
		}
		p.TS = time.UnixMilli(tsMs)
		newestFirst = append(newestFirst, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := len(newestFirst) - 1; i >= 0; i-- {
		out = append(out, newestFirst[i])
	}
	return out, nil
}
