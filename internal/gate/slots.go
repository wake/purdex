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

// Release drops the slot: the record first, then the lock, for every process that inherited it too (the gate has
// cleared the command's group by now; whatever escaped it does not keep the slot).
func (s *Slot) Release() {
	if s == nil || s.f == nil {
		return
	}
	_ = os.Remove(s.rec)
	_ = syscall.Flock(int(s.f.Fd()), syscall.LOCK_UN)
	_ = s.f.Close()
	s.f = nil
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
}

func (p *Pool) slotPath(k int) string {
	return filepath.Join(p.Dir, string(p.Class)+"-"+strconv.Itoa(k)+".lock")
}
func (p *Pool) recordPath(k int) string {
	return filepath.Join(p.Dir, string(p.Class)+"-"+strconv.Itoa(k)+".json")
}
func (p *Pool) queueDir() string { return filepath.Join(p.Dir, "queue", string(p.Class)) }

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
	name := fmt.Sprintf("%020d-%d", p.Now().UnixNano(), os.Getpid())
	path := filepath.Join(p.queueDir(), name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, dirError(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, dirError(err)
	}
	return &Ticket{Name: name, path: path, f: f}, nil
}

// liveBefore lists the live tickets older than t; a ticket whose lock is free belongs to a gate that is gone and is
// removed on the way.
func (p *Pool) liveBefore(t *Ticket) []string {
	ents, err := os.ReadDir(p.queueDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
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
			continue // removed meanwhile
		}
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			_ = os.Remove(path) // nobody holds it: its gate is gone
			_ = f.Close()
			continue
		}
		_ = f.Close()
		live = append(live, n)
	}
	return live
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
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
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
	if err := syscall.Flock(int(mu.Fd()), syscall.LOCK_EX); err != nil {
		return nil, 0, dirError(err)
	}
	defer syscall.Flock(int(mu.Fd()), syscall.LOCK_UN) //nolint:errcheck

	ahead = len(p.liveBefore(t))
	var free []*os.File
	var freeK []int
	for k := 1; k <= p.Size; k++ {
		f, err := os.OpenFile(p.slotPath(k), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			closeAll(free)
			return nil, ahead, dirError(err)
		}
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			_ = f.Close() // held
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
		free := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
		_ = f.Close() // dropping the probe frees it again
		if free {
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
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		return h, false, true // free: the record is stale
	}
	return h, true, true
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
