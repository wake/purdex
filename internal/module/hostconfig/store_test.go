package hostconfig

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	s.now = func() int64 { return 1000 }
	return s
}

// raw returns a build callback yielding v.
func raw(v string) func() (json.RawMessage, error) {
	return func() (json.RawMessage, error) { return json.RawMessage(v), nil }
}

func TestStoreGetMissingIsRevisionZero(t *testing.T) {
	s := openTestStore(t)
	e, err := s.Get(KeyProjects)
	require.NoError(t, err)
	assert.Nil(t, e.Value)
	assert.Equal(t, int64(0), e.Revision)
}

func TestStorePutFirstWriteRevisionOne(t *testing.T) {
	s := openTestStore(t)
	e, ok, err := s.Put(KeyProjects, 0, raw(`[{"id":"a"}]`))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, int64(1), e.Revision)
	assert.JSONEq(t, `[{"id":"a"}]`, string(e.Value))

	got, err := s.Get(KeyProjects)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.Revision)
	assert.JSONEq(t, `[{"id":"a"}]`, string(got.Value))
	assert.Equal(t, int64(1000), got.UpdatedAt)
}

func TestStorePutConflictLeavesRowUntouched(t *testing.T) {
	s := openTestStore(t)
	_, ok, err := s.Put(KeyCommands, 0, raw(`[1]`))
	require.NoError(t, err)
	require.True(t, ok)

	cur, ok, err := s.Put(KeyCommands, 0, raw(`[2]`)) // stale base
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, int64(1), cur.Revision)
	assert.JSONEq(t, `[1]`, string(cur.Value))

	next, ok, err := s.Put(KeyCommands, 1, raw(`[3]`))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int64(2), next.Revision)
}

func TestStorePutConflictDoesNotCallBuild(t *testing.T) {
	s := openTestStore(t)
	_, ok, err := s.Put(KeyCommands, 0, raw(`[1]`))
	require.NoError(t, err)
	require.True(t, ok)

	called := false
	cur, ok, err := s.Put(KeyCommands, 0, func() (json.RawMessage, error) {
		called = true
		return nil, errors.New("invalid")
	})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.False(t, called, "build must not run on revision mismatch")
	assert.Equal(t, int64(1), cur.Revision)
	assert.JSONEq(t, `[1]`, string(cur.Value))
}

func TestStorePutBuildErrorIsValidationErrorAndRollsBack(t *testing.T) {
	s := openTestStore(t)
	_, ok, err := s.Put(KeyCommands, 0, raw(`[1]`))
	require.NoError(t, err)
	require.True(t, ok)

	_, ok, err = s.Put(KeyCommands, 1, func() (json.RawMessage, error) {
		return nil, errors.New("bad slug")
	})
	require.Error(t, err)
	assert.False(t, ok)
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "bad slug", ve.Error())

	got, err := s.Get(KeyCommands)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.Revision)
	assert.JSONEq(t, `[1]`, string(got.Value))

	// The tx was released: a following write still succeeds.
	next, ok, err := s.Put(KeyCommands, 1, raw(`[2]`))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int64(2), next.Revision)
}

func TestStoreKeysAreIndependent(t *testing.T) {
	s := openTestStore(t)
	_, _, err := s.Put(KeyProjects, 0, raw(`[]`))
	require.NoError(t, err)
	e, err := s.Get(KeyResumeTemplates)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision)
}

// Two writers from the same baseRevision against a real WAL file with a
// multi-connection pool: exactly one wins, the other gets a clean conflict
// carrying the winner's state — never an error.
func TestStorePutConcurrentSameBaseOneWins(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "hc.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	// The first reader stalls between read and write. With a deferred tx the
	// second writer would read revision 0 in that window; with BEGIN IMMEDIATE
	// it waits on the lock and then reads the committed revision 1.
	var once sync.Once
	s.afterRead = func() {
		once.Do(func() { time.Sleep(50 * time.Millisecond) })
	}

	type result struct {
		entry Entry
		ok    bool
		err   error
	}
	results := make(chan result, 2)
	var builds atomic.Int32
	for _, v := range []string{`["a"]`, `["b"]`} {
		go func(v string) {
			e, ok, err := s.Put(KeyProjects, 0, func() (json.RawMessage, error) {
				builds.Add(1)
				return json.RawMessage(v), nil
			})
			results <- result{e, ok, err}
		}(v)
	}

	var wins, conflicts int
	for i := 0; i < 2; i++ {
		r := <-results
		require.NoError(t, r.err)
		if r.ok {
			wins++
			assert.Equal(t, int64(1), r.entry.Revision)
		} else {
			conflicts++
			assert.Equal(t, int64(1), r.entry.Revision)
		}
	}
	assert.Equal(t, 1, wins)
	assert.Equal(t, 1, conflicts)
	assert.Equal(t, int32(1), builds.Load(), "only the winner builds its value")
}
