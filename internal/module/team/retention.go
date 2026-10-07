package teammod

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
//   - done: older than 14 d (by created_at), and per chain (chainOf) all
//     but the newest 3 by created_at.
//
// An op still in flight (awaiting_approval … cleared) is NEVER a victim,
// however old (PR #1733 attacker A-2): its handoff is the only copy of the
// conversation the relay is carrying — after a /clear (cleared) it is what
// seeds the new session. A stuck op must be ended by the state machine
// first, and its file then follows that state's rule. Until P6's frame
// reconciliation, an op stuck in claimed / writing / written because its
// process died keeps its file (issue #1735).
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
		switch op.State {
		case team.RelayFailed, team.RelayCancelled:
			if now-op.UpdatedAt >= retentionFailedAge.Milliseconds() {
				take(op)
			}
		case team.RelayDone:
			if now-op.CreatedAt >= retentionMaxAge.Milliseconds() {
				take(op)
			}
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
// <relayDir>/<op id>.md; a file already gone is fine. The bound is checked
// on the path, not trusted from the row (PR #1733 attacker A-1): the op id
// must be a single path element (no separator, not "." or ".."), the
// joined path's directory must be relayDir itself, relayDir must be a
// real directory and not a symlink, and the target is unlinked only if it
// is a regular file or a symlink (os.Remove unlinks a symlink, it does not
// follow it). Anything else is logged and the row marked pruned with
// nothing removed; a relayDir that is a symlink is an error (row left as
// is, retried next sweep once the directory is fixed).
func (m *Module) removeHandoff(op team.RelayOp) error {
	relayDir := filepath.Clean(m.relayDir)
	want := filepath.Join(relayDir, op.ID+".md")
	if op.ID == "" || op.ID == "." || op.ID == ".." || strings.ContainsAny(op.ID, `/\`) ||
		filepath.Dir(want) != relayDir || filepath.Clean(op.HandoffPath) != want {
		m.logf("[team] retention: op %s: handoff path %q is not <relay dir>/<op id>.md; marking pruned without removing anything", op.ID, op.HandoffPath)
		return nil
	}
	dirInfo, err := os.Lstat(relayDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no relay dir, no file
		}
		return fmt.Errorf("stat %s: %w", relayDir, err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("%s is not a directory (a symlink or a file); nothing removed", relayDir)
	}
	fi, err := os.Lstat(want)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", want, err)
	}
	if !fi.Mode().IsRegular() && fi.Mode()&os.ModeSymlink == 0 {
		m.logf("[team] retention: op %s: %s is not a regular file (%s); marking pruned without removing it", op.ID, want, fi.Mode().Type())
		return nil
	}
	if err := os.Remove(want); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", want, err)
	}
	m.logf("[team] retention: removed %s (op %s, %s)", want, op.ID, op.State)
	return nil
}
