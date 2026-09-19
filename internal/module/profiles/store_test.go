package profiles

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock returns a clock func whose value is controlled by the returned pointer.
func fakeClock(start int64) (*int64, func() int64) {
	v := start
	return &v, func() int64 { return v }
}

// seqIDs returns a deterministic id source: p_000000000001, p_000000000002, …
func seqIDs() func() (string, error) {
	n := 0
	return func() (string, error) {
		n++
		return fmt.Sprintf("p_%012x", n), nil
	}
}

func openTestStore(t *testing.T) (*Store, *int64) {
	t.Helper()
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	clock, fn := fakeClock(1000)
	s.now = fn
	s.newID = seqIDs()
	return s, clock
}

func att(clientID, profileID, name string) Attachment {
	return Attachment{ClientID: clientID, ProfileID: profileID, DeviceName: name}
}

// insertRawSection writes a profile_sections row directly, so the DeleteProfile
// test depends on nothing but the schema. The same behaviour through the real
// section API is TestDeleteProfileRemovesSectionsAndTombstones (sections_test.go).
func insertRawSection(t *testing.T, s *Store, profileID, section string, deleted int) {
	t.Helper()
	_, err := s.db.Exec(`
		INSERT INTO profile_sections
			(profile_id, section, rev, hash, fingerprint, ordinal, payload, writer, updated_at, deleted)
		VALUES (?, ?, 1, 'h', 'f', 1, '{}', 'c_000000000001', 1, ?)`,
		profileID, section, deleted)
	require.NoError(t, err)
}

func countRows(t *testing.T, s *Store, table, where string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+where, args...).Scan(&n))
	return n
}

func TestStoreMigrateCreatesAllTables(t *testing.T) {
	s, _ := openTestStore(t)

	for _, table := range []string{"profiles", "profile_sections", "profile_attachments"} {
		assert.Equal(t, 1, countRows(t, s, "sqlite_master", "type = 'table' AND name = ?", table), table)
	}
	// The tombstone column is part of the P1 schema even though Task 1 never writes it.
	assert.Equal(t, 1, countRows(t, s, "pragma_table_info('profile_sections')",
		"name = 'deleted' AND \"notnull\" = 1 AND dflt_value = '0'"))
	// migrate is idempotent.
	require.NoError(t, s.migrate())
}

func TestStoreDefaultIDShape(t *testing.T) {
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	a, err := s.CreateProfile("A")
	require.NoError(t, err)
	b, err := s.CreateProfile("B")
	require.NoError(t, err)

	assert.Regexp(t, `^p_[0-9a-f]{12}$`, a.ID)
	assert.Regexp(t, `^p_[0-9a-f]{12}$`, b.ID)
	assert.NotEqual(t, a.ID, b.ID)
	assert.Greater(t, a.CreatedAt, int64(0))
}

func TestStoreCreateGetListRoundTrip(t *testing.T) {
	s, clock := openTestStore(t)

	empty, err := s.ListProfiles()
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)

	a, err := s.CreateProfile("Work")
	require.NoError(t, err)
	assert.Equal(t, Profile{ID: "p_000000000001", Name: "Work", CreatedAt: 1000, UpdatedAt: 1000}, a)

	*clock = 2000
	// Names may repeat (spec decision 15); the id is what distinguishes them.
	b, err := s.CreateProfile("Work")
	require.NoError(t, err)
	assert.Equal(t, "p_000000000002", b.ID)

	got, found, err := s.GetProfile(a.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, a, got)

	_, found, err = s.GetProfile("p_ffffffffffff")
	require.NoError(t, err)
	assert.False(t, found)

	list, err := s.ListProfiles()
	require.NoError(t, err)
	assert.Equal(t, []Profile{a, b}, list)
}

func TestStoreRenameProfile(t *testing.T) {
	s, clock := openTestStore(t)
	p, err := s.CreateProfile("Old")
	require.NoError(t, err)

	*clock = 5000
	ok, err := s.RenameProfile(p.ID, "New")
	require.NoError(t, err)
	assert.True(t, ok)

	got, found, err := s.GetProfile(p.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "New", got.Name)
	assert.Equal(t, int64(1000), got.CreatedAt)
	assert.Equal(t, int64(5000), got.UpdatedAt)
}

func TestStoreRenameUnknownProfile(t *testing.T) {
	s, _ := openTestStore(t)

	ok, err := s.RenameProfile("p_ffffffffffff", "X")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, 0, countRows(t, s, "profiles", "1 = 1"))
}

