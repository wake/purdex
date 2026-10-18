package hostconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/team"
)

func eventRows(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT at, enabled, by_json FROM unattended_events ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var at int64
		var on int
		var by string
		require.NoError(t, rows.Scan(&at, &on, &by))
		out = append(out, fmt.Sprintf("%d/%d/%s", at, on, by))
	}
	require.NoError(t, rows.Err())
	return out
}

// PutWith: the callback runs on the very connection of the CAS transaction (BEGIN IMMEDIATE on a dedicated *sql.Conn, not a
// *sql.Tx), after the revision matched and before the commit; a failing callback takes the whole write back.
// Mutation gate: run the callback after the commit -> the failing callback leaves the key written -> red; call it on a stale
// base -> red.
func TestStore_PutWithRunsInTheTransactionOfThePut(t *testing.T) {
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	build := func(v string) func(Entry) (json.RawMessage, error) {
		return func(Entry) (json.RawMessage, error) { return json.RawMessage(v), nil }
	}

	called := 0
	_, ok, err := s.PutWith("k", 0, build(`1`), func(q Execer) error {
		called++
		_, err := q.ExecContext(context.Background(), `INSERT INTO unattended_events (at, enabled, by_json) VALUES (1, 1, '{}')`)
		return err
	})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 1, called)
	assert.Len(t, eventRows(t, s), 1)

	// a failing callback: the value and what the callback wrote both stay as they were
	_, ok, err = s.PutWith("k", 1, build(`2`), func(q Execer) error {
		if _, err := q.ExecContext(context.Background(), `INSERT INTO unattended_events (at, enabled, by_json) VALUES (2, 0, '{}')`); err != nil {
			return err
		}
		return errors.New("the event could not be written")
	})
	require.Error(t, err)
	assert.False(t, ok)
	e, err := s.Get("k")
	require.NoError(t, err)
	assert.JSONEq(t, `1`, string(e.Value), "the value was written although the callback failed")
	assert.EqualValues(t, 1, e.Revision)
	assert.Len(t, eventRows(t, s), 1, "what the callback wrote was kept although it failed")

	// a stale base is a 409 without the callback
	called = 0
	cur, ok, err := s.PutWith("k", 0, build(`3`), func(Execer) error { called++; return nil })
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, called, "the callback ran for a write that did not match the revision")
	assert.EqualValues(t, 1, cur.Revision)

	// a callback can read what the transaction holds
	_, ok, err = s.PutWith("k", 1, build(`4`), func(q Execer) error {
		var n int
		if err := q.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM unattended_events`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("the callback saw %d events", n)
		}
		return nil
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

// A switch change and its record commit together: a record that cannot be written leaves the switch where it was.
// Mutation gate: write the record outside the transaction -> the switch changes although the record failed -> red.
func TestSetUnattended_TheRecordAndTheStateCommitTogether(t *testing.T) {
	m := newTestModule(t)
	_, changed, err := m.SetUnattended(true, appAir26, 1000)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, []string{`1000/1/{"kind":"app","label":"Purdex.app @ air26"}`}, eventRows(t, m.store), "the record carries no address")

	_, err = m.store.db.Exec(`DROP TABLE unattended_events`)
	require.NoError(t, err)
	_, _, err = m.SetUnattended(false, appA19, 2000)
	require.Error(t, err, "a record that cannot be written must fail the change")
	st, err := m.Unattended()
	require.NoError(t, err)
	assert.True(t, st.On, "the switch changed although its record failed")
	assert.EqualValues(t, 1000, st.ChangedAt)
}

func TestSetUnattended_TheSameValueAgainRecordsNothing(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.SetUnattended(true, appAir26, 1000)
	require.NoError(t, err)
	_, changed, err := m.SetUnattended(true, appA19, 2000)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Len(t, eventRows(t, m.store), 1)
}

// The record keeps the newest 100 rows, oldest first out.
// Mutation gate: no trim -> 105 rows -> red; trim the newest -> red.
func TestSetUnattended_TheRecordKeepsTheNewest100(t *testing.T) {
	m := newTestModule(t)
	for i := 1; i <= 105; i++ {
		_, changed, err := m.SetUnattended(i%2 == 1, appAir26, int64(i*10))
		require.NoError(t, err)
		require.True(t, changed)
	}
	rows := eventRows(t, m.store)
	require.Len(t, rows, 100)
	assert.Contains(t, rows[0], "60/", "the oldest kept row is the 6th change")
	assert.Contains(t, rows[99], "1050/", "the newest row is kept")
}

// UnattendedEvents answers the newest first, at most limit.
func TestUnattendedEvents_NewestFirstAndLimited(t *testing.T) {
	m := newTestModule(t)
	got, err := m.UnattendedEvents(30)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)

	for i, on := range []bool{true, false, true} {
		_, _, err := m.SetUnattended(on, appAir26, int64((i+1)*100))
		require.NoError(t, err)
	}
	got, err = m.UnattendedEvents(30)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []team.UnattendedToggle{
		{At: 300, On: true, By: team.Client{Kind: "app", Label: appAir26.Label}},
		{At: 200, On: false, By: team.Client{Kind: "app", Label: appAir26.Label}},
		{At: 100, On: true, By: team.Client{Kind: "app", Label: appAir26.Label}},
	}, got)
	got, err = m.UnattendedEvents(2)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.EqualValues(t, 300, got[0].At)
	_, err = m.UnattendedEvents(0)
	assert.Error(t, err, "a limit that is not positive")
}

// Overwriting a stored value that does not read (the person's explicit choice) still records the change.
func TestSetUnattended_OverwritingAnUnreadableValueIsRecorded(t *testing.T) {
	m := newTestModule(t)
	writeRaw(t, m, KeyUnattended, `{"on":true}`)
	_, changed, err := m.SetUnattended(false, appAir26, 500)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{`500/0/{"kind":"app","label":"Purdex.app @ air26"}`}, eventRows(t, m.store))
}

// Two switches racing: both land by CAS (the loser re-reads and tries again), and each change leaves exactly one record,
// whatever the interleaving.
// Mutation gate: record before the CAS (outside PutWith) -> a lost race leaves a record without a change -> red.
func TestSetUnattended_RacingWritersRecordEachChangeOnce(t *testing.T) {
	m := newTestModule(t)
	fired := false
	m.beforeUnattendedPut = func() {
		if fired {
			return
		}
		fired = true // another writer gets in between the read and the CAS, once
		_, _, err := m.SetUnattended(true, appA19, 700)
		assert.NoError(t, err)
	}
	st, changed, err := m.SetUnattended(true, appAir26, 800)
	require.NoError(t, err)
	assert.False(t, changed, "the loser finds the switch already on: nothing to write")
	assert.EqualValues(t, 700, st.Since)
	assert.Equal(t, []string{`700/1/{"kind":"app","label":"Purdex.app @ air19"}`}, eventRows(t, m.store), "one change, one record")
}

// The migration: a database from before this table gets it, and opening twice is fine.
func TestOpenStore_CreatesTheEventsTableOnAnOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host_config.db")
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = old.Exec(`CREATE TABLE host_config (key TEXT PRIMARY KEY, value TEXT NOT NULL, revision INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		INSERT INTO host_config VALUES ('unattended', '{"on":true,"since":5,"changed_at":5,"changed_by":{"kind":"app","label":"x"}}', 3, 5)`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	for i := 0; i < 2; i++ {
		s, err := OpenStore(path)
		require.NoError(t, err)
		require.Empty(t, eventRows(t, s), "there is no backfill: history before this version is not known")
		e, err := s.Get(KeyUnattended)
		require.NoError(t, err)
		assert.EqualValues(t, 3, e.Revision, "the existing rows are untouched")
		require.NoError(t, s.Close())
	}
}

// A copy of the live host_config.db (`sqlite3 <live> ".backup <copy>"`) opens, keeps its rows and gains the table. Gated:
// set PDX_HOSTCONFIG_COPY to the copy's path; the copy is never the live file.
func TestOpenStore_OpensACopyOfTheLiveDatabase(t *testing.T) {
	path := os.Getenv("PDX_HOSTCONFIG_COPY")
	if path == "" {
		t.Skip("PDX_HOSTCONFIG_COPY is not set")
	}
	s, err := OpenStore(path)
	require.NoError(t, err)
	defer s.Close()
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM host_config`).Scan(&n))
	t.Logf("the copy opens: %d host_config rows, %d event rows", n, len(eventRows(t, s)))
	_, err = s.Get(KeyUnattended)
	require.NoError(t, err)
}
