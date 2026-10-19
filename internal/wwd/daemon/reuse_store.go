package daemon

import (
	"context"
	"log"
	"maps"
	"strings"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/reuse"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// FileResult files a succeeded job's result for reuse (spec §4.5); it is the queue's Options.OnSucceeded. The result is filed under the
// host that made it, as that host was when it claimed the job (the worker's own words in the claim), and only when that is still what the
// host is: a host whose toolchain moved while the job ran may have made the result with either toolchain, and is not asked. Anything missing
// or doubtful is not filed (a job that runs again costs time; a wrong reuse costs a wrong answer):
//   - the claim said nothing about the host's labels or fingerprint, or the task has no definition hash (a job from before schema 7);
//   - the host that made the result is not this one and is not a reachable peer, so its fingerprint cannot be checked now;
//   - the fingerprint at the claim is not the host's fingerprint now.
func (d *Daemon) FileResult(_ context.Context, j wire.Job, p queue.Produced) {
	if d.opts.ResultCache == nil || j.Result == nil {
		return
	}
	host := j.Result.Host
	platform, arch := platformArch(p.Labels)
	if host == "" || p.DefHash == "" || len(p.Fingerprint) == 0 || platform == "" || arch == "" {
		return
	}
	now, ok := d.currentInfo(host)
	if !ok || len(now.Fingerprint) == 0 || !maps.Equal(now.Fingerprint, p.Fingerprint) {
		return
	}
	taskLabel := labels.TaskLabel(j.Repo.Name, j.Task)
	// the host as it was at the claim; HostNowFromInfo is the lookup's own reading, so the two build one key
	at := wire.Info{Alias: host, Platform: platform, Arch: arch, Labels: p.Labels, Fingerprint: p.Fingerprint}
	h := reuse.HostNowFromInfo(at, taskLabel, host == d.referenceHost(j.Repo.Name, j.Task), false)
	key := reuse.KeyForHost(j.Snapshot.Tree, j.Task, j.Args, p.DefHash, platform, arch, h)
	t := d.reuseNow()
	err := d.opts.ResultCache.PutCacheEntry(store.CacheEntry{
		Key: key, Result: *j.Result, HostFingerprint: maps.Clone(p.Fingerprint), Label: taskLabel, CreatedAt: t,
	}, t)
	if err != nil {
		log.Printf("wwd: reuse: filing the result of %s: %v", j.ID, err)
	}
}

// currentInfo is what the host with this alias says it is now: this host's own, or a reachable peer's; false for any other.
func (d *Daemon) currentInfo(alias string) (wire.Info, bool) {
	if alias == d.opts.Alias {
		return d.info(), true
	}
	for _, p := range d.pool.Peers() {
		if p.Reachable && p.Info.Alias == alias {
			return p.Info, true
		}
	}
	return wire.Info{}, false
}

// platformArch reads the os and the arch a worker advertised (os:<name>, arch:<name>); "" for one it did not.
func platformArch(lbls []string) (platform, arch string) {
	for _, l := range lbls {
		if v, ok := strings.CutPrefix(l, "os:"); ok && platform == "" {
			platform = v
		}
		if v, ok := strings.CutPrefix(l, "arch:"); ok && arch == "" {
			arch = v
		}
	}
	return platform, arch
}
