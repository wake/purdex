package jobsapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

type rig struct {
	q     *queue.Queue
	db    *sql.DB
	local http.Handler // the socket's view
	peer  http.Handler // the TCP view (the token is the daemon's business)
	pins  *refusingPinner
}

type refusingPinner struct{ refuse bool }

func (p *refusingPinner) Pin(context.Context, string, string, string) error {
	if p.refuse {
		return errors.New("not a git directory")
	}
	return nil
}
func (p *refusingPinner) Unpin(context.Context, string, string) error { return nil }
func (p *refusingPinner) ListPins(context.Context, string) ([]string, error) {
	return nil, nil
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "wwd.db") + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, queue.Migrate(context.Background(), db))
	pins := &refusingPinner{}
	now := time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC)
	q, err := queue.New(db, queue.Options{Origin: "mlab", LogDir: t.TempDir(), Pinner: pins, Now: func() time.Time { return now }})
	require.NoError(t, err)
	a := New(q)
	mk := func(peer bool) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/jobs", a.List(peer))
		mux.HandleFunc("GET /v1/jobs/{id}", a.Get(peer))
		mux.HandleFunc("GET /v1/jobs/{id}/log", a.Log(peer))
		mux.HandleFunc("POST /v1/jobs/{id}/cancel", a.Cancel(peer))
		if !peer {
			mux.HandleFunc("POST /v1/jobs", a.Submit())
			mux.HandleFunc("POST /v1/jobs/{id}/position", a.Position())
		}
		return mux
	}
	return &rig{q: q, db: db, local: mk(false), peer: mk(true), pins: pins}
}

func submission(edit ...func(*wire.JobSubmission)) wire.JobSubmission {
	s := wire.JobSubmission{
		Repo:      wire.Repo{Name: "purdex", Root: "abc123", Remote: "git@github.com:wake/purdex.git"},
		Snapshot:  wire.Snapshot{Commit: "c0ffee", Tree: "7ee", Base: "ba5e", RelCwd: "spa"},
		Task:      "spa-related",
		Args:      []string{"src/a.ts"},
		Class:     wire.ClassMerge,
		Labels:    []string{"os:darwin"},
		Locks:     []string{},
		TimeoutS:  1800,
		Submitter: wire.Submitter{Session: "s1", Ref: "_abc123", Team: "wwd", Host: "mlab"},
		GitDir:    "/repos/purdex/.git",
	}
	for _, e := range edit {
		e(&s)
	}
	return s
}

func (r *rig) submit(t *testing.T, edit ...func(*wire.JobSubmission)) string {
	t.Helper()
	resp, err := r.q.Submit(context.Background(), submission(edit...))
	require.NoError(t, err)
	return resp.ID
}

func call(h http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, rd)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	v, err := wire.Decode[T](w.Body.Bytes())
	require.NoError(t, err, "body: %s", w.Body)
	return v
}

func jobIDs(js []wire.Job) []string {
	out := []string{}
	for _, j := range js {
		out = append(out, j.ID)
	}
	return out
}

