package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// MaxCacheEntries is the most rows result_cache keeps (spec §4.5); the store enforces it, the caller cannot change it.
const MaxCacheEntries = 10000

// CacheRetention is how long an entry is reusable: the log retention (spec §4.5: "never outlives the job and log it points at"). The
// log is not kept longer for the cache and the cache keeps no copy of it. TestCacheRetentionNeverOutlivesLogAndJob pins the order.
const CacheRetention = 3 * 24 * time.Hour

// cacheClockSlack is how far past now an entry may claim to have been made (clock steps between the job's end and the write). A row
// further in the future is refused on the way in and never answers: it would outlive the log it points at.
const cacheClockSlack = time.Minute

// CacheEntry is one row of result_cache: the result of a succeeded job as stored (Result.Host is who made it), the toolchain
// fingerprint that host had, the earned label whose drift invalidates the entry ("" for none), and when it was made.
type CacheEntry struct {
	Key             string
	Result          wire.Result
	HostFingerprint map[string]string
	Label           string
	CreatedAt       time.Time
}

// validateCachedResult says whether r is a result that may be reused: a command that ran and exited 0, with its host and log named, that
// is not itself a reuse and was not superseded. Anything else is never stored and never answers.
func validateCachedResult(r wire.Result) error {
	switch {
	case r.Exit == nil || *r.Exit != 0:
		return errors.New("not a successful exit")
	case r.Host == "":
		return errors.New("no host")
	case r.Log == "":
		return errors.New("no log")
	case r.CacheHit:
		return errors.New("a reuse is not stored again")
	case r.SupersededBy != nil:
		return errors.New("superseded")
	case r.Reason != "":
		return fmt.Errorf("reason %q on a success", r.Reason)
	}
	return nil
}

func (c CacheEntry) validate(now time.Time) error {
	if c.Key == "" {
		return errors.New("store: cache entry: empty key")
	}
	if err := validateCachedResult(c.Result); err != nil {
		return fmt.Errorf("store: cache entry %s: %w", c.Key, err)
	}
	if c.CreatedAt.After(now.Add(cacheClockSlack)) {
		return fmt.Errorf("store: cache entry %s is made in the future (%s)", c.Key, c.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

// PutCacheEntry stores the entry (the same key again replaces it, with the new time) and then trims the table to MaxCacheEntries
// rows, oldest first out; rows with the same created_ms go in key order, so the same rows are chosen every time. An entry older than
// all the others of a full table is itself the one that goes out.
func (s *Store) PutCacheEntry(c CacheEntry, now time.Time) error {
	return s.putCacheEntry(c, now, MaxCacheEntries)
}

func (s *Store) putCacheEntry(c CacheEntry, now time.Time, max int) error {
	if max <= 0 {
		return fmt.Errorf("store: cache limit %d", max)
	}
	if err := c.validate(now); err != nil {
		return err
	}
	rec, err := json.Marshal(c.Result)
	if err != nil {
		return err
	}
	fp, err := fingerprintJSON(c.HostFingerprint)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO result_cache (key, rec, host, host_fingerprint, label, created_ms) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET rec = excluded.rec, host = excluded.host, host_fingerprint = excluded.host_fingerprint,
		label = excluded.label, created_ms = excluded.created_ms`,
		c.Key, string(rec), c.Result.Host, fp, c.Label, c.CreatedAt.UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM result_cache WHERE key IN (
		SELECT key FROM result_cache ORDER BY created_ms DESC, key DESC LIMIT -1 OFFSET ?)`, max); err != nil {
		return err
	}
	return tx.Commit()
}

// GetCacheEntry returns the entry for the key as of now; ok is false when there is none, it is past CacheRetention (an entry exactly
// CacheRetention old is still reusable, like a log at its limit; one millisecond older is not), or it claims to be made in the future.
// A row whose result or fingerprint no longer decodes or no longer is a reusable result is an error, not a hit.
func (s *Store) GetCacheEntry(key string, now time.Time) (c CacheEntry, ok bool, err error) {
	var rec, fp string
	var ms int64
	var host string
	err = s.db.QueryRow(`SELECT key, rec, host, host_fingerprint, label, created_ms FROM result_cache WHERE key = ?`, key).
		Scan(&c.Key, &rec, &host, &fp, &c.Label, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return CacheEntry{}, false, nil
	}
	if err != nil {
		return CacheEntry{}, false, err
	}
	if ms < now.Add(-CacheRetention).UnixMilli() || ms > now.Add(cacheClockSlack).UnixMilli() {
		return CacheEntry{}, false, nil
	}
	if err := json.Unmarshal([]byte(fp), &c.HostFingerprint); err != nil {
		return CacheEntry{}, false, fmt.Errorf("store: cache entry %s: %w", key, err)
	}
	if err := json.Unmarshal([]byte(rec), &c.Result); err != nil {
		return CacheEntry{}, false, fmt.Errorf("store: cache entry %s: %w", key, err)
	}
	if err := validateCachedResult(c.Result); err != nil || c.Result.Host != host {
		return CacheEntry{}, false, fmt.Errorf("store: cache entry %s is not a reusable result (%v)", key, err)
	}
	c.CreatedAt = time.UnixMilli(ms).UTC()
	return c, true, nil
}

// DeleteCacheByLabel drops every entry made under the label (a drift revoked it) and returns how many.
func (s *Store) DeleteCacheByLabel(label string) (int, error) {
	return execCount(s.db, `DELETE FROM result_cache WHERE label = ?`, label)
}

// PruneCache drops the entries past CacheRetention as of now, and those that claim to be made in the future, and returns how many; the
// cutoffs come from now, not from a row.
func (s *Store) PruneCache(now time.Time) (int, error) {
	return execCount(s.db, `DELETE FROM result_cache WHERE created_ms < ? OR created_ms > ?`,
		now.Add(-CacheRetention).UnixMilli(), now.Add(cacheClockSlack).UnixMilli())
}

// CountCache is the number of entries.
func (s *Store) CountCache() (n int, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*) FROM result_cache`).Scan(&n)
	return
}

func execCount(db *sql.DB, q string, args ...any) (int, error) {
	res, err := db.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
