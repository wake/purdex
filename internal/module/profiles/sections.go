package profiles

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrSectionContended is returned by PutSection when its conditional write kept
// losing to other writers of the same section (see maxPutAttempts). Nothing
// was written; the caller may simply retry.
var ErrSectionContended = errors.New("profile section is contended")

// Section is one section of a profile as stored on the daemon: the payload and
// the envelope the compare-and-set of spec §4.6 works on.
//
// As a PutSection input, Rev and UpdatedAt are ignored — the revision comes
// from the stored row and the timestamp from the store clock.
type Section struct {
	Section     string          `json:"section"`
	Rev         int64           `json:"rev"`
	Hash        string          `json:"hash"`
	Fingerprint string          `json:"fingerprint"`
	Ordinal     int             `json:"ordinal"`
	Payload     json.RawMessage `json:"payload"`
	Writer      string          `json:"writer"`
	UpdatedAt   int64           `json:"updatedAt"`
}

// SectionMeta is a Section without its payload: one entry of the section index
// a client reconciles against on connect (spec §4.6).
type SectionMeta struct {
	Section     string `json:"section"`
	Rev         int64  `json:"rev"`
	Hash        string `json:"hash"`
	Fingerprint string `json:"fingerprint"`
	Ordinal     int    `json:"ordinal"`
	Writer      string `json:"writer"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// PutOutcome is the branch of the spec §4.6 table a write ended in.
type PutOutcome int

const (
	// PutApplied — the write was stored; PutResult.Rev is the new revision.
	PutApplied PutOutcome = iota
	// PutConverged — the base was stale but the SOT already holds the same
	// hash. Nothing was written; PutResult.Rev is the SOT's revision.
	PutConverged
	// PutConflict — the base was stale and the content differs. Nothing was
	// written. PutResult.Current is the SOT side; it is nil, with Rev 0, when
	// the section does not exist (it was deleted under the client, §4.6.3).
	PutConflict
	// PutSchema — the stored shape differs and the writer is not newer (§4.5).
	// Nothing was written; CurrentFingerprint/CurrentOrdinal describe the SOT.
	PutSchema
)

// PutResult is the outcome of PutSection or DeleteSection.
//
// Changed reports whether this call wrote a row. It is true exactly when a
// conditional write hit, and it is what a caller must key notifications on:
// Outcome cannot, because DeleteSection answers PutApplied both for the delete
// that wrote the tombstone and for the idempotent repeat — and the repeat can
// even carry the same Rev (tombstone at rev N+1, baseRev N).
type PutResult struct {
	Outcome            PutOutcome
	Rev                int64
	Changed            bool
	Current            *Section // PutConflict against a live section
	CurrentFingerprint string   // PutSchema
	CurrentOrdinal     int      // PutSchema
}

// maxPutAttempts bounds PutSection's read → conditional-write loop. A retry
// happens only when the conditional write missed *and* the re-read still says
// the write should be attempted, which takes another writer's commit landing
// inside the window each time (in practice: a section being recreated and
// deleted again while we look). One retry is already rare; the bound exists so
// that a pathological neighbour costs an error instead of an unbounded loop in
// a request handler.
const maxPutAttempts = 4

// sectionRow is a stored row, tombstones included.
type sectionRow struct {
	Section
	deleted bool
}

// readSectionRow returns the stored row for (profileID, section) whether it is
// live or a tombstone. Only the write paths may look at tombstones; every
// public read goes through GetSection / ListSections, which filter them out.
func (s *Store) readSectionRow(profileID, section string) (sectionRow, bool, error) {
	var (
		row     sectionRow
		payload string
		deleted int
	)
	err := s.db.QueryRow(`
		SELECT section, rev, hash, fingerprint, ordinal, payload, writer, updated_at, deleted
		FROM profile_sections
		WHERE profile_id = ? AND section = ?`, profileID, section,
	).Scan(&row.Section.Section, &row.Rev, &row.Hash, &row.Fingerprint, &row.Ordinal,
		&payload, &row.Writer, &row.UpdatedAt, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return sectionRow{}, false, nil
	}
	if err != nil {
		return sectionRow{}, false, fmt.Errorf("read section: %w", err)
	}
	row.Payload = json.RawMessage(payload)
	row.deleted = deleted != 0
	return row, true, nil
}

// requireProfile returns ErrProfileNotFound when there is no such profile.
func (s *Store) requireProfile(profileID string) error {
	_, found, err := s.GetProfile(profileID)
	if err != nil {
		return err
	}
	if !found {
		return ErrProfileNotFound
	}
	return nil
}

// PutSection is the compare-and-set of spec §4.6: it stores `in` only if the
// section is still at baseRev. It returns ErrProfileNotFound, and writes
// nothing, when the profile does not exist.
//
// Decision order — schema is checked before revision, and fails closed:
//
//  1. No row, or a tombstone: only baseRev == 0 is accepted. A never-seen
//     section starts at rev 1; one recreated over a tombstone continues at
//     tombstone.rev + 1 (see DeleteSection for why). Any other baseRev is
//     PutConflict with Rev 0 and no Current — the section was deleted under
//     the client. A tombstone's fingerprint/ordinal are ignored: there is no
//     stored shape left to protect.
//  2. Live row with a different fingerprint: a strictly higher incoming
//     ordinal means the writer is newer and carries on to 3; anything else —
//     older, or equal ordinals, the developer error of §4.5 — is PutSchema.
//  3. row.Rev == baseRev → store, rev+1, PutApplied.
//  4. row.Hash == in.Hash → PutConverged, and *nothing* is written.
//  5. otherwise → PutConflict carrying the current row.
//
// # Why one conditional statement is enough, and there is no transaction
//
// The read at the top of each attempt decides nothing on its own. Whether the
// write happens is decided by the write statement's own WHERE clause
// (`rev = ? AND deleted = 0`, `rev = ? AND deleted = 1`, or the primary-key
// conflict of the INSERT), and we learn the verdict from RowsAffected. SQLite
// executes a single statement atomically and admits one writer at a time, so
// two racing CAS statements are strictly ordered: the first moves rev from N
// to N+1, the second then evaluates `rev = N` against N+1 and touches nothing.
// A losing writer waits for the write lock (busy_timeout in OpenStore) rather
// than failing. "Exactly one winner per revision" is therefore a property of
// the statement, not of any read/write pairing around it.
//
// The schema gate of step 2 is evaluated on the row we read, not inside the
// statement — and that is still sound, because every write to a row bumps its
// rev (update, tombstone, recreate; PutConverged writes nothing). If the
// UPDATE's `rev = ?` matches, the row is byte-for-byte the one the gate
// looked at.
//
// A BEGIN…COMMIT around read+write would add nothing to that and would cost
// something: under database/sql a transaction that reads and then writes is a
// read→write lock upgrade, which fails with SQLITE_BUSY_SNAPSHOT when another
// writer got in between — an error busy_timeout does not retry. (DeleteProfile
// does use a transaction: it is inherently multi-statement, and it starts with
// a write, so it never upgrades. See its comment.)
//
// # A profile deleted under the write
//
// DeleteProfile removes the profile row and every section row in one
// transaction, so no conditional write here can match a row of a profile that
// is being deleted: it runs either before the transaction (and is swept with
// the rest) or after the commit, where it hits zero rows. The re-read then
// finds no row, and every arm that finds no row ends in requireProfile —
// ErrProfileNotFound, with nothing written and Changed false.
//
// # Why a stale classification is harmless
//
// When the conditional write misses, the next attempt's read is only there to
// *classify* an outcome the statement has already decided against: which of
// converged / conflict / schema to report, and what Current to attach. Another
// writer can commit right after that read, so the Rev and Current we report
// may already be one revision behind when the client sees them. That is fine:
// no state was changed on the strength of the read, the client treats a
// conflict's Current as "a SOT snapshot to choose against" rather than as the
// head, and the commit that made it stale broadcast its own event — on which
// the client re-runs the base/fast-forward decision of spec §4.6.1 and catches
// up. The same holds for a read that goes stale before a response is sent at
// all; no locking could close that window anyway.
func (s *Store) PutSection(profileID string, in Section, baseRev int64) (PutResult, error) {
	payload := string(in.Payload)

	for attempt := 0; attempt < maxPutAttempts; attempt++ {
		row, found, err := s.readSectionRow(profileID, in.Section)
		if err != nil {
			return PutResult{}, err
		}
		if s.afterSectionRead != nil {
			s.afterSectionRead()
		}
		now := s.now()

		switch {
		case !found:
			// Step 1, never-seen section.
			if baseRev != 0 {
				// "Deleted under the client" only makes sense inside a profile
				// that exists; otherwise say what is actually wrong.
				if err := s.requireProfile(profileID); err != nil {
					return PutResult{}, err
				}
				return PutResult{Outcome: PutConflict}, nil
			}
			// Profile existence is part of the statement. DeleteProfile is one
			// transaction, so this insert is ordered wholly before it (and is
			// swept with the profile) or wholly after it (and finds no profile):
			// a section can never be orphaned by a concurrent DeleteProfile.
			n, err := s.execRows("insert section", `
				INSERT INTO profile_sections
					(profile_id, section, rev, hash, fingerprint, ordinal, payload, writer, updated_at, deleted)
				SELECT ?, ?, 1, ?, ?, ?, ?, ?, ?, 0
				WHERE EXISTS (SELECT 1 FROM profiles WHERE id = ?)
				ON CONFLICT(profile_id, section) DO NOTHING`,
				profileID, in.Section, in.Hash, in.Fingerprint, in.Ordinal, payload, in.Writer, now,
				profileID)
			if err != nil {
				return PutResult{}, err
			}
			if n == 1 {
				return PutResult{Outcome: PutApplied, Rev: 1, Changed: true}, nil
			}
			// Zero rows has two causes: no such profile, or another writer
			// inserted the section first. The first is an error; the second is
			// classified against that writer's row by the next attempt.
			if err := s.requireProfile(profileID); err != nil {
				return PutResult{}, err
			}

		case row.deleted:
			// Step 1, over a tombstone: CAS on the tombstone's own rev. The
			// shape columns are overwritten outright, not MAX()ed.
			if baseRev != 0 {
				return PutResult{Outcome: PutConflict}, nil
			}
			n, err := s.execRows("recreate section", `
				UPDATE profile_sections
				SET deleted = 0, rev = rev + 1, hash = ?, fingerprint = ?, ordinal = ?,
				    payload = ?, writer = ?, updated_at = ?
				WHERE profile_id = ? AND section = ? AND rev = ? AND deleted = 1`,
				in.Hash, in.Fingerprint, in.Ordinal, payload, in.Writer, now,
				profileID, in.Section, row.Rev)
			if err != nil {
				return PutResult{}, err
			}
			if n == 1 {
				return PutResult{Outcome: PutApplied, Rev: row.Rev + 1, Changed: true}, nil
			}

		default:
			// Step 2 — before any look at the revision.
			if row.Fingerprint != in.Fingerprint && in.Ordinal <= row.Ordinal {
				return PutResult{
					Outcome:            PutSchema,
					Rev:                row.Rev,
					CurrentFingerprint: row.Fingerprint,
					CurrentOrdinal:     row.Ordinal,
				}, nil
			}
			if row.Rev != baseRev {
				if row.Hash == in.Hash {
					// Step 4: 驗算，沒改變就不寫回 — rev and updated_at stay put.
					return PutResult{Outcome: PutConverged, Rev: row.Rev}, nil
				}
				// Step 5.
				current := row.Section
				return PutResult{Outcome: PutConflict, Rev: row.Rev, Current: &current}, nil
			}
			// Step 3. ordinal = MAX(ordinal, ?): with equal fingerprints an
			// older client may write (§4.5 row 1), but its lower ordinal must
			// not replace the stored one, or the next value-domain bump loses
			// its direction signal. With different fingerprints step 2 already
			// guaranteed in.Ordinal > row.Ordinal, so MAX is the incoming value.
			n, err := s.execRows("update section", `
				UPDATE profile_sections
				SET rev = rev + 1, hash = ?, fingerprint = ?, ordinal = MAX(ordinal, ?),
				    payload = ?, writer = ?, updated_at = ?
				WHERE profile_id = ? AND section = ? AND rev = ? AND deleted = 0`,
				in.Hash, in.Fingerprint, in.Ordinal, payload, in.Writer, now,
				profileID, in.Section, baseRev)
			if err != nil {
				return PutResult{}, err
			}
			if n == 1 {
				return PutResult{Outcome: PutApplied, Rev: baseRev + 1, Changed: true}, nil
			}
		}
		// The conditional write missed: somebody else committed to this row
		// between our read and our statement. Read again and classify.
	}
	return PutResult{}, ErrSectionContended
}

// DeleteSection is the compare-and-set delete of spec §4.6.3. writer is the
// client performing it and is recorded on the tombstone.
//
//   - the section is live at baseRev → tombstoned, PutApplied with the
//     tombstone's rev;
//   - live at another rev → PutConflict with the current row;
//   - already a tombstone, or never existed → PutApplied (idempotent: two
//     clients removing the same workspace must not deadlock each other). Rev is
//     the tombstone's, or 0 when there never was a row.
//
// It returns ErrProfileNotFound when there is no row and no such profile —
// which includes a profile deleted concurrently: DeleteProfile sweeps the rows
// in the same transaction that removes the profile, so the tombstone UPDATE
// cannot land on a row that is about to disappear.
//
// # Why a tombstone and not a real DELETE
//
// Removing the row lets the revision counter restart, which opens an ABA hole
// straight through the CAS:
//
//	A: DELETE tabs.w1 baseRev=1  → row removed, but A never sees the response
//	B: PUT    tabs.w1 baseRev=0  → section recreated … at rev 1 again
//	A: DELETE tabs.w1 baseRev=1  → (retry) rev matches — B's content is destroyed
//
// With a tombstone the delete moves the row to rev 2, B's recreate lands at
// rev 3, and A's retry compares 1 against 3: PutConflict, B's content intact.
// A recreated row is always live at tombstone.rev + 1, which can never equal
// any baseRev handed out before the delete — and that is also what makes the
// idempotent arm above safe. Revisions of a (profile, section) pair are
// therefore strictly increasing for the life of the profile, which spec
// §4.6.1's "rev < baseRev ⇒ the profile was recreated" relies on.
//
// The tombstone drops the payload and hash but keeps the row; it is invisible
// to GetSection / ListSections and is removed with its profile.
func (s *Store) DeleteSection(profileID, section, writer string, baseRev int64) (PutResult, error) {
	n, err := s.execRows("delete section", `
		UPDATE profile_sections
		SET deleted = 1, rev = rev + 1, payload = '{}', hash = '', writer = ?, updated_at = ?
		WHERE profile_id = ? AND section = ? AND rev = ? AND deleted = 0`,
		writer, s.now(), profileID, section, baseRev)
	if err != nil {
		return PutResult{}, err
	}
	if n == 1 {
		return PutResult{Outcome: PutApplied, Rev: baseRev + 1, Changed: true}, nil
	}

	// The statement already declined; this read only classifies why (see
	// PutSection on why a stale classification is harmless).
	row, found, err := s.readSectionRow(profileID, section)
	if err != nil {
		return PutResult{}, err
	}
	switch {
	case !found:
		if err := s.requireProfile(profileID); err != nil {
			return PutResult{}, err
		}
		return PutResult{Outcome: PutApplied}, nil
	case row.deleted:
		return PutResult{Outcome: PutApplied, Rev: row.Rev}, nil
	default:
		current := row.Section
		return PutResult{Outcome: PutConflict, Rev: row.Rev, Current: &current}, nil
	}
}

// GetSection returns the live section. A tombstone reads as absent.
func (s *Store) GetSection(profileID, section string) (Section, bool, error) {
	row, found, err := s.readSectionRow(profileID, section)
	if err != nil || !found || row.deleted {
		return Section{}, false, err
	}
	return row.Section, true, nil
}

// ListSections returns the index of profileID's live sections, by name,
// without payloads. It never returns a nil slice, including for an unknown
// profile. Tombstones are omitted.
func (s *Store) ListSections(profileID string) ([]SectionMeta, error) {
	rows, err := s.db.Query(`
		SELECT section, rev, hash, fingerprint, ordinal, writer, updated_at
		FROM profile_sections
		WHERE profile_id = ? AND deleted = 0
		ORDER BY section ASC`, profileID)
	if err != nil {
		return nil, fmt.Errorf("list sections: %w", err)
	}
	defer rows.Close()

	out := []SectionMeta{}
	for rows.Next() {
		var m SectionMeta
		if err := rows.Scan(&m.Section, &m.Rev, &m.Hash, &m.Fingerprint, &m.Ordinal, &m.Writer, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sections: %w", err)
	}
	return out, nil
}

// execRows runs one write statement and returns RowsAffected — the verdict of
// every conditional write in this file.
func (s *Store) execRows(what, query string, args ...any) (int64, error) {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("%s rows affected: %w", what, err)
	}
	return n, nil
}
