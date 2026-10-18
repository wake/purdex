package peers

// The name pool follows the persona collection (name pool v2 spec §4.4). The hostconfig module publishes the collection as a
// personas.Reader; this file turns it into the registry the naming passes read (ipeers.SetPool) at start and on every change.

import (
	"log"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/personas"
)

// buildPool turns the persona collection into the registry's two sets.
//
//   - Recognised: the built-in names and every persona code, retired ones included (spec L5: a live holder keeps its name,
//     and the code can never be taken again). A persona's display name replaces a built-in one with the same code.
//   - Pickable: the active personas in the collection's order; with no personas at all, the built-in names (spec L4).
func buildPool(set personas.Set) (recognised map[string]string, pickable []string) {
	recognised = make(map[string]string, len(ipeers.NamePool)+len(set.Items))
	for _, n := range ipeers.NamePool {
		r, size := utf8.DecodeRuneInString(n)
		recognised[n] = string(unicode.ToUpper(r)) + n[size:]
	}
	for _, p := range set.Items {
		recognised[p.Code] = p.Name
		if !p.Retired {
			pickable = append(pickable, p.Code)
		}
	}
	if len(set.Items) == 0 {
		pickable = append([]string(nil), ipeers.NamePool[:]...)
	}
	return recognised, pickable
}

// personaRetryMax bounds the retries of a read that fails: the delays double from a second up to half a minute, so a read that
// never works (a stored value that does not read) is given up after about two minutes; the next change starts over.
const personaRetryMax = 8

func personaRetryDelay(attempt int) time.Duration {
	return min(time.Second<<attempt, 30*time.Second)
}

// bindPersonas applies the collection now and again on every change. The returned function stops following.
//
//   - The hostconfig module tells its readers from goroutines of their own, so two changes can arrive out of order. Reading,
//     building and swapping are one step under a mutex, and the read happens inside it: whichever callback runs last reads the
//     last stored set, so the pool never ends on an older one.
//   - A read that fails keeps the pool as it is (the stored value's codes are reserved, so guessing an empty pool would be
//     worse), and is retried with a growing delay, because a change is told only once. A read that works makes a pending
//     retry moot.
func (m *Module) bindPersonas(r personas.Reader) (cancel func()) {
	var mu sync.Mutex
	var stopped bool
	var stopRetry func()
	var apply func(attempt int)
	apply = func(attempt int) {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return
		}
		if stopRetry != nil {
			stopRetry()
			stopRetry = nil
		}
		set, err := r.Personas()
		if err != nil {
			log.Printf("[peers] personas: %v; the name pool keeps what it had", err)
			if attempt < personaRetryMax {
				stopRetry = m.schedule(personaRetryDelay(attempt), func() { apply(attempt + 1) })
			}
			return
		}
		recognised, pickable := buildPool(set)
		ipeers.SetPool(recognised, pickable)
	}
	// Subscribe first, then read: a change that lands between the two would be told to nobody, and the first read may predate it.
	// A change told before the read finishes only makes the read run once more, and it reads the stored set again.
	unsubscribe := r.OnPersonasChange(func() { apply(0) })
	apply(0)
	return func() {
		unsubscribe()
		mu.Lock()
		stopped = true
		if stopRetry != nil {
			stopRetry()
			stopRetry = nil
		}
		mu.Unlock()
	}
}

// schedule runs fn after d; the returned function cancels it. afterFunc is the test seam.
func (m *Module) schedule(d time.Duration, fn func()) (stop func()) {
	if m.afterFunc != nil {
		return m.afterFunc(d, fn)
	}
	t := time.AfterFunc(d, fn)
	return func() { t.Stop() }
}

// startPersonaPool follows the hostconfig module's personas, when there is one (a test build without it keeps the built-in
// names).
func (m *Module) startPersonaPool() {
	if m.core == nil || m.core.Registry == nil {
		return
	}
	v, ok := m.core.Registry.Get(personas.Key)
	if !ok {
		return
	}
	if r, ok := v.(personas.Reader); ok {
		m.personaCancel = m.bindPersonas(r)
	}
}
