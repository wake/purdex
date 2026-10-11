// internal/module/team/roots_change_store.go
package teammod

import "github.com/wake/purdex/internal/team"

// LeadHostUse is one thing a lead host has running on this host: Ref is the member's ref (an active remote member) or the
// op id (a forwarded spawn that is still running), Cwd where it runs.
type LeadHostUse struct {
	Ref, Cwd string
}

// LeadHostCwds lists what the lead host has running here: its active remote members and its running forwarded spawns
// (spawn_ops with a lead_host_id of its own; a local spawn has none). Read-only; the roots-change pre-check uses it.
func (s *Store) LeadHostCwds(leadHostID string) ([]LeadHostUse, error) {
	var out []LeadHostUse
	for _, q := range []struct{ sql, state string }{
		{`SELECT ref, cwd FROM remote_members WHERE lead_host_id = ? AND state = ?`, remoteActive},
		{`SELECT id, cwd FROM spawn_ops WHERE lead_host_id = ? AND state = ?`, string(team.SpawnRunning)},
	} {
		rows, err := s.db.Query(q.sql, leadHostID, q.state)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var u LeadHostUse
			if err := rows.Scan(&u.Ref, &u.Cwd); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out = append(out, u)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}
