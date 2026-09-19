package profiles

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	clientA = "c_00000000000a"
	clientB = "c_00000000000b"
)

// sec builds a PutSection input. Fingerprint "fp1" / ordinal 1 is the default
// shape; tests that exercise §4.5 override those two fields on the result.
func sec(name, hash, payload, writer string) Section {
	return Section{
		Section:     name,
		Hash:        hash,
		Fingerprint: "fp1",
		Ordinal:     1,
		Payload:     json.RawMessage(payload),
		Writer:      writer,
	}
}

func newProfile(t *testing.T, s *Store) string {
	t.Helper()
	p, err := s.CreateProfile("P")
	require.NoError(t, err)
	return p.ID
}

// mustPut asserts the put was applied and returns the new rev.
func mustPut(t *testing.T, s *Store, profileID string, in Section, baseRev int64) int64 {
	t.Helper()
	res, err := s.PutSection(profileID, in, baseRev)
	require.NoError(t, err)
	require.Equal(t, PutApplied, res.Outcome)
	return res.Rev
}

func mustGet(t *testing.T, s *Store, profileID, section string) Section {
	t.Helper()
	got, found, err := s.GetSection(profileID, section)
	require.NoError(t, err)
	require.True(t, found, "section %q should be live", section)
	return got
}

// rawSection reads the stored row, tombstones included.
type rawRow struct {
	Rev       int64
	Hash      string
	Payload   string
	Writer    string
	UpdatedAt int64
	Ordinal   int
	Deleted   int
}

func readRaw(t *testing.T, s *Store, profileID, section string) rawRow {
	t.Helper()
	var r rawRow
	require.NoError(t, s.db.QueryRow(`
		SELECT rev, hash, payload, writer, updated_at, ordinal, deleted
		FROM profile_sections WHERE profile_id = ? AND section = ?`, profileID, section,
	).Scan(&r.Rev, &r.Hash, &r.Payload, &r.Writer, &r.UpdatedAt, &r.Ordinal, &r.Deleted))
	return r
}

// ── §4.6 table ─────────────────────────────────────────────────────────────

func TestPutSectionAbsentBaseZeroAppliesAtRevOne(t *testing.T) {
	s, clock := openTestStore(t)
	pid := newProfile(t, s)

	*clock = 2000
	in := sec("hosts", "h1", `{"a":1}`, clientA)
	in.Rev = 99       // caller-supplied rev is ignored
	in.UpdatedAt = 77 // so is the timestamp; the store clock is authoritative
	res, err := s.PutSection(pid, in, 0)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutApplied, Rev: 1}, res)

	assert.Equal(t, Section{
		Section: "hosts", Rev: 1, Hash: "h1", Fingerprint: "fp1", Ordinal: 1,
		Payload: json.RawMessage(`{"a":1}`), Writer: clientA, UpdatedAt: 2000,
	}, mustGet(t, s, pid, "hosts"))
}

func TestPutSectionAbsentNonZeroBaseConflictsAtRevZero(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)

	res, err := s.PutSection(pid, sec("hosts", "h1", `{}`, clientA), 3)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutConflict, Rev: 0}, res)
	assert.Nil(t, res.Current)
	assert.Equal(t, 0, countRows(t, s, "profile_sections", "1 = 1"))
}

func TestPutSectionMatchingBaseRevApplies(t *testing.T) {
	s, clock := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h1", `{"v":1}`, clientA), 0)

	*clock = 3000
	res, err := s.PutSection(pid, sec("hosts", "h2", `{"v":2}`, clientB), 1)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutApplied, Rev: 2}, res)

	got := mustGet(t, s, pid, "hosts")
	assert.Equal(t, int64(2), got.Rev)
	assert.Equal(t, "h2", got.Hash)
	assert.JSONEq(t, `{"v":2}`, string(got.Payload))
	assert.Equal(t, clientB, got.Writer)
	assert.Equal(t, int64(3000), got.UpdatedAt)
}

