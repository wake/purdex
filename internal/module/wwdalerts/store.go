package wwdalerts

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wake/purdex/internal/wwd/wire"
)

// digestKeep is how long a gathered digest row is kept after it was last seen: the daily digest (8a-4) reads yesterday, and a few
// days of slack cover a digest that was not read.
const digestKeep = 14 * 24 * time.Hour

// store is the deliverer's own database: its ledger of what it pushed, and the digest-level alerts it gathered. Both live here, not in
// wwd.db, because each deliverer keeps its own (spec §13.3): what mlab's daemon pushed says nothing about air26's.
type store struct{ db *sql.DB }

// delivery is one ledger row: the alert instance (its Since) it is about, when it was last put in the push module's hands, and when the
// push module said APNs took it. Zero Sent: never confirmed.
type delivery struct {
	Since, Attempt, Sent int64 // unix ms
}

func openStore(path string) (*store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open alert ledger: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS deliveries (
			host       TEXT    NOT NULL,
			key        TEXT    NOT NULL,
			since_ms   INTEGER NOT NULL,
			attempt_ms INTEGER NOT NULL DEFAULT 0,
			sent_ms    INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (host, key)
		);
		CREATE TABLE IF NOT EXISTS digest (
			host          TEXT    NOT NULL,
			key           TEXT    NOT NULL,
			since_ms      INTEGER NOT NULL,
			kind          TEXT    NOT NULL,
			detail        TEXT    NOT NULL DEFAULT '',
			first_seen_ms INTEGER NOT NULL,
			last_seen_ms  INTEGER NOT NULL,
			PRIMARY KEY (host, key, since_ms)
		);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate alert ledger: %w", err)
	}
	// host_down: of a silence: alert, whether the kind that was last put in the push module's hands said the host itself is gone (the words of
	// wire.SilenceDetailHostDown). A change of it inside one failure is pushed again, also across a restart of this daemon.
	if err := addColumn(db, "deliveries", "host_down", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate alert ledger: %w", err)
	}
	if err := migrateWake(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate wake ledger: %w", err)
	}
	return &store{db: db}, nil
}

