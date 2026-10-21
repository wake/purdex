package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// A slot is the lock on <dir>/<class>-<k>.lock, k = 1…N. flock(2) belongs to the open file description, so two gates
// conflict even in one process, and the kernel drops the lock when the gate exits however it exits.
//
// Waiters queue by ticket (nq style): a waiting gate writes <dir>/queue/<class>/<unix-ns>-<pid> and keeps a lock on it,
// so a dead waiter's ticket can be told (its lock is free) and removed. A gate takes a slot only when the live tickets
// older than its own are fewer than the free slots, so a late arrival cannot take what the head of the queue is
// waiting for. The count and the take happen under one short lock (<dir>/<class>.admit), so two gates cannot both
// believe they are first.

// ErrUnavailable says the gate directory is denied to this process (EPERM, EACCES or EROFS: a read-only sandbox such
// as codex's): the caller runs the command without a slot and says so once. Anything else that goes wrong with the
// directory (it is a file, the disk is full, the wrong owner) is ErrBroken: the install is damaged, and the gate does
// not run the command, so a broken setup cannot switch the protection off unseen.
var (
	ErrUnavailable = errors.New("gate directory unavailable")
	ErrBroken      = errors.New("gate directory broken")
)

// dirError sorts an error of the gate directory into ErrUnavailable or ErrBroken.
func dirError(err error) error {
	if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrBroken) {
		return err // already sorted: a helper that sorted it and a caller that sorts again must agree
	}
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EROFS) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return fmt.Errorf("%w: %v", ErrBroken, err)
}

// Holder is what a slot's record says about the command that holds it.
type Holder struct {
	Class   Class     `json:"class"`
	Slot    int       `json:"slot"`
	PID     int       `json:"pid"`  // the gate
	PGID    int       `json:"pgid"` // the command's pid (the leader of its process group when it has one)
	Argv    []string  `json:"argv"`
	Cwd     string    `json:"cwd"`
	Started time.Time `json:"started"`
	Session string    `json:"session,omitempty"`
	Tag     string    `json:"tag,omitempty"` // PDX_GATE_TAG: an assist run sent here shows as assist:<job>
	// Late says the slot was taken after the command had been running for a while (a packaging script put under a slot, #2996),
	// not before it started; Over (set when read, never stored) says it is beyond the size of the pool.
	Late bool `json:"late,omitempty"`
	Over bool `json:"over,omitempty"`
	// NoRecord (set when read, never stored): the slot is locked but there is no record of who holds it, so Started is zero and PID
	// is 0. It is a slot held by a process that inherited the lock after the gate released it; nothing is known to measure (#3049).
	NoRecord bool `json:"no_record,omitempty"`
	// Token is the secret of the slot's PDX_GATE_HELD: a command below the holder presents it to pass through.
	Token string `json:"token,omitempty"`
	// Orphaned (set when read, never stored): the slot is locked but the gate that took it is gone; the lock lives on
	// in the command tree that inherited it.
	Orphaned bool `json:"orphaned,omitempty"`
	// Locked (set by Status, never stored): the processes that have the slot's lock file open right now, so a slot whose
	// gate is gone shows which processes to end.
	Locked []Straggler `json:"locked,omitempty"`
}

// overSlots is how many slots beyond the pool's size a late holder (a packaging script that has run long, #2996) can take: the
// pool is over its size by as many as are held, for as long as they are. Their numbers run from Size+1.
const overSlots = 8

// maxSlotNumber is the highest slot number there can be: the largest pool size the config allows, and the late slots beyond it.
// The pool size is read again for every command, so a slot taken under a bigger size (or a late one numbered above the size
// of that time) must still be found after the size has been lowered: every scan runs to here, not to the size of now.
const maxSlotNumber = 64 + overSlots

// Slot is a slot held by this process.
type Slot struct {
	Class Class
	K     int
	f     *os.File
	rec   string
	h     Holder // what the record says now
}

// Over says whether the slot is beyond the size of its pool (a late holder took it while the pool was full).
func (s *Slot) Over(size int) bool { return s.K > size }

