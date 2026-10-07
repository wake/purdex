package nex

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/bus"

	"github.com/wake/purdex/internal/core"
)

// #1866 PR1c (spec 2026-10-08 §3.6, §3.7, §8 R3-2): the list walk — one
// page per slot hold, each page its own ver — and the lastPushed seed it
// feeds at every epoch start.

// rowsWith is a rowServer with one idle row per id.
func rowsWith(ids ...string) *rowServer {
	rows := newRowServer()
	for _, id := range ids {
		rows.set(id, "idle")
	}
	return rows
}

// walkProjector is a projector that is never started, over handler: enough
// to walk.
func walkProjector(slot *readSlot, handler http.Handler, timing projectorTiming) *projector {
	return newProjector(slot, rowReader{handler: handler, logf: discardLogf}, core.NewEventsBroadcaster(), bus.New(),
		discardLogf, timing)
}

// walkAll walks p and returns the pages it visited.
func walkAll(p *projector) ([]walkPage, error) {
	var pages []walkPage
	err := p.walk(context.Background(), "seed", 0, func(pg walkPage) error {
		pages = append(pages, pg)
		return nil
	})
	return pages, err
}

func idsOf(pg walkPage) []string {
	ids := make([]string, len(pg.rows))
	for i, r := range pg.rows {
		ids[i] = r.id
	}
	return ids
}

func TestProjectorWalk_PagesCarryTheirOwnVerAndUpTo(t *testing.T) {
	rows := rowsWith("exc_01", "exc_02", "exc_03", "exc_04", "exc_05")
	h := &recordingHandler{respond: rows.ServeHTTP}
	p := walkProjector(newReadSlot(discardLogf), h, projectorTiming{walkLimit: 2})

	pages, err := walkAll(p)
	require.NoError(t, err)
	require.Len(t, pages, 3)
	assert.Equal(t, [][]string{{"exc_01", "exc_02"}, {"exc_03", "exc_04"}, {"exc_05"}},
		[][]string{idsOf(pages[0]), idsOf(pages[1]), idsOf(pages[2])})
	assert.Equal(t, []string{"exc_02", "exc_04", ""}, []string{pages[0].upTo, pages[1].upTo, pages[2].upTo},
		"a page covers up to its last id; the final page to the end")
	assert.Equal(t, []uint64{1, 2, 3}, []uint64{pages[0].ver, pages[1].ver, pages[2].ver})
	assert.Equal(t, rowDigest{State: "idle", TurnCount: 1}, pages[0].rows[0].digest)
	assert.True(t, readSlotFree(p.slot))

	reqs := h.requests()
	require.Len(t, reqs, 3)
	for i, query := range []string{"limit=2", "cursor=exc_02&limit=2", "cursor=exc_04&limit=2"} {
		assert.Equal(t, http.MethodGet, reqs[i].method)
		assert.Equal(t, "/v1/executions", reqs[i].path)
		assert.Equal(t, query, reqs[i].rawQuery)
		assert.Empty(t, reqs[i].header, "no header may reach the engine (principal = bare host)")
		assert.Equal(t, "pdx:host1", reqs[i].principal)
	}

	// §3.8 "Bounds": walks read 100-row pages, at most 200 of them.
	d := walkProjector(newReadSlot(discardLogf), rows, projectorTiming{})
	assert.Equal(t, [2]int{100, 200}, [2]int{d.timing.walkLimit, d.timing.walkPages})
}

// The slot is released between pages: a delta flushed while the walk is
// between two pages (it needs the slot) gets a ver between theirs.
func TestProjectorWalk_SlotIsFreeBetweenPages(t *testing.T) {
	tm := fastTiming
	tm.walkLimit = 1
	e := startProjEnv(t, tm, rowsWith("exc_01", "exc_02"))

	var vers []uint64
	var between delta
	err := e.p.walk(context.Background(), "seed", 0, func(pg walkPage) error {
		vers = append(vers, pg.ver)
		if len(vers) == 1 {
			e.p.markFrame("exc_x", "execution.running")
			between = nextDelta(t, e.sub)
		}
		return nil
	})
	require.NoError(t, err)
	require.Len(t, vers, 2)
	assert.Less(t, vers[0], between.Ver)
	assert.Less(t, between.Ver, vers[1])
}

func TestProjectorWalk_StopsAtItsPageCap(t *testing.T) {
	rows := rowsWith("exc_01", "exc_02", "exc_03")
	p := walkProjector(newReadSlot(discardLogf), rows, projectorTiming{walkLimit: 1, walkPages: 2})
	pages, err := walkAll(p)
	assert.ErrorIs(t, err, errWalkPageCap)
	assert.Contains(t, err.Error(), "2 pages")
	assert.Len(t, pages, 2)
	assert.Equal(t, 2, rows.listReadCount(), "read past the cap")
}

