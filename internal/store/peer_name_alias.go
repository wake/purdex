package store

import (
	"context"
	"database/sql"
	"time"
)

// Name aliases (name pool v2 spec §6.3, U4): a rename keeps the old full virtual name as an alias, so an address that was
// written down still reaches the session. The rename and its alias row commit together (RenamePool); a name that is assigned
// anew stops being an alias. Routing reads the table through Aliases (internal/peers Resolve gets it as data).

// PeerNameAlias is one old name and the session it now stands for.
type PeerNameAlias struct {
	Alias     string // the old full virtual name
	SessionID string
	Ref       string // the session's ref, which the existing ref path resolves (so a relayed successor is reached)
	CreatedAt int64  // unix ms
}

// aliasClock is the time an alias is stamped with; tests replace it.
var aliasClock func() int64

func aliasNow() int64 {
	if aliasClock != nil {
		return aliasClock()
	}
	return time.Now().UnixMilli()
}

func migratePeerNameAliases(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS peer_name_aliases (
			alias      TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			ref        TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`)
	return err
}

// Aliases is every alias row, oldest first.
func (s *PeerNameStore) Aliases(ctx context.Context) ([]PeerNameAlias, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT alias, session_id, ref, created_at FROM peer_name_aliases ORDER BY created_at, alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PeerNameAlias
	for rows.Next() {
		var a PeerNameAlias
		if err := rows.Scan(&a.Alias, &a.SessionID, &a.Ref, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAliases removes the given aliases; one that is not there is not an error.
func (s *PeerNameStore) DeleteAliases(ctx context.Context, aliases []string) error {
	for len(aliases) > 0 {
		n := min(len(aliases), peerNameChunk)
		args := make([]any, n)
		for i := range args {
			args[i] = aliases[i]
		}
		aliases = aliases[n:]
		if _, err := s.db.ExecContext(ctx, `DELETE FROM peer_name_aliases WHERE alias IN (`+placeholders(n)+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

// TrimAliases keeps the newest keep rows (by created_at) and deletes the rest; it returns how many it deleted.
func (s *PeerNameStore) TrimAliases(ctx context.Context, keep int) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM peer_name_aliases WHERE alias IN (
			SELECT alias FROM peer_name_aliases ORDER BY created_at DESC, alias DESC LIMIT -1 OFFSET ?)`, keep)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// UndoRename reverses a RenamePool whose follow-up failed or was lost (spec §5.3): it puts oldName back when the stored name
// is still currentName (a compare-and-set, so a later rename wins), and deletes the alias that rename added. ok is false when
// the name was no longer currentName; nothing changes then.
func (s *PeerNameStore) UndoRename(ctx context.Context, sessionID, currentName, oldName string) (bool, error) {
	sid := normPeerSID(sessionID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit succeeds
	res, err := tx.ExecContext(ctx, `UPDATE peer_names SET name = ? WHERE session_id = ? AND name = ?`, oldName, sid, currentName)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM peer_name_aliases WHERE alias = ? AND session_id = ?`, oldName, sid); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func placeholders(n int) string {
	b := make([]byte, 0, 2*n)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}
