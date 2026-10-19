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

// Config is what NewWatcher needs.
type Config struct {
	Host   string // this host's alias: the entries it made carry it as result.host
	Earned Earned
	Cache  Cache
	// Needs says what a task label needs (the environment labels of its manifest task); ok is false when the task is not known now.
	Needs func(label string) (needs []string, ok bool)
	// Revoked is called after labels were taken away, so the daemon stops advertising them at once (RefreshLabels).
	Revoked func()
	// References is the task labels this host holds as the reference (it earned none: ReferenceLabels), with what each needs. A reference
	// host's drift takes nothing from anyone: it only drops the reuse entries that host made, which no longer describe it. nil: none.
	References func() []Reference
}

// Reference is a task label the host holds as its reference host.
type Reference struct {
	Label string
	Needs []string
}

// Watcher revokes the earned labels whose fingerprint has drifted. Safe for concurrent use (OnFacts calls are serialized).
type Watcher struct {
	cfg  Config
	mu   sync.Mutex
	seen map[string]map[string]string // a reference label's fingerprint keys as last seen (memory only: the lookup compares whole fingerprints too)
}

// NewWatcher builds a watcher; nothing happens until OnFacts is called.
func NewWatcher(cfg Config) *Watcher { return &Watcher{cfg: cfg, seen: map[string]map[string]string{}} }

// OnFacts looks at the fingerprint of one facts record. For each earned label that drifted it deletes the row (the coordinator then sees
// a host that lacks the label and asks for a new pair on its next tick, which is the re-qualification), drops the entries this host made
// under the label, and, once, calls Revoked. A store that cannot be read or written is logged and tried again on the next record: a
// label is never revoked on a guess, and never kept past a drift that was seen.
func (w *Watcher) OnFacts(current map[string]string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.watchReferences(current)
	rows, err := w.cfg.Earned.EarnedLabels()
	var damaged *store.SkippedRowsError
	if err != nil && !errors.As(err, &damaged) { // damaged rows are skipped and the rest is the table; any other error is no answer
		log.Printf("wwd: drift: reading the earned labels: %v", err)
		return
	}
	revoked := false
	for _, r := range Revoked(rows, w.cfg.Needs, current) {
		if err := w.cfg.Earned.DeleteEarnedLabel(r.Label); err != nil {
			log.Printf("wwd: drift: revoking %s: %v", r.Label, err)
			continue
		}
		revoked = true
		n, cerr := w.cfg.Cache.DeleteCacheByLabelAndHost(r.Label, w.cfg.Host)
		if cerr != nil {
			log.Printf("wwd: drift: dropping the reuse entries of %s: %v", r.Label, cerr)
		}
		log.Printf("wwd: drift: %s revoked (%s changed); %d reuse entries dropped", r.Label, strings.Join(r.Keys, ", "), n)
	}
	if revoked && w.cfg.Revoked != nil {
		w.cfg.Revoked()
	}
}

// watchReferences drops the reuse entries this host made under a label it holds as the reference when the part of its fingerprint that
// label depends on moves. The first time a label is seen only its fingerprint is noted (nothing is known to have changed).
func (w *Watcher) watchReferences(current map[string]string) {
	if w.cfg.References == nil {
		return
	}
	live := map[string]bool{}
	for _, ref := range w.cfg.References() {
		live[ref.Label] = true
		now := map[string]string{}
		for _, k := range KeysFor(ref.Needs) {
			if v, ok := current[k]; ok {
				now[k] = v
			}
		}
		before, known := w.seen[ref.Label]
		w.seen[ref.Label] = now
		if known && !sameKeys(before, now) {
			n, err := w.cfg.Cache.DeleteCacheByLabelAndHost(ref.Label, w.cfg.Host)
			if err != nil {
				log.Printf("wwd: drift: dropping the reuse entries of reference %s: %v", ref.Label, err)
			}
			log.Printf("wwd: drift: the reference fingerprint of %s moved; %d reuse entries this host made were dropped (no label was taken from anyone)", ref.Label, n)
		}
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
