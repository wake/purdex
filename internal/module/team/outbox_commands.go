package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	peersmod "github.com/wake/purdex/internal/module/peers"
)

// The commands table as the pump's outboxStore (cross-host team spec X3a-2). The table, enqueue, settle, void and unpair
// are outbox_store.go / outbox_ops.go (X3a-1); this is the thin adapter the generic pump drives.

// commandOutbox is the commands table as the pump's outboxStore.
type commandOutbox struct {
	s        *Store
	out      commandOutcomes
	now      func() int64
	unpair   func(hostID, reason string) error
	onChange func() // a roster-visible change (a row left `active`)
}

var _ outboxStore = (*commandOutbox)(nil)

func (o *commandOutbox) Hosts() ([]string, error) {
	rows, err := o.s.db.Query(`SELECT DISTINCT host_id FROM team_commands WHERE state = 'pending'`)
	if err != nil {
		return nil, fmt.Errorf("outbox hosts: %w", err)
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

func (o *commandOutbox) Head(hostID string) (outboxEntry, int64, bool, error) {
	c, err := scanCommand(o.s.db.QueryRow(`SELECT `+commandCols+` FROM team_commands WHERE host_id = ? AND state = 'pending' ORDER BY rowid LIMIT 1`, hostID))
	if errors.Is(err, sql.ErrNoRows) {
		return outboxEntry{}, 0, false, nil
	}
	if err != nil {
		return outboxEntry{}, 0, false, fmt.Errorf("outbox head %s: %w", hostID, err)
	}
	// An adopt / spawn past its 10 minutes is never sent: the member host has no age check of its own (and may have pruned
	// its record of the id, #2265). It waits here, and blocks the host's queue, until the expiry sweep voids it.
	if (c.Kind == CmdAdopt || c.Kind == CmdSpawn) && c.CreatedAt <= o.now()-commandExpiryMS {
		return outboxEntry{}, 0, false, nil
	}
	return outboxEntry{ID: c.ID, HostID: c.HostID, Path: commandsPath, Body: c.Body, Attempts: c.Attempts, First401At: c.First401At}, c.NextAt, true, nil
}

func (o *commandOutbox) Attempted(id string, nextAt, first401At int64) error {
	_, err := o.s.db.Exec(`UPDATE team_commands SET attempts = attempts + 1, next_at = ?, first_401_at = ?, updated_at = ? WHERE id = ? AND state = 'pending'`,
		nextAt, first401At, o.now(), id)
	return err
}

// Settle is the pump's rule 4: the store marks the command done and applies the answer in one transaction.
func (o *commandOutbox) Settle(e outboxEntry, res peersmod.CallResult) error {
	settled, err := o.s.SettleCommand(e.ID, res, o.now(), o.out)
	if err == nil && settled && o.onChange != nil {
		o.onChange()
	}
	return err
}

func (o *commandOutbox) Unpaired(hostID, reason string) error { return o.unpair(hostID, reason) }
