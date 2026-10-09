package devices

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/devices"
)

// QP-1 task 1: the device token store (spec 3.1). Tests generate their own tokens; nothing here prints one.

type clock struct{ ms int64 }

func (c *clock) now() int64              { return c.ms }
func (c *clock) advance(d time.Duration) { c.ms += d.Milliseconds() }

func openTest(t *testing.T) (*Store, *clock) {
	t.Helper()
	c := &clock{ms: 1_700_000_000_000}
	s, err := OpenStore(filepath.Join(t.TempDir(), "devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.now = c.now
	t.Cleanup(func() { s.Close() })
	return s, c
}

func mint(t *testing.T, s *Store, within time.Duration, over ...func(*MintRequest)) (Row, string) {
	t.Helper()
	req := MintRequest{PairingID: "00000000-0000-4000-8000-000000000001", Label: "iPhone", CreatedBy: "Purdex.app", UseWithin: within}
	for _, o := range over {
		o(&req)
	}
	row, tok, err := s.Mint(req)
	if err != nil {
		t.Fatal(err)
	}
	return row, tok
}

func TestMint_ReturnsTheTokenOnceAndStoresOnlyItsHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	row, tok := mint(t, s, 15*time.Minute)
	if !devices.IsDeviceToken(tok) || !devices.ValidID(row.ID) {
		t.Fatalf("id %q token shape ok=%v", row.ID, devices.IsDeviceToken(tok))
	}
	var stored string
	if err := s.db.QueryRow(`SELECT token_hash FROM device_tokens WHERE id = ?`, row.ID).Scan(&stored); err != nil || stored != devices.Hash(tok) {
		t.Fatalf("stored hash wrong (err %v)", err)
	}
	// No column and no byte of the file holds the token.
	rows, _ := s.db.Query(`SELECT * FROM device_tokens`)
	cols, _ := rows.Columns()
	rows.Close()
	for _, c := range cols {
		var v string
		_ = s.db.QueryRow(`SELECT CAST(`+c+` AS TEXT) FROM device_tokens WHERE id = ?`, row.ID).Scan(&v)
		if strings.Contains(v, tok) {
			t.Fatalf("column %s holds the token", c)
		}
	}
	s.Close()
	for _, f := range []string{path, path + "-wal"} {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), tok) {
			t.Fatalf("%s holds the token", f)
		}
	}
}

func TestMint_FillsTheRowAndTheUseByWindow(t *testing.T) {
	s, c := openTest(t)
	row, _ := mint(t, s, 10*time.Minute, func(r *MintRequest) { r.ProfileID = "p_main" })
	if row.PairingID == "" || row.ProfileID != "p_main" || row.Label != "iPhone" || row.CreatedBy != "Purdex.app" {
		t.Fatalf("row = %+v", row)
	}
	if row.CreatedAt != c.ms || row.UseBy != c.ms+10*60*1000 || row.FirstUsedAt != 0 || row.RevokedAt != 0 {
		t.Fatalf("times = %+v", row)
	}
}

func TestMint_RefusesBadInput(t *testing.T) {
	s, _ := openTest(t)
	for name, req := range map[string]MintRequest{
		"no pairing id": {Label: "x", UseWithin: time.Minute},
		"no label":      {PairingID: "p", UseWithin: time.Minute},
		"no window":     {PairingID: "p", Label: "x"},
	} {
		if _, _, err := s.Mint(req); err == nil {
			t.Errorf("%s: minted", name)
		}
	}
}

// A first use before use_by succeeds and sticks, across a restart (it is in the DB). Mutation gate: keep first use in
// memory → red.
func TestAuthenticate_FirstUseBeforeUseBySticksAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.db")
	c := &clock{ms: 1_700_000_000_000}
	s, _ := OpenStore(path)
	s.now = c.now
	row, tok := mint(t, s, 5*time.Minute)
	c.advance(4 * time.Minute)
	p, ok := s.Authenticate(devices.Hash(tok))
	if !ok || p.ID != row.ID || p.PairingID != row.PairingID {
		t.Fatalf("first use: %+v ok %v", p, ok)
	}
	s.Close()

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.now = c.now
	c.advance(time.Hour) // far past use_by: a token that was used stays valid
	if _, ok := s2.Authenticate(devices.Hash(tok)); !ok {
		t.Fatal("a token used before use_by was refused after a restart")
	}
}

