package peers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The backfill of old conversations (name pool v2 spec §6.4). Once personas exist, a live conversation whose name does not end in
// a persona code (active or retired) is named by the rule of its role: a live lead by rule 1 without a card (the first
// candidate other than its own name, or its name when there is none), a member of a local lead by rule 2 with that lead, anyone
// else by rule 3. The old name becomes an alias (RenamePool), the row's source becomes pool, and the session is told.
//
// It runs in the naming pass, after settlePoolCollisions, and picks under the same pool lock. A session in a relay is left for a
// later pass; so is one whose role or relay state cannot be read (a role is never guessed).

// renameNoticeFmt is what the renamed session is told: its new address, and that the old one still works. It reaches a session
// that is not a team seat by the same in-process sender the [pdx team] notices use (SendSelfNotice), from the session's own
// inbox to itself, so the text arrives as a prompt like the other notices.
const renameNoticeFmt = "[pdx team] 你的地址改成 %s，舊地址 %s 仍可用"

// renameNoticeTimeout bounds one notice send.
const renameNoticeTimeout = 5 * time.Second

type renameNotice struct {
	sid, inbox, oldName, newName string
}

// candidate is a live conversation the backfill may rename: its registry entry, its stored name, and its seat once read.
type candidate struct {
	entry ipeers.Entry
	name  string
	seat  team.Seat
}

// seatRank orders the backfill: a lead before its members, members before the rest.
func seatRank(role string) int {
	switch role {
	case team.SeatLead:
		return 0
	case team.SeatMember:
		return 1
	}
	return 2
}

// backfillPersonaNames renames the live conversations of the pass whose name has no persona code; names is updated for each
// rename (keyed by session id as the pass keys it), selfAlias is this host's alias for the address in the notice.
func (m *Module) backfillPersonaNames(ctx context.Context, entries []ipeers.Entry, proxyPIDs map[int]bool, names map[string]string, selfAlias string) {
	if m.namePicker == nil || m.peerNames == nil {
		return
	}
	// The scan reads the pool and the persona codes of one generation (bindPersonas holds the write side while it publishes both).
	var cands []candidate
	seen := map[string]bool{}
	m.poolGen.RLock()
	if p := m.personaCodes.Load(); p == nil || len(*p) == 0 {
		m.poolGen.RUnlock()
		return // no personas: the built-in names are the pool and nothing is old
	}
	for _, e := range entries {
		key := strings.ToLower(e.SessionID)
		if key == "" || e.IsProxy || proxyPIDs[e.PID] || seen[key] {
			continue
		}
		seen[key] = true
		name := names[e.SessionID]
		if name == "" {
			continue
		}
		if code, ok := ipeers.PoolNameOf(name); ok && m.isPersonaCode(code) {
			continue
		}
		cands = append(cands, candidate{entry: e, name: name})
	}
	m.poolGen.RUnlock()
	if m.afterBackfillScan != nil {
		m.afterBackfillScan()
	}
	if len(cands) == 0 {
		return
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].entry.SessionID < cands[j].entry.SessionID })

	if err := m.awaitPoolReady(ctx); err != nil {
		return
	}
	unlock, err := m.lockPool(ctx)
	if err != nil {
		m.logf("peers: name backfill: the naming lock was not free in time, tried next pass: %v", err)
		return
	}
	defer unlock()

	ready := make([]candidate, 0, len(cands))
	for _, c := range cands {
		sid := c.entry.SessionID
		if inRelay, err := m.sessionInRelay(sid); err != nil {
			m.logf("peers: name backfill: cannot tell whether %s is in a relay, left for the next pass: %v", sid, err)
			continue
		} else if inRelay {
			continue
		}
		seat, err := m.seatOf(sid)
		if err != nil {
			m.logf("peers: name backfill: cannot read the seat of %s, left for the next pass: %v", sid, err)
			continue
		}
		c.seat = seat
		ready = append(ready, c)
	}
	// Leads first, then members, then the rest (session ids within each): a member follows its lead's persona code, which this
	// very pass may have just given.
	sort.SliceStable(ready, func(i, j int) bool { return seatRank(ready[i].seat.Role) < seatRank(ready[j].seat.Role) })

	var notices []renameNotice
	picked := map[string]bool{}
	for _, c := range ready {
		if n, ok := m.backfillOne(ctx, c, picked, names); ok {
			notices = append(notices, n)
		}
	}
	m.sendRenameNotices(notices, selfAlias)
}

