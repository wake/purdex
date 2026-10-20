package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// lastPeer is a peer that answers with the proto it is told, or is down.
type lastPeer struct {
	srv   *httptest.Server
	proto atomic.Int32
	down  atomic.Bool
}

func newLastPeer(t *testing.T, alias string, proto int) *lastPeer {
	t.Helper()
	p := &lastPeer{}
	p.proto.Store(int32(proto))
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeJSON(w, 200, wire.Info{HostID: "id-" + alias, Alias: alias, Version: "v", Proto: int(p.proto.Load()), Platform: "darwin", Arch: "amd64", Labels: []string{}})
		case "/v1/facts":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(factsFor(wireFixture(t, "facts_darwin.json"), alias)))
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// What a peer last told about itself survives a restart of this wwd: a host that is off when the wwd comes up (a deploy restarts it)
// counts by the proto it last gave, not as never seen, so the gate that waits for every host does not shut again.
func TestTheLastKnownInfoOfAPeerSurvivesARestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "peers-last.json")
	air19 := newLastPeer(t, "air19", wire.Proto)
	peers := []PeerAddr{{"air19", addr(air19.srv)}}

	first := NewPoolPoller(peers, testToken, nil)
	first.UseLastKnownFile(file)
	first.PollOnce(context.Background())
	require.True(t, first.Peers()[0].Reachable)

	air19.down.Store(true) // off when the wwd restarts
	second := NewPoolPoller(peers, testToken, nil)
	second.UseLastKnownFile(file)
	second.PollOnce(context.Background())
	got := second.Peers()[0]
	assert.False(t, got.Reachable)
	assert.Equal(t, wire.Proto, got.Info.Proto, "the last known info is the start of the new view")
	assert.Equal(t, "id-air19", got.Info.HostID)
	assert.True(t, wire.PoolAtLeast(second.Peers(), wire.Proto))
}

// A peer never seen, a file that is not there and a file that is broken all mean the same: never seen. That only holds the gate shut.
func TestWithoutAUsableFileAPeerIsNeverSeen(t *testing.T) {
	dir := t.TempDir()
	air19 := newLastPeer(t, "air19", wire.Proto)
	air19.down.Store(true)
	peers := []PeerAddr{{"air19", addr(air19.srv)}}
	for name, content := range map[string]*string{
		"missing":   nil,
		"garbage":   ptr("not json"),
		"empty":     ptr(""),
		"other":     ptr(`{"peers":{"mserv":{"info":{"alias":"mserv","proto":3}}}}`),                 // another alias
		"wrongname": ptr(`{"peers":{"air19":{"info":{"alias":"someone-else","proto":3}}}}`),          // an answer under another name
		"future":    ptr(`{"peers":{"air19":{"info":{"alias":"air19","proto":99}}}}`),                // a proto this build does not read
		"unknown":   ptr(`{"peers":{"air19":{"info":{"alias":"air19","proto":3},"surprise":true}}}`), // strict, like every payload
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(dir, name+".json")
			if content != nil {
				require.NoError(t, os.WriteFile(file, []byte(*content), 0o600))
			}
			p := NewPoolPoller(peers, testToken, nil)
			p.UseLastKnownFile(file)
			p.PollOnce(context.Background())
			assert.Equal(t, wire.MinProto, p.Peers()[0].Info.Proto)
			assert.False(t, wire.PoolAtLeast(p.Peers(), wire.Proto))
		})
	}
}

func ptr(s string) *string { return &s }

// The file follows the peer: a new proto is written, a poll that reads the same info writes nothing, and a peer that is down does not
// touch it. The write is a rename of a temporary file, so a crash leaves the old file or the new one.
func TestTheLastKnownFileIsRewrittenOnlyWhenTheInfoChanges(t *testing.T) {
	file := filepath.Join(t.TempDir(), "peers-last.json")
	air19 := newLastPeer(t, "air19", 2)
	p := NewPoolPoller([]PeerAddr{{"air19", addr(air19.srv)}}, testToken, nil)
	p.UseLastKnownFile(file)

	p.PollOnce(context.Background())
	st1, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st1.Mode().Perm())

	p.PollOnce(context.Background())
	st2, err := os.Stat(file)
	require.NoError(t, err)
	assert.True(t, os.SameFile(st1, st2), "the same info is not written again (a rename would make another file)")

	air19.down.Store(true)
	p.PollOnce(context.Background())
	st3, err := os.Stat(file)
	require.NoError(t, err)
	assert.True(t, os.SameFile(st1, st3), "a peer that is down leaves the file as it was")

	air19.down.Store(false)
	air19.proto.Store(3)
	p.PollOnce(context.Background())
	back := NewPoolPoller([]PeerAddr{{"air19", addr(air19.srv)}}, testToken, nil)
	air19.down.Store(true)
	back.UseLastKnownFile(file)
	back.PollOnce(context.Background())
	assert.Equal(t, 3, back.Peers()[0].Info.Proto, "the new proto replaced the old one")

	entries, err := os.ReadDir(filepath.Dir(file))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file is left behind")
}
