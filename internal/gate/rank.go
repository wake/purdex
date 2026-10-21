package gate

// Queue order: front, back, hold (#3034, spec §5.2). A waiting ticket may carry a rank, a side file beside it,
// queue/<class>/<ticket>.rank (a dotted name, so the scans that list tickets skip it): front tickets are served first (in the
// order they were moved there), then normal ones (arrival order), then back ones (arrival order). A hold ticket is not served and is
// not counted ahead of anybody. A rank file that cannot be read or makes no sense reads as normal, arrival order, and is shown as an
// error, so a broken file never strands a waiter.
//
// Every change to the queue is made under the class's admit lock (<class>.admit), the one TryTake holds: SetRank, ClearRank,
// Ticket.Drop and the clean-up of a dead gate's ticket and of a rank whose ticket is gone. So a rank never outlives its ticket
// unseen, and every reader (TryTake, the status, the would-wait) works from one Snapshot.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Rank is where a waiting ticket stands in the serving order.
type Rank string

const (
	RankFront  Rank = "front"
	RankNormal Rank = "normal"
	RankBack   Rank = "back"
	RankHold   Rank = "hold"
)

// Valid says whether r is one of the four ranks.
func (r Rank) Valid() bool {
	return r == RankFront || r == RankNormal || r == RankBack || r == RankHold
}

// ErrNotWaiting: the ticket named is not (or no longer) in the queue.
var ErrNotWaiting = errors.New("the ticket is no longer waiting")

// rankFile is the JSON of <ticket>.rank. Times are unix nanoseconds; Until 0 is "until it is moved again".
type rankFile struct {
	Rank   Rank   `json:"rank"`
	At     int64  `json:"at"`
	By     string `json:"by,omitempty"`
	Reason string `json:"reason,omitempty"`
	Until  int64  `json:"until,omitempty"`
}

// RankInfo is a rank as the readers see it.
type RankInfo struct {
	Rank   Rank
	At     time.Time
	By     string
	Reason string
	Until  time.Time // zero: no end
	Error  string    // the rank file could not be used: the ticket is served as normal
}

// QueueEntry is one live ticket in serving order.
type QueueEntry struct {
	Name string
	Rank RankInfo // Rank is the EFFECTIVE rank: normal for a broken file or an expired hold
	// Argv is the command the ticket was made for, as its file says (the file is for listing: it may be empty or damaged, then the
	// ticket matches no conflict rule). A gate's own ticket is judged by the argv it keeps in memory instead.
	Argv []string
}

// Snapshot is the queue of one pool at one moment under the admit lock: the live tickets in the order they are served (front by
// the time they were moved, then normal, then back, each by ticket name; held ones last, never counted ahead).
type Snapshot struct {
	Queue []QueueEntry
}

func (p *Pool) rankPath(ticket string) string {
	return filepath.Join(p.queueDir(), ticket+".rank")
}

// lockAdmit takes the class's admit lock; the returned func gives it back.
func (p *Pool) lockAdmit() (func(), error) {
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return nil, dirError(err)
	}
	mu, err := os.OpenFile(filepath.Join(p.Dir, string(p.Class)+".admit"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, dirError(err)
	}
	if err := flockWait(mu); err != nil {
		_ = mu.Close()
		return nil, dirError(err)
	}
	return func() {
		_ = syscall.Flock(int(mu.Fd()), syscall.LOCK_UN)
		_ = mu.Close()
	}, nil
}

// dropLockWait is how long Ticket.Drop waits for the admit lock before it removes its ticket without it: the lock is held for a
// directory scan, so a longer wait means a directory in trouble, and giving up a place must not wait on that.
var dropLockWait = 2 * time.Second // a var so a test need not wait it out

