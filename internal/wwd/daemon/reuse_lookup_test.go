package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/reuse"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

type mapCache struct {
	rows map[string]store.CacheEntry
	err  error
}

func (m *mapCache) GetCacheEntry(key string, _ time.Time) (store.CacheEntry, bool, error) {
	c, ok := m.rows[key]
	return c, ok, m.err
}

var (
	fpMserv  = map[string]string{"os_version": "6.8", "node": "24.14.0"}
	taskTsc  = labels.TaskLabel("purdex", "tsc")
	exitZero = 0
)

func submissionFor(edit ...func(*wire.JobSubmission)) wire.JobSubmission {
	s := wire.JobSubmission{
		Repo: wire.Repo{Name: "purdex"}, Snapshot: wire.Snapshot{Tree: "7ee"}, Task: "tsc", Args: []string{}, DefHash: "def1",
		Labels: []string{"os:linux"}, Cacheable: true,
	}
	for _, e := range edit {
		e(&s)
	}
	return s
}

func peerInfo(alias, platform string, lbls []string, fp map[string]string) wire.Info {
	return wire.Info{Alias: alias, Platform: platform, Arch: "amd64", Labels: lbls, Fingerprint: fp}
}

// rigFor is a daemon "mlab" whose only peer is info, reachable; the cache holds an entry that peer made, filed under the key the
// writer would use for the peer as it was when it made it (made).
func rigFor(t *testing.T, info wire.Info, made wire.Info, reference string) (*reuser, *mapCache) {
	t.Helper()
	cache := &mapCache{rows: map[string]store.CacheEntry{}}
	d := New(Options{Alias: "mlab", Peers: []PeerAddr{{Alias: info.Alias, HostPort: "x:1"}}, ResultCache: cache})
	d.pool.state[0] = wire.Peer{Info: info, Reachable: true}
	if reference != "" {
		d.opts.ReferenceTasks = func() []labels.RefTask {
			return []labels.RefTask{{Repo: "purdex", Task: "tsc", Reference: reference}}
		}
	}
	h := reuse.HostNowFromInfo(made, taskTsc, reference != "" && reference == made.Alias, false)
	key := reuse.KeyForHost("7ee", "tsc", []string{}, "def1", made.Platform, made.Arch, h)
	cache.rows[key] = store.CacheEntry{Key: key, Result: wire.Result{Exit: &exitZero, Host: made.Alias, Log: "j_old/log"}, HostFingerprint: made.Fingerprint}
	return d.Reuser(), cache
}

func TestLookupAnswersFromAnEarnedPeerWithTheSameFingerprint(t *testing.T) {
	info := peerInfo("mserv", "linux", []string{"os:linux", taskTsc}, fpMserv)
	r, _ := rigFor(t, info, info, "")
	res, ok := r.Lookup(t.Context(), submissionFor())
	require.True(t, ok)
	assert.Equal(t, "mserv", res.Host)
	assert.Equal(t, "j_old/log", res.Log)
}

func TestLookupAnswersFromTheReferenceHost(t *testing.T) {
	info := peerInfo("air26", "darwin", []string{"os:darwin"}, fpMserv) // holds no task label: it is the reference
	r, _ := rigFor(t, info, info, "air26")
	_, ok := r.Lookup(t.Context(), submissionFor(func(s *wire.JobSubmission) { s.Labels = []string{"os:darwin"} }))
	assert.True(t, ok)
}

func TestLookupMisses(t *testing.T) {
	earned := peerInfo("mserv", "linux", []string{"os:linux", taskTsc}, fpMserv)
	moved := peerInfo("mserv", "linux", []string{"os:linux", taskTsc}, map[string]string{"os_version": "6.9", "node": "24.14.0"})
	noLabel := peerInfo("mserv", "linux", []string{"os:linux"}, fpMserv)
	for name, tc := range map[string]struct {
		now, made wire.Info
		edit      func(*wire.JobSubmission)
	}{
		"the host moved on since it made the result": {now: moved, made: earned},
		"the host lost the task label":               {now: noLabel, made: earned},
		"the job names an os the host is not":        {now: earned, made: earned, edit: func(s *wire.JobSubmission) { s.Labels = []string{"os:darwin"} }},
		"another definition":                         {now: earned, made: earned, edit: func(s *wire.JobSubmission) { s.DefHash = "def2" }},
		"another snapshot":                           {now: earned, made: earned, edit: func(s *wire.JobSubmission) { s.Snapshot.Tree = "other" }},
		"other arguments":                            {now: earned, made: earned, edit: func(s *wire.JobSubmission) { s.Args = []string{"x"} }},
	} {
		r, _ := rigFor(t, tc.now, tc.made, "")
		var edit []func(*wire.JobSubmission)
		if tc.edit != nil {
			edit = append(edit, tc.edit)
		}
		_, ok := r.Lookup(t.Context(), submissionFor(edit...))
		assert.False(t, ok, name)
	}
}

// Two hosts with one fingerprint file their results under one key: the host that did not make it must not answer with it.
func TestLookupOnlyTheHostThatMadeTheEntryAnswersForItself(t *testing.T) {
	asked := peerInfo("mserv", "linux", []string{"os:linux", taskTsc}, fpMserv)
	r, cache := rigFor(t, asked, asked, "")
	for k, c := range cache.rows {
		c.Result.Host = "mserv-2"
		cache.rows[k] = c
	}
	_, ok := r.Lookup(t.Context(), submissionFor())
	assert.False(t, ok)
}

func TestLookupSkipsAPeerThatDoesNotAnswerAndFailsClosedOnAStoreError(t *testing.T) {
	info := peerInfo("mserv", "linux", []string{"os:linux", taskTsc}, fpMserv)
	r, cache := rigFor(t, info, info, "")
	r.d.pool.state[0].Reachable = false
	_, ok := r.Lookup(t.Context(), submissionFor())
	assert.False(t, ok, "an unreachable peer is not asked")
	r.d.pool.state[0].Reachable = true
	cache.err = errors.New("disk")
	_, ok = r.Lookup(t.Context(), submissionFor())
	assert.False(t, ok, "a store that cannot be read is a miss")
}

func TestNoResultCacheNoReuser(t *testing.T) {
	assert.Nil(t, New(Options{Alias: "mlab"}).Reuser())
}
