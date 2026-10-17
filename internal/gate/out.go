package gate

// A ticket that is out (assist-dispatch plan X2a, contract 9): its command is running on an assist host while the gate that holds the
// ticket waits for it. The ticket keeps its name, its lock (held by the same gate process) and its rank; a side file beside it,
// queue/<class>/<ticket>.out, says it is out and where. A snapshot leaves an out ticket out of the serving order (it is never ahead of
// anyone and never served), and a ticket that comes BACK (the run failed before it started, or the host took it back) has only its
// .out removed: no ticket is made again, so the arrival time, the rank and every other rule of the queue stay as they were.
//
// Every change is made under the class's admit lock like the rank file's, so the sidecars of one ticket go together: Ticket.Drop and
// the clean-up of a dead gate's ticket remove .rank and .out in one step.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OutInfo is what the .out file says: the host the command runs on, the job there, and since when.
type OutInfo struct {
	Host string    `json:"host"`
	Job  string    `json:"job"`
	At   time.Time `json:"at"`
}

// OutEntry is one out ticket in a snapshot.
type OutEntry struct {
	Name string
	Out  OutInfo
}

func (p *Pool) outPath(ticket string) string { return filepath.Join(p.queueDir(), ticket+".out") }

// readOut reads a ticket's .out file; ok is false when the ticket is not out (an unreadable file is out with no details: the ticket
// is not served while its state is in doubt).
func (p *Pool) readOut(ticket string) (OutInfo, bool) {
	b, err := os.ReadFile(p.outPath(ticket))
	if errors.Is(err, os.ErrNotExist) {
		return OutInfo{}, false
	}
	var o OutInfo
	if err == nil {
		_ = json.Unmarshal(b, &o)
	}
	return o, true
}

// IsOut says whether the ticket is out.
func (p *Pool) IsOut(ticket string) bool {
	_, ok := p.readOut(ticket)
	return ok
}

// MarkOut sends the named ticket out. It fails with ErrNotWaiting when the ticket is not in the queue.
func (p *Pool) MarkOut(ticket string, info OutInfo) error {
	unlock, err := p.lockAdmit()
	if err != nil {
		return err
	}
	defer unlock()
	return p.markOutLocked(ticket, info)
}

func (p *Pool) markOutLocked(ticket string, info OutInfo) error {
	if !p.ticketWaiting(ticket) {
		_ = os.Remove(p.outPath(ticket))
		return ErrNotWaiting
	}
	if info.At.IsZero() {
		info.At = p.Now()
	}
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}
	tmp := p.outPath(ticket) + ".new"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return dirError(err)
	}
	if err := os.Rename(tmp, p.outPath(ticket)); err != nil {
		_ = os.Remove(tmp)
		return dirError(err)
	}
	if !p.ticketWaiting(ticket) { // it went while the file was written
		_ = os.Remove(p.outPath(ticket))
		return ErrNotWaiting
	}
	return nil
}

// ClearOut brings the ticket back to its place: only the .out file goes. ErrNotWaiting when the ticket is not in the queue.
func (p *Pool) ClearOut(ticket string) error {
	unlock, err := p.lockAdmit()
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.Remove(p.outPath(ticket)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return dirError(err)
	}
	if !p.ticketWaiting(ticket) {
		return ErrNotWaiting
	}
	return nil
}

// DispatchClass is the class of the dispatch slots mlab keeps for an assist host: assist-<host>-<k>.lock files in the gate directory.
func DispatchClass(host string) Class { return Class("assist-" + host) }

// DispatchPool is the pool of a host's dispatch slots (how many runs mlab has out there at most). It has no queue of its own: the
// queue is the heavy or light class's, and a ticket is sent out only when a dispatch slot is free.
func DispatchPool(dir, host string, size int, now func() time.Time) *Pool {
	return &Pool{Dir: dir, Class: DispatchClass(host), Size: size, Now: now}
}

// TakeDispatch takes the lowest free slot of a dispatch pool, or nil when all are held. The slot is an ordinary slot lock, so the
// kernel lets it go if the gate dies.
func (p *Pool) TakeDispatch(info WaiterInfo) (*Slot, error) {
	unlock, err := p.lockAdmit()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return p.takeLocked(0, info)
}

// ServeOut sends a waiting ticket of p out to an assist host: under p's admit lock it takes a dispatch slot from dispatch and marks the
// ticket out, both or neither. It returns nil without error when no dispatch slot is free (the ticket keeps its place), and
// ErrNotWaiting when the ticket is gone or already out.
func (p *Pool) ServeOut(ticket string, dispatch *Pool, info WaiterInfo, out OutInfo) (*Slot, error) {
	unlock, err := p.lockAdmit()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if !p.ticketWaiting(ticket) || p.IsOut(ticket) {
		return nil, ErrNotWaiting
	}
	slot, err := dispatch.TakeDispatch(info)
	if err != nil || slot == nil {
		return nil, err
	}
	if err := p.markOutLocked(ticket, out); err != nil {
		slot.Release(nil)
		return nil, err
	}
	return slot, nil
}

// Return brings an out ticket back to its place: the dispatch slot is let go FIRST (find names what still holds its lock), then the
// ticket's .out goes, so a ticket that is back in the queue never finds the host's slot still taken by its own failed run.
func (t *Ticket) Return(slot *Slot, find func(path string) []Straggler) error {
	if t == nil || t.p == nil {
		return ErrNotWaiting
	}
	slot.Release(find)
	return t.p.ClearOut(t.Name)
}

// outNames lists the tickets that have an .out file, from a directory listing.
func outNames(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		out[strings.TrimSuffix(n, ".out")] = true
	}
	return out
}
