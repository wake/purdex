// internal/module/team/adopt_remote.go
package teammod

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The remote adopt on L (cross-host team spec §4.3, plan X3c): `pdx adopt <alias>/_<ref>` takes a session of a PAIRED host
// in. The create resolves the target through the member host's GET /api/peers (3 s) and asks its capabilities (adopt in
// team.kinds, allow_team for us) BEFORE any approval is opened. The approval then means "the user consents": its approve
// transaction writes the `joining` member row and the adopt command together (adoptApprovedIn) and owes no notice from
// here. What the CLI (or the App) waits on is the membership — GET /api/team/adoptions/{approval_id}.

// remoteAdoptTarget is a remote target resolved from the member host's inventory.
type remoteAdoptTarget struct {
	hostID, alias string
	origin        team.Origin
}

// cleanRemoteText is text the member host reported, as the approval card may carry it: control characters dropped, cut to
// 256 bytes on a rune boundary.
func cleanRemoteText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) <= 256 {
		return s
	}
	cut := 256
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}

// resolveRemoteAdopt names the paired host, checks what it announces and finds the target in its inventory. Every refusal
// is a *CapError (the create answers 409, or 503 when the host cannot be asked) or an adopt code in *CapError.Code.
func (m *Module) resolveRemoteAdopt(ctx context.Context, t adoptTarget) (*remoteAdoptTarget, error) {
	if m.cmdCaller == nil || m.peerRecords == nil {
		return nil, &CapError{Code: team.ErrRemoteUnsupported, Detail: "cross-host team is not available on this daemon"}
	}
	hostID := m.cmdCaller.HostIDOf(t.Host)
	if hostID == "" && m.cmdCaller.AliasOf(t.Host) != "" {
		hostID = t.Host // the host part was a host id
	}
	if hostID == "" || !m.cmdCaller.Paired(hostID) {
		return nil, &CapError{Code: team.ErrAdoptTargetNotFound, Detail: "no paired host answers to " + t.Host}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := m.checkRemoteKind(ctx, hostID, CmdAdopt); err != nil {
		return nil, err
	}
	rows, err := m.peerRecords(ctx, hostID)
	if err != nil {
		return nil, &CapError{Code: "remote_unreachable", Detail: err.Error()}
	}
	var hits []ipeers.PeerRecord
	for _, r := range rows {
		a := r.Agent
		// Identity from another host is checked, not trimmed: a session id is a UUID and a ref "_xxxxxx"; anything else is
		// not a session this host can name (and nothing large is stored on a paired host's say-so).
		if r.RowKind != "session" || a == nil || a.Type != "cc" || !isSessionID(a.SessionID) || !ipeers.IsRef(r.Ref) {
			continue
		}
		if (t.SessionID != "" && a.SessionID == t.SessionID) || (t.SessionID == "" && r.Ref == t.Ref) {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 0:
		return nil, &CapError{Code: team.ErrAdoptTargetNotFound, Detail: "no live session on " + t.Host + " answers to " + t.label()}
	case 1:
	default:
		return nil, &CapError{Code: team.ErrAdoptTargetAmbiguous, Detail: "two live sessions on " + t.Host + " carry " + t.label() + "; name the target by its session id"}
	}
	r := hits[0]
	alias := m.remoteAlias(hostID)
	o := team.Origin{SessionID: r.Agent.SessionID, Ref: r.Ref, Name: cleanRemoteText(r.Name), PID: r.Agent.PID, ProcStart: cleanRemoteText(r.Agent.ProcStart),
		Cwd: cleanRemoteText(r.Cwd), Title: cleanRemoteText(r.Title), Address: firstNonEmpty(alias, hostID) + "/" + r.Ref}
	return &remoteAdoptTarget{hostID: hostID, alias: alias, origin: o}, nil
}

// withRemoteHost puts the member host in the payload of a remote adopt.
func withRemoteHost(p team.AdoptPayload, r *remoteAdoptTarget) team.AdoptPayload {
	p.TargetHostID, p.TargetHostAlias = r.hostID, r.alias
	return p
}

// adoptRemoteRow is the `joining` member row a remote adopt's approve inserts, keyed by the request id, which is also
// the adopt command's id and so the member key.
func (m *Module) adoptRemoteRow(id string, p team.AdoptPayload, now int64) memberRow {
	return memberRow{SpawnOp: id, TeamID: p.TeamID, HostID: p.TargetHostID, SessionID: p.TargetSessionID, Ref: p.TargetRef, Title: p.Title,
		Cwd: p.TargetCwd, State: team.MemberJoining, Origin: team.MemberOriginAdopted, CreatedAt: now, UpdatedAt: now}
}

// adoptRemoteCommand is the adopt command of the request id, to be enqueued in the approve's transaction.
func (m *Module) adoptRemoteCommand(id string, p team.AdoptPayload) (*Command, error) {
	t, found, err := m.store.TeamByID(p.TeamID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("adopt: the team is gone")
	}
	cmd, err := remoteCommand(id, CmdAdopt, p.TargetHostID, t, id, m.leadTuple(t), func(tc *team.TeamCommand) {
		tc.TargetSessionID, tc.TargetRef = p.TargetSessionID, p.TargetRef
		m.joinLook(t, tc)
	})
	if err != nil {
		return nil, err
	}
	return &cmd, nil
}

// maxAdoptionWaitS bounds the long-poll of the adoptions route.
const maxAdoptionWaitS = 30

// handleAdoption is GET /api/team/adoptions/{approval_id}[?wait=<s>]: the membership a remote adopt's approval led to.
// 404 for an unknown request or one that is not a remote adopt; 409 not_approved (with the approval's state) while the
// request is still open or was not approved. With wait, a `joining` membership is waited on (at most 30 s).
func (m *Module) handleAdoption(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, ok, err := m.store.Get(id)
	if err != nil {
		m.logf("[team] adoption %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	var p team.AdoptPayload
	if ok {
		p, err = team.AdoptPayloadOf(a)
	}
	if !ok || err != nil || p.TargetHostID == "" {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no remote adopt request with that id", nil)
		return
	}
	if a.State != team.StateApproved {
		m.writeErr(w, http.StatusConflict, "not_approved", "the request is "+string(a.State), nil)
		return
	}
	wait := 0
	if v := r.URL.Query().Get("wait"); v != "" {
		for _, c := range v {
			if c < '0' || c > '9' || wait > maxAdoptionWaitS {
				m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "wait must be a number of seconds", nil)
				return
			}
			wait = wait*10 + int(c-'0')
		}
	}
	deadline := time.Now().Add(time.Duration(min(wait, maxAdoptionWaitS)) * time.Second)
	for {
		ad, err := m.adoptionOf(id)
		if err != nil {
			m.logf("[team] adoption %s: %v", id, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
		if ad.State != team.AdoptionJoining || !time.Now().Before(deadline) || m.stopping() {
			m.writeJSON(w, http.StatusOK, ad)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-m.stopCtx.Done():
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// adoptionOf reads the membership row of request id.
func (m *Module) adoptionOf(id string) (team.Adoption, error) {
	var state, reason string
	err := m.store.db.QueryRow(`SELECT state, end_reason FROM team_members WHERE spawn_op = ?`, id).Scan(&state, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return team.Adoption{ApprovalID: id, State: team.AdoptionFailed, Code: "no_member"}, nil // approved but never written: cannot happen in one transaction
	}
	if err != nil {
		return team.Adoption{}, err
	}
	ad := team.Adoption{ApprovalID: id, State: state}
	if state == team.AdoptionFailed {
		ad.Code = reason
		if reason == "remote_unreachable" { // the 10 minute void of §3.3
			ad.State = team.AdoptionVoid
		}
	}
	return ad, nil
}