// backfillOne names one candidate, under the generation read lock from the look at its name to the write: the collection may
// have changed since the scan, and a name that is now a persona code is left alone. ok is true when a notice is owed.
func (m *Module) backfillOne(ctx context.Context, c candidate, picked map[string]bool, names map[string]string) (renameNotice, bool) {
	sid := c.entry.SessionID
	held, err := m.heldPoolNames(ctx, picked)
	if err != nil {
		m.logf("peers: name backfill: cannot read the held names, %s keeps %s this pass: %v", sid, c.name, err)
		return renameNotice{}, false
	}
	m.poolGen.RLock()
	defer m.poolGen.RUnlock()
	if p := m.personaCodes.Load(); p == nil || len(*p) == 0 {
		return renameNotice{}, false
	}
	if code, ok := ipeers.PoolNameOf(c.name); ok && m.isPersonaCode(code) {
		return renameNotice{}, false
	}
	pick, ok := m.backfillPick(ctx, held, c.name, c.seat)
	if !ok {
		return renameNotice{}, false
	}
	newName, ok := ipeers.VirtualNameWith(nameBase(c.name, sid), pick)
	if !ok || newName == c.name {
		return renameNotice{}, false
	}
	row, won, err := m.peerNames.RenamePool(ctx, sid, c.name, newName)
	switch {
	case err != nil:
		m.logf("peers: name backfill: renaming %s from %s failed, it keeps its name this pass: %v", sid, c.name, err)
		return renameNotice{}, false
	case !won:
		m.logf("peers: name backfill: %s changed under the rename of %s, keeping what is stored", sid, c.name)
		return renameNotice{}, false
	}
	picked[pick] = true
	if ipeers.RoutableName(row.Name) {
		names[sid] = row.Name
	}
	if c.entry.Inbox == "" {
		m.logf("peers: name backfill: %s has no inbox, it is renamed to %s but not told", sid, row.Name)
		return renameNotice{}, false
	}
	return renameNotice{sid: sid, inbox: c.entry.Inbox, oldName: c.name, newName: row.Name}, true
}

// backfillPick chooses the code for a session of the given seat; held is the held pool names. ok is false when nothing fits. The
// caller holds the generation read lock.
func (m *Module) backfillPick(ctx context.Context, held map[string]bool, name string, seat team.Seat) (string, bool) {
	in := m.inputForLocked(ctx, held)
	own, _ := ipeers.PoolNameOf(name)
	switch seat.Role {
	case team.SeatLead:
		for _, c := range leadCandidates(in, own) {
			if c != own {
				return c, true
			}
		}
		return "", false
	case team.SeatMember:
		if lead, ok := m.CodeOf(ctx, seat.LeadSessionID); ok {
			in.Pairs = in.LeadPairs[lead]
		}
		return pickForMember(in)
	}
	return pickForOther(in)
}

// seatOf is the seat of a session in the team module; without the module (a build without it) everything is SeatNone.
func (m *Module) seatOf(sessionID string) (team.Seat, error) {
	if m.core == nil || m.core.Registry == nil {
		return team.Seat{Role: team.SeatNone}, nil
	}
	if v, ok := m.core.Registry.Get(team.SeatReaderKey); ok {
		if r, ok := v.(team.SeatReader); ok {
			return r.SeatOf(sessionID)
		}
	}
	return team.Seat{Role: team.SeatNone}, nil
}

// sessionInRelay says whether the team module has the session in a relay that has not finished; without the module, no.
func (m *Module) sessionInRelay(sessionID string) (bool, error) {
	if m.core == nil || m.core.Registry == nil {
		return false, nil
	}
	if v, ok := m.core.Registry.Get(team.RelayGuardKey); ok {
		if g, ok := v.(team.RelayGuard); ok {
			return g.InRelay(sessionID)
		}
	}
	return false, nil
}

// sendRenameNotices tells each renamed session from its own goroutine, so the pass never waits for N sends; a failure is only
// logged (the old address keeps working, a session that missed the notice loses nothing but the news).
func (m *Module) sendRenameNotices(notices []renameNotice, selfAlias string) {
	if len(notices) == 0 {
		return
	}
	// Admission and Stop's close share a lock, so no worker is added once Stop has started to wait.
	m.noticeMu.Lock()
	if m.noticeClosed || m.stopCtx.Err() != nil {
		m.noticeMu.Unlock()
		return
	}
	if m.beforeNoticeAdmit != nil {
		m.beforeNoticeAdmit()
	}
	m.workers.Add(1)
	m.noticeMu.Unlock()
	go func() {
		defer m.workers.Done()
		for _, n := range notices {
			req := ipeers.SendRequest{
				To:          selfAlias + "/" + ipeers.RefID(n.sid),
				Text:        fmt.Sprintf(renameNoticeFmt, selfAlias+"/"+n.newName, selfAlias+"/"+n.oldName),
				OriginInbox: n.inbox,
			}
			ctx, cancel := context.WithTimeout(m.stopCtx, renameNoticeTimeout)
			err := m.sendRenameNotice(ctx, req)
			cancel()
			if err != nil {
				m.logf("peers: name backfill: notice to %s failed: %v", n.sid, err)
			}
		}
	}()
}

// sendRenameNotice is the notice send; noticeSend is the test seam.
func (m *Module) sendRenameNotice(ctx context.Context, req ipeers.SendRequest) error {
	if m.noticeSend != nil {
		return m.noticeSend(ctx, req)
	}
	_, err := moduleSender{m: m}.SendSelfNotice(ctx, req)
	return err
}
