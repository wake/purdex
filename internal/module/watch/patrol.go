package watchmod

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/watch"
)

// Patrol (host-load-control plan W3b-5b): the daemon is the only source of "go and look". The host that holds the watcher (awake or
// resting) counts the time since the watcher was last told anything and, when the interval for the situation has passed, wakes it with
// the reason patrol. The intervals are settings: the person at the computer → patrol_present_min; away and in unattended mode under
// pressure (zone warning or danger) → patrol_busy_min, otherwise patrol_calm_min; away and not in unattended mode → patrol_present_min;
// a resting watcher → patrol_rest_min (so it is woken). Free disk space under disk_low_gb wakes it at once, once per time the disk runs
// low. The watcher's skill tells it not to set patrols of its own.

const patrolTick = time.Minute

// patrolState is what the patrol keeps between ticks.
type patrolState struct {
	mu          sync.Mutex
	last        time.Time // when the watcher was last told anything
	diskAlerted bool      // the disk is low and the watcher was told
	started     time.Time // when this run of the daemon began to patrol
}

func (p *patrolState) told(at time.Time) {
	p.mu.Lock()
	p.last = at
	p.mu.Unlock()
}

// patrolSources are the facts the interval depends on (nil fields: the registry's services).
type patrolSources struct {
	atComputer func() bool
	unattended func() bool
	busy       func() bool // the host's zone is warning or danger
	diskFreeGB func() (float64, bool)
}

func (m *Module) atComputer() bool {
	if m.psrc != nil && m.psrc.atComputer != nil {
		return m.psrc.atComputer()
	}
	if svc, ok := m.core.Registry.Get(push.AtComputerKey); ok {
		if r, ok := svc.(push.AtComputerReader); ok {
			return r.AtComputer()
		}
	}
	return false
}

func (m *Module) unattendedOn() bool {
	if m.psrc != nil && m.psrc.unattended != nil {
		return m.psrc.unattended()
	}
	if svc, ok := m.core.Registry.Get(hostconfig.UnattendedKey); ok {
		if r, ok := svc.(interface {
			Unattended() (team.UnattendedState, error)
		}); ok {
			st, err := r.Unattended()
			return err == nil && st.On
		}
	}
	return false
}

func (m *Module) hostBusy() bool {
	if m.psrc != nil && m.psrc.busy != nil {
		return m.psrc.busy()
	}
	if svc, ok := m.core.Registry.Get(resources.HostReaderKey); ok {
		if r, ok := svc.(resources.HostReader); ok {
			if h, ok := r.LatestHost(); ok {
				return h.Zone == resources.ZoneWarning.String() || h.Zone == resources.ZoneDanger.String()
			}
		}
	}
	return false
}

