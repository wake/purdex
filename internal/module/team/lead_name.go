package teammod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The lead's name at approval (name pool v2 spec §5.3): the card sends a persona code in Grant.LeadName and the daemon renames the
// session as the approval is made. The order inside decide is the contract of the plan: rename first (a name another session took
// meanwhile is a 409 with the request still open), then the one close that writes the team row, then, if that close failed or was
// lost, the undo of the rename; only a won close records the lead and tells the session its new address.

var (
	errLeadNameNoService = errors.New("team: lead_name needs the name service, which is not registered")
	errLeadNameTaken     = errors.New("team: the lead name is held by another session")
	errLeadNameUnknown   = errors.New("team: the lead name is not a persona code")
)

// leadCodePattern is the shape of a persona code in a decide body; whether it is one is the name service's answer.
var leadCodePattern = regexp.MustCompile(`^[a-z]{3,12}$`)

// leadNameTimeout bounds one call into the name service (each takes its pool lock and writes meta.db once).
const leadNameTimeout = 10 * time.Second

// nameService is the peers module's NameService, looked up when used (the peers module may register after this one); nil when
// there is none.
func (m *Module) nameService() ipeers.NameService {
	svc, ok := m.core.Registry.Get(ipeers.NameServiceKey)
	if !ok {
		return nil
	}
	ns, _ := svc.(ipeers.NameService)
	return ns
}

// leadNamesNow is the list the card reads: an empty (non-nil) list when no persona exists, an error when there is no name
// service or it cannot be read.
func (m *Module) leadNamesNow(sessionID string) ([]team.LeadName, error) {
	ns := m.nameService()
	if ns == nil {
		return nil, errLeadNameNoService
	}
	ctx, cancel := context.WithTimeout(m.stopCtx, leadNameTimeout)
	defer cancel()
	cands, err := ns.LeadCandidates(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]team.LeadName, 0, len(cands))
	for _, c := range cands {
		out = append(out, team.LeadName{Code: c.Code, Name: c.Name, Kind: c.Kind, LastAt: c.LastAt})
	}
	return out, nil
}

// leadNames is leadNamesNow for the places that carry the list along (the request's payload, a name_taken body): nil when it
// cannot be had, so the request is made and the body sent anyway. No name service is not worth a log line; a read error is.
func (m *Module) leadNames(sessionID string) []team.LeadName {
	names, err := m.leadNamesNow(sessionID)
	if err != nil {
		if !errors.Is(err, errLeadNameNoService) {
			m.logf("[team] lead names of %s: %v", sessionID, err)
		}
		return nil
	}
	return names
}

// approveLead is approve's lead branch: under the approval's lock, rename (when the grant names a code), close, and undo the
// rename when the close did not stand. The lock is what keeps two decides with different names from both renaming, and the
// sweeper's close (closeExpired) from landing between the rename and the close.
func (m *Module) approveLead(a team.Approval, c Close) (after team.Approval, won bool, err error) {
	code := c.Grant.LeadName
	var oldName string
	renamed := false
	func() {
		unlock := m.approvalLocks.lock(a.ID)
		defer unlock()
		if code != "" {
			ns := m.nameService()
			if ns == nil {
				err = errLeadNameNoService
				return
			}
			// the state read before the lock may be stale: a closed request is nobody's to rename for
			cur, ok, gerr := m.store.Get(a.ID)
			if gerr != nil {
				err = gerr
				return
			}
			if !ok {
				err = ErrNoSuchApproval
				return
			}
			if cur.State != team.StateOpen {
				after = cur
				return
			}
			ctx, cancel := context.WithTimeout(m.stopCtx, leadNameTimeout)
			old, rerr := ns.RenameForLead(ctx, a.Origin.SessionID, code)
			cancel()
			switch {
			case errors.Is(rerr, ipeers.ErrNameTaken):
				err = errLeadNameTaken
				return
			case errors.Is(rerr, ipeers.ErrNameUnknown):
				err = errLeadNameUnknown
				return
			case rerr != nil:
				err = fmt.Errorf("rename for lead: %w", rerr)
				return
			}
			oldName, renamed = old, true
		}
		t := leadTeamOf(a, *c.Grant, c.DecidedAt)
		after, won, err = m.closeWith(a.ID, func() (team.Approval, bool, error) { return m.store.CloseLeadApproved(a.ID, c, t) })
		if renamed && (err != nil || !won) {
			m.undoLeadRename(a.Origin.SessionID, oldName)
		}
	}()
	if err != nil || !won {
		return after, won, err
	}
	if ns := m.nameService(); ns != nil {
		ctx, cancel := context.WithTimeout(m.stopCtx, leadNameTimeout)
		if rerr := ns.RecordLead(ctx, a.Origin.SessionID); rerr != nil {
			m.logf("[team] approval %s: record lead %s: %v", a.ID, a.Origin.SessionID, rerr)
		}
		cancel()
	}
	if renamed {
		m.noticeLeadRenamed(a.Origin.SessionID, oldName, code)
	}
	return after, won, nil
}

