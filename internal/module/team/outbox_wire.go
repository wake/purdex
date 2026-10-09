package teammod

import (
	"context"
	"slices"

	"github.com/wake/purdex/internal/team"
)

// L's commands outbox, wiring (plan X3a-2): the capability check before a command is queued (§3.1 rule 7), the start of
// the pump and the liveness tick's look at pairings (§3.2).

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
		m.outcomes = remoteOutcomes{m: m}
	}
	out := &commandOutbox{s: m.store, out: m.outcomes, now: m.now, unpair: m.unpairHost, onChange: m.rosterChanged}
	m.cmdPump = newOutboxPump("commands", m.cmdCaller, out, m.now, m.logf, m.stopCtx, &m.sweepWG)
	m.sweepWG.Add(1)
	go m.cmdPump.run()
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