// Label is the head of the value of PDX_GATE_HELD, e.g. heavy-1 (the token follows after a colon).
func (s *Slot) Label() string { return string(s.Class) + "-" + strconv.Itoa(s.K) }

// File is the open lock. The command inherits it (as fd 3), so the lock is held until the last process of its tree is
// gone, whatever happens to the gate: a gate killed with SIGKILL does not free the slot under a tree that still runs.
func (s *Slot) File() *os.File { return s.f }

// Straggler is a process that still holds a slot's lock file open when the gate lets the slot go.
type Straggler struct {
	PID int
	Cmd string
}

// Release gives the slot back: the record goes, then the lock is dropped with LOCK_UN and the descriptor closed. flock
// belongs to the open file description, which the command and its descendants share (fd 3), so one unlock frees the
// slot for everybody, even when a child escaped the clean-up and still holds fd 3 (a daemon such as a tmux server, which
// would otherwise hold the slot for good). Before the unlock, find (when given) names the other processes that hold the
// lock file; they are returned so the gate can say they were left running. A gate that is killed never gets here: the
// lock then stays with the tree that is still alive.
func (s *Slot) Release(find func(path string) []Straggler) []Straggler {
	if s == nil || s.f == nil {
		return nil
	}
	var left []Straggler
	if find != nil {
		for _, p := range find(s.f.Name()) {
			if p.PID != os.Getpid() {
				left = append(left, p)
			}
		}
	}
	_ = os.Remove(s.rec) // before the unlock: afterwards the record may be the next holder's
	_ = syscall.Flock(int(s.f.Fd()), syscall.LOCK_UN)
	_ = s.f.Close()
	s.f = nil
	return left
}

// SetHolder writes the record of the command the slot is held for (atomically: a reader sees a whole record or none).
func (s *Slot) SetHolder(h Holder) error {
	h.Class, h.Slot = s.Class, s.K
	s.h = h
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	tmp := s.rec + "." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.rec)
}

// SetCommand records the pid of the command that runs under the slot (the leader of its group when it has one): a
// process below it passes the gate by descending from this pid, which outlives the gate if the gate is killed.
func (s *Slot) SetCommand(pid int) error {
	h := s.h
	h.PGID = pid
	return s.SetHolder(h)
}

// Pool is the slots and the queue of one class in a gate directory.
type Pool struct {
	Dir   string
	Class Class
	Size  int
	Now   func() time.Time
	// afterCreate runs in Enqueue between creating the ticket file and locking it; a test seam for the one moment a scan
	// by another gate could meet an unlocked ticket.
	afterCreate func()
}

func (p *Pool) slotPath(k int) string {
	return filepath.Join(p.Dir, string(p.Class)+"-"+strconv.Itoa(k)+".lock")
}
func (p *Pool) recordPath(k int) string {
	return filepath.Join(p.Dir, string(p.Class)+"-"+strconv.Itoa(k)+".json")
}
func (p *Pool) queueDir() string { return filepath.Join(p.Dir, "queue", string(p.Class)) }

// ticketSeq numbers the tickets this process makes.
var ticketSeq atomic.Uint64

// Ticket is a place in the queue.
type Ticket struct {
	Name string
	path string
	f    *os.File
	info WaiterInfo // what the gate said about its command when it queued: the take writes it into the slot's record (kept in memory, never read back from the file)
	p    *Pool      // the pool it queues in: its admit lock orders the removal with every other queue change (rank.go)
}

// Drop removes the ticket (the slot was taken or the gate gives up).
func (t *Ticket) Drop() {
	if t == nil || t.f == nil {
		return
	}
	// Under the admit lock like every queue change: the ticket and its rank go together, and a SetRank that meets the ticket gone
	// answers "no longer waiting" instead of leaving a rank behind. A lock that cannot be taken still removes the ticket: giving up
	// must not wait on a directory that is in trouble.
	if t.p != nil {
		if unlock, err := t.p.lockAdmitWithin(dropLockWait); err == nil {
			defer unlock()
		}
		_ = os.Remove(t.p.rankPath(t.Name))
	}
	_ = os.Remove(t.path)
	_ = t.f.Close()
	t.f = nil
}