func TestStoreDeleteProfileWithAttachment(t *testing.T) {
	s, _ := openTestStore(t)
	p, err := s.CreateProfile("P")
	require.NoError(t, err)
	require.NoError(t, s.PutAttachment(att("c_000000000001", p.ID, "Mac")))

	err = s.DeleteProfile(p.ID)
	assert.ErrorIs(t, err, ErrProfileAttached)

	_, found, err := s.GetProfile(p.ID)
	require.NoError(t, err)
	assert.True(t, found, "a refused delete must leave the profile in place")
}

func TestStoreDeleteProfileAfterDetachRemovesSections(t *testing.T) {
	s, _ := openTestStore(t)
	p, err := s.CreateProfile("P")
	require.NoError(t, err)
	other, err := s.CreateProfile("Other")
	require.NoError(t, err)

	insertRawSection(t, s, p.ID, "hosts", 0)
	insertRawSection(t, s, p.ID, "tabs.w1", 1) // tombstones go too
	insertRawSection(t, s, other.ID, "hosts", 0)
	require.NoError(t, s.PutAttachment(att("c_000000000001", p.ID, "Mac")))

	removed, err := s.DeleteAttachment(p.ID, "c_000000000001")
	require.NoError(t, err)
	require.True(t, removed)

	require.NoError(t, s.DeleteProfile(p.ID))

	_, found, err := s.GetProfile(p.ID)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, 0, countRows(t, s, "profile_sections", "profile_id = ?", p.ID))
	assert.Equal(t, 1, countRows(t, s, "profile_sections", "profile_id = ?", other.ID),
		"another profile's sections must survive")
}

func TestStoreDeleteUnknownProfile(t *testing.T) {
	s, _ := openTestStore(t)

	assert.ErrorIs(t, s.DeleteProfile("p_ffffffffffff"), ErrProfileNotFound)
}

func TestStorePutAttachmentInsert(t *testing.T) {
	s, clock := openTestStore(t)
	p, err := s.CreateProfile("P")
	require.NoError(t, err)

	*clock = 3000
	// Caller-supplied timestamps are ignored; the store clock is authoritative.
	require.NoError(t, s.PutAttachment(Attachment{
		ClientID: "c_000000000001", ProfileID: p.ID, DeviceName: "Mac", AttachedAt: 7, LastSeen: 7,
	}))

	list, err := s.ListAttachments(p.ID)
	require.NoError(t, err)
	assert.Equal(t, []Attachment{{
		ClientID: "c_000000000001", ProfileID: p.ID, DeviceName: "Mac", AttachedAt: 3000, LastSeen: 3000,
	}}, list)
}

func TestStorePutAttachmentTwiceKeepsAttachedAt(t *testing.T) {
	s, clock := openTestStore(t)
	p, err := s.CreateProfile("P")
	require.NoError(t, err)

	require.NoError(t, s.PutAttachment(att("c_000000000001", p.ID, "Mac")))
	*clock = 4000
	require.NoError(t, s.PutAttachment(att("c_000000000001", p.ID, "Mac renamed")))

	list, err := s.ListAttachments(p.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Mac renamed", list[0].DeviceName)
	assert.Equal(t, int64(1000), list[0].AttachedAt, "attached_at is set on insert only")
	assert.Equal(t, int64(4000), list[0].LastSeen)
}

func TestStorePutAttachmentMovesClientBetweenProfiles(t *testing.T) {
	s, _ := openTestStore(t)
	a, err := s.CreateProfile("A")
	require.NoError(t, err)
	b, err := s.CreateProfile("B")
	require.NoError(t, err)

	require.NoError(t, s.PutAttachment(att("c_000000000001", a.ID, "Mac")))
	require.NoError(t, s.PutAttachment(att("c_000000000001", b.ID, "Mac")))

	assert.Equal(t, 1, countRows(t, s, "profile_attachments", "1 = 1"), "a client has at most one master")

	listA, err := s.ListAttachments(a.ID)
	require.NoError(t, err)
	assert.Empty(t, listA)
	listB, err := s.ListAttachments(b.ID)
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, b.ID, listB[0].ProfileID)

	// A is free again, B is now the pinned one.
	assert.NoError(t, s.DeleteProfile(a.ID))
	assert.ErrorIs(t, s.DeleteProfile(b.ID), ErrProfileAttached)
}

func TestStorePutAttachmentUnknownProfile(t *testing.T) {
	s, _ := openTestStore(t)

	err := s.PutAttachment(att("c_000000000001", "p_ffffffffffff", "Mac"))
	assert.ErrorIs(t, err, ErrProfileNotFound)
	assert.Equal(t, 0, countRows(t, s, "profile_attachments", "1 = 1"))
}

