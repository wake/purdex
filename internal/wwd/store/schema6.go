package store

// earnedLabelsDDL (v6): one row per `task:<repo>/<task>` label this host holds or had quarantined. The fingerprint is JSON whose shape
// the qualification code owns (5b); the store only keeps and returns it.
const earnedLabelsDDL = `CREATE TABLE IF NOT EXISTS earned_labels (
	label TEXT PRIMARY KEY,
	fingerprint TEXT NOT NULL,
	snapshot TEXT NOT NULL,
	granted_ms INTEGER NOT NULL,
	state TEXT NOT NULL CHECK (state IN ('earned', 'quarantined')))`

// resultCacheDDL (v6): the reusable results of succeeded jobs (spec §4.5). The record is JSON whose shape 5d owns; host and
// host_fingerprint say who made the result and under what, label is the earned label whose drift invalidates the entry.
const resultCacheDDL = `CREATE TABLE IF NOT EXISTS result_cache (
	key TEXT PRIMARY KEY,
	rec TEXT NOT NULL,
	host TEXT NOT NULL,
	host_fingerprint TEXT NOT NULL,
	label TEXT NOT NULL DEFAULT '',
	created_ms INTEGER NOT NULL)`
const resultCacheIndexDDL = `CREATE INDEX IF NOT EXISTS result_cache_created ON result_cache (created_ms, key)`
const resultCacheLabelIndexDDL = `CREATE INDEX IF NOT EXISTS result_cache_label ON result_cache (label)`
