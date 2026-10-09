package teammod

import (
	"encoding/json"
	"fmt"
)

// L's commands outbox, module side (plan X3a-1): the 10 minute void (§3.3) and the unpairing clean-up (§3.2); the pump,
// the capability check (§3.1 rule 7) and the pairing scan are X3a-2. The row state machine the answers drive
// is X3b-1's; here a voided adopt and an unpaired host only close what is live.

// liveRemoteStates are the row states of a remote member that is still in play on L (§4.2).
const liveRemoteStates = `('joining', 'active', 'releasing', 'killing')`

// unpairHost ends every team relation with hostID on L's side (§3.2): its live remote rows go `gone{reason}` (terminal
// rows are not rewritten), its pending commands are dropped, and its running forwarded spawns fail with the same reason.
// One transaction.
func (m *Module) unpairHost(hostID, reason string) error {
	now := m.now()
	tx, err := m.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`UPDATE team_commands SET state = 'dropped', updated_at = ? WHERE host_id = ? AND state = 'pending'`, now, hostID)
	if err != nil {
		return err
	}
	dropped, _ := r.RowsAffected()
	r, err = tx.Exec(`UPDATE team_members SET state = 'gone', end_reason = ?, updated_at = ?, ended_at = CASE WHEN ended_at = 0 THEN ? ELSE ended_at END
		WHERE host_id = ? AND host_id <> ? AND state IN `+liveRemoteStates+` AND `+liveTeamOfRow, reason, now, now, hostID, m.hostID())
	if err != nil {
		return err
	}
	gone, _ := r.RowsAffected()
	spawns, err := failRemoteSpawnsOfHostIn(tx, hostID, reason, now)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if dropped+gone+spawns > 0 {
		m.logf("[team] host %s %s: %d command(s) dropped, %d remote member(s) gone", hostID, reason, dropped, gone)
		m.rosterChanged()
	}
	return nil
}

// expireCommands voids the spawn and adopt commands not done within 10 minutes (X-U8, §3.3): the command stops being
// sent, the seat is freed (the joining row fails `remote_unreachable`), and a `void {command_id}` is queued behind
// everything else for the host — it never expires, so a command that M applied after all is undone when it is back.
// Release, kill, end and lead_moved queue until delivered.
func (m *Module) expireCommands() {
	if m.stopping() {
		return
	}
	now := m.now()
	rows, err := m.store.db.Query(`SELECT `+commandCols+` FROM team_commands WHERE state = 'pending' AND kind IN ('adopt', 'spawn') AND created_at <= ?`, now-commandExpiryMS)
	if err != nil {
		m.logf("[team] expire commands: %v", err)
		return
	}
	var due []commandRow
	for rows.Next() {
		if c, err := scanCommand(rows); err == nil {
			due = append(due, c)
		}
	}
	rows.Close()
	for _, c := range due {
		if err := m.voidCommand(c, now); err != nil {
			m.logf("[team] void command %s: %v", c.ID, err)
		}
	}
	if len(due) > 0 {
		m.kickCommands() // the void commands go out at once
	}
}

// voidCommand is one void, in one transaction: the CAS pending → void, the local wrap-up and the void command.
func (m *Module) voidCommand(c commandRow, now int64) error {
	tx, err := m.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`UPDATE team_commands SET state = 'void', updated_at = ? WHERE id = ? AND state = 'pending'`, now, c.ID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return nil // done, dropped or voided meanwhile
	}
	if c.Kind == CmdAdopt {
		if _, err := tx.Exec(`UPDATE team_members SET state = 'failed', end_reason = 'remote_unreachable', updated_at = ?, ended_at = CASE WHEN ended_at = 0 THEN ? ELSE ended_at END
			WHERE mk = ? AND host_id = ? AND state = 'joining' AND `+liveTeamOfRow, now, now, c.MK, c.HostID); err != nil {
			return err
		}
	}
	if c.Kind == CmdSpawn { // the forwarded op fails in the same transaction: its seat is free
		if err := failRemoteSpawnIn(tx, c.MK, c.HostID, "remote_unreachable", now); err != nil {
			return err
		}
	}
	void, err := m.voidFor(c)
	if err != nil {
		return err
	}
	if err := m.store.EnqueueCommand(tx, void, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	m.rosterChanged()
	return nil
}

// voidFor builds the `void {command_id}` that follows c: the same team, member key, host and lead tuple as c.
func (m *Module) voidFor(c commandRow) (Command, error) {
	var orig map[string]json.RawMessage
	if err := json.Unmarshal(c.Body, &orig); err != nil {
		return Command{}, fmt.Errorf("void %s: %w", c.ID, err)
	}
	id := m.newID()
	body := map[string]any{"id": id, "kind": CmdVoid, "to_host_id": c.HostID, "team_id": c.TeamID, "mk": c.MK, "command_id": c.ID}
	for _, k := range []string{"team_name", "lead"} {
		if v, ok := orig[k]; ok {
			body[k] = v
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Command{}, err
	}
	return Command{ID: id, Kind: CmdVoid, TeamID: c.TeamID, MK: c.MK, HostID: c.HostID, Body: raw}, nil
}
