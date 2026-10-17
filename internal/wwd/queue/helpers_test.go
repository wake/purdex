package queue

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/wake/purdex/internal/wwd/wire"
)

// openDB is a real file database with the pragmas wwd.db is opened with (WAL, busy_timeout, foreign keys), because the queue's
// claim and supersede transactions only mean something against a file shared by several connections.
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "wwd.db") + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// fakeClock is the origin's clock: tests move it, nothing sleeps.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// events collects what Publish was told.
type events struct {
	mu   sync.Mutex
	list []wire.JobEvent
}

func (e *events) publish(ev wire.JobEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, ev)
}

func (e *events) all() []wire.JobEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]wire.JobEvent(nil), e.list...)
}

func (e *events) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = nil
}

type fixture struct {
	q   *Queue
	db  *sql.DB
	clk *fakeClock
	ev  *events
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := openDB(t)
	clk, ev := newClock(), &events{}
	require.NoError(t, Migrate(context.Background(), db))
	q, err := New(db, Options{Origin: "mlab", Now: clk.Now, Publish: ev.publish, LogDir: t.TempDir()})
	require.NoError(t, err)
	return &fixture{q: q, db: db, clk: clk, ev: ev}
}

// sub is a valid submission; edit changes it.
func sub(edit ...func(*wire.JobSubmission)) wire.JobSubmission {
	s := wire.JobSubmission{
		Repo:         wire.Repo{Name: "purdex", Root: "abc123", Remote: "git@github.com:wake/purdex.git"},
		Snapshot:     wire.Snapshot{Commit: "c0ffee", Tree: "7ee", Base: "ba5e", RelCwd: "spa"},
		Task:         "spa-related",
		Args:         []string{"src/a.ts"},
		Class:        wire.ClassMerge,
		Labels:       []string{"os:darwin", "repo:purdex"},
		Locks:        []string{},
		Cacheable:    true,
		TimeoutS:     1800,
		SupersedeKey: "wt1:spa-related",
		Submitter:    wire.Submitter{Session: "s1", Ref: "_abc123", Team: "wwd", Host: "mlab"},
	}
	for _, e := range edit {
		e(&s)
	}
	return s
}

func (f *fixture) submit(t *testing.T, edit ...func(*wire.JobSubmission)) string {
	t.Helper()
	r, err := f.q.Submit(context.Background(), sub(edit...))
	require.NoError(t, err)
	return r.ID
}

func (f *fixture) get(t *testing.T, id string) wire.Job {
	t.Helper()
	j, err := f.q.Get(context.Background(), id)
	require.NoError(t, err)
	return j
}

// seed forces a row into a state the 3b-3 transitions will reach for real (claimed, running, terminal), so the 3b-2 rules about
// them can be tested before the claim code exists.
func (f *fixture) seed(t *testing.T, id string, state wire.JobState, set string, args ...any) {
	t.Helper()
	q := `UPDATE jobs SET state = ?`
	a := []any{string(state)}
	if set != "" {
		q += ", " + set
		a = append(a, args...)
	}
	q += ` WHERE id = ?`
	a = append(a, id)
	res, err := f.db.Exec(q, a...)
	require.NoError(t, err)
	n, _ := res.RowsAffected()
	require.EqualValues(t, 1, n)
}

func (f *fixture) cancelRequested(t *testing.T, id string) bool {
	t.Helper()
	var v int
	require.NoError(t, f.db.QueryRow(`SELECT cancel_requested FROM jobs WHERE id = ?`, id).Scan(&v))
	return v == 1
}

func ms(t time.Time) int64 { return t.UnixMilli() }