func (m *Module) diskFreeGB() (float64, bool) {
	if m.psrc != nil && m.psrc.diskFreeGB != nil {
		return m.psrc.diskFreeGB()
	}
	// While wwd reads this host (the pool source) its facts have the disk: no statfs of our own (C1). Facts list the volume of the
	// home directory and the root volume, so the data directory's own is the one whose mount holds it, when it is under home.
	if gb, ok := m.diskFreeFromFacts(); ok {
		return gb, true
	}
	dir := "/"
	if m.core != nil && m.core.Cfg != nil && m.core.Cfg.DataDir != "" {
		dir = m.core.Cfg.DataDir
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return float64(st.Bavail) * float64(st.Bsize) / (1 << 30), true
}

// patrolInterval is the wait for the situation, and a word for the note.
func (m *Module) patrolInterval(s patrolSettings, resting bool) (time.Duration, string) {
	switch {
	case resting:
		return time.Duration(s.rest) * time.Minute, "休息中"
	case m.atComputer():
		return time.Duration(s.present) * time.Minute, "人在電腦前"
	case m.unattendedOn() && m.hostBusy():
		return time.Duration(s.busy) * time.Minute, "無人值守且主機吃緊"
	case m.unattendedOn():
		return time.Duration(s.calm) * time.Minute, "無人值守且主機平穩"
	}
	return time.Duration(s.present) * time.Minute, "不在電腦前"
}

type patrolSettings struct{ present, busy, calm, rest, diskGB int }

// patrolStep is one look: the disk first, then the interval.
func (m *Module) patrolStep(ctx context.Context) {
	m.recoverSession(ctx) // a start mark left by an earlier run of the daemon is settled here, whoever holds the watcher
	s, err := m.readSettings()
	if err != nil || !s.Enabled {
		return
	}
	h := m.hs().Get()
	if h.State != watch.HolderAwake && h.State != watch.HolderResting {
		return // this host holds no watcher: another host's daemon patrols
	}
	if h.State == watch.HolderResting { // a rested holder is not the watcher when another host's is awake
		if cur, _ := m.loc.Remembered(); cur != nil && !m.isSelf(cur.Alias) {
			return
		}
	}
	now := m.now()
	wake := m.wake
	if wake == nil {
		wake = m.WakeWatcher
	}
	if free, ok := m.diskFreeGB(); ok {
		m.patrol.mu.Lock()
		low := free < float64(s.DiskLowGB)
		alerted := m.patrol.diskAlerted
		if !low {
			m.patrol.diskAlerted = false
		}
		m.patrol.mu.Unlock()
		if low && !alerted {
			// marked only once the watcher was told: a failed attempt is made again at the next tick
			if err := wake(ctx, "disk_low", fmt.Sprintf("剩 %.1f GB（低於 %d GB）", free, s.DiskLowGB)); err != nil {
				m.logf("[watch] patrol: disk low: %v", err)
				return
			}
			m.patrol.mu.Lock()
			m.patrol.diskAlerted = true
			m.patrol.mu.Unlock()
			m.patrol.told(now)
			return
		}
	}
	every, why := m.patrolInterval(patrolSettings{s.PatrolPresentMin, s.PatrolBusyMin, s.PatrolCalmMin, s.PatrolRestMin, s.DiskLowGB}, h.State == watch.HolderResting)
	// base is the latest of the last note, the watcher's registration and the daemon's start; a stamp that is ahead of the clock (it went
	// back) is replaced by now, kept as the last note, so the count goes on from here instead of waiting for the clock to catch up
	m.patrol.mu.Lock()
	if m.patrol.last.After(now) { // a clock that went back: our own stamps are brought to now and kept
		m.patrol.last = now
	}
	if m.patrol.started.After(now) {
		m.patrol.started = now
	}
	base := latest(m.patrol.last, m.patrol.started)
	if !h.Since.After(now) { // the holder's own stamp is not ours to change: one ahead of the clock is left out
		base = latest(base, h.Since)
	}
	m.patrol.mu.Unlock()
	if now.Sub(base) < every {
		return
	}
	m.patrol.told(now) // also when it fails: the next try is an interval away, not the next tick
	if err := wake(ctx, "patrol", fmt.Sprintf("距上次通知 %d 分｜%s", int(now.Sub(base)/time.Minute), why)); err != nil {
		m.logf("[watch] patrol: %v", err)
	}
}

// patrolLoop runs patrolStep every tick.
func (m *Module) patrolLoop(ctx context.Context) {
	every := patrolTick
	if m.patrolEvery > 0 {
		every = m.patrolEvery
	}
	m.patrol.mu.Lock()
	m.patrol.started = m.now()
	m.patrol.mu.Unlock()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			step, done, ok := m.enterFor(ctx, 6*time.Minute) // a wake can wait for a start
			if ok {
				m.patrolStep(step)
				done()
			}
		}
	}
}

// diskFreeFromFacts is the free space of the data directory's volume from wwd's facts of this host, ok false when there are none
// (the source is not the pool, or wwd has none fresh), when the facts list no disk, or when the data directory is not on a volume
// they list. Facts list the root volume and the volume of the home directory; their mount names are not comparable with a path
// (macOS reports /System/Volumes/Data for /Users, a symlinked home by its target), so which of them holds the data directory is not
// worked out from the names: the directory is on a listed volume when it has the device of home or of the root (a stat, no
// sampling), and then the lowest free space of the listed volumes is taken (where the two share their space, as on macOS, they say
// the same). A data directory on any other device, a separate mount or a link out, keeps the own statfs.
func (m *Module) diskFreeFromFacts() (float64, bool) {
	if m.core == nil || m.core.Registry == nil {
		return 0, false
	}
	svc, ok := m.core.Registry.Get(resources.LocalFactsKey)
	if !ok {
		return 0, false
	}
	r, ok := svc.(resources.LocalFactsReader)
	if !ok {
		return 0, false
	}
	f, ok := r.LatestLocalFacts()
	if !ok || len(f.Disk) == 0 {
		return 0, false
	}
	dir := "/"
	if m.core.Cfg != nil && m.core.Cfg.DataDir != "" {
		dir = m.core.Cfg.DataDir
	}
	home, err := os.UserHomeDir()
	if err != nil || !(sameDevice(dir, home) || sameDevice(dir, "/")) {
		return 0, false
	}
	low := f.Disk[0].FreeGB
	for _, d := range f.Disk[1:] {
		low = min(low, d.FreeGB)
	}
	return low, true
}

// sameDevice says both paths exist and are on the same device. stat follows links, so a link out to another volume is on that one.
func sameDevice(a, b string) bool {
	var sa, sb syscall.Stat_t
	if syscall.Stat(a, &sa) != nil || syscall.Stat(b, &sb) != nil {
		return false
	}
	return sa.Dev == sb.Dev
}
