package teammod

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/wake/purdex/internal/team"
)

// L's commands outbox, module side (plan X3a): the capability check before a command is queued (§3.1 rule 7), the 10
// minute void (§3.3), the unpairing clean-up (§3.2) and the wiring of the pump. The row state machine the answers drive
// is X3b-1's; here a voided adopt and an unpaired host only close what is live.

// CapError is a command refused before it was queued.
type CapError struct{ Code, Detail string }

func (e *CapError) Error() string { return e.Code + ": " + e.Detail }

// errHostNotAllowed is 409 host_not_allowed (M's allow_team is off for us).
const errHostNotAllowed = "host_not_allowed"

// checkRemoteKind asks the member host what it supports and refuses a kind it does not announce, or a host that has not
// allowed us (rule 7): to the lead, at once, before any approval, spawn accept or enqueue. A host that cannot be asked is
// refused as unreachable (nothing is queued for a kind we could not prove supported).
func (m *Module) checkRemoteKind(ctx context.Context, hostID, kind string) error {
	if m.cmdCaller == nil {
		return &CapError{Code: team.ErrRemoteUnsupported, Detail: "cross-host team is not available on this daemon"}
	}
	caps, err := m.cmdCaller.TeamCaps(ctx, hostID)
	if err != nil {
		return &CapError{Code: "remote_unreachable", Detail: err.Error()}
	}
	if !slices.Contains(caps.Kinds, kind) {
		return &CapError{Code: team.ErrRemoteUnsupported, Detail: "that host does not support " + kind}
	}
	if !caps.AllowTeam {
		return &CapError{Code: errHostNotAllowed, Detail: "that host has not allowed this host to use its sessions"}
	}
	return nil
}

// kickCommands asks the pump for a pass: called after the transaction that enqueued a command committed.
func (m *Module) kickCommands() {
	if m.cmdPump != nil {
		m.cmdPump.kick()
	}
}

// startCommandPump wires the commands outbox to the host caller and starts the pump (Init).
func (m *Module) startCommandPump() {
	if m.cmdCaller == nil {
		return
	}
	if m.outcomes == nil {
		m.outcomes = noOutcomes{}
	}
	out := &commandOutbox{s: m.store, out: m.outcomes, now: m.now, unpair: m.unpairHost, onChange: m.rosterChanged}
	m.cmdPump = newOutboxPump("commands", m.cmdCaller, out, m.now, m.logf, m.stopCtx, &m.sweepWG)
	m.sweepWG.Add(1)
	go m.cmdPump.run()
}

// liveRemoteStates are the row states of a remote member that is still in play on L (§4.2).
const liveRemoteStates = `('joining', 'active', 'releasing', 'killing')`

// unpairHost ends every team relation with hostID on L's side (§3.2): its live remote rows go `gone{reason}` (terminal
// rows are not rewritten), its pending commands are dropped. One transaction. Pending forwarded spawns of that host
// fail `unpaired` too — X4b adds that table and joins this transaction.
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
		WHERE host_id = ? AND host_id <> ? AND state IN `+liveRemoteStates, reason, now, now, hostID, m.hostID())
	if err != nil {
		return err
	}
	gone, _ := r.RowsAffected()
	if err := tx.Commit(); err != nil {
		return err
	}
	if dropped+gone > 0 {
		m.logf("[team] host %s %s: %d command(s) dropped, %d remote member(s) gone", hostID, reason, dropped, gone)
		m.rosterChanged()
	}
	return nil
}

// scanUnpaired is the liveness tick's look at pairings: a host with live remote rows or pending commands that no live
// peer entry carries any more (the entry was removed, or its alias was re-created for another host) is unpaired.
func (m *Module) scanUnpaired() {
	if m.cmdCaller == nil || m.stopping() {
		return
	}
	rows, err := m.store.db.Query(`SELECT host_id FROM team_members WHERE host_id <> ? AND state IN `+liveRemoteStates+`
		UNION SELECT host_id FROM team_commands WHERE state = 'pending'`, m.hostID())
	if err != nil {
		m.logf("[team] unpaired scan: %v", err)
		return
	}
	var hosts []string
	for rows.Next() {
		var h string
		if rows.Scan(&h) == nil {
			hosts = append(hosts, h)
		}
	}
	rows.Close()
	for _, h := range hosts {
		if !m.cmdCaller.Paired(h) {
			if err := m.unpairHost(h, "unpaired"); err != nil {
				m.logf("[team] unpaired scan %s: %v", h, err)
			}
		}
	}
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
		m.kickCommands()
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
			WHERE mk = ? AND host_id = ? AND state = 'joining'`, now, now, c.MK, c.HostID); err != nil {
			return err
		}
	}
	// A spawn's forwarded op fails `remote_unreachable` in the same transaction once X4b adds it.
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
