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
	// Token is the secret of the slot's PDX_GATE_HELD: a command below the holder presents it to pass through.
	Token string `json:"token,omitempty"`
	// Orphaned (set when read, never stored): the slot is locked but the gate that took it is gone; the lock lives on
	// in the command tree that inherited it.
	Orphaned bool `json:"orphaned,omitempty"`
	// Locked (set by Status, never stored): the processes that have the slot's lock file open right now, so a slot whose
	// gate is gone shows which processes to end.
	Locked []Straggler `json:"locked,omitempty"`
}

// Slot is a slot held by this process.
type Slot struct {
	Class Class
	K     int
	f     *os.File
	rec   string
	h     Holder // what the record says now
}

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
}

// Drop removes the ticket (the slot was taken or the gate gives up).
func (t *Ticket) Drop() {
	if t == nil || t.f == nil {
		return
	}
	_ = os.Remove(t.path)
	_ = t.f.Close()
	t.f = nil
}

// Enqueue takes a ticket. Its name sorts by arrival time, so the order of two tickets is the order of their names.
func (p *Pool) Enqueue() (*Ticket, error) {
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
	path := filepath.Join(p.queueDir(), name)
	if err := os.Rename(tmp, path); err != nil {
		return fail(err)
	}
	return &Ticket{Name: name, path: path, f: f}, nil
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
func (p *Pool) Waiting() int {
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
			_ = os.Remove(path)
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
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return nil, 0, dirError(err)
	}
	mu, err := os.OpenFile(filepath.Join(p.Dir, string(p.Class)+".admit"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, 0, dirError(err)
	}
	defer mu.Close()
	if err := flockWait(mu); err != nil {
		return nil, 0, dirError(err)
	}
	defer syscall.Flock(int(mu.Fd()), syscall.LOCK_UN) //nolint:errcheck

	before, err := p.liveBefore(t)
	if err != nil {
		return nil, 0, dirError(err)
	}
	ahead = len(before)
	var free []*os.File
	var freeK []int
	for k := 1; k <= p.Size; k++ {
		f, err := os.OpenFile(p.slotPath(k), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			closeAll(free)
			return nil, ahead, dirError(err)
		}
		st, lerr := lockProbe(f)
		if st == lockBroken {
			_ = f.Close()
			closeAll(free)
			return nil, ahead, dirError(lerr)
		}
		if st == lockHeld {
			_ = f.Close()
			continue
		}
		free = append(free, f)
		freeK = append(freeK, k)
	}
	if ahead >= len(free) {
		closeAll(free)
		return nil, ahead, nil
	}
	// keep the lowest free slot; the other probes are given back
	closeAll(free[1:])
	// The lock is ours now, so a record left by an earlier holder (kept because a child of it still held the lock) is stale:
	// remove it before anything else, or a command still carrying the old token could pass the nesting check against our
	// lock until SetHolder writes ours.
	if err := os.Remove(p.recordPath(freeK[0])); err != nil && !errors.Is(err, os.ErrNotExist) {
		closeAll(free[:1]) // the lock is let go again: a record that cannot be removed must not stay beside a lock we hand out
		return nil, ahead, dirError(err)
	}
	return &Slot{Class: p.Class, K: freeK[0], f: free[0], rec: p.recordPath(freeK[0])}, ahead, nil
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
	for k := 1; k <= p.Size; k++ {
		f, err := os.OpenFile(p.slotPath(k), os.O_RDWR, 0)
		if err != nil {
			continue
		}
		st, _ := flockNB(f)
		_ = f.Close() // dropping the probe frees it again
		if st == lockGot {
			continue
		}
		h := Holder{Class: p.Class, Slot: k}
		if b, err := os.ReadFile(p.recordPath(k)); err == nil {
			_ = json.Unmarshal(b, &h)
			h.Class, h.Slot = p.Class, k
			h.Orphaned = h.PID > 0 && !pidAlive(h.PID)
		}
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
	for k := 1; k <= p.Size; k++ {
		h, held, ok := p.Record(k)
		if ok && !held && h.PID > 0 && pidAlive(h.PID) {
			h.Class, h.Slot, h.Token = p.Class, k, ""
			out = append(out, h)
		}
	}
	return out
}
