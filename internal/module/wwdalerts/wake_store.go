package wwdalerts

import (
	"database/sql"
	"time"
)

// wakeKeep is how long a wake ledger row is kept after the alert was last seen holding.
const wakeKeep = 14 * 24 * time.Hour

// wakeVerdict is what claimWake decided for an alert episode.
type wakeVerdict int

const (
	wakeSeen      wakeVerdict = iota // the episode is already in the ledger: nothing to do
	wakeNow                          // new, and due: the caller wakes the watcher
	wakeThrottled                    // new, but the same (host, key) woke the watcher a moment ago: recorded, never made up later
	wakeAdopted                      // new to this run, but a row of the same shared key says it is the failure already seen before a restart
)

// migrateWake creates the wake ledger: one row per alert episode (host, key, since_ms), written before the watcher is asked, so a
// restart does not wake for it again. woken_ms is when the watcher was told (the call succeeded), 0 for an episode that is pending or was only
// recorded (the first deployment's standing alerts, a throttled one, an adopted one).
func migrateWake(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS wake_ledger (
			host         TEXT    NOT NULL,
			key          TEXT    NOT NULL,
			since_ms     INTEGER NOT NULL,
			first_ms     INTEGER NOT NULL,
			woken_ms     INTEGER NOT NULL DEFAULT 0,
			last_seen_ms INTEGER NOT NULL,
			PRIMARY KEY (host, key, since_ms)
		);
		CREATE TABLE IF NOT EXISTS wake_hosts (
			host TEXT PRIMARY KEY
		);`)
	if err != nil {
		return err
	}
	// pending: the watcher has not been told yet (a new episode, or a failed call to be retried); attempts counts the failed calls and
	// next_ms is when the next try is due, on this daemon's clock.
	for _, c := range [][2]string{{"pending", "INTEGER NOT NULL DEFAULT 0"}, {"attempts", "INTEGER NOT NULL DEFAULT 0"}, {"next_ms", "INTEGER NOT NULL DEFAULT 0"}} {
		if err := addColumn(db, "wake_ledger", c[0], c[1]); err != nil {
			return err
		}
	}
	return nil
}

// wakeEp is the identity of an alert episode in the wake ledger.
type wakeEp struct {
	host, key string
	since     time.Time
}

// hostSeeded says whether the host's standing alerts were recorded already (the first look at a host after the ledger exists).
func (s *store) hostSeeded(host string) bool {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM wake_hosts WHERE host = ?`, host).Scan(&n); err != nil {
		return true // unknown: do not wake for what cannot be told from new
	}
	return n > 0
}

// seedWake records the episodes standing at the first look at a host as already told, and marks the host, in one transaction.
func (s *store) seedWake(host string, eps []wakeEp, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range eps {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO wake_ledger (host, key, since_ms, first_ms, woken_ms, last_seen_ms) VALUES (?, ?, ?, ?, 0, ?)`,
			e.host, e.key, e.since.UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO wake_hosts (host) VALUES (?)`, host); err != nil {
		return err
	}
	return tx.Commit()
}

