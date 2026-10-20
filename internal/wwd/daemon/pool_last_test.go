package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
			panic(http.ErrAbortHandler) // the connection is dropped: nothing answers over HTTP, as with a stopped wwd
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
		"other":     ptr(`{"peers":{"mserv":{"info":{"alias":"mserv","proto":3}}}}`),        // another alias
		"wrongname": ptr(`{"peers":{"air19":{"info":{"alias":"someone-else","proto":3}}}}`), // an answer under another name
		"future":    ptr(`{"peers":{"air19":{"info":{"alias":"air19","proto":99}}}}`),       // a proto this build does not read
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

// A file written by a later build may carry fields this build does not know (#3710): they are skipped, and the peer is known by the rest.
func TestAFieldOfALaterBuildInTheLastKnownFileIsSkipped(t *testing.T) {
	air19 := newLastPeer(t, "air19", wire.Proto)
	air19.down.Store(true)
	file := filepath.Join(t.TempDir(), "peers-last.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"peers":{"air19":{"info":{"alias":"air19","proto":3},"surprise":true}}}`), 0o600))
	p := NewPoolPoller([]PeerAddr{{"air19", addr(air19.srv)}}, testToken, nil)
	p.UseLastKnownFile(file)
	p.PollOnce(context.Background())
	assert.Equal(t, 3, p.Peers()[0].Info.Proto)
}

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

// The file is read only when it is what this wwd wrote: a plain file that nobody else can write, of a size a few peers can make.
func TestTheLastKnownFileIsNotTrustedWhenItIsNotPlainPrivateAndSmall(t *testing.T) {
	air19 := newLastPeer(t, "air19", wire.Proto)
	air19.down.Store(true)
	peers := []PeerAddr{{"air19", addr(air19.srv)}}
	good := `{"peers":{"air19":{"info":{"alias":"air19","proto":3}}}}`
	seen := func(t *testing.T, file string) int {
		p := NewPoolPoller(peers, testToken, nil)
		p.UseLastKnownFile(file)
		return p.Peers()[0].Info.Proto
	}
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain.json")
	require.NoError(t, os.WriteFile(plain, []byte(good), 0o600))
	require.Equal(t, 3, seen(t, plain), "the file as it is written is read")

	link := filepath.Join(dir, "link.json")
	require.NoError(t, os.Symlink(plain, link))
	assert.Equal(t, wire.MinProto, seen(t, link), "a link is not followed")

	open := filepath.Join(dir, "open.json")
	require.NoError(t, os.WriteFile(open, []byte(good), 0o666))
	require.NoError(t, os.Chmod(open, 0o666))
	assert.Equal(t, wire.MinProto, seen(t, open), "a file others can write is not trusted")

	big := filepath.Join(dir, "big.json")
	require.NoError(t, os.WriteFile(big, []byte(good+strings.Repeat(" ", lastKnownMax)), 0o600))
	assert.Equal(t, wire.MinProto, seen(t, big), "a file over the cap is not read")
}

// A write that is slow does not hold the pool: the view, the downs and the gates are read while it goes on.
func TestASlowWriteOfTheLastKnownFileDoesNotBlockThePool(t *testing.T) {
	air19 := newLastPeer(t, "air19", wire.Proto)
	p := NewPoolPoller([]PeerAddr{{"air19", addr(air19.srv)}}, testToken, nil)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	p.UseLastKnownFile(filepath.Join(t.TempDir(), "peers-last.json"))
	p.saveLast = func(string, []byte) error {
		started <- struct{}{}
		<-release
		return nil
	}
	done := make(chan struct{})
	go func() { p.PollOnce(context.Background()); close(done) }()
	<-started
	read := make(chan struct{})
	go func() { _ = p.Peers(); _ = p.Downs(); close(read) }()
	select {
	case <-read:
	case <-time.After(3 * time.Second):
		t.Fatal("the pool was held by a write of the last known file")
	}
	close(release)
	<-done
}

// A write that failed is tried again by the next poll, though the info has not changed since.
func TestAFailedWriteOfTheLastKnownFileIsTriedAgain(t *testing.T) {
	file := filepath.Join(t.TempDir(), "peers-last.json")
	air19 := newLastPeer(t, "air19", wire.Proto)
	p := NewPoolPoller([]PeerAddr{{"air19", addr(air19.srv)}}, testToken, nil)
	p.UseLastKnownFile(file)
	save := p.saveLast
	fail := true
	p.saveLast = func(path string, b []byte) error {
		if fail {
			return errors.New("disk full")
		}
		return save(path, b)
	}
	p.PollOnce(context.Background())
	_, err := os.Stat(file)
	require.Error(t, err)
	fail = false
	p.PollOnce(context.Background())
	_, err = os.Stat(file)
	assert.NoError(t, err)
}