func TestPutSectionStaleBaseDifferentHashConflicts(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h1", `{"v":1}`, clientA), 0)
	mustPut(t, s, pid, sec("hosts", "h2", `{"v":2}`, clientA), 1)

	res, err := s.PutSection(pid, sec("hosts", "h3", `{"v":3}`, clientB), 1)
	require.NoError(t, err)
	assert.Equal(t, PutConflict, res.Outcome)
	assert.Equal(t, int64(2), res.Rev)
	require.NotNil(t, res.Current, "a conflict carries the SOT side")
	assert.Equal(t, "h2", res.Current.Hash)
	assert.Equal(t, int64(2), res.Current.Rev)
	assert.JSONEq(t, `{"v":2}`, string(res.Current.Payload))

	assert.Equal(t, "h2", mustGet(t, s, pid, "hosts").Hash, "a conflict writes nothing")
}

func TestPutSectionStaleBaseSameHashConvergesWithoutWriting(t *testing.T) {
	s, clock := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h1", `{"v":1}`, clientA), 0)
	mustPut(t, s, pid, sec("hosts", "h2", `{"v":2}`, clientA), 1)
	before := readRaw(t, s, pid, "hosts")

	*clock = 9000
	res, err := s.PutSection(pid, sec("hosts", "h2", `{"v":2}`, clientB), 1)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutConverged, Rev: 2}, res)

	// 驗算，沒改變就不寫回: not rev, not updated_at, not even the writer.
	assert.Equal(t, before, readRaw(t, s, pid, "hosts"))
}

// ── §4.5 schema ────────────────────────────────────────────────────────────

func TestPutSectionNewerOrdinalDifferentFingerprintApplies(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h1", `{}`, clientA), 0)

	in := sec("hosts", "h2", `{"n":1}`, clientB)
	in.Fingerprint, in.Ordinal = "fp2", 2
	res, err := s.PutSection(pid, in, 1)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutApplied, Rev: 2}, res)

	got := mustGet(t, s, pid, "hosts")
	assert.Equal(t, "fp2", got.Fingerprint, "the newer writer's shape replaces the stored one")
	assert.Equal(t, 2, got.Ordinal)
}

func TestPutSectionNewerOrdinalStillNeedsMatchingBaseRev(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h1", `{}`, clientA), 0)
	mustPut(t, s, pid, sec("hosts", "h2", `{}`, clientA), 1)

	// Being newer lets the write past the schema gate, not past the CAS.
	in := sec("hosts", "h3", `{}`, clientB)
	in.Fingerprint, in.Ordinal = "fp2", 2
	res, err := s.PutSection(pid, in, 1)
	require.NoError(t, err)
	assert.Equal(t, PutConflict, res.Outcome)
	assert.Equal(t, "fp1", mustGet(t, s, pid, "hosts").Fingerprint)
}

func TestPutSectionOlderOrdinalDifferentFingerprintIsSchema(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	first := sec("hosts", "h1", `{}`, clientA)
	first.Fingerprint, first.Ordinal = "fp2", 2
	mustPut(t, s, pid, first, 0)

	old := sec("hosts", "h2", `{}`, clientB) // fp1 / ordinal 1
	res, err := s.PutSection(pid, old, 1)
	require.NoError(t, err)
	assert.Equal(t, PutSchema, res.Outcome)
	assert.Equal(t, "fp2", res.CurrentFingerprint)
	assert.Equal(t, 2, res.CurrentOrdinal)
	assert.Nil(t, res.Current)

	got := mustGet(t, s, pid, "hosts")
	assert.Equal(t, int64(1), got.Rev, "a schema refusal writes nothing")
	assert.Equal(t, "h1", got.Hash)
}

func TestPutSectionEqualOrdinalDifferentFingerprintIsSchema(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h1", `{}`, clientA), 0)

	in := sec("hosts", "h2", `{}`, clientB)
	in.Fingerprint = "fp2" // ordinal stays 1: a shape change without a bump
	res, err := s.PutSection(pid, in, 1)
	require.NoError(t, err)
	assert.Equal(t, PutSchema, res.Outcome)
	assert.Equal(t, "fp1", res.CurrentFingerprint)
	assert.Equal(t, 1, res.CurrentOrdinal)
	assert.Equal(t, int64(1), mustGet(t, s, pid, "hosts").Rev)
}

