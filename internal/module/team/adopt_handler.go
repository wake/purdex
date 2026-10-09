package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// Adopt over HTTP (adopt plan PL-1c): the create route, the decide-time approve and the create-time
// approve of unattended mode. The refusals are the store's (adoptRefusal); the create checks the
// same ones first so a request that cannot be approved is never opened.

// adoptRefusedError is the approve's answer when its re-check refused: the row is already closed
// cancelled with Code (committed and announced), so a caller answers 409 Code and retries nothing.
type adoptRefusedError struct{ Code string }

func (e *adoptRefusedError) Error() string { return "adopt refused: " + e.Code }

// adoptTarget is a parsed `target`: the ref that decides, and whether the address named another host.
type adoptTarget struct {
	Ref       string
	SessionID string // a session id given instead of a ref: the way out of a ref two live sessions share
	Remote    bool
}

// label is the target as the refusal names it.
func (t adoptTarget) label() string {
	if t.SessionID != "" {
		return t.SessionID
	}
	return t.Ref
}

// parseAdoptTarget reads "_xxxxxx", "xxxxxx", "<host>/_xxxxxx", "<host>/xxxxxx" and
// "<host>/<name> [xxxxxx]" (the name is display only), and a session id (UUID) with or without the host. ok is false for anything else — a bare name
// included. A host that is not this one is Remote (the ref is still read).
func (m *Module) parseAdoptTarget(target string) (t adoptTarget, ok bool) {
	target = strings.TrimSpace(target)
	host, sess, qualified := ipeers.SplitAddress(target)
	if !qualified {
		if isSessionID(target) {
			return adoptTarget{SessionID: target}, true
		}
		ref := asRef(target)
		return adoptTarget{Ref: ref}, ref != ""
	}
	alias, hostID := m.selfHost()
	t.Remote = !ipeers.HostMatches(host, alias, hostID)
	if isSessionID(sess) {
		t.SessionID = sess
		return t, true
	}
	if i := strings.LastIndex(sess, " ["); i > 0 && strings.HasSuffix(sess, "]") {
		t.Ref = asRef(sess[i+2 : len(sess)-1])
		return t, t.Ref != "" && ipeers.RoutableName(sess[:i])
	}
	t.Ref = asRef(sess)
	return t, t.Ref != ""
}

// isSessionID reports whether s is a Claude Code session id (a UUID, any version).
func isSessionID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil && len(s) == 36
}

// resolveAdoptTarget is the live conversation the ref names: its current ref, else the one that
// took over from it through relays (the lineage, spec §8.4). found false is no such live
// conversation; peersmod.ErrAmbiguousRef is returned as is; any other error is a registry failure.
func (m *Module) resolveAdoptTarget(t adoptTarget) (o team.Origin, found bool, err error) {
	if t.SessionID != "" {
		return m.origins.ResolveOriginBySession(t.SessionID)
	}
	ref := t.Ref
	o, found, err = m.origins.ResolveOriginByRef(ref)
	if err != nil || found {
		return o, found, err
	}
	prev, err := m.store.PreviousRefs()
	if err != nil {
		return team.Origin{}, false, fmt.Errorf("%w: %v", errStorageRead, err)
	}
	var hit []team.Origin
	for sid, refs := range prev {
		if !slices.Contains(refs, ref) {
			continue
		}
		cur, ok, err := m.origins.ResolveOriginBySession(sid)
		if err != nil {
			return team.Origin{}, false, err
		}
		if ok {
			hit = append(hit, cur)
		}
	}
	switch len(hit) {
	case 0:
		return team.Origin{}, false, nil
	case 1:
		return hit[0], true, nil
	}
	return team.Origin{}, false, peersmod.ErrAmbiguousRef
}

// errStorageRead marks a team.db read failure inside resolveAdoptTarget (500, not a registry 503).
var errStorageRead = errors.New("team.db read failed")

// adoptHashBody is what the idempotency hash covers: the request's own words. The resolved target
// (title, cwd, even its current ref) drifts between a call and its retry, the string does not.
type adoptHashBody struct {
	Target string `json:"target"`
}

// adoptPayloadOf is the payload of an adopt request: the lead's team and the target as resolved.
func adoptPayloadOf(t team.Team, o team.Origin) team.AdoptPayload {
	return team.AdoptPayload{TeamID: t.ID, LeadSessionID: t.LeadSessionID, TargetRef: o.Ref, TargetSessionID: o.SessionID,
		Title: o.Title, TargetName: o.Name, TargetAddress: o.Address, TargetCwd: o.Cwd, TargetTmux: o.Tmux}
}