// POST /v1/jobs stores a submission and answers its id. The body is decoded strictly: an unknown field, a relative git directory or
// anything the wire refuses is a 400 and nothing is stored.
func TestSubmit(t *testing.T) {
	r := newRig(t)
	w := call(r.local, "POST", "/v1/jobs", submission())
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	resp := decode[wire.JobSubmissionResponse](t, w)
	j, err := r.q.Get(context.Background(), resp.ID)
	require.NoError(t, err)
	assert.Equal(t, wire.StateQueued, j.State)
	assert.Equal(t, "spa-related", j.Task)

	for name, body := range map[string]any{
		"unknown field": `{"repo":{"name":"p","root":"r","remote":"x"},"bogus":1}`,
		"relative dir":  submission(func(s *wire.JobSubmission) { s.GitDir = "x/.git" }),
		"no task":       submission(func(s *wire.JobSubmission) { s.Task = "" }),
		"bad class":     submission(func(s *wire.JobSubmission) { s.Class = "urgent" }),
		"not json":      "{",
		"empty":         "",
	} {
		w := call(r.local, "POST", "/v1/jobs", body)
		assert.Equal(t, 400, w.Code, name)
	}
	js, err := r.q.List(context.Background(), queue.Filter{})
	require.NoError(t, err)
	assert.Len(t, js, 1, "the refused ones stored nothing")

	// a submission that is valid in every way but its size: refused by the cap, not by the wire's checks
	big := submission(func(s *wire.JobSubmission) { s.Args = []string{strings.Repeat("x", 2<<20)} })
	assert.Equal(t, 400, call(r.local, "POST", "/v1/jobs", big).Code, "a body past the cap")
	small := submission(func(s *wire.JobSubmission) { s.Args = []string{strings.Repeat("x", 100<<10)} })
	assert.Equal(t, 200, call(r.local, "POST", "/v1/jobs", small).Code, "and one under it is taken")
}

// A snapshot that cannot be pinned is refused with 422 (pdx test exit 4): the job is not queued.
func TestSubmitWithAnUnpinnableSnapshot(t *testing.T) {
	r := newRig(t)
	r.pins.refuse = true
	w := call(r.local, "POST", "/v1/jobs", submission())
	assert.Equal(t, 422, w.Code, w.Body.String())
	js, err := r.q.List(context.Background(), queue.Filter{})
	require.NoError(t, err)
	assert.Empty(t, js)
}

// The list always carries the origin's cursor, and a read with it returns what changed since: nothing, with the same cursor, when nothing
// did; the changed job once, in its state now, when something did.
func TestListWithTheCursor(t *testing.T) {
	r := newRig(t)
	a := r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	b := r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })

	w := call(r.local, "GET", "/v1/jobs", nil)
	require.Equal(t, 200, w.Code)
	first := decode[wire.JobsListResponse](t, w)
	assert.Equal(t, []string{a, b}, jobIDs(first.Jobs))
	require.True(t, wire.ValidCursor(first.Cursor))

	w = call(r.local, "GET", "/v1/jobs?after="+first.Cursor, nil)
	again := decode[wire.JobsListResponse](t, w)
	assert.Equal(t, []string{}, jobIDs(again.Jobs))
	assert.NotContains(t, w.Body.String(), `"jobs":null`)
	assert.Equal(t, first.Cursor, again.Cursor, "nothing changed: the cursor does not move")

	_, err := r.q.Cancel(context.Background(), a)
	require.NoError(t, err)
	w = call(r.local, "GET", "/v1/jobs?after="+first.Cursor, nil)
	changed := decode[wire.JobsListResponse](t, w)
	require.Equal(t, []string{a}, jobIDs(changed.Jobs))
	assert.Equal(t, wire.StateCancelled, changed.Jobs[0].State)
	assert.NotEqual(t, first.Cursor, changed.Cursor)

	// pieces
	w = call(r.local, "GET", "/v1/jobs?limit=1", nil)
	piece := decode[wire.JobsListResponse](t, w)
	assert.Len(t, piece.Jobs, 1)
}

