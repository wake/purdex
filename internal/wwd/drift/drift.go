// Package drift is how a host notices that what it earned a task label on has moved (spec §7, "Drift"): a fingerprint change in the OS
// version or in a tool the label needs revokes the label. The decision is a pure function; the Watcher is its caller, run at the facts
// cadence.
package drift

import (
	"errors"
	"log"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// OSVersionKey is the fingerprint key of the OS version (labels.FromFacts).
const OSVersionKey = "os_version"

// ToolKey is the fingerprint key a tool: need depends on: tool:node24 → node, tool:go1.26 → go, tool:pnpm10 → pnpm, tool:php84 → php.
// ok is false for a label that is not a tool: need.
func ToolKey(need string) (key string, ok bool) {
	name, found := strings.CutPrefix(need, "tool:")
	if !found {
		return "", false
	}
	name = strings.TrimRight(name, "0123456789.")
	if name == "" {
		return "", false
	}
	return name, true
}

// KeysFor is the fingerprint keys a label depends on given the needs of its task: the OS version and the key of every tool: need,
// sorted, without repeats.
func KeysFor(needs []string) []string {
	keys := []string{OSVersionKey}
	for _, n := range needs {
		if k, ok := ToolKey(n); ok && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Changed is the keys among keys whose value is not the same now as in stored. A key stored but absent now has changed (the tool is gone),
// and so does one that is absent from stored (nothing shows it is the same): the label is then not trusted.
func Changed(stored, current map[string]string, keys []string) []string {
	var out []string
	for _, k := range keys {
		s, sok := stored[k]
		c, cok := current[k]
		if !sok || !cok || s != c {
			out = append(out, k)
		}
	}
	return out
}

// Revocation is a label to take away and the fingerprint keys that moved.
type Revocation struct {
	Label string
	Keys  []string
}

// Revoked is the earned labels (state earned) whose fingerprint has drifted: needs says what a task label needs (ok is false for a task
// that is no longer known, whose label is then left alone: that is a manifest change, not a drift). A quarantined label is not looked at;
// it is already out. Sorted by label.
func Revoked(earned []wire.EarnedLabel, needs func(label string) (needs []string, ok bool), current map[string]string) []Revocation {
	var out []Revocation
	for _, e := range earned {
		if e.State != wire.EarnedLabelEarned {
			continue
		}
		n, ok := needs(e.Label)
		if !ok {
			continue
		}
		if keys := Changed(e.Fingerprint, current, KeysFor(n)); len(keys) > 0 {
			out = append(out, Revocation{Label: e.Label, Keys: keys})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// Earned is the earned-label table, as far as the watcher needs it.
type Earned interface {
	EarnedLabels() ([]wire.EarnedLabel, error)
	DeleteEarnedLabel(label string) error
}

// Cache is the result cache, as far as the watcher needs it: the entries this host made under a label.
type Cache interface {
	DeleteCacheByLabelAndHost(label, host string) (int, error)
}

// Task is a task label the host knows from the manifests of its prepared clones.
type Task struct {
	Label string
	Needs []string // the environment labels of the task
	// IsReference: this host is the task's reference host. It holds the label without earning it (labels.ReferenceLabels): its drift
	// takes nothing from anyone and only drops the reuse entries this host made.
	IsReference bool
}

// Config is what NewWatcher needs.
type Config struct {
	Host   string // this host's alias: the entries it made carry it as result.host
	Earned Earned
	Cache  Cache
	// Tasks is the tasks the host knows now. It is called once per record (the manifests are files: the caller keeps it cheap).
	Tasks func() []Task
	// Revoked is called after labels were taken away, so the daemon stops advertising them at once (RefreshLabels).
	Revoked func()
}

// Watcher revokes the earned labels whose fingerprint has drifted. Safe for concurrent use (OnFacts calls are serialized).
type Watcher struct {
	cfg     Config
	mu      sync.Mutex
	seen    map[string]map[string]string // a reference label's fingerprint keys as last seen (memory only: the lookup compares whole fingerprints too)
	pending map[string]bool              // labels whose reuse entries still have to be dropped (the label itself is already revoked)
}

// NewWatcher builds a watcher; nothing happens until OnFacts is called.
func NewWatcher(cfg Config) *Watcher {
	return &Watcher{cfg: cfg, seen: map[string]map[string]string{}, pending: map[string]bool{}}
}

// OnFacts looks at the fingerprint of one facts record. For each earned label that drifted it deletes the row (the coordinator then sees
// a host that lacks the label and asks for a new pair on its next tick, which is the re-qualification), drops the entries this host made
// under the label, and, once, calls Revoked. Taking the label away comes first: if the entries cannot be dropped just now the label is
// still gone, and the drop is tried again on every following record until it works (the lookup compares fingerprints too, so a leftover
// entry is not reused in the meantime). A store that cannot be read is logged and nothing is revoked on a guess.
func (w *Watcher) OnFacts(current map[string]string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	tasks := w.cfg.Tasks() // once: it reads the manifests
	w.watchReferences(tasks, current)
	rows, err := w.cfg.Earned.EarnedLabels()
	var damaged *store.SkippedRowsError
	if err != nil && !errors.As(err, &damaged) { // damaged rows are skipped and the rest is the table; any other error is no answer
		log.Printf("wwd: drift: reading the earned labels: %v", err)
		return
	}
	w.retryPending(rows)
	needs := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		needs[t.Label] = t.Needs
	}
	revoked := false
	for _, r := range Revoked(rows, func(label string) ([]string, bool) { n, ok := needs[label]; return n, ok }, current) {
		if err := w.cfg.Earned.DeleteEarnedLabel(r.Label); err != nil {
			log.Printf("wwd: drift: revoking %s: %v", r.Label, err)
			continue
		}
		revoked = true
		log.Printf("wwd: drift: %s revoked (%s changed)", r.Label, strings.Join(r.Keys, ", "))
		w.dropEntries(r.Label, true)
	}
	if revoked && w.cfg.Revoked != nil {
		w.cfg.Revoked()
	}
}

// dropEntries drops the reuse entries this host made under the label; a failure is remembered and retried.
func (w *Watcher) dropEntries(label string, track bool) bool {
	n, err := w.cfg.Cache.DeleteCacheByLabelAndHost(label, w.cfg.Host)
	if err != nil {
		log.Printf("wwd: drift: dropping the reuse entries of %s: %v (will try again)", label, err)
		if track { // a reference label retries through its unmoved note instead
			w.pending[label] = true
		}
		return false
	}
	delete(w.pending, label)
	if n > 0 {
		log.Printf("wwd: drift: %d reuse entries of %s dropped", n, label)
	}
	return true
}

// retryPending drops what an earlier record could not. A label that has been earned again since is let go instead: the entries now under
// it belong to the new qualification, and a leftover old one cannot be reused (the lookup compares fingerprints).
func (w *Watcher) retryPending(rows []wire.EarnedLabel) {
	earned := map[string]bool{}
	for _, r := range rows {
		earned[r.Label] = true
	}
	for label := range w.pending {
		if earned[label] {
			delete(w.pending, label)
			continue
		}
		w.dropEntries(label, true)
	}
}

// watchReferences drops the reuse entries this host made under a label it holds as the reference when the part of its fingerprint that
// label depends on moves. The first time a label is seen only its fingerprint is noted (nothing is known to have changed); the noted
// fingerprint moves on only once the entries are dropped, so a failure is met again on the next record.
func (w *Watcher) watchReferences(tasks []Task, current map[string]string) {
	live := map[string]bool{}
	for _, t := range tasks {
		if !t.IsReference {
			continue
		}
		live[t.Label] = true
		now := map[string]string{}
		for _, k := range KeysFor(t.Needs) {
			if v, ok := current[k]; ok {
				now[k] = v
			}
		}
		before, known := w.seen[t.Label]
		if known && !sameKeys(before, now) {
			log.Printf("wwd: drift: the reference fingerprint of %s moved (no label is taken from anyone)", t.Label)
			if !w.dropEntries(t.Label, false) {
				continue // keep the old note: the difference is still there on the next record
			}
		}
		w.seen[t.Label] = now
	}
	for l := range w.seen { // a task that is no longer ours as the reference
		if !live[l] {
			delete(w.seen, l)
		}
	}
}

func sameKeys(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