// WaiterInfo is what a ticket says about the command that waits (JSON in the ticket file, written before the ticket is
// visible): `pdx gate status` lists it. Argv is already cut and scrubbed as in a holder record.
type WaiterInfo struct {
	PID     int      `json:"pid"` // the gate
	Argv    []string `json:"argv"`
	Cwd     string   `json:"cwd"`
	Session string   `json:"session,omitempty"`
	// Tag says what the command is for when its own gate is a run that was sent here (assist:<job>), from PDX_GATE_TAG.
	Tag string `json:"tag,omitempty"`
}

// Enqueue takes a ticket that says nothing about its waiter.
func (p *Pool) Enqueue() (*Ticket, error) { return p.EnqueueFor(WaiterInfo{}) }

// EnqueueFor takes a ticket. Its name sorts by arrival time, so the order of two tickets is the order of their names.
func (p *Pool) EnqueueFor(info WaiterInfo) (*Ticket, error) {
	if err := os.MkdirAll(p.queueDir(), 0o755); err != nil {
		return nil, dirError(err)
	}
	// <time>-<pid>-<n>: n is this process's own counter, so two tickets made in one nanosecond by one process differ, and the
	// file is created with O_EXCL so a name that is taken is never opened (and never removed) by the second maker.
	//
	// The ticket is made under a name with a dot in it (the scans skip those), locked, and only then renamed to its real
	// name: a ticket that can be seen in the directory is always already locked. Made under the real name, a scan between
	// "created" and "locked" would find a free lock, take the ticket for a dead gate's and remove it, and the new waiter
	// would hold a lock on a file that is no longer in the queue and could be passed by anyone.
	var (
		name, tmp string
		f         *os.File
		err       error
	)
	for try := 0; try < 8; try++ {
		name = fmt.Sprintf("%020d-%d-%06d", p.Now().UnixNano(), os.Getpid(), ticketSeq.Add(1))
		tmp = filepath.Join(p.queueDir(), name+".new")
		f, err = os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if err != nil {
		return nil, dirError(err)
	}
	if p.afterCreate != nil {
		p.afterCreate()
	}
	fail := func(err error) (*Ticket, error) {
		_ = f.Close()
		_ = os.Remove(tmp) // ours: it was created here, exclusively
		return nil, dirError(err)
	}
	if st, lerr := flockNB(f); st != lockGot {
		if lerr == nil {
			lerr = errors.New("the new ticket is locked by somebody else")
		}
		return fail(lerr)
	}
	if info.PID != 0 || len(info.Argv) > 0 {
		// Written before the rename, so a ticket that can be seen already says who it is. A write that fails leaves a ticket
		// that says nothing: the queue works without it, only the listing is poorer.
		if b, err := json.Marshal(info); err == nil {
			_, _ = f.Write(b)
		}
	}
	path := filepath.Join(p.queueDir(), name)
	if err := os.Rename(tmp, path); err != nil {
		return fail(err)
	}
	info.Argv = append([]string(nil), info.Argv...) // the ticket keeps its own copy: the caller's slice may be reused or changed
	return &Ticket{Name: name, path: path, f: f, p: p, info: info}, nil
}

// liveBefore lists the live tickets older than t; a ticket whose lock is free belongs to a gate that is gone and is
// removed on the way.
func (p *Pool) liveBefore(t *Ticket) ([]string, error) {
	ents, err := os.ReadDir(p.queueDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // no queue yet
		}
		return nil, err // a queue that cannot be read is not an empty one: the oldest waiter would be passed
	}
	var names []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".new") {
			p.reapStaging(e)
			continue
		}
		if e.IsDir() || strings.Contains(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var live []string
	for _, n := range names {
		if n >= t.Name {
			break
		}
		path := filepath.Join(p.queueDir(), n)
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // removed meanwhile
			}
			return nil, err
		}
		if st, _ := flockNB(f); st == lockGot {
			_ = os.Remove(path) // nobody holds it: its gate is gone
			_ = f.Close()
			continue
		}
		_ = f.Close() // held, or a lock that cannot be probed: counted as waiting, the safe side of fairness
		live = append(live, n)
	}
	return live, nil
}