// Unused past use_by is refused at once, with no sweep. Mutation gate: ignore use_by → red.
func TestAuthenticate_UnusedPastUseByIsRefusedImmediately(t *testing.T) {
	s, c := openTest(t)
	_, tok := mint(t, s, time.Minute)
	c.advance(time.Minute + time.Millisecond)
	if _, ok := s.Authenticate(devices.Hash(tok)); ok {
		t.Fatal("an unused token past use_by authenticated")
	}
	// The refusal did not mark it used: a refused first use leaves no trace.
	if got := s.mustRowByToken(t, tok).FirstUsedAt; got != 0 {
		t.Fatalf("first_used_at = %d after a refused first use", got)
	}
}

func TestAuthenticate_UseByIsInclusive(t *testing.T) {
	s, c := openTest(t)
	_, tok := mint(t, s, time.Minute)
	c.advance(time.Minute) // exactly use_by
	if _, ok := s.Authenticate(devices.Hash(tok)); !ok {
		t.Fatal("refused at exactly use_by")
	}
}

func TestAuthenticate_UnknownAndMalformedHashesAreRefused(t *testing.T) {
	s, _ := openTest(t)
	mint(t, s, time.Minute)
	for _, h := range []string{"", "x", devices.Hash("pdxd_" + strings.Repeat("0", 32))} {
		if _, ok := s.Authenticate(h); ok {
			t.Fatalf("%q authenticated", h)
		}
	}
}

// Revoked is refused, used or not. Mutation gate: skip the revoked check on a used token → red.
func TestAuthenticate_RevokedIsRefused(t *testing.T) {
	s, c := openTest(t)
	row, tok := mint(t, s, 10*time.Minute)
	if _, ok := s.Authenticate(devices.Hash(tok)); !ok {
		t.Fatal("first use refused")
	}
	changed, err := s.RevokeID(row.ID)
	if err != nil || !changed {
		t.Fatalf("revoke: %v %v", changed, err)
	}
	c.advance(time.Second)
	if _, ok := s.Authenticate(devices.Hash(tok)); ok {
		t.Fatal("a revoked token that had been used still authenticates")
	}
	_, tok2 := mint(t, s, 10*time.Minute)
	row2 := s.mustRowByToken(t, tok2)
	if _, err := s.RevokeID(row2.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Authenticate(devices.Hash(tok2)); ok {
		t.Fatal("a revoked token that was never used authenticates")
	}
}

// Two first uses at once both succeed, and the first use is recorded once. Mutation gate: read-then-write without the
// conditional statement → a second writer overwrites first_used_at.
func TestAuthenticate_ConcurrentFirstUsesBothSucceedAndRecordOneFirstUse(t *testing.T) {
	s, c := openTest(t)
	_, tok := mint(t, s, 10*time.Minute)
	var wg sync.WaitGroup
	results := make([]bool, 16)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = s.Authenticate(devices.Hash(tok))
		}(i)
	}
	wg.Wait()
	for i, ok := range results {
		if !ok {
			t.Fatalf("concurrent first use %d refused", i)
		}
	}
	row := s.mustRowByToken(t, tok)
	if row.FirstUsedAt != c.ms {
		t.Fatalf("first_used_at = %d", row.FirstUsedAt)
	}
}

// last_used_at is written at most once a minute per token. Mutation gate: write every time → red.
func TestAuthenticate_LastUsedAtIsThrottledToOnceAMinute(t *testing.T) {
	s, c := openTest(t)
	_, tok := mint(t, s, 10*time.Minute)
	h := devices.Hash(tok)
	s.Authenticate(h)
	first := s.mustRowByToken(t, tok).LastUsedAt
	if first != c.ms {
		t.Fatalf("last_used_at = %d after the first use", first)
	}
	c.advance(59 * time.Second)
	s.Authenticate(h)
	if got := s.mustRowByToken(t, tok).LastUsedAt; got != first {
		t.Fatalf("last_used_at moved inside the minute: %d", got)
	}
	c.advance(2 * time.Second)
	s.Authenticate(h)
	if got := s.mustRowByToken(t, tok).LastUsedAt; got != c.ms {
		t.Fatalf("last_used_at = %d after the minute", got)
	}
}

