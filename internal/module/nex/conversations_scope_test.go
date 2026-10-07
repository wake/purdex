package nex

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/conversations"
	"github.com/wake/purdex/internal/module/agent"
	pstore "github.com/wake/purdex/internal/store"
)

// scopeCwds is the dataset: id -> cwd. T* are test cwds, N* normal ones
// (N3 has no cwd at all, which is normal).
var scopeCwds = map[string]string{
	ceSID(1): "/tmp/x",
	ceSID(2): "/private/tmp/y",
	ceSID(3): "/private/tmp/a/../../etc", // Clean -> /private/etc: normal
	ceSID(4): "/Users/w/proj",
	ceSID(5): "",
	ceSID(6): "/tmpfoo",
}

var (
	wantScopeTest   = []string{ceSID(1), ceSID(2)}
	wantScopeNormal = []string{ceSID(3), ceSID(4), ceSID(5), ceSID(6)}
)

// ceUnknownTest / ceUnknownNormal are held only by unverified frames.
var (
	ceUnknownTest   = ceSID(7)
	ceUnknownNormal = ceSID(8)
)

func scopeEnv(t *testing.T) *convEnv {
	t.Helper()
	env := newConvEnv(t)
	present := map[string]conversations.Entry{}
	cwds := map[string]string{ceUnknownTest: "/tmp/u", ceUnknownNormal: "/Users/w/u"}
	for id, cwd := range scopeCwds {
		cwds[id] = cwd
	}
	i := int64(0)
	for _, id := range sortedKeys(cwds) {
		i++
		env.idx.put(pstore.ConversationIndexRow{SessionID: id, FirstEntrypoint: "cli", Cwd: cwds[id], MtimeMs: i})
		present[id] = conversations.Entry{SessionID: id, MtimeMs: i}
	}
	env.m.convScan = func(_ context.Context, _ string, _ conversations.Index, now func() time.Time) (conversations.ScanResult, error) {
		return conversations.ScanResult{Present: present, Files: len(present), ScannedAt: now().UnixMilli()}, nil
	}
	env.terminals.live = map[string][]agent.TerminalSession{
		ceUnknownTest:   {{FrameID: "F7", SessionID: ceUnknownTest, AgentType: "cc", Verified: false}},
		ceUnknownNormal: {{FrameID: "F8", SessionID: ceUnknownNormal, AgentType: "cc", Verified: false}},
	}
	return env
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedIDs(rows []conversationRow) []string {
	ids := cvIDs(rows)
	sort.Strings(ids)
	return ids
}

func TestConversationsHTTP_ScopePartitionsTheDataset(t *testing.T) {
	env := scopeEnv(t)

	status, test := env.get(t, "?state=ended&scope=test")
	require.Equal(t, http.StatusOK, status, test.Error)
	_, normal := env.get(t, "?state=ended&scope=normal")
	_, all := env.get(t, "?state=ended&scope=all")
	_, dflt := env.get(t, "?state=ended")

	assert.Equal(t, wantScopeTest, sortedIDs(test.Conversations))
	assert.Equal(t, wantScopeNormal, sortedIDs(normal.Conversations))
	assert.Equal(t, sortedIDs(all.Conversations), sortedIDs(dflt.Conversations), "no scope = all")
	assert.Len(t, all.Conversations, len(wantScopeTest)+len(wantScopeNormal))
	union := append(append([]string{}, sortedIDs(test.Conversations)...), sortedIDs(normal.Conversations)...)
	sort.Strings(union)
	assert.Equal(t, sortedIDs(all.Conversations), union, "test + normal = all, disjoint")

	assert.Equal(t, len(wantScopeTest), test.Total)
	assert.Equal(t, len(wantScopeNormal), normal.Total)
	assert.Equal(t, 1, test.UnknownOwner)
	assert.Equal(t, 1, normal.UnknownOwner)
	assert.Equal(t, 2, all.UnknownOwner)
	assert.Equal(t, 2, dflt.UnknownOwner)
}

func TestConversationsHTTP_BadScope(t *testing.T) {
	env := scopeEnv(t)
	for _, q := range []string{"?state=ended&scope=TEST", "?state=ended&scope=x", "?state=ended&scope=test,normal"} {
		status, res := env.get(t, q)
		assert.Equal(t, http.StatusBadRequest, status, q)
		assert.Equal(t, "bad_scope", res.Code, q)
	}
	// an explicitly empty scope is the default
	status, _ := env.get(t, "?state=ended&scope=")
	assert.Equal(t, http.StatusOK, status)
}

// total and truncated count the filtered list, not the whole state.
func TestConversationsHTTP_ScopeTotalAndTruncatedAreFiltered(t *testing.T) {
	env := newConvEnv(t)
	present := map[string]conversations.Entry{}
	n := conversationRowCap + 1
	for i := 1; i <= n; i++ { // test side over the cap
		env.idx.put(pstore.ConversationIndexRow{SessionID: ceSID(i), FirstEntrypoint: "cli", Cwd: "/tmp/t", MtimeMs: int64(i)})
		present[ceSID(i)] = conversations.Entry{SessionID: ceSID(i), MtimeMs: int64(i)}
	}
	for i := 1; i <= 3; i++ { // normal side well under it
		id := ceSID(100000 + i)
		env.idx.put(pstore.ConversationIndexRow{SessionID: id, FirstEntrypoint: "cli", Cwd: "/work", MtimeMs: int64(i)})
		present[id] = conversations.Entry{SessionID: id, MtimeMs: int64(i)}
	}
	env.m.convScan = func(_ context.Context, _ string, _ conversations.Index, now func() time.Time) (conversations.ScanResult, error) {
		return conversations.ScanResult{Present: present, Files: len(present), ScannedAt: now().UnixMilli()}, nil
	}

	_, test := env.get(t, "?state=ended&scope=test")
	assert.Equal(t, n, test.Total)
	assert.True(t, test.Truncated)
	assert.Len(t, test.Conversations, conversationRowCap)

	_, normal := env.get(t, "?state=ended&scope=normal")
	assert.Equal(t, 3, normal.Total)
	assert.False(t, normal.Truncated, "the normal side is under the cap even though the state is over it")
	assert.Len(t, normal.Conversations, 3)

	_, all := env.get(t, "?state=ended&scope=all")
	assert.Equal(t, n+3, all.Total)
	assert.True(t, all.Truncated)
}

// The snapshot is shared across requests and scopes; filtering never edits it.
func TestConversationsHTTP_ScopeLeavesTheSnapshotUntouched(t *testing.T) {
	env := scopeEnv(t)
	_, first := env.get(t, "?state=ended&scope=all")
	snap := env.cached()
	require.NotNil(t, snap)
	endedBefore := append([]conversationRow{}, snap.res.Ended...)
	goneBefore := append([]conversationRow{}, snap.res.Gone...)
	unknownBefore := snap.res.UnknownOwner

	var results [][]string
	for _, q := range []string{"test", "normal", "all", "test", "normal"} {
		_, res := env.get(t, "?state=ended&scope="+q)
		results = append(results, sortedIDs(res.Conversations))
		_, _ = env.get(t, "?state=gone&scope="+q)
	}
	assert.Equal(t, wantScopeTest, results[0])
	assert.Equal(t, wantScopeNormal, results[1])
	assert.Equal(t, sortedIDs(first.Conversations), results[2])
	assert.Equal(t, results[0], results[3], "test again, unaffected by the queries between")
	assert.Equal(t, results[1], results[4])

	require.Same(t, snap, env.cached())
	assert.Equal(t, endedBefore, snap.res.Ended, "snapshot rows unchanged and in the same order")
	assert.Equal(t, goneBefore, snap.res.Gone)
	assert.Equal(t, unknownBefore, snap.res.UnknownOwner)
}

func TestConversationsHTTP_ScopeConcurrentRequestsAreStable(t *testing.T) {
	env := scopeEnv(t)
	_, _ = env.get(t, "?state=ended") // warm the snapshot
	want := map[string][]string{
		"test":   wantScopeTest,
		"normal": wantScopeNormal,
	}
	var wg sync.WaitGroup
	errs := make(chan string, 400)
	for w := 0; w < 12; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			scopes := []string{"test", "normal", "all"}
			for i := 0; i < 15; i++ {
				sc := scopes[(w+i)%3]
				status, res, err := fetchConversations(env.srv.URL, "?state=ended&scope="+sc)
				if err != nil || status != http.StatusOK {
					errs <- "request failed: " + sc
					continue
				}
				got := sortedIDs(res.Conversations)
				if sc == "all" {
					if len(got) != len(wantScopeTest)+len(wantScopeNormal) {
						errs <- "all: wrong size"
					}
					continue
				}
				exp := want[sc]
				if len(got) != len(exp) {
					errs <- sc + ": wrong size"
					continue
				}
				for k := range exp {
					if got[k] != exp[k] {
						errs <- sc + ": wrong rows"
						break
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
