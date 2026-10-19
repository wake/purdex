package peers

import (
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// The pool is not a fixed list any more (name pool v2 spec §4.4): the peers module swaps this registry whenever the persona
// collection changes. It holds two things apart:
//
//   - the recognised names, with their display names: every name PoolNameOf may read at the end of a virtual name. A retired
//     persona stays here, so a live holder keeps its name and the journal still shows it (spec L5);
//   - the pickable names: what a new conversation may be given. Retired personas are not here.
//
// Until SetPool is called, and after ResetPool, both are the built-in NamePool (spec L4).
type pool struct {
	recognised map[string]string // code -> display name
	pickable   []string
}

var current atomic.Pointer[pool]

var builtInPool = func() *pool {
	p := &pool{recognised: make(map[string]string, len(NamePool)), pickable: append([]string(nil), NamePool[:]...)}
	for _, n := range NamePool {
		r, size := utf8.DecodeRuneInString(n)
		p.recognised[n] = string(unicode.ToUpper(r)) + n[size:]
	}
	return p
}()

func loadPool() *pool {
	if p := current.Load(); p != nil {
		return p
	}
	return builtInPool
}

// SetPool replaces the pool. recognised maps each code to its display name; pickable lists the codes to pick from, in the
// order the picker is offered them. Both are copied.
func SetPool(recognised map[string]string, pickable []string) {
	p := &pool{recognised: make(map[string]string, len(recognised)), pickable: append([]string(nil), pickable...)}
	for code, display := range recognised {
		p.recognised[code] = display
	}
	current.Store(p)
}

// ResetPool goes back to the built-in names.
func ResetPool() { current.Store(nil) }

// PickablePool is the codes a new conversation may take, as a copy.
func PickablePool() []string { return append([]string(nil), loadPool().pickable...) }

// PoolDisplayName is the display name of a recognised code ("Hiro Tanaka" for hiro; "Adam" for the built-in adam).
func PoolDisplayName(code string) (string, bool) {
	d, ok := loadPool().recognised[strings.TrimSpace(code)]
	return d, ok
}
