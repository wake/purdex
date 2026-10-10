package teammod

import (
	"fmt"
	"os"

	"github.com/wake/purdex/internal/team"
)

// The boot's reconciliation (issue #1968): every step of Module.Start that corrects data left by the last run, in the one order
// they are allowed to run in. The order is a contract between state machines — PU-1b3's "approved an expired row at boot" came
// from it — so it is written here once, with what each step reads and writes and what a failure of it does:
//
//	step              reads / writes                                            needs                         on failure
//	relay dir         the data dir (mkdir relay/)                               —                             log, go on
//	leases            approval_requests.lease_until (W)                         —                             FATAL: Start fails
//	roster baseline   the roster (R) → the last roster sent                     leases; BEFORE every write    log, go on
//	                  below, so each of them announces itself
//	relay             relay_ops, approval_requests (R/W), titles, frames,       leases (no row is overdue)    log, go on
//	                  member control messages
//	kill recover      team_members killing → active (W), the registry (R)       —                             log, go on
//	spawn resume      spawn_ops (R/W), tmux sessions (R/W), starts runners      —                             log, go on
//	unattended        approval_requests (R/W) through autoApprove, which opens  leases (an overdue row is     log, go on
//	                  ops and may enqueue commands                              left to the sweeper), relay
//	                                                                            and spawn reconciled first
//
// What Start does after it, in its own order: the event subscriptions and the background goroutines, then expireCommands (a
// spawn / adopt / relay command past its 10 minutes is voided BEFORE the pumps run, #2265), then the pumps.

// bootStep is one step of the reconciliation. fatal: its error stops Start; every other step's problems are logged by the step
// itself and the boot goes on (a store that cannot be read now is read again by the sweeper).
type bootStep struct {
	name  string
	fatal bool
	run   func() error
}

// bootSteps are the steps, in order.
func (m *Module) bootSteps() []bootStep {
	return []bootStep{
		{"relay dir", false, func() error {
			// <data_dir>/relay/ exists from boot (spec §8.3); begin re-creates it too. A failure is logged, not fatal: begin
			// reports its own.
			if err := os.MkdirAll(m.relayDir, 0o700); err != nil {
				m.logf("[team] relay dir %s: %v", m.relayDir, err)
			}
			return nil
		}},
		{"leases", true, func() error {
			n, err := m.store.ExtendOpenLeases(m.now() + team.BootGraceS*1000)
			if err != nil {
				return fmt.Errorf("team: %w", err)
			}
			if n > 0 {
				m.logf("[team] boot: extended the lease of %d open approval request(s) by %ds", n, team.BootGraceS)
			}
			return nil
		}},
		{"roster baseline", false, func() error {
			m.rosterBaseline() // before the boot's own writes: each of them announces itself
			return nil
		}},
		{"relay", false, func() error { m.reconcileRelays(); return nil }},
		{"kill recover", false, func() error {
			m.recoverKillingMembers() // a kill that died between its claim and its end (#2152)
			return nil
		}},
		{"spawn resume", false, func() error { m.resumeSpawns(); return nil }},
		{"unattended", false, func() error {
			// U23 rule 7: requests left open across a restart while the switch is on are approved now, not at the first
			// tick; createMu as every reader of the switch.
			m.createMu.Lock()
			defer m.createMu.Unlock()
			if m.unattendedOn() {
				m.sweepUnattended("boot")
			}
			return nil
		}},
	}
}

// bootReconcile runs the steps in order. A fatal step's error is returned at once (the steps after it do not run).
func (m *Module) bootReconcile() error {
	for _, s := range m.bootSteps() {
		if m.bootTrace != nil {
			m.bootTrace(s.name)
		}
		if err := s.run(); err != nil && s.fatal {
			return err
		}
	}
	return nil
}
