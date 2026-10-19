package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// MaxCacheEntries is the most rows result_cache keeps (spec §4.5); the store enforces it, the caller cannot change it.
const MaxCacheEntries = 10000

// CacheRetention is how long an entry is reusable: the log retention (spec §4.5: "never outlives the job and log it points at"). The
// log is not kept longer for the cache and the cache keeps no copy of it. TestCacheRetentionNeverOutlivesLogAndJob pins the order.
const CacheRetention = 3 * 24 * time.Hour

// CacheEntry is one row of result_cache: the stored result record (JSON, whose shape the submit path owns), who made it and under
// which toolchain, the earned label whose drift invalidates it ("" for none), and when.
type CacheEntry struct {
	Key             string
	Rec             json.RawMessage
	Host            string
	HostFingerprint map[string]string
	Label           string
	CreatedAt       time.Time
}

func (c CacheEntry) validate() error {
	if c.Key == "" || c.Host == "" {
		return errors.New("store: cache entry: empty key or host")
	}
	if !json.Valid(c.Rec) {
		return fmt.Errorf("store: cache entry %s: rec is not JSON", c.Key)
	}
	return nil
}

// PutCacheEntry stores the entry (the same key again replaces it) and then trims the table to MaxCacheEntries rows, oldest first out;
// rows with the same created_ms go in key order, so the same rows are chosen every time.
func (s *Store) PutCacheEntry(c CacheEntry) error { return s.putCacheEntry(c, MaxCacheEntries) }

func (s *Store) putCacheEntry(c CacheEntry, max int) error {
	if max <= 0 {
		return fmt.Errorf("store: cache limit %d", max)
	}
	if err := c.validate(); err != nil {
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
		c.Key, string(c.Rec), c.Host, fp, c.Label, c.CreatedAt.UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM result_cache WHERE key IN (
		SELECT key FROM result_cache ORDER BY created_ms DESC, key DESC LIMIT -1 OFFSET ?)`, max); err != nil {
		return err
	}
	return tx.Commit()
}

// GetCacheEntry returns the entry for the key as of now; ok is false when there is none or it is past CacheRetention (an entry exactly
// CacheRetention old is still reusable, like a log at its limit; one millisecond older is not). A row that no longer decodes is
// reported as an error, not as a miss.
func (s *Store) GetCacheEntry(key string, now time.Time) (c CacheEntry, ok bool, err error) {
	var rec, fp string
	var ms int64
	err = s.db.QueryRow(`SELECT key, rec, host, host_fingerprint, label, created_ms FROM result_cache WHERE key = ?`, key).
		Scan(&c.Key, &rec, &c.Host, &fp, &c.Label, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return CacheEntry{}, false, nil
	}
	if err != nil {
		return CacheEntry{}, false, err
	}
	if ms < now.Add(-CacheRetention).UnixMilli() {
		return CacheEntry{}, false, nil
	}
	if err := json.Unmarshal([]byte(fp), &c.HostFingerprint); err != nil {
		return CacheEntry{}, false, fmt.Errorf("store: cache entry %s: %w", key, err)
	}
	if !json.Valid([]byte(rec)) {
		return CacheEntry{}, false, fmt.Errorf("store: cache entry %s: rec is not JSON", key)
	}
	c.Rec, c.CreatedAt = json.RawMessage(rec), time.UnixMilli(ms).UTC()
	return c, true, nil
}

// DeleteCacheByLabel drops every entry made under the label (a drift revoked it) and returns how many.
func (s *Store) DeleteCacheByLabel(label string) (int, error) {
	return execCount(s.db, `DELETE FROM result_cache WHERE label = ?`, label)
}

// PruneCache drops the entries past CacheRetention as of now and returns how many; the cutoff comes from now, not from a row.
func (s *Store) PruneCache(now time.Time) (int, error) {
	return execCount(s.db, `DELETE FROM result_cache WHERE created_ms < ?`, now.Add(-CacheRetention).UnixMilli())
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