// adoptMemberRow is the member row an adoption inserts, keyed by the request id. It carries the
// target as the registry shows it now (o) over what the payload recorded at create; the tmux
// session and pane come from "<session>:@<win>.%<pane>", and the tmux id / instance stay empty:
// the member's tmux session is not ours.
func (m *Module) adoptMemberRow(id string, p team.AdoptPayload, o *team.Origin, now int64) memberRow {
	ref, title, cwd, tmux, pid, start := p.TargetRef, p.Title, p.TargetCwd, p.TargetTmux, 0, ""
	if o != nil {
		ref, title, cwd, tmux, pid, start = o.Ref, o.Title, o.Cwd, o.Tmux, o.PID, o.ProcStart
	}
	row := memberRow{SpawnOp: id, TeamID: p.TeamID, HostID: m.hostID(), SessionID: p.TargetSessionID, Ref: ref, Title: title,
		Cwd: cwd, PID: pid, ProcStart: start, State: team.MemberActive, Origin: team.MemberOriginAdopted,
		NoticePending: team.NoticeAdopted, NoticeSince: now, CreatedAt: now, UpdatedAt: now}
	if i := strings.Index(tmux, ":"); i > 0 {
		row.TmuxSession = tmux[:i]
		if j := strings.LastIndex(tmux, "."); j > i {
			row.PaneID = tmux[j+1:]
		}
	}
	return row
}

