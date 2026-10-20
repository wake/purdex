package daemon

import (
	"errors"
	"fmt"
	"log"
	"math"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	maxExceptions   = 32
	maxOwnerRuleLen = 256
	maxDiskMinGB    = 1e6 // the gate's own limit for disk_min_gb
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
	Host     string                       // this host's alias, on the events
	Initial  wire.WorkerSettings          // the config's starting settings, used until the first PUT
	Now      func() time.Time             // default time.Now
	PidAlive func(pid int) bool           // default: kill(pid, 0)
	PidIdent func(pid int) (string, bool) // the process behind a pid (its start time); default facts.ProcIdent
	Events   *Broker                      // may be nil
	// OwnerRuleSupported is true on a host that has the owner rule (air26): its
	// owner_rule setting is then a real setting ("" or hid<N>m) that drives the
	// rule. Elsewhere any non-empty owner_rule is refused instead of saved.
	OwnerRuleSupported bool
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
	if o.PidIdent == nil {
		o.PidIdent = facts.ProcIdent
	}
	s := &HostState{opts: o, store: o.Store, excs: map[string]store.Stored{}}
	s.worker = s.sane(o.Initial, "config")
	if s.store == nil {
		return s, nil
	}
	if ws, ok, err := s.store.LoadWorker(); err != nil {
		return nil, err
	} else if ok {
		s.worker = s.sane(ws, "wwd.db")
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

// ended says whether an exception is over: its time has come, or the process it
// was made for is gone (a different process holding the same pid is not it).
func (s *HostState) ended(e store.Stored, now time.Time) bool {
	if e.Exception.Until != nil {
		return !now.Before(*e.Exception.Until)
	}
	if e.Exception.PID == nil {
		return false
	}
	pid := *e.Exception.PID
	if !s.opts.PidAlive(pid) {
		return true
	}
	if e.PIDIdent != "" {
		cur, ok := s.opts.PidIdent(pid)
		return !ok || cur != e.PIDIdent
	}
	return false
}

var reOwnerRule = regexp.MustCompile(`^hid<([1-9][0-9]{0,3})m$`)

// ParseOwnerRule reads an owner_rule setting: "" is the rule's default (0 here,
// meaning 5 minutes), "hid<Nm" is N minutes of HID idle time (1-9999). ok is
// false for anything else.
func ParseOwnerRule(text string) (minutes int, ok bool) {
	if text == "" {
		return 0, true
	}
	m := reOwnerRule.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}

// Worker is the current worker settings.
func (s *HostState) Worker() wire.WorkerSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return withFloor(s.worker, nil)
}

// withFloor is ws with its own copy of the disk floor: the one it carries, else fallback's, else the default. A caller of Worker never
// shares the pointer the state holds.
func withFloor(ws wire.WorkerSettings, fallback *wire.WorkerSettings) wire.WorkerSettings {
	v := wire.DefaultDiskMinGB
	switch {
	case ws.DiskMinGB != nil:
		v = *ws.DiskMinGB
	case fallback != nil && fallback.DiskMinGB != nil:
		v = *fallback.DiskMinGB
	}
	ws.DiskMinGB = &v
	return ws
}

// sameWorker says whether two settings are the same, the floors by value.
func sameWorker(a, b wire.WorkerSettings) bool {
	return a.Enabled == b.Enabled && a.Slots == b.Slots && a.OwnerRule == b.OwnerRule && *withFloor(a, nil).DiskMinGB == *withFloor(b, nil).DiskMinGB
}

// checkWorker is the one rule for worker settings, for a PUT and for what wwd
// starts with alike.
func (s *HostState) checkWorker(ws wire.WorkerSettings) error {
	if ws.Slots.Heavy < 0 || ws.Slots.Light < 0 {
		return invalid("slots cannot be negative")
	}
	if v := ws.DiskMinGB; v != nil && (math.IsNaN(*v) || *v < 0 || *v > maxDiskMinGB) {
		return invalid("disk_min_gb must be between 0 and %v", maxDiskMinGB)
	}
	if len(ws.OwnerRule) > maxOwnerRuleLen {
		return invalid("owner_rule is longer than %d bytes", maxOwnerRuleLen)
	}
	if ws.OwnerRule != "" {
		if !s.opts.OwnerRuleSupported {
			return invalid("this host has no owner rule")
		}
		if _, ok := ParseOwnerRule(ws.OwnerRule); !ok {
			return invalid("owner_rule must be empty or hid<N>m (N = 1-9999)")
		}
	}
	return nil
}

// sane takes settings wwd starts with (from the config or from wwd.db): an
// owner_rule that does not pass checkWorker is cleared and logged, the rest is
// kept, so the shown rule is always the one in force.
func (s *HostState) sane(ws wire.WorkerSettings, from string) wire.WorkerSettings {
	if err := s.checkWorker(ws); err != nil {
		log.Printf("wwd: worker settings from %s: %v; owner_rule cleared", from, err)
		ws.OwnerRule = ""
		ws.Slots.Heavy, ws.Slots.Light = max(ws.Slots.Heavy, 0), max(ws.Slots.Light, 0)
		if v := ws.DiskMinGB; v != nil && (math.IsNaN(*v) || *v < 0 || *v > maxDiskMinGB) {
			ws.DiskMinGB = nil // the default
		}
	}
	return withFloor(ws, nil)
}

// SetWorker replaces the worker settings. changed is false (and nothing is saved
// or announced) when they are the same as before.
func (s *HostState) SetWorker(ws wire.WorkerSettings) (changed bool, err error) {
	if err := s.checkWorker(ws); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ws = withFloor(ws, &s.worker) // a PUT without the floor keeps the one in force
	if sameWorker(ws, s.worker) {
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
	var ident string
	if e.PID != nil {
		if !s.opts.PidAlive(*e.PID) {
			return false, invalid("pid %d is not running", *e.PID)
		}
		// the identity is what tells this process from a later one with the same pid;
		// without it the exception could outlive its process
		id, ok := s.opts.PidIdent(*e.PID)
		if !ok || id == "" {
			return false, invalid("cannot identify the process of pid %d", *e.PID)
		}
		ident = id
	}
	if e.Holds == nil {
		e.Holds = []wire.Hold{}
	}
	// the one shape the database gives back after a restart, so that the same request is the same hold before and after one: no flags is
	// nil (not an empty list), an allow has both lists (an empty one admits nothing; one left out is an empty one)
	if len(e.Flags) == 0 {
		e.Flags = nil
	}
	if a := e.Allow; a != nil {
		e.Allow = &wire.HoldAllow{Classes: append([]wire.JobClass{}, a.Classes...), Repos: append([]string{}, a.Repos...)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.excs[e.Name]; ok && reflect.DeepEqual(old.Exception, e) {
		return false, nil
	}
	if _, ok := s.excs[e.Name]; !ok && len(s.excs) >= maxExceptions {
		return false, invalid("at most %d exceptions at a time", maxExceptions)
	}
	st := store.Stored{Exception: e, Since: now, PIDIdent: ident}
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
	s.pruneLocked(now)
}

func (s *HostState) pruneLocked(now time.Time) {
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

// StateSnapshot is the worker settings, the exceptions and the holds they cause,
// all from one moment.
type StateSnapshot struct {
	Worker     wire.WorkerSettings
	Exceptions []wire.FactExc
	Holds      []derived.Hold
}

// Snapshot ends the exceptions whose time has come (announcing them) and returns
// everything facts needs under one lock, so a record never shows an exception
// without the headroom it causes or the other way round.
func (s *HostState) Snapshot(now time.Time) StateSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	return StateSnapshot{Worker: s.worker, Exceptions: s.activeLocked(), Holds: s.holdsLocked()}
}

// Active is the exceptions in force, as facts shows them (oldest first).
func (s *HostState) Active() []wire.FactExc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeLocked()
}

func (s *HostState) activeLocked() []wire.FactExc {
	out := make([]wire.FactExc, 0, len(s.excs))
	for _, e := range s.sorted() {
		fe := wire.FactExc{Name: e.Exception.Name, Since: e.Since}
		if e.Exception.Until != nil {
			u := *e.Exception.Until
			fe.Until = &u
		}
		if a := e.Exception.Allow; a != nil { // a hold: every reader sees it from the facts (spec §13.4)
			fe.Allow = &wire.HoldAllow{Classes: append([]wire.JobClass{}, a.Classes...), Repos: append([]string{}, a.Repos...)}
		}
		fe.Flags = append([]wire.HoldFlag(nil), e.Exception.Flags...)
		out = append(out, fe)
	}
	return out
}

// Exceptions is the exceptions in force as they were made, holds, allow and flags included (oldest first): what facts' exceptions[] does
// not carry. The ones whose time has come are ended first, as Snapshot does.
func (s *HostState) Exceptions() []wire.Exception {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.opts.Now())
	out := make([]wire.Exception, 0, len(s.excs))
	for _, e := range s.sorted() {
		x := e.Exception
		x.Holds = append([]wire.Hold{}, x.Holds...) // copies: a reader that changes what it was given does not change the hold
		if a := x.Allow; a != nil {
			x.Allow = &wire.HoldAllow{Classes: append([]wire.JobClass{}, a.Classes...), Repos: append([]string{}, a.Repos...)}
		}
		if len(x.Flags) > 0 {
			x.Flags = append([]wire.HoldFlag(nil), x.Flags...)
		}
		out = append(out, x)
	}
	return out
}

// Holds is the exceptions in force as headroom sees them: each holds heavy work,
// and light work too when it lists light.
func (s *HostState) Holds() []derived.Hold {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holdsLocked()
}

func (s *HostState) holdsLocked() []derived.Hold {
	out := make([]derived.Hold, 0, len(s.excs))
	for _, e := range s.sorted() {
		h := derived.Hold{Name: e.Exception.Name}
		for _, x := range e.Exception.Holds {
			h.Light = h.Light || x == wire.HoldLight
		}
		h.Release = slices.Contains(e.Exception.Flags, wire.HoldFlagRelease)
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
