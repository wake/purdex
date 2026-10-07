package teammod

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/wake/purdex/internal/team"
)

// Retention of handoff files (spec §8.3 "Retention"): the daemon cleans,
// nobody else. Per lineage chain the newest 3 files stay; nothing older
// than 14 days; a failed or cancelled op's file 3 days. The row keeps the
// path and is marked pruned once the file is gone.
const (
	retentionInterval     = time.Hour
	retentionKeepPerChain = 3
	retentionMaxAge       = 14 * 24 * time.Hour
	retentionFailedAge    = 3 * 24 * time.Hour
)

// runRetention runs the sweep at boot and then hourly until Stop.
func (m *Module) runRetention() {
	defer m.sweepWG.Done()
	m.sweepRetention()
	ticker := time.NewTicker(retentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCtx.Done():
			return
		case <-ticker.C:
			m.sweepRetention()
		}
	}
}

// retentionVictims decides, from every unpruned row, which ops lose their
// file at now (unix ms). Pure, so the rule is tested without a filesystem:
//   - failed / cancelled: older than 3 d (by updated_at — when it ended),
//     and ONLY that rule: the 14 d rule below does not reach these two
//     states (an op that failed yesterday keeps its file for 3 days however
//     old the file is — the switch, not two ifs, is what makes that so);
//   - any other op: older than 14 d (by created_at);
//   - done: per chain (chainOf), all but the newest 3 by created_at.
//
// Active ops (awaiting_approval … cleared) are never victims except by the
// 14 d rule, which an op that long in flight has earned.
func retentionVictims(ops []team.RelayOp, chainOf func(team.RelayOp) string, now int64) []team.RelayOp {
	var out []team.RelayOp
	seen := map[string]bool{}
	take := func(op team.RelayOp) {
		if !seen[op.ID] {
			seen[op.ID] = true
			out = append(out, op)
		}
	}
	byChain := map[string][]team.RelayOp{}
	for _, op := range ops {
		if op.Pruned {
			continue
		}
		switch {
		case op.State == team.RelayFailed || op.State == team.RelayCancelled:
			if now-op.UpdatedAt >= retentionFailedAge.Milliseconds() {
				take(op)
			}
		case now-op.CreatedAt >= retentionMaxAge.Milliseconds():
			take(op)
		}
		if op.State == team.RelayDone {
			c := chainOf(op)
			byChain[c] = append(byChain[c], op)
		}
	}
	for _, chain := range byChain {
		if len(chain) <= retentionKeepPerChain {
			continue
		}
		sort.Slice(chain, func(i, j int) bool { return chain[i].CreatedAt > chain[j].CreatedAt })
		for _, op := range chain[retentionKeepPerChain:] {
			take(op)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// sweepRetention applies retentionVictims to the store and the relay dir.
// Only a path inside m.relayDir named <op id>.md is ever removed (spec §15:
// nothing outside <data_dir>/relay/ is touched); a row whose path is
// anything else is marked pruned without a removal, and logged.
func (m *Module) sweepRetention() {
	ops, err := m.store.ListUnprunedRelayOps()
	if err != nil {
		m.logf("[team] retention: %v", err)
		return
	}
	chains, err := m.store.ChainRoots()
	if err != nil {
		m.logf("[team] retention: chains: %v", err)
		return
	}
	chainOf := func(op team.RelayOp) string {
		if root, ok := chains[op.SessionID]; ok {
			return root
		}
		return op.SessionID
	}
	for _, op := range retentionVictims(ops, chainOf, m.now()) {
		if err := m.removeHandoff(op); err != nil {
			m.logf("[team] retention: op %s: %v", op.ID, err)
			continue
		}
		if err := m.store.MarkRelayPruned(op.ID); err != nil {
			m.logf("[team] retention: op %s: %v", op.ID, err)
		}
	}
}

// removeHandoff deletes op's handoff file when, and only when, it is
// <relayDir>/<op id>.md; a file already gone is fine.
func (m *Module) removeHandoff(op team.RelayOp) error {
	want := filepath.Join(m.relayDir, op.ID+".md")
	if filepath.Clean(op.HandoffPath) != want {
		m.logf("[team] retention: op %s: handoff path %q is not %q; marking pruned without removing anything", op.ID, op.HandoffPath, want)
		return nil
	}
	if err := os.Remove(want); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", want, err)
	}
	m.logf("[team] retention: removed %s (op %s, %s)", want, op.ID, op.State)
	return nil
}
