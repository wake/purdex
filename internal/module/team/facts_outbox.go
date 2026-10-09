// internal/module/team/facts_outbox.go
package teammod

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"

	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
)

// M's facts outbox as the generic outbox pump's store (cross-host team spec §3.1 rules 1, 4, 6; plan X2c-2). The
// table and the facts' causes are facts_store.go (X2c-1a); the pump, its backoff and the 401 rule are outbox_pump.go
// (X3a-2) — this is the thin adapter, the mirror of outbox_commands.go.

// factsPath is the lead host's facts route (X3b-2). Until that host serves it, the 404 blocks M's queue and is
// retried with the pump's backoff: expected, and nothing needs L deployed first.
const factsPath = "/api/peers/team/facts"

// factOutbox is team_facts as the pump's outboxStore.
type factOutbox struct {
	s      *Store
	now    func() int64
	unpair func(hostID, reason string) error
	logf   func(string, ...any)
}

var _ outboxStore = (*factOutbox)(nil)

// newFactOutbox is the facts table wired to this module.
func (m *Module) newFactOutbox() *factOutbox {
	return &factOutbox{s: m.store, now: m.now, unpair: m.unpairLeadHost, logf: m.logf}
}

func (o *factOutbox) Hosts() ([]string, error) {
	rows, err := o.s.db.Query(`SELECT DISTINCT host_id FROM team_facts WHERE state = ?`, factPending)
	if err != nil {
		return nil, fmt.Errorf("facts outbox hosts: %w", err)
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

// Head is the host's oldest pending fact; the body names to_host_id, which HostCaller checks against the host.
func (o *factOutbox) Head(hostID string) (outboxEntry, int64, bool, error) {
	var e outboxEntry
	var body string
	var nextAt int64
	err := o.s.db.QueryRow(`SELECT id, host_id, kind, body_json, attempts, first_401_at, next_at FROM team_facts
		WHERE host_id = ? AND state = ? ORDER BY rowid LIMIT 1`, hostID, factPending).
		Scan(&e.ID, &e.HostID, &e.Kind, &body, &e.Attempts, &e.First401At, &nextAt)
	if errors.Is(err, sql.ErrNoRows) {
		return outboxEntry{}, 0, false, nil
	}
	if err != nil {
		return outboxEntry{}, 0, false, fmt.Errorf("facts outbox head %s: %w", hostID, err)
	}
	e.Path, e.Body = factsPath, []byte(body)
	return e, nextAt, true, nil
}

// Announces is the pump's kind gate (kindGate): a fact is sent only when the lead host lists its kind in fact_kinds. A host
// that lists none (a daemon without the facts route) gets nothing, as before it would have answered 404.
func (o *factOutbox) Announces(caps ipeers.TeamCaps, kind string) bool {
	return slices.Contains(caps.FactKinds, kind)
}

func (o *factOutbox) Attempted(id string, nextAt, first401At int64) error {
	_, err := o.s.db.Exec(`UPDATE team_facts SET attempts = attempts + 1, next_at = ?, first_401_at = ?, updated_at = ? WHERE id = ? AND state = ?`,
		nextAt, first401At, o.now(), id, factPending)
	return err
}

// Settle ends the fact: a 2xx answer is done; the lead host's permanent refusal (a JSON 4xx, a wrong_host) means the
// fact will never be taken, so it is recorded in the log and dropped — the queue behind it goes on. There is nothing
// to apply on this side: an `ended` fact only informs the lead host. One statement, a CAS on pending.
func (o *factOutbox) Settle(e outboxEntry, res peersmod.CallResult) error {
	state := factDone
	if res.Class != peersmod.ClassDone {
		state = factDropped
		o.logf("[team] fact %s for host %s refused by the lead host (%s %s); dropped", e.ID, e.HostID, res.Class, res.Code)
	}
	_, err := o.s.db.Exec(`UPDATE team_facts SET state = ?, updated_at = ? WHERE id = ? AND state = ?`, state, o.now(), e.ID, factPending)
	return err
}

func (o *factOutbox) Unpaired(hostID, reason string) error { return o.unpair(hostID, reason) }

// unpairLeadHost is spec §3.2 on the member host for one lead host, called when the pump finds it unpaired (no live
// entry carries its id) or unpaired_by_peer (its 401 lasted 10 minutes): its live members end locally, its queued
// facts are dropped, nobody is told.
//
// "unpaired" is a verdict the pump reached a moment ago, and the cleanup is irreversible: it is made with the config
// read-locked until it commits, and only while no live entry carries the host id (a host added or re-verified since
// keeps its members). "unpaired_by_peer" is the peer's own refusal of our token for 10 minutes; nothing in the config
// can contradict it.
func (m *Module) unpairLeadHost(hostID, reason string) error {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	if reason == "unpaired" {
		for _, h := range m.core.Cfg.Peers.Hosts {
			if h.HostID == hostID {
				m.logf("[team] lead host %s is paired again; its remote members and facts are kept", hostID)
				return nil
			}
		}
	}
	n, err := m.store.EndRemoteMembersOfHost(hostID, m.now())
	if err != nil {
		return err
	}
	m.logf("[team] lead host %s %s: %d remote member(s) ended, its queued facts dropped", hostID, reason, n)
	return nil
}

// kickFacts asks the facts pump for a pass: called after the transaction that queued a fact committed.
func (m *Module) kickFacts() {
	if m.factPump != nil {
		m.factPump.kick()
	}
}

// startFactPump wires the facts outbox to the host caller and starts the pump (Init), like the commands pump.
func (m *Module) startFactPump() {
	if m.cmdCaller == nil {
		return
	}
	m.factPump = newOutboxPump("facts", m.cmdCaller, m.newFactOutbox(), m.now, m.logf, m.stopCtx, &m.sweepWG)
	m.sweepWG.Add(1)
	go m.factPump.run()
}