// Schema is decided before revision: a shape mismatch must not be reported as
// "converged" (same hash) or "conflict" (stale base) — both would send the
// client down a path that ends in it writing, or adopting, the wrong shape.
func TestPutSectionSchemaIsCheckedBeforeRevision(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h1", `{}`, clientA), 0)
	mustPut(t, s, pid, sec("hosts", "h2", `{}`, clientA), 1)

	sameHash := sec("hosts", "h2", `{}`, clientB)
	sameHash.Fingerprint = "fp2"
	res, err := s.PutSection(pid, sameHash, 1) // stale base + same hash would be "converged"
	require.NoError(t, err)
	assert.Equal(t, PutSchema, res.Outcome)

	otherHash := sec("hosts", "h9", `{}`, clientB)
	otherHash.Fingerprint = "fp2"
	res, err = s.PutSection(pid, otherHash, 1) // stale base + other hash would be "conflict"
	require.NoError(t, err)
	assert.Equal(t, PutSchema, res.Outcome)
}

func TestPutSectionSameFingerprintLowerOrdinalKeepsStoredOrdinal(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	first := sec("hosts", "h1", `{}`, clientA)
	first.Ordinal = 5
	mustPut(t, s, pid, first, 0)

	older := sec("hosts", "h2", `{}`, clientB)
	older.Ordinal = 3 // same fingerprint: an older client may write (§4.5 row 1)…
	assert.Equal(t, int64(2), mustPut(t, s, pid, older, 1))

	got := mustGet(t, s, pid, "hosts")
	assert.Equal(t, "h2", got.Hash)
	assert.Equal(t, 5, got.Ordinal, "…but the stored ordinal never goes down")
}

func TestPutSectionSameFingerprintHigherOrdinalRaisesStoredOrdinal(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	first := sec("hosts", "h1", `{}`, clientA)
	first.Ordinal = 5
	mustPut(t, s, pid, first, 0)

	newer := sec("hosts", "h2", `{}`, clientB)
	newer.Ordinal = 7
	mustPut(t, s, pid, newer, 1)

	assert.Equal(t, 7, mustGet(t, s, pid, "hosts").Ordinal)
}

// ── delete / tombstones ────────────────────────────────────────────────────

func TestDeleteSectionMatchingRevLeavesATombstone(t *testing.T) {
	s, clock := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("tabs.w1", "h1", `{"big":"payload"}`, clientA), 0)

	*clock = 4000
	res, err := s.DeleteSection(pid, "tabs.w1", clientB, 1)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutApplied, Rev: 2}, res)

	_, found, err := s.GetSection(pid, "tabs.w1")
	require.NoError(t, err)
	assert.False(t, found, "gone as far as any reader can tell")

	raw := readRaw(t, s, pid, "tabs.w1")
	assert.Equal(t, rawRow{
		Rev: 2, Hash: "", Payload: "{}", Writer: clientB, UpdatedAt: 4000, Ordinal: 1, Deleted: 1,
	}, raw, "the row stays, as a tombstone that keeps the revision counter")
}

func TestDeleteSectionTwiceIsAppliedBothTimes(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("tabs.w1", "h1", `{}`, clientA), 0)

	first, err := s.DeleteSection(pid, "tabs.w1", clientA, 1)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutApplied, Rev: 2}, first)

	// Two clients removing the same workspace must not deadlock each other.
	second, err := s.DeleteSection(pid, "tabs.w1", clientB, 1)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutApplied, Rev: 2}, second)
	assert.Equal(t, clientA, readRaw(t, s, pid, "tabs.w1").Writer, "the repeat wrote nothing")
}

func TestDeleteSectionNeverExistedIsApplied(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)

	res, err := s.DeleteSection(pid, "tabs.nope", clientA, 4)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutApplied, Rev: 0}, res)
	assert.Equal(t, 0, countRows(t, s, "profile_sections", "1 = 1"), "no tombstone for a section that never was")
}