// stagingGrace and stagingMaxAge say when a half-made ticket (<name>.new, see Enqueue) is abandoned: its maker is gone and it
// is at least stagingGrace old, or it is stagingMaxAge old whoever has its pid now (a healthy Enqueue takes microseconds).
const (
	stagingGrace  = 5 * time.Second
	stagingMaxAge = 10 * time.Minute
)

// reapStaging removes a half-made ticket whose maker died between creating and renaming it. The scans skip these files on
// purpose (a ticket being made must not be taken for a dead gate's), so nothing else would ever remove one.
func (p *Pool) reapStaging(e os.DirEntry) {
	info, err := e.Info()
	if err != nil {
		return
	}
	age := p.Now().Sub(info.ModTime())
	parts := strings.Split(strings.TrimSuffix(e.Name(), ".new"), "-")
	dead := false
	if len(parts) == 3 {
		if pid, err := strconv.Atoi(parts[1]); err == nil && pid > 0 && !pidAlive(pid) {
			dead = true
		}
	}
	if (dead && age >= stagingGrace) || age >= stagingMaxAge {
		_ = os.Remove(filepath.Join(p.queueDir(), e.Name()))
	}
}

// Waiting is the number of live tickets in the queue (all of them, for the status).
func (p *Pool) Waiting() int { return p.waiting(true) }

// PeekWaiting counts the live tickets like Waiting and removes nothing (a dead ticket is just not counted).
func (p *Pool) PeekWaiting() int { return p.waiting(false) }

func (p *Pool) waiting(clean bool) int {
	ents, err := os.ReadDir(p.queueDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if e.IsDir() || strings.Contains(e.Name(), ".") {
			continue
		}
		path := filepath.Join(p.queueDir(), e.Name())
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			continue
		}
		if st, _ := flockNB(f); st == lockGot {
			if clean {
				_ = os.Remove(path)
				_ = os.Remove(path + ".rank") // a rank never outlives its ticket
			}
			_ = f.Close()
			continue
		}
		_ = f.Close()
		n++
	}
	return n
}

// TryTake takes a slot if the ticket may have one now: the live tickets before it are fewer than the free slots.
// ahead is how many live tickets are before it (for the place the gate prints). ok is false when it must wait.
func (p *Pool) TryTake(t *Ticket) (slot *Slot, ahead int, err error) {
	tk, err := p.TryTakeRanked(t)
	return tk.Slot, tk.Ahead, err
}

// Take is TryTake's answer with the ticket's rank: Slot is nil when it must wait; Ahead is the tickets served before it (none while it
// is held) and AheadFront how many of those were moved to the front; Rank is the ticket's own. Held is true for a hold: it takes no
// slot however many are free.
type Take struct {
	Slot       *Slot
	Ahead      int
	AheadFront int
	Rank       RankInfo
	Held       bool
	// Blocked is set when a conflict rule keeps the ticket from taking a slot though there is room: what it waits for and the rule.
	// ConflictsProblem is the reason the rules file could not be used (then there are no rules).
	Blocked          *ConflictBlock
	ConflictsProblem string
}

