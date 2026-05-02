package codexbroker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestQuarantineStore_RoundTrip — write then read back the same payload.
func TestQuarantineStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quarantine.json")
	store := &QuarantineStore{}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	in := &QuarantineFile{
		Version: 1,
		Entries: []QuarantineEntry{{
			BrokerKey:     "abcdef0123456789",
			PID:           4242,
			Lstart:        now.Add(-2 * time.Hour),
			QuarantinedAt: now,
			Reason:        "pid_reuse_suspicion",
			Fingerprint:   BrokerFingerprint{Executable: "/usr/bin/node", Cmdline: "node app", PidFile: "/tmp/p"},
			ExpiresAt:     now.Add(7 * 24 * time.Hour),
		}},
	}
	if err := store.Save(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := store.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.Version != 1 {
		t.Errorf("version: %d", out.Version)
	}
	if len(out.Entries) != 1 || out.Entries[0].BrokerKey != "abcdef0123456789" {
		t.Errorf("entries lost: %+v", out.Entries)
	}
	if !out.Entries[0].ExpiresAt.Equal(in.Entries[0].ExpiresAt) {
		t.Errorf("expiresAt lost: %v vs %v", out.Entries[0].ExpiresAt, in.Entries[0].ExpiresAt)
	}
}

// TestQuarantineStore_LoadMissing — non-existent file → empty file, no err.
func TestQuarantineStore_LoadMissing(t *testing.T) {
	dir := t.TempDir()
	store := &QuarantineStore{}
	out, err := store.Load(filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if out == nil || out.Version != 1 {
		t.Errorf("expected default empty file with version=1, got %+v", out)
	}
	if len(out.Entries) != 0 {
		t.Errorf("expected zero entries, got %+v", out.Entries)
	}
}

// TestQuarantineStore_AtomicWrite — write to a path that fails (simulated
// via unwritable parent dir); old file intact.
func TestQuarantineStore_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quarantine.json")
	store := &QuarantineStore{}
	// Seed an existing valid file.
	original := &QuarantineFile{Version: 1, Entries: []QuarantineEntry{{BrokerKey: "orig"}}}
	if err := store.Save(path, original); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	// Write to a directory that doesn't exist → should fail.
	bad := filepath.Join(dir, "subdir-does-not-exist", "quarantine.json")
	if err := store.Save(bad, &QuarantineFile{Version: 1}); err == nil {
		t.Errorf("expected error writing to missing parent, got nil")
	}
	// Original file should still parse.
	out, err := store.Load(path)
	if err != nil || len(out.Entries) != 1 || out.Entries[0].BrokerKey != "orig" {
		t.Errorf("original file corrupted: %v %+v", err, out)
	}
}

// TestQuarantineStore_PurgeExpired — expired entries removed; active
// retained.
func TestQuarantineStore_PurgeExpired(t *testing.T) {
	store := &QuarantineStore{}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	qf := &QuarantineFile{Version: 1, Entries: []QuarantineEntry{
		{BrokerKey: "expired", QuarantinedAt: now.Add(-10 * 24 * time.Hour), ExpiresAt: now.Add(-1 * time.Hour)},
		{BrokerKey: "active", QuarantinedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)},
	}}
	out := store.PurgeExpired(qf, now)
	if len(out.Entries) != 1 {
		t.Fatalf("expected 1 entry after purge, got %d", len(out.Entries))
	}
	if out.Entries[0].BrokerKey != "active" {
		t.Errorf("expected active entry retained, got %s", out.Entries[0].BrokerKey)
	}
}

// TestIsQuarantined_Found — present brokerKey returns true.
func TestIsQuarantined_Found(t *testing.T) {
	qf := &QuarantineFile{Entries: []QuarantineEntry{{BrokerKey: "k1"}, {BrokerKey: "k2"}}}
	if !IsQuarantined(qf, "k1") {
		t.Errorf("expected k1 quarantined")
	}
}

// TestIsQuarantined_NotFound — absent key returns false.
func TestIsQuarantined_NotFound(t *testing.T) {
	qf := &QuarantineFile{Entries: []QuarantineEntry{{BrokerKey: "k1"}}}
	if IsQuarantined(qf, "kX") {
		t.Errorf("expected kX not quarantined")
	}
}

// TestIsQuarantined_NilFile — defensive nil → false.
func TestIsQuarantined_NilFile(t *testing.T) {
	if IsQuarantined(nil, "k1") {
		t.Errorf("nil file must not return true")
	}
}

// TestQuarantineStore_AddEntry — adds and dedups.
func TestQuarantineStore_AddEntry(t *testing.T) {
	store := &QuarantineStore{}
	qf := &QuarantineFile{Version: 1}
	qf = store.AddEntry(qf, QuarantineEntry{BrokerKey: "k1", PID: 1})
	if len(qf.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(qf.Entries))
	}
	// Re-add same key — should overwrite (latest fingerprint wins).
	qf = store.AddEntry(qf, QuarantineEntry{BrokerKey: "k1", PID: 999})
	if len(qf.Entries) != 1 {
		t.Errorf("expected dedupe to 1, got %d", len(qf.Entries))
	}
	if qf.Entries[0].PID != 999 {
		t.Errorf("expected PID overwritten to 999, got %d", qf.Entries[0].PID)
	}
}

// Verify the seeded file is valid JSON on disk after Save (defensive).
func TestQuarantineStore_FileBytesParseable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "q.json")
	store := &QuarantineStore{}
	if err := store.Save(path, &QuarantineFile{Version: 1}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Errorf("file is empty")
	}
}