func TestDeleteSectionStaleRevConflicts(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("tabs.w1", "h1", `{"v":1}`, clientA), 0)
	mustPut(t, s, pid, sec("tabs.w1", "h2", `{"v":2}`, clientA), 1)

	res, err := s.DeleteSection(pid, "tabs.w1", clientB, 1)
	require.NoError(t, err)
	assert.Equal(t, PutConflict, res.Outcome)
	assert.Equal(t, int64(2), res.Rev)
	require.NotNil(t, res.Current)
	assert.JSONEq(t, `{"v":2}`, string(res.Current.Payload))

	assert.Equal(t, "h2", mustGet(t, s, pid, "tabs.w1").Hash)
}

func TestDeleteSectionUnknownProfile(t *testing.T) {
	s, _ := openTestStore(t)

	_, err := s.DeleteSection("p_ffffffffffff", "hosts", clientA, 1)
	assert.ErrorIs(t, err, ErrProfileNotFound)
}

func TestTombstoneIsInvisibleAndRejectsNonZeroBase(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h0", `{}`, clientA), 0)
	mustPut(t, s, pid, sec("tabs.w1", "h1", `{}`, clientA), 0)
	_, err := s.DeleteSection(pid, "tabs.w1", clientA, 1)
	require.NoError(t, err)

	_, found, err := s.GetSection(pid, "tabs.w1")
	require.NoError(t, err)
	assert.False(t, found)

	list, err := s.ListSections(pid)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "hosts", list[0].Section)

	// The section was deleted under this client (§4.6.3): whatever base it
	// holds — even the tombstone's own rev — it gets "conflict, rev 0".
	for _, base := range []int64{1, 2, 7} {
		res, err := s.PutSection(pid, sec("tabs.w1", "h2", `{}`, clientB), base)
		require.NoError(t, err)
		assert.Equal(t, PutResult{Outcome: PutConflict, Rev: 0}, res, "baseRev %d", base)
	}
	assert.Equal(t, 1, readRaw(t, s, pid, "tabs.w1").Deleted)
}

func TestPutSectionOverTombstoneIgnoresItsOldShape(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	first := sec("tabs.w1", "h1", `{}`, clientA)
	first.Fingerprint, first.Ordinal = "fp9", 9
	mustPut(t, s, pid, first, 0)
	_, err := s.DeleteSection(pid, "tabs.w1", clientA, 1)
	require.NoError(t, err)

	// fp1 / ordinal 1 against a live fp9 / 9 row would be PutSchema. Over a
	// tombstone there is no stored shape left to protect.
	assert.Equal(t, int64(3), mustPut(t, s, pid, sec("tabs.w1", "h2", `{"v":2}`, clientB), 0))

	got := mustGet(t, s, pid, "tabs.w1")
	assert.Equal(t, "fp1", got.Fingerprint)
	assert.Equal(t, 1, got.Ordinal)
	assert.JSONEq(t, `{"v":2}`, string(got.Payload))
}

// TestSectionABA is plan review #1. With a real DELETE the recreated section
// restarts at rev 1, and A's retried "delete baseRev=1" (its first response
// was lost) matches it and destroys B's content.
func TestSectionABA(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)

	require.Equal(t, int64(1), mustPut(t, s, pid, sec("tabs.w1", "hA", `{"by":"A"}`, clientA), 0))

	del, err := s.DeleteSection(pid, "tabs.w1", clientA, 1) // A deletes; the response is lost
	require.NoError(t, err)
	require.Equal(t, PutApplied, del.Outcome)

	recreated := mustPut(t, s, pid, sec("tabs.w1", "hB", `{"by":"B"}`, clientB), 0)
	assert.Equal(t, int64(3), recreated, "the counter continues past the tombstone; it does not restart at 1")

	retry, err := s.DeleteSection(pid, "tabs.w1", clientA, 1) // A retries its stale delete
	require.NoError(t, err)
	assert.Equal(t, PutConflict, retry.Outcome)
	require.NotNil(t, retry.Current)
	assert.Equal(t, int64(3), retry.Current.Rev)

	got := mustGet(t, s, pid, "tabs.w1")
	assert.Equal(t, "hB", got.Hash, "B's content survives A's retry")
	assert.JSONEq(t, `{"by":"B"}`, string(got.Payload))
}