// TryTakeRanked is TryTake that also says where the ticket stands in the serving order (rank.go).
func (p *Pool) TryTakeRanked(t *Ticket) (Take, error) {
	unlock, err := p.lockAdmit()
	if err != nil {
		return Take{}, err
	}
	defer unlock()

	snap, err := p.snapshotLocked()
	if err != nil {
		return Take{}, err
	}
	ahead, aheadFront, rank, found := snap.Ahead(t.Name)
	if !found {
		// Not listed (its file was removed from under it): it stands where its name puts it, behind the live tickets older than it,
		// as before ranks - never at the head of the line.
		rank = RankInfo{Rank: RankNormal}
		ahead, aheadFront = snap.PlaceOfMissing(t.Name)
	}
	out := Take{Ahead: ahead, AheadFront: aheadFront, Rank: rank}
	if rank.Rank == RankHold {
		out.Held = true
		return out, nil // held: not served while a slot is free either
	}
	rules, problem := LoadConflicts(p.Dir) // read inside the lock: a rule added before this take is honoured by it
	out.ConflictsProblem = problem
	capacity, err := p.freeCapacity()
	if err != nil {
		return out, err
	}
	queue := snap.Queue
	if !found {
		queue = withMissing(queue, t.Name) // not listed: it stands where its name puts it, and the rules apply to it as to any
	}
	res := walkQueue(p.Class, queue, p.Held(), rules, capacity, t.Name, t.info.Argv, nil)
	served, blocked := res.Served[t.Name], res.Blocked[t.Name]
	out.Blocked = blocked
	if !served {
		return out, nil
	}
	slot, err := p.takeLocked(t.info)
	out.Slot = slot
	return out, err
}

// freeCapacity is how many more tickets the pool can serve now: its free slots less the late holders beyond its size (they use up
// free ones: the pool is full while they are held).
func (p *Pool) freeCapacity() (int, error) {
	free := 0
	for k := 1; k <= p.Size; k++ {
		f, err := os.OpenFile(p.slotPath(k), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return 0, dirError(err)
		}
		st, lerr := lockProbe(f)
		_ = f.Close() // a probe that got the lock gives it back here
		if st == lockBroken {
			return 0, dirError(lerr)
		}
		if st != lockHeld {
			free++
		}
	}
	return max(free-p.overHeld(), 0), nil
}

// takeLocked is the slot part of TryTake, with the admit lock held and the walk having served the ticket: the lowest free slot.
func (p *Pool) takeLocked(info WaiterInfo) (*Slot, error) {
	var free []*os.File
	var freeK []int
	for k := 1; k <= p.Size; k++ {
		f, err := os.OpenFile(p.slotPath(k), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			closeAll(free)
			return nil, dirError(err)
		}
		st, lerr := lockProbe(f)
		if st == lockBroken {
			_ = f.Close()
			closeAll(free)
			return nil, dirError(lerr)
		}
		if st == lockHeld {
			_ = f.Close()
			continue
		}
		free = append(free, f)
		freeK = append(freeK, k)
	}
	// A slot held beyond the size of the pool (a late holder) uses up a free one: the pool is full while it is held.
	if len(free)-p.overHeld() <= 0 {
		closeAll(free)
		return nil, nil
	}
	// keep the lowest free slot; the other probes are given back
	closeAll(free[1:])
	// The lock is ours now, so a record left by an earlier holder (kept because a child of it still held the lock) is stale:
	// replace it before anything else, or a command still carrying the old token could pass the nesting check against our
	// lock until SetHolder writes ours. The replacement says who took the slot and when (no token): the gate has work to do
	// before it writes the holder, and a slot with no record in that time read as a holder of no pid and no start (#3049).
	if err := p.writePlaceholder(freeK[0], info, false); err != nil {
		closeAll(free[:1]) // the lock is let go again: a record that cannot be replaced must not stay beside a lock we hand out
		return nil, dirError(err)
	}
	return &Slot{Class: p.Class, K: freeK[0], f: free[0], rec: p.recordPath(freeK[0])}, nil
}

// writePlaceholder replaces the record of slot k, whatever it holds, by one that names this process and the moment: atomically, with
// no token, so that nothing can pass the nesting check on it and a reader never meets a locked slot with no record.
func (p *Pool) writePlaceholder(k int, info WaiterInfo, late bool) error {
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	b, err := json.Marshal(Holder{Class: p.Class, Slot: k, PID: os.Getpid(), Started: now, Argv: info.Argv, Cwd: info.Cwd, Session: info.Session, Tag: info.Tag, Late: late})
	if err != nil {
		return err
	}
	rec := p.recordPath(k)
	tmp := rec + "." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, rec)
}