// A page that fails ends the walk with an error and consumes no ver: the
// next read gets the very next one.
func TestProjectorWalk_FailedPageEndsTheWalkWithoutAVer(t *testing.T) {
	cases := map[string]func(http.ResponseWriter, *http.Request){
		"500":           answer(http.StatusInternalServerError, `{"error":"boom","code":"internal"}`),
		"not an object": answer(http.StatusOK, `[]`),
		"null":          answer(http.StatusOK, `null`),
		"no progress":   answer(http.StatusOK, `{"items":[],"next_cursor":"exc_01"}`),
		"panic":         func(http.ResponseWriter, *http.Request) { panic("list exploded") },
		"panic after a complete page": func(w http.ResponseWriter, r *http.Request) {
			answer(http.StatusOK, `{"items":[],"next_cursor":""}`)(w, r)
			panic("late")
		},
		"abort": func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) },
	}
	for name, fail := range cases {
		t.Run(name, func(t *testing.T) {
			rows := rowsWith("exc_01", "exc_02")
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("cursor") != "" {
					fail(w, r)
					return
				}
				rows.ServeHTTP(w, r)
			})
			slot := newReadSlot(discardLogf)
			pages, err := walkAll(walkProjector(slot, h, projectorTiming{walkLimit: 1}))
			assert.Error(t, err)
			require.Len(t, pages, 1)
			assert.True(t, readSlotFree(slot))
			st, err := slot.read(context.Background(), "row", 0, okRead)
			require.NoError(t, err)
			assert.Equal(t, pages[0].ver+1, st.Ver, "the failed page consumed a ver")
		})
	}
}

// The start seed records every row the list shows as lastPushed — its
// page's ver, its digest — and pushes nothing: clients reconcile against
// list pages read after the epoch began, so they hold that baseline
// already (§8 R3-2).
func TestProjector_StartSeedRecordsEveryListedRowWithoutPushing(t *testing.T) {
	rows := rowsWith("exc_01", "exc_02")
	rows.setBody("exc_03", `{"id":"exc_03","state":"idle","pending_permission":{"request_id":"perm_9"},"turn_count":2}`)
	rows.setBody("exc_04", `{"id":"exc_04","state":"idle","archived":true,"turn_count":1}`) // not in the list
	tm := fastTiming
	tm.walkLimit = 2
	e := startProjEnv(t, tm, rows)

	idle := rowDigest{State: "idle", TurnCount: 1}
	e.p.mu.Lock()
	assert.Equal(t, map[string]pushedRow{
		"exc_01": {ver: 1, digest: idle},
		"exc_02": {ver: 1, digest: idle},
		"exc_03": {ver: 2, digest: rowDigest{State: "idle", PermissionRequest: "perm_9", TurnCount: 2}},
	}, e.p.pushed)
	e.p.mu.Unlock()
	noFrame(t, e.sub, 50*time.Millisecond)
	require.NoError(t, e.slot.hold(context.Background(), "test", 0, func(context.Context) error {
		assert.Equal(t, uint64(0), e.slot.bseq, "the seed consumed a bseq")
		return nil
	}))
	for _, id := range []string{"exc_01", "exc_02", "exc_03", "exc_04"} {
		assert.Zero(t, rows.readsOf(id), "the seed read %s on its own", id)
	}
}

// A seed applies a page by ver: an entry newer than the page (a delta read
// after it) is kept, an older one takes the page's row. An older entry the
// page covers but does not list (archived or gone since) is dropped, as no
// client holds a row for it either; one outside the page's range is not
// the page's to judge.
func TestProjector_SeedPageNeverOverwritesANewerVer(t *testing.T) {
	running, idle := rowDigest{State: "running"}, rowDigest{State: "idle"}
	p := &projector{pushed: map[string]pushedRow{
		"exc_01": {ver: 1, digest: running}, // before the page's range
		"exc_02": {ver: 9, digest: running}, // flushed after the page was read
		"exc_03": {ver: 3, digest: running}, // older than the page
		"exc_04": {ver: 4, digest: running}, // covered, not listed, older
		"exc_05": {ver: 9, digest: running}, // covered, not listed, newer
		"exc_09": {ver: 1, digest: running}, // after the page's range
	}}
	p.seedPage(walkPage{ver: 5, upTo: "exc_06", rows: []walkRow{{id: "exc_02", digest: idle}, {id: "exc_03", digest: idle},
		{id: "exc_06", digest: idle}}}, "exc_01")
	assert.Equal(t, map[string]pushedRow{
		"exc_01": {ver: 1, digest: running},
		"exc_02": {ver: 9, digest: running},
		"exc_03": {ver: 5, digest: idle},
		"exc_05": {ver: 9, digest: running},
		"exc_06": {ver: 5, digest: idle},
		"exc_09": {ver: 1, digest: running},
	}, p.pushed)

	// The final page (upTo "") covers everything after the previous one.
	p.seedPage(walkPage{ver: 10}, "exc_06")
	assert.NotContains(t, p.pushed, "exc_09")
	assert.Contains(t, p.pushed, "exc_06")
}

// A seed the page cap stopped keeps what it read and says so.
func TestProjector_SeedStoppedByThePageCapLogsIt(t *testing.T) {
	tm := fastTiming
	tm.walkLimit, tm.walkPages = 1, 2
	e := startProjEnv(t, tm, rowsWith("exc_01", "exc_02", "exc_03"))
	var line string
	for _, l := range e.logs.all() {
		if strings.HasPrefix(l, "nex-delta: seeding lastPushed stopped:") {
			line = l
		}
	}
	assert.Contains(t, line, "2 pages")
	e.p.mu.Lock()
	defer e.p.mu.Unlock()
	assert.Len(t, e.p.pushed, 2)
}