// Every list names the database it came from. A cursor past the origin's last change was made by another database (this one was removed
// and made again), where reading from it would find nothing for ever: it is a 409 that says where this database is and which it is, and
// the client starts again from "0" (#3303). A cursor at the head, or any below it, is an ordinary read.
func TestListCarriesTheEpochAndRefusesACursorAheadOfTheHead(t *testing.T) {
	r := newRig(t)
	epoch, err := r.q.Epoch(context.Background())
	require.NoError(t, err)
	r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })

	w := call(r.local, "GET", "/v1/jobs", nil)
	require.Equal(t, 200, w.Code)
	list := decode[wire.JobsListResponse](t, w)
	assert.Equal(t, epoch, list.Epoch)
	head, err := r.q.Head(context.Background())
	require.NoError(t, err)
	require.Equal(t, queue.CursorString(head), list.Cursor)

	for _, view := range []http.Handler{r.local, r.peer} {
		w = call(view, "GET", "/v1/jobs?after="+queue.CursorString(head), nil)
		require.Equal(t, 200, w.Code, "a cursor at the head is the ordinary \"nothing new\"")
		assert.Equal(t, epoch, decode[wire.JobsListResponse](t, w).Epoch)

		for _, ahead := range []string{queue.CursorString(head + 1), "999999999999999999"} {
			w = call(view, "GET", "/v1/jobs?after="+ahead, nil)
			require.Equal(t, 409, w.Code, ahead)
			c := decode[wire.CursorAhead](t, w)
			assert.Equal(t, queue.CursorString(head), c.Head)
			assert.Equal(t, epoch, c.Epoch)
		}
	}
	assert.Equal(t, 200, call(r.local, "GET", "/v1/jobs?after=0", nil).Code)
}

// The head is read before the list: a write that lands between the two cannot turn a cursor from another database into one that is
// accepted (it would be answered with nothing and stored, and the changes below it skipped for ever). A cursor that was valid is not
// refused either, since the head only grows (3b-4d R1).
func TestACursorAheadIsRefusedEvenWhenAWriteLandsMeanwhile(t *testing.T) {
	r := newRig(t)
	r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	head, err := r.q.Head(context.Background())
	require.NoError(t, err)

	a := New(r.q)
	a.afterHead = func() { r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" }) } // the head reaches the cursor now
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs", a.List(false))

	w := call(mux, "GET", "/v1/jobs?after="+queue.CursorString(head+1), nil)
	require.Equal(t, 409, w.Code, "refused on the head as it was when the request was read: %s", w.Body)

	// a cursor that was the head when the request came in stays good, however many writes follow
	next, err := r.q.Head(context.Background())
	require.NoError(t, err)
	w = call(mux, "GET", "/v1/jobs?after="+queue.CursorString(next), nil)
	require.Equal(t, 200, w.Code)
	assert.NotEmpty(t, decode[wire.JobsListResponse](t, w).Cursor)
}

func TestListRefusesBadParameters(t *testing.T) {
	r := newRig(t)
	for _, q := range []string{"after=abc", "after=-1", "after=1.5", "state=bogus", "limit=0", "limit=-3", "limit=x", "limit=99999999"} {
		w := call(r.local, "GET", "/v1/jobs?"+q, nil)
		assert.Equal(t, 400, w.Code, q)
	}
}

func TestListFilters(t *testing.T) {
	r := newRig(t)
	a := r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	b := r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = ""; s.Submitter.Session = "s2" })
	_, err := r.q.Cancel(context.Background(), a)
	require.NoError(t, err)

	got := decode[wire.JobsListResponse](t, call(r.local, "GET", "/v1/jobs?state=queued", nil))
	assert.Equal(t, []string{b}, jobIDs(got.Jobs))
	got = decode[wire.JobsListResponse](t, call(r.local, "GET", "/v1/jobs?submitter.session=s2", nil))
	assert.Equal(t, []string{b}, jobIDs(got.Jobs))
	got = decode[wire.JobsListResponse](t, call(r.local, "GET", "/v1/jobs?state=cancelled&submitter.session=s1", nil))
	assert.Equal(t, []string{a}, jobIDs(got.Jobs))
}