// addColumn adds a column to a table that does not have it yet (a second start changes nothing).
func addColumn(db *sql.DB, table, column, decl string) error {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	have := false
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		if n == column {
			have = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if have {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + decl)
	return err
}

// setKind records the kind of the silence whose push the push module said APNs took (host and key as in the delivery row). A row that is
// about another instance by now is left alone, as markSent leaves it.
func (s *store) setKind(host, key string, since time.Time, hostDown bool) error {
	_, err := s.db.Exec(`UPDATE deliveries SET host_down = ? WHERE host = ? AND key = ? AND since_ms = ?`, hostDown, host, key, since.UnixMilli())
	return err
}

// toldKind is the kind that was last put in the push module's hands for the key; false when there is no row.
func (s *store) toldKind(host, key string) (hostDown, ok bool) {
	if err := s.db.QueryRow(`SELECT host_down FROM deliveries WHERE host = ? AND key = ?`, host, key).Scan(&hostDown); err != nil {
		return false, false
	}
	return hostDown, true
}

func (s *store) close() error { return s.db.Close() }

// delivery returns the ledger row of an alert key (false when it has none).
func (s *store) delivery(host, key string) (delivery, bool) {
	var d delivery
	err := s.db.QueryRow(`SELECT since_ms, attempt_ms, sent_ms FROM deliveries WHERE host = ? AND key = ?`, host, key).Scan(&d.Since, &d.Attempt, &d.Sent)
	if err != nil {
		return delivery{}, false
	}
	return d, true
}

// beginAttempt records, before the push module is asked, that the alert instance since is being put in its hands now. A different
// instance than the row's (the alert cleared and holds again) starts the row over; the same one keeps its Sent.
func (s *store) beginAttempt(host, key string, since, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO deliveries (host, key, since_ms, attempt_ms, sent_ms) VALUES (?, ?, ?, ?, 0)
		ON CONFLICT (host, key) DO UPDATE SET
			sent_ms = CASE WHEN since_ms = excluded.since_ms THEN sent_ms ELSE 0 END,
			since_ms = excluded.since_ms, attempt_ms = excluded.attempt_ms`, host, key, since.UnixMilli(), now.UnixMilli())
	return err
}

// restoreDelivery gives back an attempt that queued nothing: the ledger row of an alert key goes back to what it was before beginAttempt
// (had: there was a row; prev: its values), but only while the row is still the one beginAttempt wrote (this instance, this attempt
// time). A push of the same alert queued earlier may get its Sent on the sender's goroutine meanwhile; that delivery stays:
//   - the same instance as before: only the attempt time goes back, sent_ms is never touched;
//   - an older instance (the alert was raised again) or no row: the old row returns, or the new one goes, only if it has no delivery.
func (s *store) restoreDelivery(host, key string, prev delivery, had bool, since, attempt time.Time) error {
	var err error
	switch {
	case had && prev.Since == since.UnixMilli():
		_, err = s.db.Exec(`UPDATE deliveries SET attempt_ms = ? WHERE host = ? AND key = ? AND since_ms = ? AND attempt_ms = ?`,
			prev.Attempt, host, key, since.UnixMilli(), attempt.UnixMilli())
	case had:
		_, err = s.db.Exec(`UPDATE deliveries SET since_ms = ?, attempt_ms = ?, sent_ms = ?
			WHERE host = ? AND key = ? AND since_ms = ? AND attempt_ms = ? AND sent_ms = 0`,
			prev.Since, prev.Attempt, prev.Sent, host, key, since.UnixMilli(), attempt.UnixMilli())
	default:
		_, err = s.db.Exec(`DELETE FROM deliveries WHERE host = ? AND key = ? AND since_ms = ? AND attempt_ms = ? AND sent_ms = 0`,
			host, key, since.UnixMilli(), attempt.UnixMilli())
	}
	return err
}

// markSent records that the push module said APNs took the push of this alert instance. A row that is about another instance by now
// (the alert cleared and was raised again while the push was queued) is left alone.
func (s *store) markSent(host, key string, since, now time.Time) error {
	_, err := s.db.Exec(`UPDATE deliveries SET sent_ms = ? WHERE host = ? AND key = ? AND since_ms = ?`, now.UnixMilli(), host, key, since.UnixMilli())
	return err
}

// forgetExcept drops the ledger rows of host whose key is not among keys: those alerts cleared, so the next time one holds it is a new
// one and is told at once.
func (s *store) forgetExcept(host string, keys []string) error {
	rows, err := s.db.Query(`SELECT key FROM deliveries WHERE host = ?`, host)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, k := range keys {
		have[k] = true
	}
	var gone []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		if !have[k] {
			gone = append(gone, k)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, k := range gone {
		if _, err := s.db.Exec(`DELETE FROM deliveries WHERE host = ? AND key = ?`, host, k); err != nil {
			return err
		}
	}
	return nil
}

// noteDigest gathers a digest-level alert: one row per alert instance (host, key, since), moved forward while it holds.
func (s *store) noteDigest(host string, a wire.Alert, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO digest (host, key, since_ms, kind, detail, first_seen_ms, last_seen_ms) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (host, key, since_ms) DO UPDATE SET kind = excluded.kind, detail = excluded.detail, last_seen_ms = excluded.last_seen_ms`,
		host, a.Key, a.Since.UnixMilli(), string(a.Kind), a.Detail, now.UnixMilli(), now.UnixMilli())
	return err
}

func (s *store) digestCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM digest`).Scan(&n)
	return n, err
}

// prune drops the digest rows not seen for digestKeep.
func (s *store) prune(now time.Time) error {
	_, err := s.db.Exec(`DELETE FROM digest WHERE last_seen_ms < ?`, now.Add(-digestKeep).UnixMilli())
	return err
}