// lockAdmitWithin is lockAdmit that gives up after d (the error then says the lock is busy).
func (p *Pool) lockAdmitWithin(d time.Duration) (func(), error) {
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return nil, dirError(err)
	}
	mu, err := os.OpenFile(filepath.Join(p.Dir, string(p.Class)+".admit"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, dirError(err)
	}
	deadline := time.Now().Add(d)
	for {
		st, lerr := flockNB(mu)
		if st == lockGot {
			return func() {
				_ = syscall.Flock(int(mu.Fd()), syscall.LOCK_UN)
				_ = mu.Close()
			}, nil
		}
		if st == lockBroken || !time.Now().Before(deadline) {
			_ = mu.Close()
			if lerr == nil {
				lerr = errors.New("the admit lock is busy")
			}
			return nil, dirError(lerr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readRank reads a ticket's rank file; a missing one is normal, an unusable one is normal with Error set. now decides whether a
// hold has run out.
func (p *Pool) readRank(ticket string, now time.Time) RankInfo {
	b, err := os.ReadFile(p.rankPath(ticket))
	if errors.Is(err, os.ErrNotExist) {
		return RankInfo{Rank: RankNormal}
	}
	if err != nil {
		return RankInfo{Rank: RankNormal, Error: "rank file unreadable: " + err.Error()}
	}
	var rf rankFile
	if err := json.Unmarshal(b, &rf); err != nil {
		return RankInfo{Rank: RankNormal, Error: "rank file is not JSON: " + err.Error()}
	}
	if !rf.Rank.Valid() {
		return RankInfo{Rank: RankNormal, Error: fmt.Sprintf("rank file names an unknown rank %q", string(rf.Rank))}
	}
	if rf.At <= 0 {
		return RankInfo{Rank: RankNormal, Error: "rank file has no time it was set at"} // it would sort before every real front ticket
	}
	info := RankInfo{Rank: rf.Rank, At: time.Unix(0, rf.At), By: rf.By, Reason: rf.Reason}
	if rf.Until < 0 {
		return RankInfo{Rank: RankNormal, Error: "rank file has a negative until"} // read as "forever" it would strand the waiter
	}
	if rf.Until > 0 {
		info.Until = time.Unix(0, rf.Until)
	}
	if rf.Rank == RankHold && !info.Until.IsZero() && !now.Before(info.Until) {
		info.Rank = RankNormal // the hold has run out
	}
	return info
}

// Snapshot is the queue now, in serving order. It takes the admit lock itself.
func (p *Pool) Snapshot() (Snapshot, error) {
	unlock, err := p.lockAdmit()
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	return p.snapshotLocked()
}

// snapshotLocked is Snapshot for a caller that holds the admit lock. It removes the tickets of dead gates, the rank files whose
// ticket is gone, and reaps abandoned staging files.
func (p *Pool) snapshotLocked() (Snapshot, error) { return p.scanQueue(true) }

// peekSnapshot is the queue as it can be read without the lock and without removing anything (a dead gate's ticket is just not
// listed): for the readers that must stay cheap and read-only (the status, the would-wait hint). A queue that cannot be read is empty.
func (p *Pool) peekSnapshot() Snapshot {
	s, _ := p.scanQueue(false)
	return s
}

// scanQueue builds the Snapshot; clean says it holds the admit lock and may remove what it finds dead.
func (p *Pool) scanQueue(clean bool) (Snapshot, error) {
	ents, err := os.ReadDir(p.queueDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, nil // no queue yet
		}
		return Snapshot{}, dirError(err) // a queue that cannot be read is not an empty one: the oldest waiter would be passed
	}
	var names, ranks []string
	for _, e := range ents {
		n := e.Name()
		switch {
		case strings.HasSuffix(n, ".new"):
			if clean {
				p.reapStaging(e)
			}
		case e.IsDir():
		case strings.HasSuffix(n, ".rank"):
			ranks = append(ranks, strings.TrimSuffix(n, ".rank"))
		case strings.Contains(n, "."):
		default:
			names = append(names, n)
		}
	}
	sort.Strings(names)
	live := map[string]bool{}
	var entries []QueueEntry
	now := p.Now()
	for _, n := range names {
		path := filepath.Join(p.queueDir(), n)
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // removed meanwhile
			}
			return Snapshot{}, dirError(err)
		}
		if st, _ := flockNB(f); st == lockGot {
			if clean {
				_ = os.Remove(path) // nobody holds it: its gate is gone
			}
			_ = f.Close()
			continue
		}
		_ = f.Close() // held, or a lock that cannot be probed: counted as waiting, the safe side of fairness
		live[n] = true
		var info WaiterInfo
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &info)
		}
		entries = append(entries, QueueEntry{Name: n, Rank: p.readRank(n, now), Argv: info.Argv})
	}
	for _, n := range ranks {
		if clean && !live[n] {
			_ = os.Remove(p.rankPath(n)) // its ticket is gone: a rank never outlives it
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if ga, gb := servingGroup(a.Rank.Rank), servingGroup(b.Rank.Rank); ga != gb {
			return ga < gb
		}
		if a.Rank.Rank == RankFront && !a.Rank.At.Equal(b.Rank.At) {
			return a.Rank.At.Before(b.Rank.At)
		}
		return a.Name < b.Name
	})
	return Snapshot{Queue: entries}, nil
}

// servingGroup orders the ranks: front, normal, back, then hold.
func servingGroup(r Rank) int {
	switch r {
	case RankFront:
		return 0
	case RankBack:
		return 2
	case RankHold:
		return 3
	}
	return 1
}

// Ahead says how many tickets are served before the named one and how many of those were moved to the front; ok is false when the
// ticket is not in the queue. A held ticket is not served, so nothing counts as ahead of it and it reports held.
func (s Snapshot) Ahead(name string) (ahead, aheadFront int, rank RankInfo, ok bool) {
	for i, e := range s.Queue {
		if e.Name != name {
			continue
		}
		if e.Rank.Rank == RankHold {
			return 0, 0, e.Rank, true
		}
		for _, o := range s.Queue[:i] {
			ahead++
			if o.Rank.Rank == RankFront {
				aheadFront++
			}
		}
		return ahead, aheadFront, e.Rank, true
	}
	return 0, 0, RankInfo{}, false
}

