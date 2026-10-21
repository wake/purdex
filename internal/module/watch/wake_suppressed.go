package watchmod

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxSuppressed is how many suppressed wake-ups the source keeps (spec section 5).
const maxSuppressed = 50

// The bounds of what a host may report in /wake-suppressed: a paired host is trusted to be a Purdex daemon, not to be well-behaved.
const (
	maxSuppressedPerReport = 50
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

// The vocabulary of a suppressed entry (spec section 5): the kinds of wake-up that can be suppressed, and why. A report with anything else is
// not kept: the App translates by these words, and a paired host that misbehaves must not be able to put new ones in front of it.
var (
	suppressedReasons = []string{"patrol", "disk_low", "wwd_alert", "unattended_changed", "user_back"}
	suppressedWhys    = []string{"master_off", "rule_off", "level_off", "kind_off", "no_auto_start", "patrol_failed"}
	// patrolFailCodes are the values of patrol_status.last_fail_code; a patrol_failed entry's detail starts with one of them.
	patrolFailCodes = []string{"targets_unreachable", "targets_unknown", "start_failed", "deliver_failed"}
)

// validSuppressed says whether a reported entry is one a GET can show: a time, a reason and a why of the contract, a detail within the bound
// (and, for a failed patrol, one that begins with its code). The host is not checked here: the route sets it to the sender's alias.
func validSuppressed(e wakeSuppressed, now time.Time) bool {
	if e.At.IsZero() || !e.At.Before(now.Add(24*time.Hour)) || len(e.Detail) > maxSuppressedDetail ||
		!slices.Contains(suppressedReasons, e.Reason) || !slices.Contains(suppressedWhys, e.Why) {
		return false
	}
	if e.Why == "patrol_failed" {
		for _, c := range patrolFailCodes {
			if strings.HasPrefix(e.Detail, c) {
				return true
			}
		}
		return false
	}
	return true
}
