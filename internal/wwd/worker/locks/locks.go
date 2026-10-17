// Package locks is the table of mutual-exclusion keys held on this host (spec §4.1, §5 Locks). A job's `locks` are keys that no
// two jobs may hold at once on the host that runs them, whichever origin they came from and whether a worker or the local fast
// lane runs them. Whoever is about to start a job — the worker claiming a remote job, the fast-lane entry — goes through one
// Table: check, register and record are one step with respect to every other start on the host.
package locks

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Conflict says which owner already holds a key a start asked for.
type Conflict struct {
	Key    string
	Holder string
}

func (c Conflict) String() string { return fmt.Sprintf("%s (held by %s)", c.Key, c.Holder) }

// BusyError is the refusal of an Acquire: every conflicting key with its holder, sorted by key.
type BusyError struct{ Conflicts []Conflict }

func (e *BusyError) Error() string {
	parts := make([]string, len(e.Conflicts))
	for i, c := range e.Conflicts {
		parts[i] = c.String()
	}
	return "locks busy: " + strings.Join(parts, ", ")
}

// ErrAlreadyHeld: the owner already has keys registered; an owner is one job and registers once.
var ErrAlreadyHeld = errors.New("locks: the owner already holds keys")

// Table is the keys held on this host. The zero value is not usable; use New.
type Table struct {
	mu     sync.Mutex
	byKey  map[string]string // key -> owner
	byHold map[string]hold   // owner -> what it holds
	gen    uint64
}

type hold struct {
	keys []string // sorted
	gen  uint64   // which Acquire made it: a late finish of an older one must not touch a newer one
}

// New returns an empty table.
func New() *Table {
	return &Table{byKey: map[string]string{}, byHold: map[string]hold{}}
}

// normalize checks the keys and returns them sorted. A key is non-empty text without NUL or newline; a repeated key is an
// error (the manifest rejects it too).
func normalize(keys []string) ([]string, error) {
	out := append([]string(nil), keys...)
	sort.Strings(out)
	for i, k := range out {
		if k == "" || strings.ContainsAny(k, "\x00\n") {
			return nil, fmt.Errorf("locks: invalid key %q", k)
		}
		if i > 0 && out[i-1] == k {
			return nil, fmt.Errorf("locks: key %q is repeated", k)
		}
	}
	return out, nil
}

// Acquire registers owner as the holder of keys, all or none. When a key is held by another owner nothing is registered and a
// *BusyError names every conflict. If record is not nil it runs after the keys are registered and before Acquire returns, and
// without the table locked: the keys are already reserved, so no other start can slip in while the job is being recorded (for
// example in the queue), and a record that fails releases them again. An owner with no keys is registered as holding nothing.
func (t *Table) Acquire(owner string, keys []string, record func() error) error {
	if owner == "" {
		return errors.New("locks: empty owner")
	}
	sorted, err := normalize(keys)
	if err != nil {
		return err
	}
	t.mu.Lock()
	if _, held := t.byHold[owner]; held {
		t.mu.Unlock()
		return ErrAlreadyHeld
	}
	var busy []Conflict
	for _, k := range sorted {
		if h, ok := t.byKey[k]; ok {
			busy = append(busy, Conflict{Key: k, Holder: h})
		}
	}
	if len(busy) > 0 {
		t.mu.Unlock()
		return &BusyError{Conflicts: busy}
	}
	for _, k := range sorted {
		t.byKey[k] = owner
	}
	t.gen++
	mine := t.gen
	t.byHold[owner] = hold{keys: sorted, gen: mine}
	t.mu.Unlock()

	if record == nil {
		return nil
	}
	err = record()
	t.mu.Lock()
	defer t.mu.Unlock()
	if cur, ok := t.byHold[owner]; !ok || cur.gen != mine {
		// Released while it was being recorded (a cancel, a cleanup): the keys may be somebody else's by now, so the job must
		// not start, and nothing of a newer acquisition by the same owner is touched.
		if err == nil {
			err = ErrReleased
		}
		return err
	}
	if err != nil {
		t.releaseLocked(owner)
	}
	return err
}

// ErrReleased: the owner's keys were released while its Acquire was still recording the job. The caller must not start it.
var ErrReleased = errors.New("locks: released before the job was recorded")

// Release removes every key owner holds. Releasing an owner that holds nothing is not an error.
func (t *Table) Release(owner string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.releaseLocked(owner)
}

func (t *Table) releaseLocked(owner string) {
	for _, k := range t.byHold[owner].keys {
		if t.byKey[k] == owner {
			delete(t.byKey, k)
		}
	}
	delete(t.byHold, owner)
}

// Holder is the owner of key, if any.
func (t *Table) Holder(key string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h, ok := t.byKey[key]
	return h, ok
}

// Conflicts lists the keys of keys that another owner holds, without registering anything (for a worker deciding which job to
// try; the claim itself still goes through Acquire).
func (t *Table) Conflicts(keys []string) []Conflict {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Conflict
	for _, k := range keys {
		if h, ok := t.byKey[k]; ok {
			out = append(out, Conflict{Key: k, Holder: h})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Held is a copy of the table: key to owner.
func (t *Table) Held() map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]string, len(t.byKey))
	for k, o := range t.byKey {
		out[k] = o
	}
	return out
}

// Owners is how many owners are registered (including those that hold no keys).
func (t *Table) Owners() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.byHold)
}
