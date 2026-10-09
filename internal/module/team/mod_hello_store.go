package teammod

import "fmt"

// modHelloSchema persists the mod's hello (P6-2a), so mod presence and its protocol version survive a daemon restart:
// without it every restart would answer relay_unsupported until each mod said hello again.
const modHelloSchema = `
	CREATE TABLE IF NOT EXISTS mod_hello (
		session_id  TEXT PRIMARY KEY,
		mod_version TEXT NOT NULL,
		agent       TEXT NOT NULL,
		at          INTEGER NOT NULL
	);`

// UpsertModHello records h for sessionID and keeps only the newest keep rows (the oldest go first).
func (s *Store) UpsertModHello(sessionID string, h helloInfo, keep int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("mod hello %s: %w", sessionID, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO mod_hello (session_id, mod_version, agent, at) VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET mod_version = excluded.mod_version, agent = excluded.agent, at = excluded.at`,
		sessionID, h.ModVersion, h.Agent, h.At); err != nil {
		return fmt.Errorf("mod hello %s: %w", sessionID, err)
	}
	if _, err := tx.Exec(`DELETE FROM mod_hello WHERE session_id IN
		(SELECT session_id FROM mod_hello ORDER BY at DESC, session_id LIMIT -1 OFFSET ?)`, keep); err != nil {
		return fmt.Errorf("mod hello %s: evict: %w", sessionID, err)
	}
	return tx.Commit()
}

// LoadModHello returns the newest limit hellos by session id.
func (s *Store) LoadModHello(limit int) (map[string]helloInfo, error) {
	rows, err := s.db.Query(`SELECT session_id, mod_version, agent, at FROM mod_hello ORDER BY at DESC, session_id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("load mod hello: %w", err)
	}
	defer rows.Close()
	out := map[string]helloInfo{}
	for rows.Next() {
		var sid string
		var h helloInfo
		if err := rows.Scan(&sid, &h.ModVersion, &h.Agent, &h.At); err != nil {
			return nil, fmt.Errorf("load mod hello: %w", err)
		}
		out[sid] = h
	}
	return out, rows.Err()
}