// PlaceOfMissing is where a normal ticket named name would stand if it were in the queue (a ticket whose file was taken from under
// it): behind every front ticket and the normal ones with an older name; back and held ones do not count. aheadFront is how many of
// those are front tickets.
func (s Snapshot) PlaceOfMissing(name string) (ahead, aheadFront int) {
	for _, e := range s.Queue {
		switch {
		case e.Rank.Rank == RankFront:
			ahead, aheadFront = ahead+1, aheadFront+1
		case e.Rank.Rank == RankNormal && e.Name < name:
			ahead++
		}
	}
	return ahead, aheadFront
}

// AheadOfNew is how many tickets a new normal ticket would wait behind: the live front and normal ones (back and held are
// passed by it).
func (s Snapshot) AheadOfNew() int {
	n := 0
	for _, e := range s.Queue {
		if e.Rank.Rank == RankFront || e.Rank.Rank == RankNormal {
			n++
		}
	}
	return n
}

// SetRank moves the named ticket and returns the rank it had. until is for a hold (zero: until it is moved again). It fails with
// ErrNotWaiting when the ticket is not in the queue (its gate is gone), and then leaves no rank file behind.
func (p *Pool) SetRank(ticket string, rank Rank, by, reason string, until time.Time) (old RankInfo, err error) {
	if !rank.Valid() {
		return RankInfo{}, fmt.Errorf("unknown rank %q", string(rank))
	}
	unlock, err := p.lockAdmit()
	if err != nil {
		return RankInfo{}, err
	}
	defer unlock()
	if !p.ticketWaiting(ticket) {
		_ = os.Remove(p.rankPath(ticket))
		return RankInfo{}, ErrNotWaiting
	}
	old = p.readRank(ticket, p.Now())
	rf := rankFile{Rank: rank, At: p.Now().UnixNano(), By: by, Reason: reason}
	if !until.IsZero() {
		rf.Until = until.UnixNano()
	}
	if err := p.writeRankFile(ticket, rf); err != nil {
		return old, err
	}
	if !p.ticketWaiting(ticket) { // it went while the file was written
		_ = os.Remove(p.rankPath(ticket))
		return old, ErrNotWaiting
	}
	return old, nil
}

// writeRankFile writes <ticket>.rank atomically (a temp name, then rename); the caller holds the admit lock.
func (p *Pool) writeRankFile(ticket string, rf rankFile) error {
	b, err := json.Marshal(rf)
	if err != nil {
		return err
	}
	tmp := p.rankPath(ticket) + ".new"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return dirError(err)
	}
	if err := os.Rename(tmp, p.rankPath(ticket)); err != nil {
		_ = os.Remove(tmp)
		return dirError(err)
	}
	return nil
}

// ClearRank puts the named ticket back to normal (removes its rank file); ErrNotWaiting when it is not in the queue.
func (p *Pool) ClearRank(ticket string) (old RankInfo, err error) {
	unlock, err := p.lockAdmit()
	if err != nil {
		return RankInfo{}, err
	}
	defer unlock()
	if !p.ticketWaiting(ticket) {
		_ = os.Remove(p.rankPath(ticket))
		return RankInfo{}, ErrNotWaiting
	}
	old = p.readRank(ticket, p.Now())
	if err := os.Remove(p.rankPath(ticket)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return old, dirError(err)
	}
	return old, nil
}

// ticketWaiting says whether the ticket file exists and its lock is held (its gate is alive).
func (p *Pool) ticketWaiting(ticket string) bool {
	if ticket == "" || strings.ContainsAny(ticket, "/.") {
		return false
	}
	f, err := os.OpenFile(filepath.Join(p.queueDir(), ticket), os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	st, _ := flockNB(f)
	return st != lockGot
}

// rankName is the rank's name for the decision record: empty for a plain normal ticket (nothing to say).
func rankName(r RankInfo) string {
	if r.Rank == RankNormal {
		return ""
	}
	return string(r.Rank)
}

// rankOfTicket finds the ticket's rank in a snapshot; normal when it is not listed.
func rankOfTicket(s Snapshot, name string) RankInfo {
	for _, e := range s.Queue {
		if e.Name == name {
			return e.Rank
		}
	}
	return RankInfo{Rank: RankNormal}
}

// withMissing is the queue with a normal ticket of this name added where its name puts it: after the front tickets and the normal
// ones with an older name, before the rest. For a ticket whose file was taken from under it.
func withMissing(q []QueueEntry, name string) []QueueEntry {
	at := len(q)
	for i, e := range q {
		if e.Rank.Rank == RankBack || e.Rank.Rank == RankHold || (e.Rank.Rank == RankNormal && e.Name > name) {
			at = i
			break
		}
	}
	out := append([]QueueEntry(nil), q[:at]...)
	out = append(out, QueueEntry{Name: name, Rank: RankInfo{Rank: RankNormal}})
	return append(out, q[at:]...)
}