// ── reads ──────────────────────────────────────────────────────────────────

func TestListSectionsNeverNilAndOmitsPayload(t *testing.T) {
	s, clock := openTestStore(t)
	pid := newProfile(t, s)

	empty, err := s.ListSections(pid)
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)

	unknown, err := s.ListSections("p_ffffffffffff")
	require.NoError(t, err)
	assert.NotNil(t, unknown)

	*clock = 2000
	mustPut(t, s, pid, sec("workspaces", "hw", `{"secret":"payload"}`, clientA), 0)
	mustPut(t, s, pid, sec("hosts", "hh", `{"secret":"payload"}`, clientB), 0)

	list, err := s.ListSections(pid)
	require.NoError(t, err)
	assert.Equal(t, []SectionMeta{
		{Section: "hosts", Rev: 1, Hash: "hh", Fingerprint: "fp1", Ordinal: 1, Writer: clientB, UpdatedAt: 2000},
		{Section: "workspaces", Rev: 1, Hash: "hw", Fingerprint: "fp1", Ordinal: 1, Writer: clientA, UpdatedAt: 2000},
	}, list)

	// SectionMeta has no payload field at all; make sure none sneaks into the wire form.
	wire, err := json.Marshal(list)
	require.NoError(t, err)
	assert.NotContains(t, string(wire), "payload")
	assert.NotContains(t, string(wire), "secret")
}

func TestGetSectionIsScopedToItsProfile(t *testing.T) {
	s, _ := openTestStore(t)
	a := newProfile(t, s)
	b := newProfile(t, s)
	mustPut(t, s, a, sec("hosts", "hA", `{}`, clientA), 0)

	_, found, err := s.GetSection(b, "hosts")
	require.NoError(t, err)
	assert.False(t, found)

	// …and the same section name in another profile has its own counter.
	assert.Equal(t, int64(1), mustPut(t, s, b, sec("hosts", "hB", `{}`, clientA), 0))
}

// ── profile lifetime ───────────────────────────────────────────────────────

func TestDeleteProfileRemovesSectionsAndTombstones(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	other := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h1", `{}`, clientA), 0)
	mustPut(t, s, pid, sec("tabs.w1", "h2", `{}`, clientA), 0)
	_, err := s.DeleteSection(pid, "tabs.w1", clientA, 1)
	require.NoError(t, err)
	mustPut(t, s, other, sec("hosts", "h3", `{}`, clientA), 0)

	require.NoError(t, s.DeleteProfile(pid))

	assert.Equal(t, 0, countRows(t, s, "profile_sections", "profile_id = ?", pid), "live rows and tombstones both go")
	assert.Equal(t, 1, countRows(t, s, "profile_sections", "profile_id = ?", other))
}

func TestPutSectionUnknownProfile(t *testing.T) {
	s, _ := openTestStore(t)

	for _, base := range []int64{0, 2} {
		_, err := s.PutSection("p_ffffffffffff", sec("hosts", "h1", `{}`, clientA), base)
		assert.ErrorIs(t, err, ErrProfileNotFound, "baseRev %d", base)
	}
	assert.Equal(t, 0, countRows(t, s, "profile_sections", "1 = 1"))
}

func TestPutSectionJustDeletedProfile(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h1", `{}`, clientA), 0)
	require.NoError(t, s.DeleteProfile(pid))

	// Neither a fresh insert nor the client's next ordinary push may resurrect a row.
	for _, base := range []int64{0, 1} {
		_, err := s.PutSection(pid, sec("hosts", "h2", `{}`, clientA), base)
		assert.ErrorIs(t, err, ErrProfileNotFound, "baseRev %d", base)
	}
	assert.Equal(t, 0, countRows(t, s, "profile_sections", "1 = 1"))
}