// handleCreateAdopt is POST /api/team/approvals for kind adopt (req.ID already canonical).
func (m *Module) handleCreateAdopt(w http.ResponseWriter, req team.CreateApprovalRequest) {
	if req.WaitS < 0 {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "wait_s must not be negative", nil)
		return
	}
	target, ok := m.parseAdoptTarget(req.Target)
	if !ok {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "target must be a ref (_xxxxxx) or an address carrying one (<host>/_xxxxxx, <host>/<name> [xxxxxx])", nil)
		return
	}
	origin, found, err := m.origins.ResolveOrigin(req.OriginInbox)
	if err != nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	}
	if !found {
		m.writeErr(w, http.StatusBadRequest, team.ErrOriginUnknown, "origin_inbox is not a live Claude Code session on this host", nil)
		return
	}
	waitS := req.WaitS
	if waitS == 0 {
		waitS = team.DefaultWaitS
	}
	waitS = min(waitS, team.MaxWaitS)
	hashed, _ := json.Marshal(adoptHashBody{Target: strings.TrimSpace(req.Target)})
	hash := requestHash(req.Kind, origin.SessionID, waitS, hashed)

	if m.beforeCreateLock != nil {
		m.beforeCreateLock()
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	failStore := func(err error) {
		m.logf("[team] create %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
	}
	existing, storedHash, err := m.store.getRow(req.ID)
	switch {
	case err == nil:
		if storedHash != hash {
			m.writeErr(w, http.StatusConflict, team.ErrIDConflict, "id already used by a different request", nil)
			return
		}
		m.writeJSON(w, http.StatusOK, existing)
		return
	case !errors.Is(err, ErrNoSuchApproval):
		failStore(err)
		return
	}
	lead, found, err := m.store.LiveTeamByLead(origin.SessionID)
	if err != nil {
		failStore(err)
		return
	}
	if !found {
		m.writeErr(w, http.StatusConflict, team.ErrNotLead, "this session leads no live team", nil)
		return
	}
	if target.Remote {
		m.writeErr(w, http.StatusConflict, team.ErrRemoteUnsupported, "adopt is same-host only in v1", nil)
		return
	}
	tgt, found, err := m.resolveAdoptTarget(target)
	switch {
	case errors.Is(err, peersmod.ErrAmbiguousRef):
		m.writeErr(w, http.StatusConflict, team.ErrAdoptTargetAmbiguous, "two live sessions carry "+target.Ref+"; name the target by its session id", nil)
		return
	case errors.Is(err, errStorageRead):
		failStore(err)
		return
	case err != nil:
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	case !found:
		m.writeErr(w, http.StatusConflict, team.ErrAdoptTargetNotFound, "no live session answers to "+target.label(), nil)
		return
	}
	if tgt.SessionID == origin.SessionID {
		m.writeErr(w, http.StatusConflict, team.ErrAdoptSelf, "a lead cannot adopt itself", nil)
		return
	}
	if _, isLead, err := m.store.LiveTeamByLead(tgt.SessionID); err != nil {
		failStore(err)
		return
	} else if isLead {
		m.writeErr(w, http.StatusConflict, team.ErrAdoptTargetIsLead, "the target leads a live team", nil)
		return
	}
	if _, t, isMember, err := m.store.ActiveMemberInLiveTeam(tgt.SessionID); err != nil {
		failStore(err)
		return
	} else if isMember {
		m.writeErr(w, http.StatusConflict, team.ErrAdoptAlreadyMember, "the target is already a member of team "+t.ID, nil)
		return
	}
	if open, isOpen, err := m.store.OpenAdoptForTarget(tgt.SessionID); err != nil {
		failStore(err)
		return
	} else if isOpen {
		m.writeErr(w, http.StatusConflict, team.ErrRequestOpen, "an adopt request for this session is already open", &open)
		return
	}
	if used, limit, err := m.store.SeatsUsed(lead.ID); err != nil {
		failStore(err)
		return
	} else if used >= limit {
		m.writeErr(w, http.StatusConflict, team.ErrTeamFull, "the team has no free place", nil)
		return
	}
	p := adoptPayloadOf(lead, tgt)
	payload, err := json.Marshal(p)
	if err != nil {
		failStore(err)
		return
	}
	now := m.now()
	row := team.Approval{ID: req.ID, Kind: req.Kind, HostID: m.hostID(), Origin: origin, Payload: payload, State: team.StateOpen,
		CreatedAt: now, DeadlineAt: now + int64(waitS)*1000, LeaseUntil: now + team.LeaseS*1000}
	if m.unattendedOn() { // under createMu: a switch-on's sweep is never behind this read
		m.createApprovedAdopt(w, row, hash, p, tgt)
		return
	}
	stored, _, inserted, err := m.store.Create(row, hash)
	if err != nil {
		failStore(err)
		return
	}
	if !inserted {
		m.logf("[team] create %s: id appeared between check and insert", req.ID)
		m.writeErr(w, http.StatusConflict, team.ErrIDConflict, "id already used by a different request", nil)
		return
	}
	m.logf("[team] approval %s opened: kind=adopt origin=%s (%s) target=%s (%s)", stored.ID, origin.Ref, origin.SessionID, p.TargetRef, p.TargetSessionID)
	m.broadcast("opened", &stored)
	m.writeJSON(w, http.StatusCreated, stored)
}

// createApprovedAdopt is handleCreateAdopt's write while the switch is on: the row, every re-check
// and the member row in one transaction, so the row's first committed state is approved. A refusal
// rolls all of it back and is the create's own 409 (no row). Caller holds createMu.
func (m *Module) createApprovedAdopt(w http.ResponseWriter, row team.Approval, hash string, p team.AdoptPayload, tgt team.Origin) {
	c := daemonClose(row.CreatedAt, nil)
	mem := m.adoptMemberRow(row.ID, p, &tgt, row.CreatedAt)
	chk := adoptCheck{HostID: m.hostID(), TargetLive: true} // resolved a moment ago, under createMu
	after, err := m.store.CreateApproved(row, hash, func(tx *sql.Tx) error {
		n, refused, err := adoptApprovedIn(tx, row.ID, c, p, chk, mem)
		switch {
		case err != nil:
			return err
		case refused != "":
			return &adoptRefusedError{Code: refused}
		case n == 0:
			return fmt.Errorf("adopt %s: the row just inserted was not closed", row.ID)
		}
		return nil
	})
	var refused *adoptRefusedError
	switch {
	case errors.As(err, &refused):
		m.writeErr(w, http.StatusConflict, refused.Code, "the request cannot be approved: "+refused.Code, nil)
	case err != nil:
		m.logf("[team] create %s: %v", row.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
	default:
		m.logf("[team] approval %s approved by unattended at create: kind=adopt origin=%s (%s) target=%s", after.ID, after.Origin.Ref, after.Origin.SessionID, p.TargetRef)
		m.announceClosed(after, nil)
		m.writeJSON(w, http.StatusCreated, after)
	}
}

// approveAdopt is approve()'s adopt branch (a click, the sweep, a tick, boot): the registry is read
// just before the transaction, which re-checks everything again. A refusal closes the row cancelled
// (committed and announced here) and is returned as *adoptRefusedError; a registry failure wraps
// errRegistry and writes nothing.
func (m *Module) approveAdopt(a team.Approval, c Close) (after team.Approval, won bool, err error) {
	p, err := team.AdoptPayloadOf(a)
	if err != nil {
		return team.Approval{}, false, err
	}
	live, ok, err := m.origins.ResolveOriginBySession(p.TargetSessionID)
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("%w: %v", errRegistry, err)
	}
	var o *team.Origin
	if ok {
		o = &live
	}
	chk := adoptCheck{HostID: m.hostID(), TargetLive: ok}
	after, won, refused, err := m.store.CloseAdoptApproved(a.ID, c, p, chk, m.adoptMemberRow(a.ID, p, o, c.DecidedAt))
	if err != nil {
		return team.Approval{}, false, err
	}
	if won {
		m.announceClosed(after, nil)
	}
	if refused != "" && won {
		return after, true, &adoptRefusedError{Code: refused}
	}
	return after, won, nil
}
