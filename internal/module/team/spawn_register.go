package teammod

// The spawn runner's last steps (spec §7.2 steps 5–6): the member registers
// on its pane, and is stored.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/team"
)

// frameReader lists the live agent frames (agent.TerminalSessionsKey).
type frameReader interface {
	LiveSessions(ctx context.Context, agentType string) ([]agent.TerminalSession, error)
}

// TitleSetter sets a session's title: *store.PeerLabelStore, the value
// WithTitles was given. Without it a spawned member keeps no title.
type TitleSetter interface {
	Claim(sessionID, label string, now time.Time) (store.PeerLabel, error)
}

var _ TitleSetter = (*store.PeerLabelStore)(nil) // else members silently get no title

// initRegister resolves the registration's seams: the frames are a hard
// error, as every runner service is (initSpawn); the title store is optional.
func (m *Module) initRegister(c *core.Core) error {
	var err error
	m.frames, err = lookup[frameReader](c, agent.TerminalSessionsKey)
	m.titleSet, _ = m.titles.(TitleSetter)
	return err
}

// sleepCtx is the registration poll's pause: d, or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// spawnRegister is launched → registered (spec §7.2 step 5). A verified root
// frame on the op's pane with a session id whose registry entry is live is
// the member, once one tmux answer confirms the pane is still the op's: the
// generation it was created in, its session, its tag (review H4: a
// restarted server mints pane ids again, and frames carry no tmux identity).
// A pane that answers but is not the op's means the session died with its
// server: the op is abandoned. An unreadable pane is looked at again.
//
// Every poll judges the deadline first (spec §7.2 step 5: "Wait up to 60 s
// … On timeout: kill"; review R1, ruled again after the critic): past
// launched_at + the budget the op times out (timeOutSpawn), even when the
// member has shown up by then — there is no record of when it registered to
// compare with the deadline.
func (m *Module) spawnRegister(op spawnRow) (*team.Origin, bool) {
	last := "" // what the previous poll found missing: the timeout's detail says it (#2853)
	for {
		if m.now() >= op.LaunchedAt+m.spawnBudget {
			m.timeOutSpawn(op, last)
			return nil, false
		}
		o, missing := m.paneState(op.PaneID) // one read of the frames and the registry per poll
		last = missing
		if missing == "" {
			id, err := m.paneIdentity(op.PaneID)
			if err == nil && !ownsPane(op, id) {
				m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, fmt.Errorf("pane %s is no longer this op's: %+v", op.PaneID, id))
				return nil, false
			}
			if err == nil {
				won, err := m.store.AdvanceSpawnOp(op.ID, team.StepLaunched, spawnUpdate{Step: team.StepRegistered, SessionID: o.SessionID, At: m.now()})
				if err != nil {
					m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, err)
				}
				return &o, err == nil && won
			}
		}
		m.spawnSleep(m.stopCtx, m.spawnPoll)
		if m.stopping() {
			return nil, false
		}
	}
}

// timeOutSpawn takes the timeout's decision before anything else (P4-5
// re-review): one compare-and-set from launched to
// failed{member_start_timeout}, which excludes the registration's own CAS
// from launched (the same row and step). Only its winner kills the session,
// under its recorded generation; a runner that lost it, to a registration
// or another timeout, touches nothing. A daemon that dies between the CAS
// and the kill leaves the session of a failed op behind.
func (m *Module) timeOutSpawn(op spawnRow, lastSeen string) {
	won, err := m.store.FailSpawnOpAtStep(op.ID, team.StepLaunched, team.SpawnReasonStartTimeout, m.now())
	if err != nil {
		m.logf("[team] spawn %s: %v", op.ID, err)
	}
	if !won {
		return
	}
	m.logf("[team] spawn %s failed: %s (last seen: %s)", op.ID, team.SpawnReasonStartTimeout, lastSeen)
	m.spawnSeen.set(op.ID, lastSeen)
	m.releaseMemberName(op.ID) // now: the runner reaps the op later (reapFailedSpawn), but that is a second read that can fail
	m.rosterChanged()          // the timeout is committed: the seat is free again (in_use)
	m.kickFacts()              // a forwarded op's spawn_failed fact is committed with it
	m.wake(op.ID)
	m.killSpawnSession(op.ID, op.TmuxID, op.TmuxInstance)
}