// overHeld is how many slots beyond the pool's size are held now.
func (p *Pool) overHeld() int {
	n := 0
	for k := p.Size + 1; k <= maxSlotNumber; k++ {
		f, err := os.OpenFile(p.slotPath(k), os.O_RDWR, 0)
		if err != nil {
			continue
		}
		if st, _ := flockNB(f); st != lockGot {
			n++
		}
		_ = f.Close()
	}
	return n
}

// TakeLate takes a slot for a command that is already running, without a ticket and without asking whether the host admits
// more work: the lowest free slot of the pool, or when the pool is full one beyond its size. nil when even those are taken
// (overSlots late holders at once). It holds the same admit lock as TryTake, so the two never take the same slot or count a
// slot twice.
func (p *Pool) TakeLate(info WaiterInfo) (*Slot, error) {
	if p.Size == 0 {
		return nil, nil // a paused class takes no claim, late or over
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return nil, dirError(err)
	}
	mu, err := os.OpenFile(filepath.Join(p.Dir, string(p.Class)+".admit"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, dirError(err)
	}
	defer mu.Close()
	if err := flockWait(mu); err != nil {
		return nil, dirError(err)
	}
	defer syscall.Flock(int(mu.Fd()), syscall.LOCK_UN) //nolint:errcheck
	if p.overHeld() >= overSlots {
		return nil, nil // as many late holders as there is room for
	}
	for k := 1; k <= maxSlotNumber; k++ {
		f, err := os.OpenFile(p.slotPath(k), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, dirError(err)
		}
		st, lerr := lockProbe(f)
		if st == lockBroken {
			_ = f.Close()
			return nil, dirError(lerr)
		}
		if st == lockHeld {
			_ = f.Close()
			continue
		}
		if err := p.writePlaceholder(k, info, true); err != nil {
			_ = f.Close()
			return nil, dirError(err)
		}
		return &Slot{Class: p.Class, K: k, f: f, rec: p.recordPath(k)}, nil
	}
	return nil, nil
}

func closeAll(fs []*os.File) {
	for _, f := range fs {
		_ = f.Close() // closing drops the probe's lock
	}
}

// Held reads the slots of the pool: the holder of each slot whose lock is held (a record beside a free lock is stale and
// ignored), and the size it was asked with.
func (p *Pool) Held() []Holder {
	var out []Holder
	for k := 1; k <= maxSlotNumber; k++ {
		f, err := os.OpenFile(p.slotPath(k), os.O_RDWR, 0)
		if err != nil {
			continue
		}
		st, _ := flockNB(f)
		_ = f.Close() // dropping the probe frees it again
		if st == lockGot {
			continue
		}
		h := Holder{Class: p.Class, Slot: k, NoRecord: true}
		if b, err := os.ReadFile(p.recordPath(k)); err == nil && json.Unmarshal(b, &h) == nil {
			h.Class, h.Slot, h.NoRecord = p.Class, k, false
			h.Orphaned = h.PID > 0 && !pidAlive(h.PID)
		}
		h.Over = k > p.Size
		h.Token = "" // never shown
		out = append(out, h)
	}
	return out
}

// lockState is what trying a lock told: got it, somebody else holds it, or the lock itself is broken.
type lockState int

const (
	lockGot lockState = iota
	lockHeld
	lockBroken
)

// flockNB tries a non-blocking exclusive lock. Only EWOULDBLOCK means somebody holds it; EINTR is tried again; anything
// else (the file system cannot lock, a bad descriptor) says the lock is broken and is not to be read as "busy", which
// would make a command wait forever on a slot nobody holds.
func flockNB(f *os.File) (lockState, error) {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return lockGot, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return lockHeld, nil
		}
		return lockBroken, err
	}
}

// lockProbe is how TryTake probes a slot; a seam so a test can make a lock break.
var lockProbe = flockNB