// ── lost races, made deterministic ─────────────────────────────────────────
//
// afterSectionRead runs between PutSection's read and its conditional write,
// which is exactly the window a racing writer has to hit.

func TestPutSectionLostInsertRaceIsReclassified(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)

	s.afterSectionRead = func() {
		s.afterSectionRead = nil
		mustPut(t, s, pid, sec("hosts", "hX", `{"by":"X"}`, clientB), 0)
	}
	res, err := s.PutSection(pid, sec("hosts", "hA", `{"by":"A"}`, clientA), 0)
	require.NoError(t, err)
	assert.Equal(t, PutConflict, res.Outcome)
	assert.Equal(t, int64(1), res.Rev)
	require.NotNil(t, res.Current)
	assert.Equal(t, "hX", res.Current.Hash)
	assert.Equal(t, "hX", mustGet(t, s, pid, "hosts").Hash)
}

func TestPutSectionLostInsertRaceWithSameContentConverges(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)

	s.afterSectionRead = func() {
		s.afterSectionRead = nil
		mustPut(t, s, pid, sec("hosts", "same", `{}`, clientB), 0)
	}
	res, err := s.PutSection(pid, sec("hosts", "same", `{}`, clientA), 0)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutConverged, Rev: 1}, res)
	assert.Equal(t, clientB, mustGet(t, s, pid, "hosts").Writer)
}

func TestPutSectionLostUpdateRaceConflicts(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("hosts", "h1", `{}`, clientA), 0)

	s.afterSectionRead = func() {
		s.afterSectionRead = nil
		mustPut(t, s, pid, sec("hosts", "hX", `{"by":"X"}`, clientB), 1)
	}
	res, err := s.PutSection(pid, sec("hosts", "hA", `{"by":"A"}`, clientA), 1)
	require.NoError(t, err)
	assert.Equal(t, PutConflict, res.Outcome)
	assert.Equal(t, int64(2), res.Rev)

	got := mustGet(t, s, pid, "hosts")
	assert.Equal(t, int64(2), got.Rev, "exactly one of the two writers advanced the rev")
	assert.Equal(t, "hX", got.Hash, "the loser did not overwrite the winner")
}

func TestPutSectionLostRecreateRaceConflicts(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("tabs.w1", "h1", `{}`, clientA), 0)
	_, err := s.DeleteSection(pid, "tabs.w1", clientA, 1)
	require.NoError(t, err)

	s.afterSectionRead = func() {
		s.afterSectionRead = nil
		mustPut(t, s, pid, sec("tabs.w1", "hX", `{}`, clientB), 0) // rev 3
	}
	res, err := s.PutSection(pid, sec("tabs.w1", "hA", `{}`, clientA), 0)
	require.NoError(t, err)
	assert.Equal(t, PutConflict, res.Outcome)
	assert.Equal(t, int64(3), res.Rev)
	assert.Equal(t, "hX", mustGet(t, s, pid, "tabs.w1").Hash)
}

func TestPutSectionDeletedUnderTheWriterConflictsAtRevZero(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)
	mustPut(t, s, pid, sec("tabs.w1", "h1", `{}`, clientA), 0)

	s.afterSectionRead = func() {
		s.afterSectionRead = nil
		_, err := s.DeleteSection(pid, "tabs.w1", clientB, 1)
		require.NoError(t, err)
	}
	res, err := s.PutSection(pid, sec("tabs.w1", "h2", `{}`, clientA), 1)
	require.NoError(t, err)
	assert.Equal(t, PutResult{Outcome: PutConflict, Rev: 0}, res)
	assert.Equal(t, 1, readRaw(t, s, pid, "tabs.w1").Deleted, "the tombstone was not overwritten")
}

