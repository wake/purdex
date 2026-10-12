// internal/module/team/pause_lead_notice.go — telling the remote members of a paused lead (unattended quota pause spec 2026-10-12
// §3.3 "Remote members", UP-3c). A member on another host is not paused (its deliveries are judged on its own host); it is told.
// The lead host queues the team-level command team.lead_paused / team.lead_resumed, in the transaction that pauses / resumes the
// lead, once per member host that has an active remote member of the team. The member host applies it by owing each of its active
// remote members of that team a notice (remote_notices), which its notice pump delivers in the fixed texts.
package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// LeadNoticer queues kind (CmdLeadPaused / CmdLeadResumed) for the member hosts of team teamID, on tx. Set by the module at Init;
// nil in a bare store, where a lead's pause tells nobody.
type LeadNoticer func(tx *sql.Tx, teamID, kind string, at int64) error

// noticeRemoteMembersIn queues the team-level command for the team's remote members on tx, in the pause / resume transaction.
func (s *Store) noticeRemoteMembersIn(tx *sql.Tx, teamID, kind string, at int64) error {
	if s.leadNotice == nil || teamID == "" {
		return nil
	}
	if err := s.leadNotice(tx, teamID, kind, at); err != nil {
		return fmt.Errorf("tell the remote members of team %s (%s): %w", teamID, kind, err)
	}
	if s.afterLeadNotice != nil { // test seam: a failure after the commands were queued, before the commit
		return s.afterLeadNotice()
	}
	return nil
}

// activeRemoteHostsIn are the member hosts with an ACTIVE remote member of the live team teamID: not joining, releasing, killing
// or gone ones (they are leaving or not yet there), and not this host.
func (s *Store) activeRemoteHostsIn(tx *sql.Tx, teamID string) ([]string, error) {
	if s.localHostID == "" {
		return nil, nil
	}
	rows, err := tx.Query(`SELECT DISTINCT m.host_id FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.team_id = ? AND t.ended_at = 0 AND m.state = 'active' AND m.host_id <> '' AND m.host_id <> ? ORDER BY m.host_id`, teamID, s.localHostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hosts []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

// queueLeadNotice is the module's LeadNoticer: one team-level command per member host with an active remote member of the team.
// A team that has ended, or has no such host, queues nothing.
func (m *Module) queueLeadNotice(tx *sql.Tx, teamID, kind string, at int64) error {
	hosts, err := m.store.activeRemoteHostsIn(tx, teamID)
	if err != nil || len(hosts) == 0 {
		return err
	}
	t, err := scanTeam(tx.QueryRow(`SELECT `+teamCols+` FROM teams WHERE id = ? AND ended_at = 0`, teamID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	lead := m.leadTupleIn(tx, t)
	for _, h := range hosts {
		cmd, err := remoteCommand(m.newID(), kind, h, t, "", lead, nil)
		if err != nil {
			return err
		}
		if err := m.store.EnqueueCommand(tx, cmd, at); err != nil {
			return err
		}
	}
	return nil
}

// noticeKindOf maps a command kind to the notice kind its member host owes.
func noticeKindOf(commandKind string) (string, bool) {
	switch commandKind {
	case team.CommandLeadPaused:
		return noticeLeadPaused, true
	case team.CommandLeadResumed:
		return noticeLeadResumed, true
	}
	return "", false
}

// applyLeadNoticeIn is `team.lead_paused` / `team.lead_resumed` on the MEMBER host: every active remote member of that (lead host,
// team) is owed the notice (one per member and command, so a replay owes nothing more). A resume that finds the pause notice still
// unsent supersedes it and owes nothing: the member never heard of the pause. No active member is not an error: the command is
// already true.
func applyLeadNoticeIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.cmd
	kind, _ := noticeKindOf(c.Kind)
	rows, err := tx.Query(`SELECT mk, lead_address, team_name FROM remote_members WHERE lead_host_id = ? AND team_id = ? AND state = ? ORDER BY mk`,
		p.LeadHostID, c.TeamID, remoteActive)
	if err != nil {
		return CommandResult{}, err
	}
	type target struct{ mk, lead, name string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.mk, &t.lead, &t.name); err != nil {
			rows.Close()
			return CommandResult{}, err
		}
		targets = append(targets, t)
	}
	if err := rows.Close(); err != nil {
		return CommandResult{}, err
	}
	owed := 0
	for _, t := range targets {
		if kind == noticeLeadResumed {
			res, err := tx.Exec(`UPDATE remote_notices SET state = ?, updated_at = ? WHERE mk = ? AND kind = ? AND state = ?`,
				noticeSuperseded, p.Now, t.mk, noticeLeadPaused, noticeOwed)
			if err != nil {
				return CommandResult{}, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				continue
			}
			// Only a member that was told of the pause (a lead_paused sent since its last resume) is told of the resume: one that
			// joined in between never heard of the pause (codex R1).
			var told int
			err = tx.QueryRow(`SELECT 1 FROM remote_notices p WHERE p.mk = ? AND p.kind = ? AND p.state = ?
				AND p.id > COALESCE((SELECT MAX(r.id) FROM remote_notices r WHERE r.mk = p.mk AND r.kind = ?), 0) LIMIT 1`,
				t.mk, noticeLeadPaused, noticeSent, noticeLeadResumed).Scan(&told)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return CommandResult{}, err
			}
		}
		lead := t.lead
		if lead == "" {
			lead = c.Lead.Address
		}
		if err := oweNoticeIn(tx, t.mk, kind, c.ID, lead, t.name, p.Now); err != nil {
			return CommandResult{}, err
		}
		owed++
	}
	return okResult(map[string]any{"state": "ok", "affected": owed})
}
