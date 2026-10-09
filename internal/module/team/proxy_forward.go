// internal/module/team/proxy_forward.go
package teammod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// The member host's side of the report / task proxy (cross-host team spec §7, plan X6-2). `pdx report` and `pdx task …`
// of a REMOTE member reach this daemon like any member's: with the member's own inbox. Before the local caller resolution
// (which would answer not_member: no local row), the three allowed routes look at who the inbox is: an active remote member
// of this host. If so the call is forwarded to the lead host, synchronously, as POST /api/peers/team/proxy {mk, method,
// path, body}, and the lead host's answer — status and body — is the CLI's answer. The caller is resolved HERE from this
// host's own registry; the lead host is told only the member key.

// ActiveRemoteMemberBySession is the active remote member row of this host that the local session is.
func (s *Store) ActiveRemoteMemberBySession(sessionID string) (remoteMemberRow, bool, error) {
	var r remoteMemberRow
	err := s.db.QueryRow(`SELECT `+remoteMemberCols+` FROM remote_members WHERE member_session_id = ? AND state = ?`, sessionID, remoteActive).Scan(r.dest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return remoteMemberRow{}, false, nil
	}
	if err != nil {
		return remoteMemberRow{}, false, fmt.Errorf("remote member of %s: %w", sessionID, err)
	}
	return r, true, nil
}

// forwardAsRemoteMember answers the request itself and returns true when the inbox is an active remote member of this
// host; false leaves the request to the ordinary handler (any lookup that fails or finds nothing does: the ordinary path
// then gives its own refusals). body is the forwarded JSON body (nil for a GET), which must carry no origin_inbox.
func (m *Module) forwardAsRemoteMember(w http.ResponseWriter, inbox, method, path string, q url.Values, body any, retrySafe bool, resendHint string) bool {
	if m.cmdCaller == nil || inbox == "" {
		return false
	}
	origin, found, err := m.origins.ResolveOrigin(inbox)
	if err != nil || !found {
		return false
	}
	row, ok, err := m.store.ActiveRemoteMemberBySession(origin.SessionID)
	if err != nil {
		m.logf("[team] proxy: %v", err)
		return false
	}
	if !ok {
		return false
	}
	req := team.ProxyRequest{ToHostID: row.LeadHostID, MK: row.MK, Method: method, Path: path}
	if len(q) > 0 {
		q = cloneQuery(q)
		q.Del("origin_inbox")
		if enc := q.Encode(); enc != "" {
			req.Path += "?" + enc
		}
	}
	if body != nil {
		if req.Body, err = json.Marshal(body); err != nil {
			m.writeErr(w, http.StatusInternalServerError, errStorage, err.Error(), nil)
			return true
		}
	}
	ctx, cancel := context.WithTimeout(m.stopCtx, proxyForwardTimeout)
	defer cancel()
	res := m.cmdCaller.Call(ctx, row.LeadHostID, team.ProxyRoute, req)
	if res.Class == peersmod.ClassTransient && res.Status == 0 && retrySafe && ctx.Err() == nil { // Status 0: no HTTP answer at all (a transport failure)
		// The call may have been applied with its answer lost (a lead host that ANSWERED, 429 and 5xx included, is not asked again): the very same request again is safe (a report is idempotent on
		// its id, a list reads), a different one would not be.
		res = m.cmdCaller.Call(ctx, row.LeadHostID, team.ProxyRoute, req)
	}
	if res.Class != peersmod.ClassDone {
		m.logf("[team] proxy %s %s for %s: %s %s %v", method, path, row.MK, res.Class, res.Code, res.Err)
		m.writeProxyFailure(w, res, resendHint)
		return true
	}
	var ans team.ProxyAnswer
	if err := json.Unmarshal(res.Body, &ans); err != nil || ans.Status < 200 || ans.Status > 599 {
		m.writeErr(w, http.StatusBadGateway, team.ErrProxyLeadUnreachable, "the lead host answered something that is not a proxy answer", nil)
		return true
	}
	out := []byte(ans.Body)
	if method == http.MethodPost && path == "/api/team/reports" && (ans.Status == http.StatusCreated || ans.Status == http.StatusOK) {
		out = m.reportAnswerForMember(out, row)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ans.Status)
	_, _ = w.Write(out)
	return true
}

// writeProxyFailure answers a forwarded call that got no proxy answer, keeping what the host caller knows: only a transport
// failure (or a token the lead host does not accept yet) is "retry"; a lead host that is no longer paired, one that does not
// serve the route, or one that refused the call itself are not.
func (m *Module) writeProxyFailure(w http.ResponseWriter, res peersmod.CallResult, resendHint string) {
	switch res.Class {
	case peersmod.ClassUnpaired:
		m.writeErr(w, http.StatusConflict, team.ErrProxyLeadUnpaired, "this host is no longer paired with the lead host", nil)
	case peersmod.ClassWrongHost:
		m.writeErr(w, http.StatusConflict, team.ErrCommandWrongHost, "the lead host's address now belongs to another host", nil)
	case peersmod.ClassUnsupported:
		m.writeErr(w, http.StatusConflict, team.ErrRemoteUnsupported, "the lead host does not support member reports yet; upgrade it", nil)
	case peersmod.ClassRefused:
		m.writeErr(w, http.StatusBadGateway, team.ErrProxyLeadRefused, "the lead host refused the call ("+boundText(res.Code)+")", nil)
	default: // transient, unauthorized (a token it has not learnt yet), anything unexpected
		detail := "the lead host cannot be reached (" + string(res.Class) + "); retry"
		if resendHint != "" {
			detail += "; the call may have been applied — " + resendHint
		}
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrProxyLeadUnreachable, detail, nil)
	}
}

// proxyForwardTimeout bounds one forwarded call (the host caller has its own cap too).
const proxyForwardTimeout = 15 * time.Second

// reportAnswerForMember puts the lead's address AS THIS HOST KNOWS IT into a report answer (spec §7: the up message goes
// to the lead as the member host reaches it, not as the lead host calls itself).
func (m *Module) reportAnswerForMember(body []byte, row remoteMemberRow) []byte {
	var resp team.ReportResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return body
	}
	resp.Lead = team.ReportLead{Ref: row.LeadRef, Address: row.LeadAddress}
	out, err := json.Marshal(resp)
	if err != nil {
		return body
	}
	return out
}

func cloneQuery(q url.Values) url.Values {
	out := make(url.Values, len(q))
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}
