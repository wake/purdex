package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T) *SyncStore {
	t.Helper()
	s, err := OpenSyncStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

// TestSyncStorePushAndPullCanonical creates a group, adds a client, pushes a
// bundle, and verifies PullCanonical returns that bundle.
func TestSyncStorePushAndPullCanonical(t *testing.T) {
	s := openTestStore(t)

	groupID, err := generateGroupID()
	require.NoError(t, err)

	require.NoError(t, s.CreateGroup(groupID))
	require.NoError(t, s.AddClientToGroup(groupID, "client-1", "MacBook"))
	require.NoError(t, s.PushBundle("client-1", `{"theme":"dark","fontSize":14}`))

	bundle, err := s.PullCanonical("client-1")
	require.NoError(t, err)
	assert.NotEmpty(t, bundle)
	assert.Equal(t, `{"theme":"dark","fontSize":14}`, bundle)
}

// TestSyncStoreGroupIsolation verifies that a client in group2 cannot see data
// pushed by a client in group1.
func TestSyncStoreGroupIsolation(t *testing.T) {
	s := openTestStore(t)

	gid1, err := generateGroupID()
	require.NoError(t, err)
	gid2, err := generateGroupID()
	require.NoError(t, err)

	require.NoError(t, s.CreateGroup(gid1))
	require.NoError(t, s.CreateGroup(gid2))
	require.NoError(t, s.AddClientToGroup(gid1, "client-a", "DevA"))
	require.NoError(t, s.AddClientToGroup(gid2, "client-b", "DevB"))

	require.NoError(t, s.PushBundle("client-a", `{"group":"one"}`))

	bundle, err := s.PullCanonical("client-b")
	require.NoError(t, err)
	assert.Empty(t, bundle, "client-b must not see group1 data")
}

// TestSyncStorePairingCode creates a pairing code and verifies it returns the
// correct groupID on successful verification.
func TestSyncStorePairingCode(t *testing.T) {
	s := openTestStore(t)

	gid, err := generateGroupID()
	require.NoError(t, err)
	require.NoError(t, s.CreateGroup(gid))
	require.NoError(t, s.AddClientToGroup(gid, "owner-1", "Owner"))

	code, err := s.CreatePairingCode("owner-1")
	require.NoError(t, err)
	assert.Len(t, code, 8)

	returnedGroupID, err := s.VerifyPairingCode(code)
	require.NoError(t, err)
	assert.Equal(t, gid, returnedGroupID)
}

// TestSyncStorePairingCodeRateLimit verifies that after 5 failed verification
// attempts the pairing code is locked out and can no longer be verified.
func TestSyncStorePairingCodeRateLimit(t *testing.T) {
	s := openTestStore(t)

	gid, err := generateGroupID()
	require.NoError(t, err)
	require.NoError(t, s.CreateGroup(gid))
	require.NoError(t, s.AddClientToGroup(gid, "owner-rl", "RLDevice"))

	code, err := s.CreatePairingCode("owner-rl")
	require.NoError(t, err)

	// Simulate 5 failed attempts by passing the real code but with a
	// spoofed DB state — we can't do that through the API, so instead we
	// insert a second code row directly via a helper.  Actually, the cleanest
	// approach is to verify 5 times with a WRONG code that shares the same
	// row (not possible from outside), so we test the publicly observable
	// behaviour: each call to VerifyPairingCode with a non-existent or
	// over-attempted code returns an error.
	//
	// To properly test rate-limit we need to set attempts=4 before the 5th
	// call.  We do this by calling VerifyPairingCode(code) 4 times — but the
	// first successful call will delete the code and return success, making
	// further calls return "not found" (also an error, just a different one).
	// That still verifies the observable contract: code is single-use.
	//
	// For a true rate-limit test we manipulate the DB directly via the
	// exported db field — not available here.  Use a white-box approach:
	// call the internal incrementAttempts path by using a known-bad code
	// to exhaust attempts on a row we pre-seed via the public API, then
	// try the real code.
	//
	// Simplest TDD-valid test: use CreatePairingCode to get a real code,
	// then manually set its attempts to 4 via s.db (white-box).  The
	// SyncStore.db field is unexported but we're in the same package (sync),
	// so it IS accessible.
	// Pre-seed attempts=5 so that the very next call is over the limit.
	err = setAttempts(s, code, 5)
	require.NoError(t, err, "pre-seed attempts=5 in same-package white-box test")

	// attempts >= 5 → should be rejected immediately.
	_, err = s.VerifyPairingCode(code)
	assert.Error(t, err, "attempt with attempts=5 should be rejected")

	// 6th attempt: attempts >= 5 → also rejected.
	_, err = s.VerifyPairingCode(code)
	assert.Error(t, err, "6th attempt should still be rejected")
}

// setAttempts is a white-box helper (same package) to pre-seed the attempts
// counter for a pairing code row.
func setAttempts(s *SyncStore, code string, n int) error {
	_, err := s.db.Exec(`UPDATE sync_pairing SET attempts = ? WHERE code = ?`, n, code)
	return err
}

// TestSyncStoreHistory pushes 5 bundles and verifies ListHistory with limit 3
// returns exactly 3 entries with correct metadata.
func TestSyncStoreHistory(t *testing.T) {
	s := openTestStore(t)

	gid, err := generateGroupID()
	require.NoError(t, err)
	require.NoError(t, s.CreateGroup(gid))
	require.NoError(t, s.AddClientToGroup(gid, "client-h", "HistDevice"))

	for i := 0; i < 5; i++ {
		require.NoError(t, s.PushBundle("client-h", `{"n":`+string(rune('0'+i))+`}`))
	}

	entries, err := s.ListHistory("client-h", 3)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
	for _, e := range entries {
		assert.Equal(t, "client-h", e.ClientID)
		assert.Equal(t, "HistDevice", e.Device)
		assert.NotZero(t, e.Timestamp)
	}
}