// paneState says what the registration is waiting for on pane, in the words of the timeout's detail (#2853): no frame on the pane yet, a
// frame not verified yet, a frame without a session id, or a session id the registry does not list (yet). The second value is empty when
// the member is there.
func (m *Module) paneState(pane string) (team.Origin, string) {
	if pane == "" {
		return team.Origin{}, "沒有 pane"
	}
	frames, err := m.frames.LiveSessions(m.stopCtx, "cc")
	if err != nil {
		m.logf("[team] spawn registration: frames: %v", err) // the cause stays in the log; the detail says only that
		return team.Origin{}, "讀不到 frame"
	}
	state := "pane 上還沒有 frame"
	for _, f := range frames {
		if f.PaneID != pane {
			continue
		}
		switch {
		case !f.Verified:
			state = "有 frame，尚未驗證"
		case f.SessionID == "":
			state = "有 frame，還沒有 session id"
		default:
			o, ok, err := m.origins.ResolveOriginBySession(f.SessionID)
			if err == nil && ok {
				return o, ""
			}
			state = "有 session id，registry 還沒有它"
		}
	}
	return team.Origin{}, state
}

// startTimeoutDetail is why a member did not register in time, for the failed op (#2761). The signal is the mod's own hello:
// any session of this host whose mod said hello (it survives a restart, mod_hello) proves the hooks work here, so a member that
// is late on such a host is a busy host; a host that never heard one is the one that needs `pdx setup`. Both name the budget
// and, when the resources module has a sample, the host's load under it. Computed when the op is read, so the words of one
// failed op can change if a hello arrives later.
func (m *Module) startTimeoutDetail(op spawnRow) string {
	secs := m.spawnBudget / 1000
	waited := ""
	if op.LaunchedAt > 0 && op.UpdatedAt >= op.LaunchedAt {
		waited = fmt.Sprintf("已等 %d 秒", (op.UpdatedAt-op.LaunchedAt)/1000)
	}
	if v, ok := m.spawnSeen.get(op.ID); ok && v != "" { // lost with a restart (or aged out): then only the seconds are said
		if waited != "" {
			waited += "；"
		}
		waited += "最後看到：" + v
	}
	if waited != "" {
		waited = "（" + waited + "）"
	}
	m.mu.Lock()
	seen := false
	for _, h := range m.modSeen {
		if h.Agent == "cc" {
			seen = true
			break
		}
	}
	m.mu.Unlock()
	load := m.hostLoadNote()
	if seen {
		return fmt.Sprintf("主機忙：member 沒在 %d 秒內完成啟動%s%s，名額已釋出，可以稍後重試", secs, waited, load)
	}
	return fmt.Sprintf("member 沒在 %d 秒內完成啟動%s%s，這台主機從沒看過 Purdex hooks（pdx setup --agent cc）", secs, waited, load)
}

// hostLoadNote is "（load 1/5/15，N 核；記憶體已用 X%，壓力：…）" from the latest resources sample, or "" without one: the
// sample is read, never taken here (no fork). The figures are the resources module's own (load average, memory in use, the
// kernel's memory pressure level: 1 normal, 2 warning, 4 critical).
func (m *Module) hostLoadNote() string {
	d, ok := m.hostDetail()
	h := d.Host
	if !ok || h.NCPU <= 0 {
		return ""
	}
	loads := fmt.Sprintf("%.1f／%.1f／%.1f", h.Load1, h.Load5, h.Load15)
	if d.SessionsRoom != nil { // a reading from wwd, whose facts carry no 15-minute load: say the two it has
		loads = fmt.Sprintf("%.1f／%.1f", h.Load1, h.Load5)
	}
	note := fmt.Sprintf("load %s，%d 核；記憶體已用 %.0f%%", loads, h.NCPU, h.Mem)
	switch h.Pressure {
	case 1:
		note += "，壓力：正常"
	case 2:
		note += "，壓力：警告"
	case 4:
		note += "，壓力：嚴重"
	}
	return "（" + note + "）"
}