// A request that loses the first-use race to another one that is then followed by a revoke must not be let in: the
// re-read after the lost race checks revoked too. Mutation gate: drop that check → red.
func TestAuthenticate_LostFirstUseRaceThenRevokedIsRefused(t *testing.T) {
	s, c := openTest(t)
	row, tok := mint(t, s, 10*time.Minute)
	s.afterLookup = func() {
		// Between this request's lookup and its first-use statement, another request used the token and an admin revoked it.
		if _, err := s.db.Exec(`UPDATE device_tokens SET first_used_at = ?, revoked_at = ? WHERE id = ?`, c.ms, c.ms, row.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := s.Authenticate(devices.Hash(tok)); ok {
		t.Fatal("a revoked token was let in after a lost first-use race")
	}
	// And the same race without the revoke still lets the request in: the token was used, which is all first use asks.
	s.afterLookup = nil
	row2, tok2 := mint(t, s, 10*time.Minute)
	s.afterLookup = func() {
		if _, err := s.db.Exec(`UPDATE device_tokens SET first_used_at = ? WHERE id = ?`, c.ms, row2.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := s.Authenticate(devices.Hash(tok2)); !ok {
		t.Fatal("a request that lost the first-use race to a live use was refused")
	}
}

func TestAuthenticate_PrincipalCarriesTheProfile(t *testing.T) {
	s, _ := openTest(t)
	_, tok := mint(t, s, time.Minute, func(r *MintRequest) { r.ProfileID = "p_main" })
	p, ok := s.Authenticate(devices.Hash(tok))
	if !ok || p.ProfileID != "p_main" {
		t.Fatalf("principal = %+v", p)
	}
	_, tok2 := mint(t, s, time.Minute)
	p2, _ := s.Authenticate(devices.Hash(tok2))
	if p2.ProfileID != "" {
		t.Fatalf("a token with no profile has %q", p2.ProfileID)
	}
}

func TestRevoke_IsIdempotentAndByPairing(t *testing.T) {
	s, _ := openTest(t)
	a, _ := mint(t, s, time.Minute)
	b, _ := mint(t, s, time.Minute)
	other, _ := mint(t, s, time.Minute, func(r *MintRequest) { r.PairingID = "00000000-0000-4000-8000-000000000002" })

	ids, err := s.RevokePairing("00000000-0000-4000-8000-000000000001")
	if err != nil || len(ids) != 2 {
		t.Fatalf("revoked %v err %v", ids, err)
	}
	if again, _ := s.RevokePairing("00000000-0000-4000-8000-000000000001"); len(again) != 0 {
		t.Fatalf("a second revoke of the pairing changed %v", again)
	}
	if changed, _ := s.RevokeID(a.ID); changed {
		t.Fatal("revoking a revoked id changed it")
	}
	if changed, err := s.RevokeID("d_000000000000"); changed || err != nil {
		t.Fatalf("an unknown id: %v %v", changed, err)
	}
	rows, _ := s.List()
	for _, r := range rows {
		switch r.ID {
		case a.ID, b.ID:
			if r.RevokedAt == 0 {
				t.Fatalf("%s not revoked", r.ID)
			}
		case other.ID:
			if r.RevokedAt != 0 {
				t.Fatal("another pairing's token was revoked")
			}
		}
	}
}

func TestSetLabel_OnlyTheOwnLiveRow(t *testing.T) {
	s, _ := openTest(t)
	a, _ := mint(t, s, time.Minute)
	b, _ := mint(t, s, time.Minute)
	if err := s.SetLabel(a.ID, "iPhone 8"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.List()
	for _, r := range rows {
		if r.ID == a.ID && r.Label != "iPhone 8" || r.ID == b.ID && r.Label != "iPhone" {
			t.Fatalf("labels = %+v", rows)
		}
	}
	if err := s.SetLabel("d_000000000000", "x"); err != ErrNotFound {
		t.Fatalf("an unknown id: %v", err)
	}
	s.RevokeID(a.ID)
	if err := s.SetLabel(a.ID, "x"); err != ErrNotFound {
		t.Fatalf("a revoked id: %v", err)
	}
}

func TestList_NeverCarriesATokenOrAHash(t *testing.T) {
	s, _ := openTest(t)
	_, tok := mint(t, s, time.Minute)
	rows, _ := s.List()
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	dump := strings.ToLower(strings.Join([]string{rows[0].ID, rows[0].PairingID, rows[0].ProfileID, rows[0].Label, rows[0].CreatedBy}, "|"))
	if strings.Contains(dump, strings.ToLower(tok[5:])) || strings.Contains(dump, devices.Hash(tok)) {
		t.Fatal("a row carries the token or its hash")
	}
}

// The sweep deletes rows that can no longer work: never used and past use_by, or revoked more than 30 days ago. A used live
// token, a token still before use_by and a recently revoked used one stay. Mutation gates: sweep a used token → red; sweep a
// recently revoked used one → red.
func TestSweep_DeletesOnlyDeadRows(t *testing.T) {
	s, c := openTest(t)
	unusedOld, _ := mint(t, s, time.Minute)                  // never used, will be past use_by
	usedLive, usedTok := mint(t, s, time.Minute)             // used: stays whatever its use_by says
	revokedOld, revokedOldTok := mint(t, s, time.Hour)       // used, revoked, then 30+ days pass
	revokedRecent, revokedRecentTok := mint(t, s, time.Hour) // used, revoked just now
	s.Authenticate(devices.Hash(usedTok))
	s.Authenticate(devices.Hash(revokedOldTok))
	s.Authenticate(devices.Hash(revokedRecentTok))
	s.RevokeID(revokedOld.ID)
	c.advance(31 * 24 * time.Hour)
	s.RevokeID(revokedRecent.ID)
	freshUnused, _ := mint(t, s, time.Hour) // minted now: still before its use_by
	c.advance(2 * time.Minute)              // unusedOld is long past use_by
	n, err := s.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	rows, _ := s.List()
	for _, r := range rows {
		left[r.ID] = true
	}
	for id, want := range map[string]bool{
		unusedOld.ID: false, usedLive.ID: true, revokedOld.ID: false, revokedRecent.ID: true, freshUnused.ID: true,
	} {
		if left[id] != want {
			t.Errorf("row %s kept=%v want %v", id, left[id], want)
		}
	}
	if n != 2 {
		t.Errorf("swept %d, want 2", n)
	}
}

// A token that expired unused is refused by Authenticate whether or not a sweep has run (the sweep is only housekeeping).
func TestSweep_IsNotWhatRefusesAnExpiredToken(t *testing.T) {
	s, c := openTest(t)
	_, tok := mint(t, s, time.Minute)
	c.advance(2 * time.Minute)
	if _, ok := s.Authenticate(devices.Hash(tok)); ok {
		t.Fatal("authenticated before any sweep")
	}
	if n, _ := s.Sweep(); n != 1 {
		t.Fatalf("swept %d", n)
	}
	if _, ok := s.Authenticate(devices.Hash(tok)); ok {
		t.Fatal("authenticated after the sweep")
	}
}

// The file is owner-only, WAL sidecars included. Mutation gate: skip the chmod → red.
func TestOpenStore_FileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	mint(t, s, time.Minute)
	s.Close()
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(f)
		if err != nil {
			continue // a sidecar SQLite removed at close
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s is %o", filepath.Base(f), fi.Mode().Perm())
		}
	}
	// A file that is already there with looser rights is tightened.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("not tightened: %o", fi.Mode().Perm())
	}
}

func TestStore_NoRowsMeansAnEmptyListNotAnError(t *testing.T) {
	s, _ := openTest(t)
	rows, err := s.List()
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows %v err %v", rows, err)
	}
}

// mustRowByToken reads a device's row by the hash of its token (a test helper; the store has no such public lookup).
func (s *Store) mustRowByToken(t *testing.T, tok string) Row {
	t.Helper()
	var r Row
	err := s.db.QueryRow(`SELECT `+columns+` FROM device_tokens WHERE token_hash = ?`, devices.Hash(tok)).
		Scan(&r.ID, &r.PairingID, &r.ProfileID, &r.Label, &r.CreatedAt, &r.CreatedBy, &r.UseBy, &r.FirstUsedAt, &r.LastUsedAt, &r.RevokedAt)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
