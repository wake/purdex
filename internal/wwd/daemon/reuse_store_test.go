package daemon

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

func succeededJob(host string) wire.Job {
	return wire.Job{
		ID: "j_1", Repo: wire.Repo{Name: "purdex"}, Snapshot: wire.Snapshot{Tree: "7ee"}, Task: "tsc", Args: []string{}, Cacheable: true,
		State:  wire.StateSucceeded,
		Result: &wire.Result{Exit: &exitZero, Host: host, Log: "j_1/log"},
	}
}

func producedBy(info wire.Info) queue.Produced {
	return queue.Produced{DefHash: "def1", Labels: info.Labels, Fingerprint: info.Fingerprint}
}

var mservLabels = []string{"os:linux", "arch:amd64", taskTsc}

// What FileResult files is what Lookup asks for: a result filed for an earned peer answers the next submission, and so does one filed for
// the reference host. This is the test that the two sides build one key.
func TestAFiledResultIsFoundByTheLookup(t *testing.T) {
	for name, tc := range map[string]struct {
		info      wire.Info
		reference string
		labels    []string
	}{
		"an earned peer":     {info: peerInfo("mserv", "linux", mservLabels, fpMserv), labels: []string{"os:linux"}},
		"the reference host": {info: peerInfo("mserv", "linux", mservLabels[:2], fpMserv), reference: "mserv", labels: []string{"os:linux"}},
	} {
		r, cache := rigFor(t, tc.info, tc.info, tc.reference)
		cache.rows = map[string]store.CacheEntry{} // rigFor filed one by hand: start empty
		_, ok := r.Lookup(t.Context(), submissionFor())
		require.False(t, ok, name)

		r.d.FileResult(t.Context(), succeededJob("mserv"), producedBy(tc.info))
		require.Len(t, cache.rows, 1, name)
		res, ok := r.Lookup(t.Context(), submissionFor())
		assert.True(t, ok, name)
		assert.Equal(t, "mserv", res.Host, name)
		for _, c := range cache.rows {
			assert.Equal(t, taskTsc, c.Label, name)
			assert.Equal(t, fpMserv, c.HostFingerprint, name)
		}
	}
}

// The host's own results too: it has no standing for the task, so only its own submission is answered (spec §4.5 condition 4).
func TestALocalResultIsFiledAndAnswersOnlyItsOwnHost(t *testing.T) {
	cache := &mapCache{rows: map[string]store.CacheEntry{}}
	d := New(Options{Alias: "mlab", ResultCache: cache, Earned: &fakeEarned{}})
	facts := linuxFacts // this host's own platform: Info.Platform is the build's, the labels come from the facts
	facts.Platform, facts.Arch = runtime.GOOS, runtime.GOARCH
	d.OnFacts(facts)
	lbls, fp := d.Labels()
	d.FileResult(t.Context(), succeededJob("mlab"), queue.Produced{DefHash: "def1", Labels: lbls, Fingerprint: fp})
	require.Len(t, cache.rows, 1)
	ask := submissionFor(func(s *wire.JobSubmission) { s.Labels = []string{"os:" + runtime.GOOS} })
	_, ok := d.Reuser().Lookup(t.Context(), ask)
	assert.True(t, ok, "its own host asks")
	d2 := New(Options{Alias: "air26", ResultCache: cache, Earned: &fakeEarned{}})
	d2.pool.state = []wire.Peer{{Info: d.info(), Reachable: true}}
	_, ok = d2.Reuser().Lookup(t.Context(), ask)
	assert.False(t, ok, "another host that is not the submitter of the same host gets nothing from a host without standing")
}

func TestAResultIsNotFiledWhenAnythingIsMissingOrDoubtful(t *testing.T) {
	good := peerInfo("mserv", "linux", mservLabels, fpMserv)
	moved := peerInfo("mserv", "linux", mservLabels, map[string]string{"os_version": "6.9", "node": "24.14.0"})
	for name, tc := range map[string]struct {
		now   wire.Info // what the pool says the host is now
		reach bool
		p     queue.Produced
		job   func(*wire.Job)
	}{
		"no fingerprint in the claim": {now: good, reach: true, p: queue.Produced{DefHash: "def1", Labels: mservLabels}},
		"no definition hash":          {now: good, reach: true, p: queue.Produced{Labels: mservLabels, Fingerprint: fpMserv}},
		"no os label":                 {now: good, reach: true, p: queue.Produced{DefHash: "def1", Labels: []string{"arch:amd64"}, Fingerprint: fpMserv}},
		"the host moved while it ran": {now: moved, reach: true, p: producedBy(good)},
		// the same toolchain, but the task label it claimed under is gone (revoked, quarantined) or is there now and was not: its standing
		// changed while the job ran, and the result is not filed under the standing of the claim (the entry would come back to life if the
		// host earned the label again with the same fingerprint)
		"the label was taken away while it ran": {now: peerInfo("mserv", "linux", []string{"os:linux", "arch:amd64"}, fpMserv), reach: true, p: producedBy(good)},
		"the label was earned while it ran":     {now: good, reach: true, p: queue.Produced{DefHash: "def1", Labels: []string{"os:linux", "arch:amd64"}, Fingerprint: fpMserv}},
		"the host cannot be asked":              {now: good, reach: false, p: producedBy(good)},
		"a host the pool does not know":         {now: good, reach: true, p: producedBy(good), job: func(j *wire.Job) { j.Result.Host = "elsewhere" }},
		"no result":                             {now: good, reach: true, p: producedBy(good), job: func(j *wire.Job) { j.Result = nil }},
	} {
		cache := &mapCache{rows: map[string]store.CacheEntry{}}
		d := New(Options{Alias: "mlab", Peers: []PeerAddr{{Alias: "mserv", HostPort: "x:1"}}, ResultCache: cache})
		d.pool.state[0] = wire.Peer{Info: tc.now, Reachable: tc.reach}
		j := succeededJob("mserv")
		if tc.job != nil {
			tc.job(&j)
		}
		d.FileResult(t.Context(), j, tc.p)
		assert.Empty(t, cache.rows, name)
	}
}

// A store that cannot be written is logged, not raised: the job's result has already been given to its worker.
func TestAStoreThatRefusesTheWriteDoesNotFailTheResult(t *testing.T) {
	info := peerInfo("mserv", "linux", mservLabels, fpMserv)
	r, cache := rigFor(t, info, info, "")
	cache.rows = map[string]store.CacheEntry{}
	cache.putErr = errors.New("disk full")
	r.d.FileResult(t.Context(), succeededJob("mserv"), producedBy(info))
	assert.Empty(t, cache.rows)
}

var _ = labels.TaskLabel
var _ = time.Second