// spawnFinish is registered → done (spec §7.2 step 6): the member row, its
// title, then the op, each idempotent. A registry read error leaves the op
// registered for the next boot; a refused write aborts it. o is the
// registry entry the registration saw (nil when resumed at registered: read
// again, or the ref alone if gone).
func (m *Module) spawnFinish(op spawnRow, o *team.Origin) {
	if op.LeadHostID != "" { // forwarded from a lead host: its member row is a remote one, its end a fact
		m.spawnFinishRemote(op, o)
		return
	}
	if o == nil || o.SessionID != op.SessionID {
		r, ok, err := m.origins.ResolveOriginBySession(op.SessionID)
		if err != nil {
			m.logf("[team] spawn %s: %v", op.ID, err)
			return
		}
		if !ok {
			r = team.Origin{SessionID: op.SessionID, Ref: ipeers.RefID(op.SessionID)}
		}
		o = &r
	}
	now := m.now()
	mem := memberRow{SpawnOp: op.ID, TeamID: op.TeamID, HostID: op.HostID, SessionID: op.SessionID,
		Ref: o.Ref, Title: op.Title, Cwd: op.Cwd, TmuxSession: op.TmuxName, TmuxID: op.TmuxID, TmuxInstance: op.TmuxInstance,
		PaneID: op.PaneID, PID: o.PID, ProcStart: o.ProcStart, Model: op.Model, Effort: op.Effort,
		State: team.MemberActive, Origin: team.MemberOriginSpawned, CreatedAt: now, UpdatedAt: now}
	var first *TaskRow
	if op.TaskSubject != "" {
		// The spawn's task, created with the member row in one transaction (T-2).
		done, err := op.taskDoneWhen()
		if err == nil {
			err = op.taskErr()
		}
		if err != nil { // a stored row damaged since it was accepted: no task without its conditions
			m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, err)
			return
		}
		first = &TaskRow{TeamID: op.TeamID, Subject: op.TaskSubject, Description: op.TaskDescription,
			DoneWhen: done, OwnerKey: op.ID, CreatedByRef: ipeers.RefID(op.OriginSessionID),
			SpawnOp: op.ID, CreatedAt: now, UpdatedAt: now}
	}
	won, err := m.store.FinishSpawn(op.ID, mem, first, now)
	if err != nil {
		m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, err)
		return
	}
	if !won {
		return // the op ended meanwhile (an abort, a team end): nothing of it was written
	}
	if op.Title != "" && m.titleSet != nil {
		if _, err := m.titleSet.Claim(op.SessionID, op.Title, time.UnixMilli(now)); err != nil {
			m.logf("[team] spawn %s: title %q for %s: %v", op.ID, op.Title, op.SessionID, err)
		}
	}
	// The op, the member and its task are committed together: announce once the title claim has run, whether it won or
	// failed, so the roster sent carries the member as it will be seen.
	m.rosterChanged()
	m.logf("[team] spawn %s done: member %s (%s) in %s", op.ID, o.Ref, op.SessionID, op.TmuxName)
	m.releaseMemberName(op.ID) // the session is registered: it takes the name at its first naming, no longer held by the op
	m.wake(op.ID)
}

// spawnSeenBook remembers, for the ops that timed out, what their registration last found missing, so the failed op's detail can say it
// (#2853). It is bounded: the oldest entries go past spawnSeenCap, so a daemon that spawns for months holds a few hundred strings.
const spawnSeenCap = 256

type spawnSeenBook struct {
	mu    sync.Mutex
	m     map[string]string
	order []string
}

func (b *spawnSeenBook) set(id, state string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string]string{}
	}
	if _, had := b.m[id]; !had {
		b.order = append(b.order, id)
		for len(b.order) > spawnSeenCap {
			delete(b.m, b.order[0])
			b.order = b.order[1:]
		}
	}
	b.m[id] = state
}

func (b *spawnSeenBook) get(id string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.m[id]
	return v, ok
}
