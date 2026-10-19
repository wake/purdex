package daemon

import (
	"context"
	"log"
	"slices"
	"time"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/reuse"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// ResultCache is the result cache (store.Store): a submission is answered from it (reuse_lookup.go) and a result is filed in it
// (reuse_store.go).
type ResultCache interface {
	GetCacheEntry(key string, now time.Time) (store.CacheEntry, bool, error)
	PutCacheEntry(c store.CacheEntry, now time.Time) error
}

// ReferenceHost is referenceHost for the queue (Options.ReferenceHost): the reference of a task at the moment a job of it ends.
func (d *Daemon) ReferenceHost(repo, task string) string { return d.referenceHost(repo, task) }

// referenceHost is the alias of the host that is the task's reference ("" for a task none names), from the manifests of this host's repos.
func (d *Daemon) referenceHost(repo, task string) string {
	if d.opts.ReferenceTasks == nil {
		return ""
	}
	for _, t := range d.opts.ReferenceTasks() {
		if t.Repo == repo && t.Task == task {
			return t.Reference
		}
	}
	return ""
}

// reuser answers a submission from a stored result (spec §4.5). The key a result is filed under holds the producing host's own
// fingerprint, which the submitter does not know, so every host that could have made the result is asked in turn: this one and the
// reachable peers. For each, the key is built from what that host is now, the entry is read, and reuse.Answers decides.
type reuser struct{ d *Daemon }

// Reuser is the daemon as jobsapi.Reuser; nil without a result cache.
func (d *Daemon) Reuser() *reuser {
	if d.opts.ResultCache == nil {
		return nil
	}
	return &reuser{d: d}
}

// reuseNow is the clock the cache reads and writes are dated by.
func (d *Daemon) reuseNow() time.Time {
	if d.opts.Now != nil {
		return d.opts.Now()
	}
	return time.Now()
}

// Lookup returns the stored result that answers s, or false. Any doubt (a store that cannot be read, an entry made by another host
// than the one asked about) is a miss: the job runs.
func (r *reuser) Lookup(_ context.Context, s wire.JobSubmission) (wire.Result, bool) {
	d := r.d
	taskLabel := labels.TaskLabel(s.Repo.Name, s.Task)
	reference := d.referenceHost(s.Repo.Name, s.Task)
	q := reuse.Query{Tree: s.Snapshot.Tree, Task: s.Task, Args: s.Args, DefHash: s.DefHash, Labels: s.Labels, Host: d.opts.Alias}
	try := func(info wire.Info, neverQuarantined bool) (wire.Result, bool) {
		h := reuse.HostNowFromInfo(info, taskLabel, reference != "" && reference == info.Alias, neverQuarantined)
		key := reuse.KeyForHost(q.Tree, q.Task, q.Args, q.DefHash, info.Platform, info.Arch, h)
		c, ok, err := d.opts.ResultCache.GetCacheEntry(key, r.d.reuseNow())
		if err != nil {
			log.Printf("wwd: reuse: reading the cache entry of %s: %v", info.Alias, err)
			return wire.Result{}, false
		}
		// the same fingerprint on two hosts files their results under one key: only the host asked about may answer for itself
		if !ok || c.Result.Host != info.Alias {
			return wire.Result{}, false
		}
		e := reuse.Entry{Tree: q.Tree, Task: q.Task, Args: q.Args, DefHash: q.DefHash, Host: c.Result.Host, Fingerprint: c.HostFingerprint}
		if !reuse.Answers(e, q, h) {
			return wire.Result{}, false
		}
		return c.Result, true
	}
	if res, ok := try(d.info(), r.localNeverQuarantined(taskLabel)); ok {
		return res, true
	}
	for _, p := range d.pool.Peers() {
		if !p.Reachable {
			continue
		}
		// a quarantined label is not advertised, so a peer's standing is unverified until the origin can read it (WWD-5c-2)
		if res, ok := try(p.Info, false); ok {
			return res, true
		}
	}
	return wire.Result{}, false
}

// localNeverQuarantined says whether this host's own earned labels hold no quarantined row for the task; an unreadable store says no.
func (r *reuser) localNeverQuarantined(taskLabel string) bool {
	if r.d.opts.Earned == nil {
		return false
	}
	rows, err := r.d.opts.Earned.EarnedLabels()
	if err != nil {
		return false
	}
	return !slices.ContainsFunc(rows, func(l wire.EarnedLabel) bool {
		return l.Label == taskLabel && l.State == wire.EarnedLabelQuarantined
	})
}