func TestStorePutAttachmentUnknownProfileKeepsExistingRow(t *testing.T) {
	s, _ := openTestStore(t)
	p, err := s.CreateProfile("P")
	require.NoError(t, err)
	require.NoError(t, s.PutAttachment(att("c_000000000001", p.ID, "Mac")))

	// Re-pointing an attached client at a profile that does not exist must not
	// move (or touch) the row it already has.
	err = s.PutAttachment(att("c_000000000001", "p_ffffffffffff", "Mac2"))
	assert.ErrorIs(t, err, ErrProfileNotFound)

	list, err := s.ListAttachments(p.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Mac", list[0].DeviceName)
}

func TestStoreDeleteAttachmentWrongProfile(t *testing.T) {
	s, _ := openTestStore(t)
	a, err := s.CreateProfile("A")
	require.NoError(t, err)
	b, err := s.CreateProfile("B")
	require.NoError(t, err)
	require.NoError(t, s.PutAttachment(att("c_000000000001", a.ID, "Mac")))

	removed, err := s.DeleteAttachment(b.ID, "c_000000000001")
	require.NoError(t, err)
	assert.False(t, removed)

	list, err := s.ListAttachments(a.ID)
	require.NoError(t, err)
	assert.Len(t, list, 1, "a detach aimed at the wrong profile is a no-op")
}

func TestStoreDeleteAttachment(t *testing.T) {
	s, _ := openTestStore(t)
	p, err := s.CreateProfile("P")
	require.NoError(t, err)
	require.NoError(t, s.PutAttachment(att("c_000000000001", p.ID, "Mac")))

	removed, err := s.DeleteAttachment(p.ID, "c_000000000001")
	require.NoError(t, err)
	assert.True(t, removed)

	removed, err = s.DeleteAttachment(p.ID, "c_000000000001")
	require.NoError(t, err)
	assert.False(t, removed, "detaching twice is not an error")
}

func TestStoreListAttachments(t *testing.T) {
	s, clock := openTestStore(t)
	p, err := s.CreateProfile("P")
	require.NoError(t, err)

	empty, err := s.ListAttachments(p.ID)
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)

	unknown, err := s.ListAttachments("p_ffffffffffff")
	require.NoError(t, err)
	assert.NotNil(t, unknown)

	require.NoError(t, s.PutAttachment(att("c_000000000002", p.ID, "Air")))
	*clock = 2000
	require.NoError(t, s.PutAttachment(att("c_000000000001", p.ID, "Mac")))

	list, err := s.ListAttachments(p.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "c_000000000002", list[0].ClientID, "oldest attachment first")
	assert.Equal(t, "c_000000000001", list[1].ClientID)
}

// TestStoreConcurrentDeleteProfileVsPutAttachment races the two writers that
// plan review #3 is about. It needs a file-backed database: ":memory:" is
// pinned to a single connection, which serialises everything in database/sql
// and cannot race.
func TestStoreConcurrentDeleteProfileVsPutAttachment(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "profiles.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	const rounds = 200
	var deleteWon, putWon int
	for i := 0; i < rounds; i++ {
		p, err := s.CreateProfile("race")
		require.NoError(t, err)
		clientID := fmt.Sprintf("c_%012x", i)

		var delErr, putErr error
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			delErr = s.DeleteProfile(p.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			putErr = s.PutAttachment(att(clientID, p.ID, "Mac"))
		}()
		close(start)
		wg.Wait()

		_, profileExists, err := s.GetProfile(p.ID)
		require.NoError(t, err)
		atts, err := s.ListAttachments(p.ID)
		require.NoError(t, err)

		switch {
		case delErr == nil:
			// Delete won: the attach must have been refused and left nothing.
			deleteWon++
			require.ErrorIs(t, putErr, ErrProfileNotFound, "round %d", i)
			require.False(t, profileExists, "round %d", i)
			require.Empty(t, atts, "round %d: attachment points at a deleted profile", i)
		case putErr == nil:
			// Attach won: the delete must have been refused.
			putWon++
			require.ErrorIs(t, delErr, ErrProfileAttached, "round %d", i)
			require.True(t, profileExists, "round %d", i)
			require.Len(t, atts, 1, "round %d", i)
		default:
			t.Fatalf("round %d: both writers failed: delete=%v put=%v", i, delErr, putErr)
		}
	}
	t.Logf("delete won %d, attach won %d of %d rounds", deleteWon, putWon, rounds)
}