// flockWait takes an exclusive lock and waits for it; EINTR is tried again.
func flockWait(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// pidAlive says a process with this pid exists.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Record reads the record of a slot, and whether the slot's lock is held right now.
func (p *Pool) Record(k int) (h Holder, held bool, ok bool) {
	b, err := os.ReadFile(p.recordPath(k))
	if err != nil || json.Unmarshal(b, &h) != nil {
		return Holder{}, false, false
	}
	f, err := os.OpenFile(p.slotPath(k), os.O_RDWR, 0)
	if err != nil {
		return h, false, true
	}
	defer f.Close()
	if st, _ := flockNB(f); st == lockGot {
		return h, false, true // free: the record is stale
	}
	return h, true, true // held (or a lock that cannot be probed: not taken for free, so nobody passes on its word)
}

// Suspect lists the records whose gate is alive while the slot's lock is free: the lock is the truth, so the slot is
// free, and the record says something is off (a gate that dropped its lock without exiting).
func (p *Pool) Suspect() []Holder {
	var out []Holder
	for k := 1; k <= maxSlotNumber; k++ {
		h, held, ok := p.Record(k)
		if ok && !held && h.PID > 0 && pidAlive(h.PID) {
			h.Class, h.Slot, h.Token = p.Class, k, ""
			out = append(out, h)
		}
	}
	return out
}

// Waiter is one live ticket of a pool as `pdx gate status` lists it, in the order the queue serves them.
type Waiter struct {
	Class   Class     `json:"class"`
	Ticket  string    `json:"ticket"` // the ticket's name: what `pdx gate queue front <ticket>` takes
	Place   int       `json:"place"`  // 1 = next in line among the waiters
	PID     int       `json:"pid"`    // the waiting gate
	Since   time.Time `json:"since"`  // when it joined the queue
	Argv    []string  `json:"argv,omitempty"`
	Cwd     string    `json:"cwd,omitempty"`
	Session string    `json:"session,omitempty"`
	Tag     string    `json:"tag,omitempty"`
	// Rank is front | normal | back | hold (queue order, spec §5.2); the rest says who moved it, why and, for a hold, until when.
	// RankError is set when its rank file could not be used and the ticket is served as normal.
	Rank       Rank      `json:"rank"`
	RankBy     string    `json:"rank_by,omitempty"`
	RankReason string    `json:"rank_reason,omitempty"`
	HeldUntil  time.Time `json:"held_until,omitzero"`
	RankError  string    `json:"rank_error,omitempty"`
	// ConflictWith and ConflictRule say what a conflict rule keeps the ticket waiting for though there is room ("slot heavy-1" or
	// "ticket <name>") and which rule ("<a> × <b>"); absent when no rule holds it (spec §5.3).
	ConflictWith string `json:"conflict_with,omitempty"`
	ConflictRule string `json:"conflict_rule,omitempty"`
	// ConflictReason is that rule's reason: why the rule exists, so a person can judge whether it should go.
	ConflictReason string `json:"conflict_reason,omitempty"`
}

// Waiters lists the live tickets in serving order (front, normal, back, then the held ones); it removes nothing (a ticket whose
// gate is gone is just not listed).
func (p *Pool) Waiters() []Waiter {
	snap := p.peekSnapshot()
	var out []Waiter
	for _, e := range snap.Queue {
		b, _ := os.ReadFile(filepath.Join(p.queueDir(), e.Name))
		w := Waiter{Class: p.Class, Ticket: e.Name, Place: len(out) + 1, Rank: e.Rank.Rank, RankBy: e.Rank.By, RankReason: e.Rank.Reason,
			HeldUntil: e.Rank.Until, RankError: e.Rank.Error}
		if w.Rank == RankNormal {
			w.RankBy, w.RankReason = "", ""
		}
		parts := strings.Split(e.Name, "-") // <unix-ns>-<pid>-<n>
		if len(parts) == 3 {
			if ns, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
				w.Since = time.Unix(0, ns)
			}
			w.PID, _ = strconv.Atoi(parts[1])
		}
		var info WaiterInfo
		if json.Unmarshal(b, &info) == nil {
			if info.PID > 0 {
				w.PID = info.PID
			}
			w.Argv, w.Cwd, w.Session, w.Tag = info.Argv, info.Cwd, info.Session, info.Tag
		}
		out = append(out, w)
	}
	return out
}
