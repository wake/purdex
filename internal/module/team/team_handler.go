package teammod

// GET /api/team and POST /api/team/kill (spec §7.3, U20 (e)): the lead's
// team with what each member runs, and the kill of one of its members.

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// errRegistry marks a failure to read the registry (503, retry).
var errRegistry = errors.New("registry unavailable")

// handleTeam is GET /api/team?origin_inbox=<inbox>: the caller's live team
// and every member of it, in any state, oldest first.
func (m *Module) handleTeam(w http.ResponseWriter, r *http.Request) {
	t, ok := m.callerTeam(w, r.URL.Query().Get("origin_inbox"))
	if !ok {
		return
	}
	rows, err := m.store.MembersOf(t.ID)
	if err != nil {
		m.logf("[team] team %s: %v", t.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	// One read for the whole team (finished tasks are never a current task),
	// grouped by owner, rather than one per member.
	open, err := m.store.ListTasks(t.ID, "", false)
	if err != nil {
		m.logf("[team] team %s: %v", t.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	memberTurns, err := m.store.MemberLastTurnAts(t.ID)
	if err != nil {
		m.logf("[team] team %s: %v", t.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	byOwner := map[string][]TaskRow{}
	for _, tk := range open {
		byOwner[tk.OwnerKey] = append(byOwner[tk.OwnerKey], tk)
	}
	v := team.TeamView{Team: t, Members: make([]team.Member, 0, len(rows))}
	for _, mr := range rows {
		mv := m.memberView(mr)
		if mr.SpawnOp != "" { // every row has a key: a spawned member's is its spawn op, an adopted one's the adoption's request id (the wire's SpawnOp is empty for it)
			if cur, ok := currentTaskOf(byOwner[mr.SpawnOp]); ok {
				mv.Task = &team.MemberTask{ID: team.TaskDisplayID(t.ID, cur.Seq), Subject: cur.Subject, Status: cur.Status}
				mv.LastAt = max(cur.LastTurnAt, cur.LastReportAt, memberTurns[mr.SpawnOp]) // a pending task keeps the turns the row took
			} else {
				mv.LastAt = memberTurns[mr.SpawnOp] // no task: the member row's own last turn (T-3a2)
			}
		}
		v.Members = append(v.Members, mv)
	}
	m.writeJSON(w, http.StatusOK, v)
}

// callerTeam is the live team the inbox's session leads; false means an
// error was written: 503 (registry), 400 origin_unknown, 409 not_lead.
// The inbox names the caller on trust: the host token is shared by the SPA
// and pdx (spec §6.5), and lead/team is not a security boundary between
// processes of one uid (P4-6 review, ruled by the coordinator).
func (m *Module) callerTeam(w http.ResponseWriter, inbox string) (team.Team, bool) {
	origin, ok := m.callerOrigin(w, inbox)
	if !ok {
		return team.Team{}, false
	}
	t, found, err := m.store.LiveTeamByLead(origin.SessionID)
	if err != nil {
		m.logf("[team] team of %s: %v", origin.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return team.Team{}, false
	}
	if !found {
		m.writeErr(w, http.StatusConflict, team.ErrNotLead, "this session leads no live team", nil)
		return team.Team{}, false
	}
	return t, true
}

// callerOrigin is the live session the inbox names; false means an error
// was written: 503 (registry), 400 origin_unknown.
func (m *Module) callerOrigin(w http.ResponseWriter, inbox string) (team.Origin, bool) {
	origin, ok, err := m.origins.ResolveOrigin(inbox)
	if err != nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return team.Origin{}, false
	}
	if !ok {
		m.writeErr(w, http.StatusBadRequest, team.ErrOriginUnknown, "origin_inbox is not a live Claude Code session on this host", nil)
		return team.Origin{}, false
	}
	return origin, true
}

// memberView is a member row in the wire's shape. Context is the agent
// module's live reading, else the one the sweeper persisted (spec §8.5),
// else absent: so its model and effort stay blank until the member's first
// statusline (U20 (e)). An active member's address is the one the origin
// resolver answers (its virtual address, the one pdx msg send routes by);
// any other's, or one the registry does not list, is <self alias>/<ref>.
func (m *Module) memberView(mr memberRow) team.Member {
	alias, _ := m.selfHost()
	v := team.Member{SessionID: mr.SessionID, Ref: mr.Ref, Address: alias + "/" + mr.Ref, TeamID: mr.TeamID,
		HostID: mr.HostID, Title: mr.Title, Cwd: mr.Cwd, TmuxSession: mr.TmuxSession, State: mr.State,
		Model: mr.Model, Effort: mr.Effort, SpawnOp: mr.SpawnOp, CreatedAt: mr.CreatedAt,
		Origin: team.MemberOriginSpawned} // every row is spawned until adopt lands (PL-1b)
	if mr.State == team.MemberActive {
		if o, ok, err := m.origins.ResolveOriginBySession(mr.SessionID); err == nil && ok {
			v.Address = o.Address
		}
	}
	v.Context = m.sessionContext(mr.SessionID, mr.Usage)
	return v
}

// sessionContext is the ONE place a session's context reading is chosen: the
// agent module's live reading of sessionID, else persisted (the one the
// sweeper stored on the member's row or the lead's team row), else nil. The
// team view, GET /api/team and the roster all call it, so they never
// disagree; U1-7 changes the context source here and nowhere else. A nil
// m.usage (tests, early boot) is "no live reading".
func (m *Module) sessionContext(sessionID string, persisted *team.MemberContext) *team.MemberContext {
	if m.usage != nil {
		if u, ok := m.usage.ContextUsage(sessionID); ok {
			c := contextOf(u)
			return &c
		}
	}
	return persisted
}

func (m *Module) selfHost() (alias, hostID string) {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return m.core.Cfg.PeerAlias(), m.core.Cfg.HostID
}

// handleKill is POST /api/team/kill (spec §7.3): the caller's own member
// only (else 409 not_your_member), its tmux session ended and the row it
// read marked killed (killAndMark). A killed member answers 200 again with
// no tmux call. Worktrees are the lead's.
func (m *Module) handleKill(w http.ResponseWriter, r *http.Request) {
	if m.tmux == nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon has no tmux", nil)
		return
	}
	var req team.KillRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	t, ok := m.callerTeam(w, req.OriginInbox)
	if !ok {
		return
	}
	mr, found, err := m.matchMember(t, req.Target)
	switch {
	case errors.Is(err, errRegistry):
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	case err != nil:
		m.logf("[team] kill %q: %v", req.Target, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	case !found:
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, fmt.Sprintf("%q is no member of team %s", req.Target, t.ID), nil)
		return
	}
	if mr.State != team.MemberKilled {
		if mr, ok = m.killAndMark(w, t, mr); !ok {
			return
		}
	}
	m.writeJSON(w, http.StatusOK, m.memberView(mr))
}

// killAndMark ends mr's tmux session and marks killed the row as read (P4-6
// review R1). A member mid-relay (claimed, writing, written) is refused
// before anything is killed: 409 relay_open with its op. The mark is a
// compare-and-set on the session read (MarkMemberKilled). If it loses, a
// row another kill marked is a success; anything else (a relay claimed, or
// completed and moved the row to its new session) is 409 relay_open, the row
// as it is. false means an error was written.
func (m *Module) killAndMark(w http.ResponseWriter, t team.Team, mr memberRow) (memberRow, bool) {
	failStore := func(err error) (memberRow, bool) {
		m.logf("[team] kill %s: %v", mr.SpawnOp, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return memberRow{}, false
	}
	relayOpen := func(op team.RelayOp, ok bool, detail string) (memberRow, bool) {
		e := team.APIError{Error: team.ErrRelayOpen, Detail: "member " + mr.Ref + " " + detail + "; its row is unchanged; kill it again"}
		if ok {
			e.Op = &op
		}
		m.writeJSON(w, http.StatusConflict, e)
		return memberRow{}, false
	}
	if op, open, err := m.store.OpenRelayOpBySession(mr.SessionID); err != nil {
		return failStore(err)
	} else if open && (op.State == team.RelayClaimed || op.State == team.RelayWriting || op.State == team.RelayWritten) {
		return relayOpen(op, true, "is relaying; nothing was killed")
	}
	if status, code, why := m.killMember(mr); why != "" {
		m.writeErr(w, status, code, why, nil)
		return memberRow{}, false
	}
	if m.beforeKillMark != nil {
		m.beforeKillMark(mr)
	}
	killed, err := m.store.MarkMemberKilled(mr.SpawnOp, mr.SessionID, m.now())
	if err != nil {
		return failStore(err)
	}
	if killed {
		m.logf("[team] member %s (%s) of team %s killed", mr.Ref, mr.SessionID, t.ID)
		mr.State = team.MemberKilled
		m.rosterChanged()
		return mr, true
	}
	rows, err := m.store.MembersOf(t.ID)
	if err != nil {
		return failStore(err)
	}
	for _, now := range rows {
		if now.SpawnOp == mr.SpawnOp && now.State == team.MemberKilled {
			return now, true // another kill marked it first
		}
	}
	op, open, err := m.store.OpenRelayOpBySession(mr.SessionID)
	if err != nil {
		return failStore(err)
	}
	return relayOpen(op, open, "relayed while it was being killed")
}

// killMember ends the tmux session the member's spawn created, and no other
// (P4-5: id, generation, @pdx_spawn_op tag): one identity read of the id,
// then a kill by id under that generation. On another generation the
// session died with its server: nothing is killed. "" means nothing of the
// member runs any more; else why the row must stay: the tag is gone (409),
// or the read failed (503). A gone member (its shell may be left) skips the
// kill only on ErrNoSession — tmux said no such session (P4-6 critic); an
// active one never does. An unrecorded id never becomes a target (":" names
// tmux's current session).
func (m *Module) killMember(mr memberRow) (int, string, string) {
	var id tmux.PaneIdentity
	err := fmt.Errorf("no tmux session id recorded (%q): %w", mr.TmuxID, tmux.ErrNoSession)
	if strings.HasPrefix(mr.TmuxID, "$") {
		id, err = m.paneIdentity(mr.TmuxID + ":")
	}
	switch {
	case errors.Is(err, tmux.ErrNoSession) && mr.State == team.MemberGone:
		return 0, "", ""
	case err != nil:
		return http.StatusServiceUnavailable, team.ErrNotReady, "tmux did not show the member's session; retry: " + err.Error()
	case id.Instance != mr.TmuxInstance:
		return 0, "", ""
	case id.SessionID != mr.TmuxID || id.Tag != mr.SpawnOp:
		return http.StatusConflict, team.ErrNotYourMember, "tmux session " + mr.TmuxID + " no longer carries this member's spawn tag; not killed"
	}
	if _, err := m.tmux.KillSessionIfInstance(mr.TmuxID, mr.TmuxInstance); err != nil && !errors.Is(err, tmux.ErrNoSession) {
		return http.StatusServiceUnavailable, team.ErrNotReady, "tmux kill-session failed; retry: " + err.Error()
	}
	return 0, "", ""
}

// matchMember finds the one member of team t that target names (plan v3
// P4-6; P4c-4 adds remote hosts). Only t's members are looked at, in any
// state. A ref matches a member's current ref, else one of its previous
// refs (the lineage, spec §8.4); a name matches the name in an active
// member's live address (membersNamed); "<name> [<ref>]" must match both. No
// match, or more than one, is ok=false.
func (m *Module) matchMember(t team.Team, target string) (memberRow, bool, error) {
	name, ref, ok := m.parseKillTarget(target)
	if !ok {
		return memberRow{}, false, nil
	}
	hits, err := m.store.MembersOf(t.ID)
	if err == nil && ref != "" {
		hits, err = m.membersByRef(hits, ref)
	}
	if err == nil && name != "" {
		hits, err = m.membersNamed(hits, name)
	}
	if err != nil || len(hits) != 1 {
		return memberRow{}, false, err
	}
	return hits[0], true, nil
}

// parseKillTarget reads "_xxxxxx" and "xxxxxx", and "<host>/" followed by
// either, by "<name>" or by "<name> [xxxxxx]", where host is this host's
// alias or id. ok is false for anything else.
func (m *Module) parseKillTarget(target string) (name, ref string, ok bool) {
	host, sess, qualified := ipeers.SplitAddress(target)
	if !qualified {
		ref = asRef(target)
		return "", ref, ref != ""
	}
	if alias, hostID := m.selfHost(); !ipeers.HostMatches(host, alias, hostID) {
		return "", "", false
	}
	if i := strings.LastIndex(sess, " ["); i > 0 && strings.HasSuffix(sess, "]") {
		name, ref = sess[:i], asRef(sess[i+2:len(sess)-1])
		return name, ref, ref != "" && ipeers.RoutableName(name)
	}
	if ref = asRef(sess); ref != "" {
		return "", ref, true
	}
	return sess, "", ipeers.RoutableName(sess)
}

// asRef is s as a ref ("_xxxxxx"), with its underscore added; "" if it is none.
func asRef(s string) string {
	if !strings.HasPrefix(s, "_") {
		s = "_" + s
	}
	if ipeers.IsRef(s) {
		return s
	}
	return ""
}

// membersByRef are the rows whose current ref is ref, else those whose
// session took over from ref through relays.
func (m *Module) membersByRef(rows []memberRow, ref string) ([]memberRow, error) {
	var hits []memberRow
	for _, r := range rows {
		if r.Ref == ref {
			hits = append(hits, r)
		}
	}
	if len(hits) > 0 {
		return hits, nil
	}
	prev, err := m.store.PreviousRefs()
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if slices.Contains(prev[r.SessionID], ref) {
			hits = append(hits, r)
		}
	}
	return hits, nil
}

// membersNamed are the active rows whose live conversation's address carries
// name: its virtual name (Peer Address v5, peer mailbox spec §3.3), the name
// pdx msg send routes by — not the registry name in Origin.Name, which Claude
// Code changes on every start. Only the address's session part is compared;
// parseKillTarget has already matched its host to this one. A ref-form
// address ("_xxxxxx") can never equal name, which is routable.
func (m *Module) membersNamed(rows []memberRow, name string) ([]memberRow, error) {
	var hits []memberRow
	for _, r := range rows {
		if r.State != team.MemberActive {
			continue
		}
		o, ok, err := m.origins.ResolveOriginBySession(r.SessionID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errRegistry, err)
		}
		if _, sess, split := ipeers.SplitAddress(o.Address); ok && split && sess == name {
			hits = append(hits, r)
		}
	}
	return hits, nil
}
