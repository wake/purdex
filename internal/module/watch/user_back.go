package watchmod

import (
	"context"
	"fmt"
	"sync"
	"time"

	ifleet "github.com/wake/purdex/internal/fleet"
	fleetmod "github.com/wake/purdex/internal/module/fleet"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/watchwake"
)

// user_back (watcher wake settings spec 6.5): the person comes back to the computer after being away for at least rules.user_back.away_min
// minutes, and the watcher is told once. Only the fleet primary reads presence (the standby would tell the same thing a second time); presence
// has three values - at the computer, away, and not known (no Mac reports) - and "not known" is no reading at all; the first reading only
// sets the baseline; a change of the role starts over. The wake goes through WakeWatcher like any other, so the rules of section 6.1 (the
// switches, the prompt) are that function's to apply.

// userBackEvery is how often presence is read.
const userBackEvery = 60 * time.Second

// defaultAwayMin is the away_min of a document that cannot be read (the default of the settings).
const defaultAwayMin = 30

// userBackState is what the reading keeps between ticks.
type userBackState struct {
	mu        sync.Mutex
	primed    bool      // a reading has been taken as the baseline
	atPrev    bool      // the person was at the computer at the last reading
	term      int64     // the fleet term the baseline was taken in (a new term is a change of role, even if no tick saw the standby)
	awaySince time.Time // when the person was first seen away (zero: at the computer, or no baseline)
}

// reset forgets the baseline: the next reading is a baseline again. s.mu is held.
func (s *userBackState) reset() { s.primed, s.atPrev, s.awaySince = false, false, time.Time{} }

// userBackSources are the facts a reading depends on (nil fields: the registry's services and the settings).
type userBackSources struct {
	role     func() (primary bool, term int64)
	presence func() (atComputer, known bool)
	awayMin  func() int
}

// presenceState is what the push module's presence says: at the computer, and whether any report says anything. A value that is only an
// AtComputerReader (an older push module) does not tell the difference, so it is "not known".
func (m *Module) presenceState() (atComputer, known bool) {
	if m.ubsrc != nil && m.ubsrc.presence != nil {
		return m.ubsrc.presence()
	}
	if svc, ok := m.core.Registry.Get(push.AtComputerKey); ok {
		if r, ok := svc.(push.PresenceStateReader); ok {
			return r.PresenceState()
		}
	}
	return false, false
}

// fleetRole is one reading of the fleet's role: whether this daemon is the primary now, and the term that reading is of. With no fleet module,
// or the fleet off, it is not the primary.
func (m *Module) fleetRole() (primary bool, term int64) {
	if m.ubsrc != nil && m.ubsrc.role != nil {
		return m.ubsrc.role()
	}
	svc, ok := m.core.Registry.Get(fleetmod.RoleViewKey)
	if !ok {
		return false, 0
	}
	rv, ok := svc.(ifleet.RoleView)
	if !ok {
		return false, 0
	}
	st := rv.State()
	return st.Role == ifleet.RolePrimary, st.Term
}

// userBackAwayMin is rules.user_back.away_min of the settings this host has now (the source's document, or its copy of it); the default when
// the document cannot be read or is of a newer version.
func (m *Module) userBackAwayMin() int {
	if m.ubsrc != nil && m.ubsrc.awayMin != nil {
		return m.ubsrc.awayMin()
	}
	var raw []byte
	if source, isSource := m.wakeSourceAlias(); isSource {
		d, err := m.readWakeSource()
		if err != nil {
			return defaultAwayMin
		}
		raw = d.settings
	} else {
		raw = m.followerView(source).Settings
	}
	s, errs := watchwake.Normalize(raw)
	if len(errs) > 0 {
		return defaultAwayMin
	}
	return s.Rules.UserBack.AwayMin
}

// userBackStep takes one reading and, when the person has come back after being away long enough, wakes the watcher.
func (m *Module) userBackStep(ctx context.Context) {
	st := &m.ub
	primary, term := m.fleetRole()
	if !primary {
		st.mu.Lock()
		st.reset()
		st.mu.Unlock()
		return
	}
	at, known := m.presenceState()
	now := m.now()
	var awayFor time.Duration
	back := false
	st.mu.Lock()
	if st.primed && st.term != term { // primary -> standby -> primary between two ticks
		st.reset()
	}
	st.term = term
	switch {
	case !known: // nobody reports: neither away nor back, and what was seen before says nothing of what comes next
		st.reset()
	case !st.primed: // the first reading is only a baseline; one that is away counts from now
		st.primed, st.atPrev = true, at
		st.awaySince = time.Time{}
		if !at {
			st.awaySince = now
		}
	case at && !st.atPrev: // away -> at the computer
		if !st.awaySince.IsZero() {
			awayFor, back = now.Sub(st.awaySince), true
		}
		st.atPrev, st.awaySince = true, time.Time{}
	case !at && st.atPrev: // at the computer -> away
		st.atPrev, st.awaySince = false, now
	}
	st.mu.Unlock()
	if !back || awayFor < time.Duration(m.userBackAwayMin())*time.Minute {
		return
	}
	// the role is read again right before the wake: reading the settings above can take a while, and a daemon that is no longer the primary
	// (or is in another term) does not tell
	if again, termAgain := m.fleetRole(); !again || termAgain != term {
		st.mu.Lock()
		st.reset()
		st.mu.Unlock()
		return
	}
	// the state is already moved on: a wake that fails is not sent again (the person is at the computer, and the next one is a new absence)
	detail := fmt.Sprintf("使用者回到電腦前（離開約 %d 分鐘）", int(awayFor.Round(time.Minute).Minutes()))
	if err := m.wakeUserBack(ctx, detail); err != nil && ctx.Err() == nil {
		m.logf("[watch] user_back: the watcher could not be told: %v", err)
	}
}

// wakeUserBack tells the watcher through the module's WakeWatcher (m.wake is the seam of the tests).
func (m *Module) wakeUserBack(ctx context.Context, detail string) error {
	if m.wake != nil {
		return m.wake(ctx, "user_back", detail)
	}
	return m.WakeWatcher(ctx, "user_back", detail)
}

// userBackLoop reads presence every userBackEvery until ctx ends.
func (m *Module) userBackLoop(ctx context.Context) {
	every := userBackEvery
	if m.userBackEvery > 0 {
		every = m.userBackEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.userBackStep(ctx)
		}
	}
}