// claimWake writes the episode before anyone is asked, atomically. A row that exists: seen. Otherwise the row is created and the verdict
// says whether to wake: not when the same (host, key) woke the watcher within throttle (a later wake is not made up), and, when
// adoptWithin is not zero, not when another row of the same key was last seen holding within adoptWithin (the same shared failure,
// after a restart of this daemon). Only this daemon's clock is used: the alert's Since is another host's.
func (s *store) claimWake(e wakeEp, now time.Time, throttle time.Duration, adoptWithin time.Duration) (wakeVerdict, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return wakeSeen, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT OR IGNORE INTO wake_ledger (host, key, since_ms, first_ms, woken_ms, last_seen_ms) VALUES (?, ?, ?, ?, 0, ?)`,
		e.host, e.key, e.since.UnixMilli(), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return wakeSeen, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := tx.Exec(`UPDATE wake_ledger SET last_seen_ms = ? WHERE host = ? AND key = ? AND since_ms = ?`, now.UnixMilli(), e.host, e.key, e.since.UnixMilli()); err != nil {
			return wakeSeen, err
		}
		return wakeSeen, tx.Commit()
	}
	verdict := wakeNow
	if adoptWithin > 0 {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM wake_ledger WHERE host = ? AND key = ? AND since_ms <> ? AND last_seen_ms >= ?`,
			e.host, e.key, e.since.UnixMilli(), now.Add(-adoptWithin).UnixMilli()).Scan(&n); err != nil {
			return wakeSeen, err
		}
		if n > 0 {
			verdict = wakeAdopted
		}
	}
	if verdict == wakeNow {
		var last sql.NullInt64
		if err := tx.QueryRow(`SELECT max(woken_ms) FROM wake_ledger WHERE host = ? AND key = ? AND woken_ms > 0`, e.host, e.key).Scan(&last); err != nil {
			return wakeSeen, err
		}
		if last.Valid && within(now, last.Int64, throttle) {
			verdict = wakeThrottled
		}
	}
	if verdict == wakeNow {
		if _, err := tx.Exec(`UPDATE wake_ledger SET pending = 1, next_ms = ? WHERE host = ? AND key = ? AND since_ms = ?`, now.UnixMilli(), e.host, e.key, e.since.UnixMilli()); err != nil {
			return wakeSeen, err
		}
	}
	return verdict, tx.Commit()
}

// pendingDue says whether the episode's watcher call is still to be made and its time has come.
func (s *store) pendingDue(e wakeEp, now time.Time) bool {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM wake_ledger WHERE host = ? AND key = ? AND since_ms = ? AND pending = 1 AND next_ms <= ?`,
		e.host, e.key, e.since.UnixMilli(), now.UnixMilli()).Scan(&n)
	return err == nil && n > 0
}

// pendingOf lists the host's episodes whose call is still to be made.
func (s *store) pendingOf(host string) ([]wakeEp, error) {
	rows, err := s.db.Query(`SELECT key, since_ms FROM wake_ledger WHERE host = ? AND pending = 1`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wakeEp
	for rows.Next() {
		var k string
		var ms int64
		if err := rows.Scan(&k, &ms); err != nil {
			return nil, err
		}
		out = append(out, wakeEp{host, k, time.UnixMilli(ms)})
	}
	return out, rows.Err()
}

// dropPending gives up on an episode that is no longer standing.
func (s *store) dropPending(e wakeEp) error {
	_, err := s.db.Exec(`UPDATE wake_ledger SET pending = 0 WHERE host = ? AND key = ? AND since_ms = ?`, e.host, e.key, e.since.UnixMilli())
	return err
}

// markWoken records that the watcher call succeeded for these episodes (the ten minutes count from here).
func (s *store) markWoken(eps []wakeEp, now time.Time) error {
	for _, e := range eps {
		if _, err := s.db.Exec(`UPDATE wake_ledger SET pending = 0, woken_ms = ?, attempts = 0 WHERE host = ? AND key = ? AND since_ms = ?`,
			now.UnixMilli(), e.host, e.key, e.since.UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

// markWakeFailed records a failed call for these episodes: they stay pending, next due after the backoff of their failure count.
func (s *store) markWakeFailed(eps []wakeEp, now time.Time) error {
	for _, e := range eps {
		var n int
		if err := s.db.QueryRow(`SELECT attempts FROM wake_ledger WHERE host = ? AND key = ? AND since_ms = ?`, e.host, e.key, e.since.UnixMilli()).Scan(&n); err != nil {
			return err
		}
		n++
		if _, err := s.db.Exec(`UPDATE wake_ledger SET attempts = ?, next_ms = ? WHERE host = ? AND key = ? AND since_ms = ?`,
			n, now.Add(wakeBackoff(n)).UnixMilli(), e.host, e.key, e.since.UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

// pruneWake drops the wake ledger rows of alerts not seen holding for wakeKeep.
func (s *store) pruneWake(now time.Time) error {
	_, err := s.db.Exec(`DELETE FROM wake_ledger WHERE last_seen_ms < ?`, now.Add(-wakeKeep).UnixMilli())
	return err
}
