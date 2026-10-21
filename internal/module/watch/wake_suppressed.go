package watchmod

import (
	"sort"
	"sync"
	"time"
)

// maxSuppressed is how many suppressed wake-ups the source keeps (spec section 5).
const maxSuppressed = 50

// The bounds of what a host may report in /wake-suppressed: a paired host is trusted to be a Purdex daemon, not to be well-behaved.
const (
	maxSuppressedPerReport = 50
	maxSuppressedField     = 64
	maxSuppressedDetail    = 500
)

// suppressLog is the last wake-ups the settings stopped, newest first, in memory: the source's own and the ones the other hosts report. A
// host that is not the source keeps only its own. (WK-2 and WK-3 are the ones that record; this file keeps and serves them.)
type suppressLog struct {
	mu      sync.Mutex
	entries []wakeSuppressed
}

// add keeps the entries with the ones already there: newest first, at most maxSuppressed.
func (l *suppressLog) add(es ...wakeSuppressed) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, es...)
	sort.SliceStable(l.entries, func(i, j int) bool { return l.entries[i].At.After(l.entries[j].At) })
	if len(l.entries) > maxSuppressed {
		l.entries = l.entries[:maxSuppressed]
	}
}

// list is a copy, never nil.
func (l *suppressLog) list() []wakeSuppressed {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]wakeSuppressed{}, l.entries...)
}

// validSuppressed says whether a reported entry is one a GET can show: a time, a host, a reason and a why, within the bounds.
func validSuppressed(e wakeSuppressed, now time.Time) bool {
	return !e.At.IsZero() && e.At.Before(now.Add(24*time.Hour)) &&
		e.Host != "" && len(e.Host) <= maxSuppressedField &&
		e.Reason != "" && len(e.Reason) <= maxSuppressedField &&
		e.Why != "" && len(e.Why) <= maxSuppressedField &&
		len(e.Detail) <= maxSuppressedDetail
}
