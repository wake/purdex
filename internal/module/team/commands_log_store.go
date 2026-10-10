// internal/module/team/commands_log_store.go
package teammod

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// bodyHash is the content hash of a command: its bytes as the sender wrote them. The sender resends the very
// bytes it stored, so a replay hashes the same; and a field this version does not know still counts (a
// version-skewed sender cannot slip a different command in under an old id as a "replay").
func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// PruneCommandLog deletes at most batch decided-command records older than before (unix ms) and reports how many; the
// caller repeats until it reports fewer than batch. team_command_voids is never touched (the lead host's late void is
// answered from it). The receiver has no age check of its own for adopt / spawn — only the lead host settles or voids them
// within 10 minutes — so the record of an adopt whose member row is still active stays however old it is: a late void
// finds it and undoes the adoption. Every other record (a refusal, an adopt whose member ended, spawn, release, end,
// lead_moved, team.appearance, void) is replayed as an idempotent no-op or never resent, and goes after the retention
// (#2265). lead_moved and team.appearance are not monotonic, but the lead host sends them strictly in order per host (its
// outbox is head-of-line FIFO, and an older one settles before a newer one is sent), so a replay of an old one can only
// land before the newer one and is overwritten by it. An overdue adopt / spawn is voided at the lead host's boot before
// its pump starts (Start), so the lead host cannot resend one after its record is gone.
func (s *Store) PruneCommandLog(before int64, batch int) (int, error) {
	res, err := s.db.Exec(`DELETE FROM team_command_log WHERE rowid IN (
			SELECT l.rowid FROM team_command_log l
			WHERE l.at < ?
			  AND NOT (l.kind = 'adopt' AND EXISTS (
				SELECT 1 FROM remote_members r WHERE r.mk = l.id AND r.lead_host_id = l.lead_host_id AND r.state = 'active'))
			ORDER BY l.at LIMIT ?)`, before, batch)
	if err != nil {
		return 0, fmt.Errorf("prune command log: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