// undoLeadRename gives the old name back. Nothing to undo is a normal answer, and a refused or failed undo changes nothing about
// the decide's own answer: it is only logged.
func (m *Module) undoLeadRename(sessionID, oldName string) {
	ns := m.nameService()
	if ns == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), leadNameTimeout)
	defer cancel()
	if err := ns.UndoRenameForLead(ctx, sessionID, oldName); err != nil {
		m.logf("[team] undo lead rename of %s (to %q): %v", sessionID, oldName, err)
	}
}

// leadRenameNoticeFmt is the same notice the name backfill sends (peers.renameNoticeFmt, not exported there).
const leadRenameNoticeFmt = "[pdx team] 你的地址改成 %s，舊地址 %s 仍可用"

// noticeLeadRenamed tells the renamed session its new address, from its own inbox (SendSelfNotice, the one send that may address
// its origin). It goes to the session by ref, which a rename does not change. A failure is only logged.
func (m *Module) noticeLeadRenamed(sessionID, oldName, code string) {
	newName, ok := leadNewName(oldName, sessionID, code)
	if !ok || newName == oldName || m.selfNotice == nil {
		return
	}
	inbox, ok, err := m.origins.InboxOf(sessionID)
	if err != nil || !ok {
		m.logf("[team] lead rename notice to %s: no live inbox (%v)", sessionID, err)
		return
	}
	alias, _ := m.selfHost()
	ctx, cancel := context.WithTimeout(m.stopCtx, noticeSendTimeout)
	defer cancel()
	req := ipeers.SendRequest{
		To:          alias + "/" + ipeers.RefID(sessionID),
		Text:        fmt.Sprintf(leadRenameNoticeFmt, alias+"/"+newName, alias+"/"+oldName),
		OriginInbox: inbox,
	}
	if _, err := m.selfNotice.SendSelfNotice(ctx, req); err != nil {
		m.logf("[team] lead rename notice to %s: %v", sessionID, err)
	}
}

// leadNewName is the name RenameForLead gives the session: the old name without its suffix (a pool name, or the two ref
// characters), then the code.
func leadNewName(oldName, sessionID, code string) (string, bool) {
	base := oldName
	if cur, ok := ipeers.PoolNameOf(oldName); ok {
		base = strings.TrimSuffix(oldName, "-"+cur)
	} else if ref := ipeers.RefID(sessionID); ipeers.IsRef(ref) {
		base = strings.TrimSuffix(oldName, "-"+ref[1:3])
	}
	return ipeers.VirtualNameWith(base, code)
}

// handleLeadNames is GET /api/team/approvals/{id}/lead-names: the names the request's session could take now, for a card that
// opens long after the request was made.
func (m *Module) handleLeadNames(w http.ResponseWriter, r *http.Request) {
	a, ok, err := m.store.Get(r.PathValue("id"))
	if err != nil {
		m.logf("[team] lead-names %s: %v", r.PathValue("id"), err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	switch {
	case !ok:
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
	case a.Kind != team.KindLead:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "only a lead request has names to pick", nil)
	case a.State != team.StateOpen:
		m.writeErr(w, http.StatusConflict, team.ErrNotOpen, "this request is already closed", nil)
	case m.nameService() == nil:
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "the name service is not available", nil)
	default:
		// a failed read is not an empty list: the card keeps the list it has
		names, err := m.leadNamesNow(a.Origin.SessionID)
		if err != nil {
			m.logf("[team] lead-names %s: %v", a.ID, err)
			m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "the names could not be read; retry", nil)
			return
		}
		m.writeJSON(w, http.StatusOK, team.LeadNamesResponse{LeadNames: names})
	}
}

// writeLeadNameErr answers the decide errors of a lead name; false when err is not one of them.
func (m *Module) writeLeadNameErr(w http.ResponseWriter, a team.Approval, err error) bool {
	switch {
	case errors.Is(err, errLeadNameTaken):
		// the request stays open; the body carries the list now, so the card reopens with it. When it cannot be read the list the
		// request was made with goes instead (stale, but a list: the card never reopens empty-handed).
		names := m.leadNames(a.Origin.SessionID)
		if names == nil {
			var p team.LeadPayload
			if json.Unmarshal(a.Payload, &p) == nil {
				names = p.LeadNames
			}
		}
		m.writeJSON(w, http.StatusConflict, team.APIError{Error: team.ErrNameTaken, Detail: "that name is held by another session; pick another", LeadNames: names})
	case errors.Is(err, errLeadNameUnknown):
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "lead_name is not a name that can be taken", nil)
	case errors.Is(err, errLeadNameNoService):
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "lead_name: this daemon has no name service", nil)
	default:
		return false
	}
	return true
}
