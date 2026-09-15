package devicestate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock returns a clock func whose value is controlled by the returned pointer.
func fakeClock(start int64) (*int64, func() int64) {
	v := start
	return &v, func() int64 { return v }
}

func openTestStore(t *testing.T) (*Store, *int64) {
	t.Helper()
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	clock, fn := fakeClock(1000)
	s.now = fn
	return s, clock
}

func rec(clientID string, capturedAt int64, name string, ws, tabs int, payload string) Record {
	return Record{
		ClientID:       clientID,
		DeviceName:     name,
		AppVersion:     "1.0.0",
		CapturedAt:     capturedAt,
		WorkspaceCount: ws,
		TabCount:       tabs,
		Payload:        json.RawMessage(payload),
	}
}

func TestStoreUpsertInsert(t *testing.T) {
	s, _ := openTestStore(t)

	stored, err := s.Upsert(rec("c_000000000001", 500, "Mac", 2, 3, `{"version":1}`))
	require.NoError(t, err)
	assert.True(t, stored)

	got, found, err := s.Get("c_000000000001")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "c_000000000001", got.ClientID)
	assert.Equal(t, "Mac", got.DeviceName)
	assert.Equal(t, "1.0.0", got.AppVersion)
	assert.Equal(t, int64(500), got.CapturedAt)
	assert.Equal(t, int64(1000), got.UpdatedAt)
	assert.Equal(t, 2, got.WorkspaceCount)
	assert.Equal(t, 3, got.TabCount)
	assert.JSONEq(t, `{"version":1}`, string(got.Payload))
}

func TestStoreUpsertOverwriteNewer(t *testing.T) {
	s, clock := openTestStore(t)

	_, err := s.Upsert(rec("c_000000000001", 500, "Mac", 1, 1, `{"a":1}`))
	require.NoError(t, err)

	*clock = 2000
	stored, err := s.Upsert(rec("c_000000000001", 600, "Mac2", 4, 5, `{"a":2}`))
	require.NoError(t, err)
	assert.True(t, stored)

	got, found, err := s.Get("c_000000000001")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Mac2", got.DeviceName)
	assert.Equal(t, int64(600), got.CapturedAt)
	assert.Equal(t, int64(2000), got.UpdatedAt)
	assert.Equal(t, 4, got.WorkspaceCount)
	assert.Equal(t, 5, got.TabCount)
	assert.JSONEq(t, `{"a":2}`, string(got.Payload))
}

func TestStoreUpsertEqualCapturedAtOverwrites(t *testing.T) {
	s, clock := openTestStore(t)

	_, err := s.Upsert(rec("c_000000000001", 500, "Mac", 1, 1, `{"a":1}`))
	require.NoError(t, err)

	*clock = 3000
	stored, err := s.Upsert(rec("c_000000000001", 500, "Retry", 2, 2, `{"a":2}`))
	require.NoError(t, err)
	assert.True(t, stored)

	got, _, err := s.Get("c_000000000001")
	require.NoError(t, err)
	assert.Equal(t, "Retry", got.DeviceName)
	assert.Equal(t, int64(3000), got.UpdatedAt)
	assert.JSONEq(t, `{"a":2}`, string(got.Payload))
}

func TestStoreUpsertStaleNotStored(t *testing.T) {
	s, clock := openTestStore(t)

	_, err := s.Upsert(rec("c_000000000001", 500, "Mac", 1, 7, `{"a":1}`))
	require.NoError(t, err)

	*clock = 4000
	stored, err := s.Upsert(rec("c_000000000001", 499, "Stale", 9, 9, `{"a":"stale"}`))
	require.NoError(t, err)
	assert.False(t, stored)

	got, found, err := s.Get("c_000000000001")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Mac", got.DeviceName)
	assert.Equal(t, int64(500), got.CapturedAt)
	assert.Equal(t, int64(1000), got.UpdatedAt)
	assert.Equal(t, 1, got.WorkspaceCount)
	assert.Equal(t, 7, got.TabCount)
	assert.JSONEq(t, `{"a":1}`, string(got.Payload))
}

func TestStoreListOrderAndNoPayload(t *testing.T) {
	s, clock := openTestStore(t)

	*clock = 100
	_, err := s.Upsert(rec("c_00000000000a", 1, "A", 1, 1, `{"x":"a"}`))
	require.NoError(t, err)
	*clock = 300
	_, err = s.Upsert(rec("c_00000000000b", 1, "B", 2, 2, `{"x":"b"}`))
	require.NoError(t, err)
	*clock = 200
	_, err = s.Upsert(rec("c_00000000000c", 1, "C", 3, 3, `{"x":"c"}`))
	require.NoError(t, err)

	list, err := s.List()
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, "c_00000000000b", list[0].ClientID)
	assert.Equal(t, "c_00000000000c", list[1].ClientID)
	assert.Equal(t, "c_00000000000a", list[2].ClientID)
	assert.Equal(t, int64(300), list[0].UpdatedAt)
	assert.Equal(t, "B", list[0].DeviceName)
	assert.Equal(t, 2, list[0].TabCount)
	for _, r := range list {
		assert.Nil(t, r.Payload, "list must not include payload")
	}

	b, err := json.Marshal(list)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "payload")
}

func TestStoreListEmptyNonNil(t *testing.T) {
	s, _ := openTestStore(t)

	list, err := s.List()
	require.NoError(t, err)
	require.NotNil(t, list)
	assert.Len(t, list, 0)

	b, err := json.Marshal(list)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

func TestStoreGetNotFound(t *testing.T) {
	s, _ := openTestStore(t)

	_, found, err := s.Get("c_ffffffffffff")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestStoreDelete(t *testing.T) {
	s, _ := openTestStore(t)

	_, err := s.Upsert(rec("c_000000000001", 500, "Mac", 1, 1, `{}`))
	require.NoError(t, err)

	require.NoError(t, s.Delete("c_000000000001"))
	_, found, err := s.Get("c_000000000001")
	require.NoError(t, err)
	assert.False(t, found)

	// Missing row: still no error.
	require.NoError(t, s.Delete("c_000000000001"))
	require.NoError(t, s.Delete("c_ffffffffffff"))
}

func TestOpenStoreDefaultClock(t *testing.T) {
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	require.NotNil(t, s.now)
	assert.Greater(t, s.now(), int64(1_700_000_000_000))
}
