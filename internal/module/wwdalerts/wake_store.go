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
// restart does not wake for it again. woken_ms is 0 for an episode that was only recorded (the first deployment's standing alerts, a
// throttled one, an adopted one).
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
	return err
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
// adoptSince is not zero, not when another row of the same key was first seen no earlier than adoptSince (the same shared failure,
// after a restart of this daemon).
func (s *store) claimWake(e wakeEp, now time.Time, throttle time.Duration, adoptSince int64) (wakeVerdict, error) {
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
	if adoptSince > 0 {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM wake_ledger WHERE host = ? AND key = ? AND since_ms <> ? AND first_ms >= ?`,
			e.host, e.key, e.since.UnixMilli(), adoptSince).Scan(&n); err != nil {
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
		if _, err := tx.Exec(`UPDATE wake_ledger SET woken_ms = ? WHERE host = ? AND key = ? AND since_ms = ?`, now.UnixMilli(), e.host, e.key, e.since.UnixMilli()); err != nil {
			return wakeSeen, err
		}
	}
	return verdict, tx.Commit()
}

// pruneWake drops the wake ledger rows of alerts not seen holding for wakeKeep.
func (s *store) pruneWake(now time.Time) error {
	_, err := s.db.Exec(`DELETE FROM wake_ledger WHERE last_seen_ms < ?`, now.Add(-wakeKeep).UnixMilli())
	return err
}
