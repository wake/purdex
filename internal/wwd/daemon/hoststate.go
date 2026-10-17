package daemon

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	maxExceptions   = 32
	maxOwnerRuleLen = 256
)

var reExceptionName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ErrInvalid marks a request the host state refused for its content (the routes
// answer 400); any other error is a failure of the host (500).
var ErrInvalid = errors.New("invalid request")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// HostStateOptions configures a HostState. Store may be nil (memory only).
type HostStateOptions struct {
	Store    *store.Store
	Host     string              // this host's alias, on the events
	Initial  wire.WorkerSettings // the config's starting settings, used until the first PUT
	Now      func() time.Time    // default time.Now
	PidAlive func(pid int) bool  // default: kill(pid, 0)
	Events   *Broker             // may be nil
}

// HostState is the host's settings the facts and routes agree on: the worker
// switch, the slots and the active exceptions. Every change is saved first, then
// announced once; a request that changes nothing says nothing.
type HostState struct {
	opts  HostStateOptions
	store *store.Store

	mu     sync.Mutex
	worker wire.WorkerSettings
	excs   map[string]store.Stored
}

// NewHostState loads the saved settings (the config's Initial values when none
// were saved) and the saved exceptions that are still in force.
func NewHostState(o HostStateOptions) (*HostState, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PidAlive == nil {
		o.PidAlive = pidAlive
	}
	s := &HostState{opts: o, store: o.Store, worker: o.Initial, excs: map[string]store.Stored{}}
	if s.store == nil {
		return s, nil
	}
	if ws, ok, err := s.store.LoadWorker(); err != nil {
		return nil, err
	} else if ok {
		s.worker = ws
	}
	saved, err := s.store.LoadExceptions()
	if err != nil {
		return nil, err
	}
	now := o.Now()
	for _, e := range saved {
		if s.ended(e, now) {
			if err := s.store.DeleteException(e.Exception.Name); err != nil { // ended while wwd was down
				return nil, err
			}
			continue
		}
		s.excs[e.Exception.Name] = e
	}
	return s, nil
}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (s *HostState) ended(e store.Stored, now time.Time) bool {
	if e.Exception.Until != nil {
		return !now.Before(*e.Exception.Until)
	}
	return e.Exception.PID != nil && !s.opts.PidAlive(*e.Exception.PID)
}

// Worker is the current worker settings.
func (s *HostState) Worker() wire.WorkerSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.worker
}

// SetWorker replaces the worker settings. changed is false (and nothing is saved
// or announced) when they are the same as before.
func (s *HostState) SetWorker(ws wire.WorkerSettings) (changed bool, err error) {
	if ws.Slots.Heavy < 0 || ws.Slots.Light < 0 {
		return false, invalid("slots cannot be negative")
	}
	if len(ws.OwnerRule) > maxOwnerRuleLen {
		return false, invalid("owner_rule is longer than %d bytes", maxOwnerRuleLen)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ws == s.worker {
		return false, nil
	}
	if s.store != nil {
		if err := s.store.SaveWorker(ws); err != nil {
			return false, err
		}
	}
	s.worker = ws
	if s.opts.Events != nil {
		s.opts.Events.PublishWorker(wire.WorkerEvent{Host: s.opts.Host, Worker: ws})
	}
	return true, nil
}

// AddException starts an exception, or replaces the one of the same name. changed
// is false when an identical one is already in force.
func (s *HostState) AddException(e wire.Exception) (changed bool, err error) {
	if !reExceptionName.MatchString(e.Name) {
		return false, invalid("exception name must be 1-64 of letters, digits, . _ -")
	}
	if err := e.Validate(); err != nil {
		return false, invalid("%v", err)
	}
	now := s.opts.Now()
	if e.Until != nil && !e.Until.After(now) {
		return false, invalid("until is already past")
	}
	if e.PID != nil && !s.opts.PidAlive(*e.PID) {
		return false, invalid("pid %d is not running", *e.PID)
	}
	if e.Holds == nil {
		e.Holds = []wire.Hold{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.excs[e.Name]; ok && reflect.DeepEqual(old.Exception, e) {
		return false, nil
	}
	if _, ok := s.excs[e.Name]; !ok && len(s.excs) >= maxExceptions {
		return false, invalid("at most %d exceptions at a time", maxExceptions)
	}
	st := store.Stored{Exception: e, Since: now}
	if s.store != nil {
		if err := s.store.SaveException(st); err != nil {
			return false, err
		}
	}
	s.excs[e.Name] = st
	if s.opts.Events != nil {
		c := e
		s.opts.Events.PublishException(wire.ExceptionEvent{Host: s.opts.Host, Name: e.Name, Active: true, Exception: &c})
	}
	return true, nil
}

// RemoveException ends an exception; false when there was none. When the
// database cannot make the change the exception stays in force and the error is
// returned: saved first, then announced.
func (s *HostState) RemoveException(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.excs[name]; !ok {
		return false, nil
	}
	if s.store != nil {
		if err := s.store.DeleteException(name); err != nil {
			return false, err
		}
	}
	s.dropLocked(name)
	return true, nil
}

func (s *HostState) dropLocked(name string) {
	delete(s.excs, name)
	if s.opts.Events != nil {
		s.opts.Events.PublishException(wire.ExceptionEvent{Host: s.opts.Host, Name: name, Active: false})
	}
}

// Prune ends the exceptions whose time has come or whose process is gone, each
// announced once.
func (s *HostState) Prune(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for name, e := range s.excs {
		if s.ended(e, now) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		// An ended exception is over whether or not its row could be deleted (a row
		// left behind is dropped as ended on the next start), so it is announced.
		if s.store != nil {
			if err := s.store.DeleteException(n); err != nil {
				log.Printf("wwd: dropping the ended exception %q from wwd.db: %v", n, err)
			}
		}
		s.dropLocked(n)
	}
}

// Active is the exceptions in force, as facts shows them (oldest first).
func (s *HostState) Active() []wire.FactExc {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]wire.FactExc, 0, len(s.excs))
	for _, e := range s.sorted() {
		fe := wire.FactExc{Name: e.Exception.Name, Since: e.Since}
		if e.Exception.Until != nil {
			u := *e.Exception.Until
			fe.Until = &u
		}
		out = append(out, fe)
	}
	return out
}

// Holds is the exceptions in force as headroom sees them: each holds heavy work,
// and light work too when it lists light.
func (s *HostState) Holds() []derived.Hold {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]derived.Hold, 0, len(s.excs))
	for _, e := range s.sorted() {
		h := derived.Hold{Name: e.Exception.Name}
		for _, x := range e.Exception.Holds {
			h.Light = h.Light || x == wire.HoldLight
		}
		out = append(out, h)
	}
	return out
}

func (s *HostState) sorted() []store.Stored {
	all := make([]store.Stored, 0, len(s.excs))
	for _, e := range s.excs {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].Since.Equal(all[j].Since) {
			return all[i].Since.Before(all[j].Since)
		}
		return all[i].Exception.Name < all[j].Exception.Name
	})
	return all
}
