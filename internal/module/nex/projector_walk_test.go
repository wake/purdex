package nex

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/bus"

	"github.com/wake/purdex/internal/core"
)

// #1866 PR1c (spec 2026-10-08 §3.6, §3.7, §8 R3-2): the list walk — one
// page per slot hold, each page its own ver.

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