// A peer sees the queued work a worker could take, and nothing of the host's own ad-hoc and fast-lane runs: not in the list, not by id,
// not their logs, and a peer cannot cancel one (spec 4.4, 8.3, 11). The host itself sees all of them.
func TestPeersDoNotSeeAdHocJobs(t *testing.T) {
	r := newRig(t)
	plain := r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	adhoc := r.submit(t, func(s *wire.JobSubmission) { s.AdHoc = true; s.SupersedeKey = "" })

	got := decode[wire.JobsListResponse](t, call(r.peer, "GET", "/v1/jobs", nil))
	assert.Equal(t, []string{plain}, jobIDs(got.Jobs))
	got = decode[wire.JobsListResponse](t, call(r.local, "GET", "/v1/jobs", nil))
	assert.ElementsMatch(t, []string{plain, adhoc}, jobIDs(got.Jobs))

	assert.Equal(t, 404, call(r.peer, "GET", "/v1/jobs/"+adhoc, nil).Code)
	assert.Equal(t, 200, call(r.local, "GET", "/v1/jobs/"+adhoc, nil).Code)
	assert.Equal(t, 404, call(r.peer, "POST", "/v1/jobs/"+adhoc+"/cancel", nil).Code)
	j, err := r.q.Get(context.Background(), adhoc)
	require.NoError(t, err)
	assert.Equal(t, wire.StateQueued, j.State, "the peer's cancel changed nothing")
	assert.Equal(t, 200, call(r.peer, "POST", "/v1/jobs/"+plain+"/cancel", nil).Code, "a peer may cancel an ordinary job")
}

func TestGet(t *testing.T) {
	r := newRig(t)
	id := r.submit(t)
	w := call(r.local, "GET", "/v1/jobs/"+id, nil)
	require.Equal(t, 200, w.Code)
	j := decode[wire.Job](t, w)
	assert.Equal(t, id, j.ID)
	assert.NotContains(t, w.Body.String(), "git_dir", "the git directory never leaves the host")
	assert.NotContains(t, w.Body.String(), "claim_id")

	assert.Equal(t, 404, call(r.local, "GET", "/v1/jobs/j_nope", nil).Code)
	for _, bad := range []string{"x", "j_" + strings.Repeat("a", 80), "j_a.b", "j_a%2Fb", "j_a%00"} {
		assert.Equal(t, 404, call(r.local, "GET", "/v1/jobs/"+bad, nil).Code, bad)
	}
}

func TestLog(t *testing.T) {
	r := newRig(t)
	id := r.submit(t)
	assert.Equal(t, 404, call(r.local, "GET", "/v1/jobs/"+id+"/log", nil).Code, "no log yet")

	c, err := r.q.Claim(context.Background(), id, wire.ClaimRequest{Worker: "air26"})
	require.NoError(t, err)
	require.NoError(t, r.q.PutLog(context.Background(), id, c.ClaimID, strings.NewReader("line 1\nline 2\n")))

	w := call(r.local, "GET", "/v1/jobs/"+id+"/log", nil)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "line 1\nline 2\n", w.Body.String())
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, 200, call(r.peer, "GET", "/v1/jobs/"+id+"/log", nil).Code)
}

func TestLogOfAnAdHocJobIsNotForPeers(t *testing.T) {
	r := newRig(t)
	id := r.submit(t, func(s *wire.JobSubmission) { s.AdHoc = true })
	c, err := r.q.Claim(context.Background(), id, wire.ClaimRequest{Worker: "mlab"})
	require.NoError(t, err)
	require.NoError(t, r.q.PutLog(context.Background(), id, c.ClaimID, strings.NewReader("secret output")))
	assert.Equal(t, 200, call(r.local, "GET", "/v1/jobs/"+id+"/log", nil).Code)
	assert.Equal(t, 404, call(r.peer, "GET", "/v1/jobs/"+id+"/log", nil).Code)
}

