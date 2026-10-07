package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// inflightMaxKeys caps how many distinct keys Summary lists.
const inflightMaxKeys = 10

// inflightTracker counts requests that have entered the outer handler and not
// yet returned (including upgraded WS handlers), keyed by METHOD plus the
// first two path segments. Pure observation (#1767): the key never carries a
// query string, token or deeper path parameter, and it is read only at
// shutdown.
type inflightTracker struct {
	mu sync.Mutex
	m  map[string]int
}

func newInflightTracker() *inflightTracker {
	return &inflightTracker{m: map[string]int{}}
}

// inflightKey is "METHOD /seg1/seg2" — never the query, never deeper segments.
func inflightKey(r *http.Request) string {
	segs := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 3)
	if len(segs) > 2 {
		segs = segs[:2]
	}
	return r.Method + " /" + strings.Join(segs, "/")
}

// Enter registers the request and returns the func that releases it.
func (t *inflightTracker) Enter(r *http.Request) func() {
	key := inflightKey(r)
	t.mu.Lock()
	t.m[key]++
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		if t.m[key] <= 1 {
			delete(t.m, key)
		} else {
			t.m[key]--
		}
		t.mu.Unlock()
	}
}

// Total is the number of requests currently inside a handler.
func (t *inflightTracker) Total() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, c := range t.m {
		n += c
	}
	return n
}

// Summary renders "[GET /api/events 1, GET /ws/terminal 2]": highest count
// first (ties by key), at most inflightMaxKeys entries.
func (t *inflightTracker) Summary() string {
	t.mu.Lock()
	type kv struct {
		k string
		n int
	}
	all := make([]kv, 0, len(t.m))
	for k, n := range t.m {
		all = append(all, kv{k, n})
	}
	t.mu.Unlock()
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].k < all[j].k
	})
	if len(all) > inflightMaxKeys {
		all = all[:inflightMaxKeys]
	}
	parts := make([]string, len(all))
	for i, e := range all {
		parts[i] = fmt.Sprintf("%s %d", e.k, e.n)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// Wrap counts every request through h for the duration of its handler.
func (t *inflightTracker) Wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done := t.Enter(r)
		defer done()
		h.ServeHTTP(w, r)
	})
}

// inflightSource is what serveAndWait reads at shutdown.
type inflightSource interface {
	Total() int
	Summary() string
}

// processInflight is the daemon's tracker: newOuterHandler wraps the outer
// mux with it and runServe hands it to serveAndWait.
var processInflight = newInflightTracker()