func TestPutSectionGivesUpUnderEndlessContention(t *testing.T) {
	s, _ := openTestStore(t)
	pid := newProfile(t, s)

	// Every time PutSection looks, somebody has recreated-and-deleted the
	// section again, so its recreate-CAS keeps missing a tombstone that is
	// still a tombstone. It must stop rather than spin.
	hook := func() {
		saved := s.afterSectionRead
		s.afterSectionRead = nil
		rev := mustPut(t, s, pid, sec("tabs.w1", "hX", `{}`, clientB), 0)
		_, err := s.DeleteSection(pid, "tabs.w1", clientB, rev)
		require.NoError(t, err)
		s.afterSectionRead = saved
	}
	s.afterSectionRead = hook

	_, err := s.PutSection(pid, sec("tabs.w1", "hA", `{}`, clientA), 0)
	assert.ErrorIs(t, err, ErrSectionContended)
	assert.Equal(t, 1, readRaw(t, s, pid, "tabs.w1").Deleted)
}

// ── real concurrency ───────────────────────────────────────────────────────
//
// File-backed WAL database: ":memory:" is pinned to one connection, which
// serialises every call inside database/sql and cannot race.

func openFileStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "profiles.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

// racePut fires n PutSection calls at once, each with its own hash, and
// returns the results. Any error — SQLITE_BUSY included — fails the test.
func racePut(t *testing.T, s *Store, profileID, section string, baseRev int64, n int, tag string) []PutResult {
	t.Helper()
	results := make([]PutResult, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := sec(section, fmt.Sprintf("%s-h%d", tag, i), fmt.Sprintf(`{"writer":%d}`, i),
				fmt.Sprintf("c_%012x", i))
			<-start
			results[i], errs[i] = s.PutSection(profileID, in, baseRev)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "%s: writer %d", tag, i)
	}
	return results
}

// requireOneWinner asserts exactly one PutApplied at wantRev, every other
// result a PutConflict against the winner, and the stored row is the winner's.
func requireOneWinner(t *testing.T, s *Store, profileID, section string, results []PutResult, wantRev int64, tag string) {
	t.Helper()
	winner := -1
	for i, r := range results {
		switch r.Outcome {
		case PutApplied:
			require.Equal(t, -1, winner, "%s: writers %d and %d both applied", tag, winner, i)
			require.Equal(t, wantRev, r.Rev, "%s", tag)
			winner = i
		case PutConflict:
			require.Equal(t, wantRev, r.Rev, "%s: writer %d", tag, i)
			require.NotNil(t, r.Current, "%s: writer %d", tag, i)
		default:
			t.Fatalf("%s: writer %d got outcome %v", tag, i, r.Outcome)
		}
	}
	require.NotEqual(t, -1, winner, "%s: nobody applied", tag)

	got := mustGet(t, s, profileID, section)
	require.Equal(t, wantRev, got.Rev, "%s: final rev", tag)
	require.Equal(t, fmt.Sprintf("%s-h%d", tag, winner), got.Hash, "%s: stored hash is the winner's", tag)
	require.JSONEq(t, fmt.Sprintf(`{"writer":%d}`, winner), string(got.Payload), "%s", tag)
}

func TestConcurrentPutSectionSameBaseRevHasOneWinner(t *testing.T) {
	s := openFileStore(t)
	pid := newProfile(t, s)

	const writers, rounds = 12, 40
	base := mustPut(t, s, pid, sec("hosts", "seed", `{}`, clientA), 0)
	for round := 0; round < rounds; round++ {
		tag := fmt.Sprintf("r%d", round)
		results := racePut(t, s, pid, "hosts", base, writers, tag)
		requireOneWinner(t, s, pid, "hosts", results, base+1, tag)
		base++
	}
}

func TestConcurrentFirstInsertHasOneWinner(t *testing.T) {
	s := openFileStore(t)
	pid := newProfile(t, s)

	const writers, rounds = 12, 40
	for round := 0; round < rounds; round++ {
		section := fmt.Sprintf("tabs.w%d", round)
		tag := fmt.Sprintf("r%d", round)
		results := racePut(t, s, pid, section, 0, writers, tag)
		requireOneWinner(t, s, pid, section, results, 1, tag)
	}
}