// Cancel: a queued job ends at once (200, the job); a held one only gets the request (200, state unchanged); an ended one is a 409 with
// its state; an unknown id is 404.
func TestCancel(t *testing.T) {
	r := newRig(t)
	queued := r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	w := call(r.local, "POST", "/v1/jobs/"+queued+"/cancel", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, wire.StateCancelled, decode[wire.Job](t, w).State)

	w = call(r.local, "POST", "/v1/jobs/"+queued+"/cancel", nil)
	require.Equal(t, 409, w.Code)
	assert.Equal(t, wire.StateCancelled, decode[wire.Conflict409](t, w).State)

	held := r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	_, err := r.q.Claim(context.Background(), held, wire.ClaimRequest{Worker: "air26"})
	require.NoError(t, err)
	w = call(r.local, "POST", "/v1/jobs/"+held+"/cancel", nil)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, wire.StateClaimed, decode[wire.Job](t, w).State)

	assert.Equal(t, 404, call(r.local, "POST", "/v1/jobs/j_nope/cancel", nil).Code)
}

// POST /v1/jobs/{id}/position moves a queued job by hand: the answer is the job with its place; a bad body or position is a 400, a job
// that is not queued a 409 with its state, an unknown one a 404, and nothing changes in any of those.
func TestPosition(t *testing.T) {
	r := newRig(t)
	id := r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	w := call(r.local, "POST", "/v1/jobs/"+id+"/position", `{"position":"front"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	j := decode[wire.Job](t, w)
	assert.Equal(t, wire.PositionFront, j.Manual)
	assert.Less(t, j.ManualSeq, int64(0))

	w = call(r.local, "POST", "/v1/jobs/"+id+"/position", `{"position":"hold"}`)
	assert.Equal(t, wire.PositionHold, decode[wire.Job](t, w).Manual)
	w = call(r.local, "POST", "/v1/jobs/"+id+"/position", `{"position":"normal"}`)
	assert.Equal(t, wire.Position(""), decode[wire.Job](t, w).Manual)

	for _, body := range []string{`{"position":"sideways"}`, `{"position":""}`, `{}`, `{"position":"front","extra":1}`, `nope`, ``} {
		w = call(r.local, "POST", "/v1/jobs/"+id+"/position", body)
		assert.Equal(t, 400, w.Code, body)
	}
	assert.Equal(t, wire.Position(""), decode[wire.Job](t, call(r.local, "GET", "/v1/jobs/"+id, nil)).Manual, "no bad request changed it")

	assert.Equal(t, 404, call(r.local, "POST", "/v1/jobs/j_nope/position", `{"position":"front"}`).Code)
	assert.Equal(t, 404, call(r.local, "POST", "/v1/jobs/x%2F..%2Fy/position", `{"position":"front"}`).Code)

	_, err := r.q.Claim(context.Background(), id, wire.ClaimRequest{Worker: "air26"})
	require.NoError(t, err)
	w = call(r.local, "POST", "/v1/jobs/"+id+"/position", `{"position":"front"}`)
	require.Equal(t, 409, w.Code)
	assert.Equal(t, wire.StateClaimed, decode[wire.Conflict409](t, w).State)

	assert.Equal(t, 404, call(r.peer, "POST", "/v1/jobs/"+id+"/position", `{"position":"front"}`).Code, "the peer surface has no such route")
}

// An error the caller cannot act on is a 500 that says so and nothing more: no SQL, no paths.
func TestInternalErrorsAreNotLeaked(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.db.Close())
	w := call(r.local, "GET", "/v1/jobs", nil)
	assert.Equal(t, 500, w.Code)
	assert.NotContains(t, strings.ToLower(w.Body.String()), "sql")
	assert.NotContains(t, w.Body.String(), "wwd.db")
}

// Submit is not a peer route at all: the peer view has no handler for it (the daemon test shows the same through the real TCP server).
func TestPeerMuxHasNoSubmit(t *testing.T) {
	r := newRig(t)
	w := call(r.peer, "POST", "/v1/jobs", submission())
	assert.Contains(t, []int{404, 405}, w.Code)
	js, err := r.q.List(context.Background(), queue.Filter{})
	require.NoError(t, err)
	assert.Empty(t, js)
}
