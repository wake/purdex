package peers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// The rename preview (issue #3520): how many live conversations the backfill would rename if a collection with these persona codes
// were saved now. The App told the person "N conversations" from the tmux session rows it could see, while the backfill walks every
// live registry entry; the number now comes from the daemon, from the same judgement.

func previewCodes() []string { return []string{"hiro", "anna", "mia"} }

// previewN is the number the App shows: the codes are all pickable.
func previewN(e *backfillEnv, codes []string) (int, error) {
	_, n, err := e.m.renamePreview(context.Background(), codes, codes)
	return n, err
}

// A live conversation with no tmux session row (a headless one) is a registry entry like any other: it is renamed, so it is counted.
// This is the contract the App's own count broke. Mutation gate: skip the entries that have no session row → red (0).
func TestRenamePreview_CountsLiveRegistryEntriesThatHaveNoSessionRow(t *testing.T) {
	e := newBackfillEnv(t)
	e.live(poolSidA, e.refName(poolSidA)) // no session row anywhere: only a registry entry and a stored name
	e.live(poolSidB, "scratch-1")
	n, err := previewN(e, previewCodes())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

// The preview and the backfill are one judgement: whatever the backfill renames in a pass is what the preview counted, including
// when the collection has fewer free names than old conversations (the pool runs out and the rest keep their names).
// Mutation gate: count a name that already ends in a code → the numbers differ → red.
func TestRenamePreview_IsTheCountTheBackfillThenRenames(t *testing.T) {
	e := newBackfillEnv(t)
	entries := []ipeers.Entry{
		e.live(poolSidA, e.refName(poolSidA)), // old ref-suffix name: renamed
		e.live(poolSidB, "scratch-1"),         // old free name: renamed
		e.live(poolSidC, "purdex-54-anna"),    // already ends in a persona code: kept, and anna is held by it
		e.live(poolSidD, "scratch-2"),         // a third old name, but hiro and mia are the only names left: it keeps its own
	}
	want, err := previewN(e, previewCodes())
	require.NoError(t, err)
	before := map[string]string{}
	for _, en := range entries {
		before[en.SessionID] = storedRow(t, e.meta, en.SessionID).Name
	}
	e.backfill(entries...)
	renamed := 0
	for _, en := range entries {
		if storedRow(t, e.meta, en.SessionID).Name != before[en.SessionID] {
			renamed++
		}
	}
	assert.Equal(t, 2, want)
	assert.Equal(t, renamed, want, "what the preview counted is what the backfill renamed")
}

// What the backfill leaves alone is not counted: a name that ends in a code of the collection that would be saved, a conversation
// with no stored name (a proxy and a duplicate session id are left out by backfillCandidates, below).
func TestRenamePreview_LeavesOutWhatTheBackfillLeavesOut(t *testing.T) {
	e := newBackfillEnv(t)
	e.live(poolSidA, "purdex-54-mia") // ends in a proposed code
	b := e.start(poolSidB, tLate)     // live, never named
	_ = b
	n, err := previewN(e, previewCodes())
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// the same name with a code that is not in the proposed collection is old
	n, err = previewN(e, []string{"hiro"})
	require.NoError(t, err)
	assert.Equal(t, 1, n, "purdex-54-mia is old when mia is not among the codes being saved")
}

// A code is matched on the last segment of the name, exactly as the pool does (the backfill uses PoolNameOf).
func TestRenamePreview_MatchesTheLastSegmentOnly(t *testing.T) {
	e := newBackfillEnv(t)
	e.live(poolSidA, "mia-notes") // 'mia' is not the last segment
	e.live(poolSidB, "x-anna-y")  // neither
	n, err := previewN(e, previewCodes())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}

func TestRenamePreview_Route(t *testing.T) {
	e := newBackfillEnv(t)
	e.live(poolSidA, "scratch-1")
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		e.m.handleRenamePreview(w, httptest.NewRequest(http.MethodPost, "/api/peers/rename-preview", strings.NewReader(body)))
		return w
	}
	w := post(`{"codes":["hiro","anna"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	var out struct {
		WouldRename int `json:"would_rename"`
		Candidates  int `json:"candidates"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, 1, out.WouldRename)
	assert.Equal(t, 1, out.Candidates)
	// pickable absent: every code is pickable; present and empty: none is
	w = post(`{"codes":["hiro"],"pickable":[]}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Equal(t, 0, out.WouldRename)
	assert.Equal(t, 1, out.Candidates)

	assert.Equal(t, http.StatusBadRequest, post(`not json`).Code)
	assert.Equal(t, http.StatusBadRequest, post(`{"codes":["ok","`+strings.Repeat("a", 200)+`"]}`).Code, "a code over the limit")
	assert.Equal(t, http.StatusBadRequest, post(`{"codes":[`+strings.Repeat(`"a",`, previewMaxCodes)+`"a"]}`).Code, "too many codes")
	assert.Equal(t, http.StatusOK, post(`{"codes":[]}`).Code, "no codes: every named conversation would be renamed")
}

var _ = store.PeerNameSourcePool

// The one judgement both use: an entry with no session id, a proxy (by flag or by pid), a duplicate session id, an entry with no name,
// and a name that already ends in a persona code are not candidates.
func TestBackfillCandidates_TheJudgementTheBackfillAndThePreviewShare(t *testing.T) {
	e := func(sid string, pid int, proxy bool) ipeers.Entry {
		return ipeers.Entry{SessionID: sid, PID: pid, IsProxy: proxy}
	}
	entries := []ipeers.Entry{
		e("", 1, false), e("s-proxy-flag", 2, true), e("s-proxy-pid", 3, false), e("s-dup", 4, false), e("S-DUP", 5, false),
		e("s-noname", 6, false), e("s-coded", 7, false), e("s-old", 8, false),
	}
	names := map[string]string{"s-proxy-flag": "x", "s-proxy-pid": "x", "s-dup": "x", "S-DUP": "x", "s-coded": "p-hiro", "s-old": "x"}
	got := backfillCandidates(entries, map[int]bool{3: true}, names, func(name string) bool { return strings.HasSuffix(name, "-hiro") })
	var sids []string
	for _, c := range got {
		sids = append(sids, c.entry.SessionID)
	}
	assert.ElementsMatch(t, []string{"s-dup", "s-old"}, sids)
}

// Fewer free names than old conversations (codex R1 on #3568): the count is what can actually be renamed, and the candidates are
// reported beside it. Mutation gate: return the candidates as would_rename → red.
func TestRenamePreview_CountsNoMoreThanTheFreeNames(t *testing.T) {
	e := newBackfillEnv(t)
	e.live(poolSidA, "scratch-1")
	e.live(poolSidB, "scratch-2")
	e.live(poolSidC, "scratch-3")
	cands, would, err := e.m.renamePreview(context.Background(), []string{"hiro", "anna", "mia"}, []string{"hiro", "anna"})
	require.NoError(t, err)
	assert.Equal(t, 3, cands)
	assert.Equal(t, 2, would, "two pickable names for three old conversations")

	// a pickable name already held by a live conversation is not free; a retired code (in codes, not pickable) is never given out
	e.live(poolSidD, "purdex-54-hiro")
	_, would, err = e.m.renamePreview(context.Background(), []string{"hiro", "anna", "mia"}, []string{"hiro", "anna"})
	require.NoError(t, err)
	assert.Equal(t, 1, would, "hiro is held, only anna is left")
	_, would, err = e.m.renamePreview(context.Background(), []string{"hiro"}, []string{})
	require.NoError(t, err)
	assert.Equal(t, 0, would, "nothing pickable: nothing can be renamed")
}
