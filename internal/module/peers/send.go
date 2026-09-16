package peers

// POST /api/peers/send (spec §4.4): the sending daemon's endpoint. A local
// admin caller (pdx msg send inside a Claude Code session) names a peer
// address and its own inbox socket; this daemon attributes the origin to a
// live, non-proxy row of its own inventory, fetches the target host's
// inventory with that host's outbound token, resolves the session name to
// one deliverable tuple, records the attempt, and forwards a
// DeliverRequest to the remote daemon. Only the resolved tuple travels;
// the receiver re-verifies it (deliver.go).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// maxSendBodyBytes bounds the /send request body: MaxTextBytes of text
// plus the envelope, with generous headroom for JSON escaping.
const maxSendBodyBytes = 1 << 20

// peerNotFoundHint is appended to the peer_not_found detail for an
// address that matched no row. An unnamed agent's default label is now
// derived from its tmux session name, so every "_xxxxxx" address written
// down before the target host upgraded stopped resolving the moment that
// daemon restarted (default-label spec §4.1) — and a stale hash is the
// likeliest way to land here. Saying so, and naming the one command that
// lists the current addresses, saves the round trip. The wire "error"
// code is unchanged: anything matching on peer_not_found is unaffected.
const peerNotFoundHint = "run `pdx peers --all` for the current addresses — an unnamed session is now addressed by its tmux session name, not by a _xxxxxx label"

// maxDeliverRespBytes caps a remote daemon's /deliver answer: a
// DeliverResponse or an APIError is a few hundred bytes at most, and the
// body is attacker-controlled (any configured peer host).
const maxDeliverRespBytes = 64 << 10

// newDeliverClient returns the *http.Client used for every outbound
// /deliver call: InterDaemonTimeout end to end, and redirects are never
// followed, so the bearer it carries cannot be replayed to another host.
func newDeliverClient() *http.Client {
	return &http.Client{
		Timeout: ipeers.InterDaemonTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// postDeliver POSTs req as JSON to <baseURL>/api/peers/deliver with
// "Authorization: Bearer <bearer>" and reads at most maxDeliverRespBytes of
// the answer. 200 ⇒ (resp, nil, nil). Any other status (every 3xx
// included, since redirects are never followed) ⇒ (zero, &RemoteError{
// Status, Error, Detail}, nil), the code and detail taken from an APIError
// body when one decodes and bounded (boundRemoteText) since they are the
// remote's text, else Error "http_<code>". A transport failure (including
// the client timeout), an oversized body or an undecodable 200 body ⇒
// (zero, nil, err). A 200 body decodes only when it is a DeliverResponse
// this daemon would itself produce: Result ∈ {delivered,
// delivery_uncertain} and EffectiveMode a non-empty valid mode — those
// two fields go into an audit row and a SendResponse verbatim, so
// anything else is a decode error (bounded text), never a response.
func postDeliver(ctx context.Context, client *http.Client, baseURL, bearer string, req ipeers.DeliverRequest) (ipeers.DeliverResponse, *ipeers.RemoteError, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return ipeers.DeliverResponse{}, nil, fmt.Errorf("encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/peers/deliver", bytes.NewReader(body))
	if err != nil {
		return ipeers.DeliverResponse{}, nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+bearer)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return ipeers.DeliverResponse{}, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDeliverRespBytes+1))
	if err != nil {
		return ipeers.DeliverResponse{}, nil, fmt.Errorf("read response body: %w", err)
	}
	if len(raw) > maxDeliverRespBytes {
		return ipeers.DeliverResponse{}, nil, fmt.Errorf("response body exceeds %d bytes", maxDeliverRespBytes)
	}

	if resp.StatusCode != http.StatusOK {
		remote := &ipeers.RemoteError{Status: resp.StatusCode, Error: fmt.Sprintf("http_%d", resp.StatusCode)}
		var ae ipeers.APIError
		if json.Unmarshal(raw, &ae) == nil && ae.Error != "" {
			remote.Error = boundRemoteText(ae.Error)
			remote.Detail = boundRemoteText(ae.Detail)
		}
		return ipeers.DeliverResponse{}, remote, nil
	}

	var out ipeers.DeliverResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return ipeers.DeliverResponse{}, nil, fmt.Errorf("decode response: %w", err)
	}
	if out.Result != ipeers.ResultDelivered && out.Result != ipeers.ResultDeliveryUncertain {
		return ipeers.DeliverResponse{}, nil, fmt.Errorf("decode response: unexpected result %q", boundRemoteText(out.Result))
	}
	if _, err := ipeers.ValidateMode(out.EffectiveMode); err != nil || out.EffectiveMode == "" {
		return ipeers.DeliverResponse{}, nil, fmt.Errorf("decode response: unexpected effective_mode %q", boundRemoteText(out.EffectiveMode))
	}
	return out, nil, nil
}

// findOrigin picks the inventory row the caller presented as its own: a
// deliverable cc agent whose inbox is exactly inbox. Proxy rows (this
// daemon's own helpers, or another daemon's recognised via IsProxy) have
// Agent.Type "proxy" and are never deliverable, so they never qualify.
// candidate reports whether any row carried that inbox at all: false
// means the inventory says nothing about it, which — in a partial
// inventory — is not a verdict (handleSend step 4).
func findOrigin(records []ipeers.PeerRecord, inbox string) (rec ipeers.PeerRecord, ok, candidate bool) {
	for _, rec := range records {
		a := rec.Agent
		if a == nil || a.Inbox != inbox {
			continue
		}
		candidate = true
		if a.Type == "cc" && rec.Deliverable {
			return rec, true, true
		}
	}
	return ipeers.PeerRecord{}, false, candidate
}

// wireFromRecord builds the sender's wire identity from its origin row:
// the v1 fields (the tmux session name when the session is inside tmux,
// "cc:<peer_name>" otherwise — spec §4.4 from-name, still sent for a v1
// receiver) plus the address, "<canonical>:<suffix>", which a v2 receiver
// names the sender's helper after.
//
// AddressRev is 0, always, and is NOT rec.LabelRev (spec §4.4). LabelRev
// still counts how many times this conversation has set its label, but a
// v3 address is derived from the sessionId and cannot move: putting the
// label's revision in the ADDRESS's revision claims a change that never
// happened. The field stays on the wire because v2 senders still populate
// it, and deliver.go's stale-rev guard still protects against a v2 peer's
// address changing — for a v3 origin that path is simply never armed.
func wireFromRecord(hostID string, rec ipeers.PeerRecord, declaredMode string) ipeers.WireFrom {
	sessionName := rec.SessionName
	if sessionName == "" {
		sessionName = "cc:" + rec.Agent.PeerName
	}
	return ipeers.WireFrom{
		HostID:         hostID,
		AgentSessionID: rec.Agent.SessionID,
		PID:            rec.Agent.PID,
		ProcStart:      rec.Agent.ProcStart,
		PeerName:       rec.Agent.PeerName,
		SessionName:    sessionName,
		DeclaredMode:   declaredMode,
		Address:        rec.WireAddress(),
		AddressRev:     0,
	}
}

// handleSend serves POST /api/peers/send. The step order is the contract
// (Task 8): everything before the audit insert (steps 1–6) is a caller
// error, configuration state, or a resolution failure and is logged rather
// than audited; the insert (step 7) precedes the outbound call (step 8),
// whose every outcome is recorded.
func (m *Module) handleSend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 1. Admin only (the auth layer's HostRoutePolicy already refuses a host
	// principal here; enforced again in depth).
	principal, ok := middleware.PrincipalFrom(r.Context())
	if !ok || principal.Kind != middleware.PrincipalAdmin {
		writeWireError(w, http.StatusForbidden, ipeers.APIError{Error: ipeers.ErrForbidden, Detail: "send is admin-only"})
		return
	}
	if m.stopCtx.Err() != nil {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrNotReady, Detail: "daemon is stopping"})
		return
	}
	refuseUnaudited := func(status int, code, detail string) {
		m.logf("peers: send refused (%s): %s", code, detail)
		writeWireError(w, status, ipeers.APIError{Error: code, Detail: detail})
	}

	// 2. Decode and validate.
	var req ipeers.SendRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSendBodyBytes)).Decode(&req); err != nil {
		refuseUnaudited(http.StatusBadRequest, ipeers.ErrBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if err := ipeers.ValidateText(req.Text); err != nil {
		refuseUnaudited(http.StatusBadRequest, ipeers.ValidationCode(err), err.Error())
		return
	}
	mode, err := ipeers.ValidateMode(req.Mode)
	if err != nil {
		refuseUnaudited(http.StatusBadRequest, ipeers.ErrBadMode, err.Error())
		return
	}
	host, session, ok := ipeers.SplitAddress(req.To)
	if !ok {
		refuseUnaudited(http.StatusBadRequest, ipeers.ErrBadAddress, "to must be <host>/<session>")
		return
	}
	if req.OriginInbox == "" {
		refuseUnaudited(http.StatusBadRequest, ipeers.ErrOriginUnknown, "origin_inbox is empty (CLAUDE_CODE_MESSAGING_SOCKET unset?)")
		return
	}

	// 3. The host entry (D1: the local host is never a target).
	snap := m.configSnapshot()
	if ipeers.HostMatches(host, snap.alias, snap.hostID) {
		refuseUnaudited(http.StatusBadRequest, ipeers.ErrLocalTarget, "same-host sessions reach each other natively; the bridge only delivers to other hosts")
		return
	}
	var entry config.PeerHost
	found := false
	for _, h := range snap.hosts {
		if ipeers.HostMatches(host, h.Alias, h.HostID) {
			entry, found = h, true
			break
		}
	}
	if !found {
		refuseUnaudited(http.StatusNotFound, ipeers.ErrHostUnknown, fmt.Sprintf("no peer host %q", host))
		return
	}
	if entry.Token == "" || entry.HostID == "" {
		refuseUnaudited(http.StatusConflict, ipeers.ErrHostUnverified, fmt.Sprintf("host %q has no verified outbound route; run pdx peers host set-token", entry.Alias))
		return
	}

	// 4. The origin: the caller's own session, attributed by its inbox.
	// Peer Address v2 gives every live, non-proxy registry entry its own
	// entry row (spec §3.4) whether or not its tmux session is listed, so
	// a live origin always has SOME row naming its inbox even when its
	// tmux session's owner lookup failed or never ran — the old "no row
	// while partial" premise no longer holds, and a bare label-store
	// failure (spec §3.3's Partial trigger that hides no rows at all) is
	// certainly not this caller's trouble. Only an alive-but-undecodable
	// registry file (Diagnosis.BlockingUnknown) can hide the very row a
	// candidate search would otherwise find — the file that failed to
	// decode could be the origin's own — so that is the one case answered
	// 503 not_ready (retryable), never origin_unknown, which the CLI
	// reports as a verdict.
	local := m.localEnvelope(r.Context(), snap.hostID, snap.alias)
	if !local.OK {
		refuseUnaudited(http.StatusBadRequest, ipeers.ErrOriginUnknown, "local inventory unavailable: "+local.Error)
		return
	}
	origin, ok, candidate := findOrigin(local.Peers, req.OriginInbox)
	if !ok {
		if !candidate && len(local.UnknownRegistryFiles) > 0 {
			refuseUnaudited(http.StatusServiceUnavailable, ipeers.ErrNotReady, "local inventory has an unresolved registry file; retry")
			return
		}
		refuseUnaudited(http.StatusBadRequest, ipeers.ErrOriginUnknown, "origin_inbox is not a live, deliverable Claude Code session on this host")
		return
	}
	from := wireFromRecord(snap.hostID, origin, mode)

	// 5. The remote snapshot, with the entry's token, at the entry's URL.
	// Whatever the rows claim about their host, they are normalised to
	// the entry (P2 #5); the envelope must name the entry's host_id.
	remoteRefused := func(text string) {
		writeWireError(w, http.StatusBadGateway, ipeers.APIError{
			Error:  ipeers.ErrRemoteError,
			Detail: "fetching the peer inventory failed",
			Remote: &ipeers.RemoteError{Status: 0, Error: text},
		})
	}
	fetchCtx, cancelFetch := context.WithTimeout(r.Context(), remoteFetchTimeout)
	env, err := m.fetch(fetchCtx, m.client, entry.URL, entry.Token)
	cancelFetch()
	if err != nil {
		// The error may carry the remote's own text (an HTTP status line
		// is fine; a body-derived decode error is not): bounded everywhere.
		text := boundRemoteText(err.Error())
		m.logf("peers: send to %q: fetch inventory: %s", entry.Alias, text)
		remoteRefused(text)
		return
	}
	if env.HostID != entry.HostID {
		m.logf("peers: send to %q: host_id mismatch: got %s", entry.Alias, boundRemoteText(env.HostID))
		remoteRefused("host_id mismatch: got " + boundRemoteText(env.HostID))
		return
	}
	if !env.OK {
		m.logf("peers: send to %q: peer inventory not ok: %s", entry.Alias, boundRemoteText(env.Error))
		remoteRefused("peer: " + boundRemoteText(env.Error))
		return
	}
	rows := normalizeRemoteRows(env.Peers, entry.Alias, entry.HostID)

	// 6. Resolve the session part over the remote's rows (Peer Address v2
	// spec §3.2). The snapshot flags are the remote envelope's own: Partial
	// as reported, and RegistryIncomplete whenever it named an
	// alive-but-undecodable registry file — the one Partial cause that can
	// hide a whole live process, under which even a single label hit is
	// not_ready (spec §3.2, X1).
	target, err := ipeers.Resolve(rows, session, ipeers.ResolveSnapshot{
		Partial:            env.Partial,
		RegistryIncomplete: len(env.UnknownRegistryFiles) > 0,
	})
	if err != nil {
		var amb *ipeers.AmbiguousError
		switch {
		case errors.As(err, &amb):
			candidates := make([]ipeers.AmbiguousCandidate, 0, len(amb.Candidates))
			for _, c := range amb.Candidates {
				cand := ipeers.AmbiguousCandidate{Address: c.Address, Cwd: c.Cwd}
				// Tier 2 (a bare tmux session name) can match a row with
				// no agent at all, so this is not the tier-1 guarantee.
				if c.Agent != nil {
					cand.AgentName, cand.PID = c.Agent.PeerName, c.Agent.PID
				}
				candidates = append(candidates, cand)
			}
			m.logf("peers: send refused (%s): %q on %q has %d candidates", ipeers.ErrAmbiguous, session, entry.Alias, len(candidates))
			writeWireError(w, http.StatusConflict, ipeers.APIError{Error: ipeers.ErrAmbiguous, Detail: err.Error(), Candidates: candidates})
		case errors.Is(err, ipeers.ErrResolveNotReady):
			detail := fmt.Sprintf("peer inventory on %q is partial; retry, or address the tmux session as tmux:<name>", entry.Alias)
			m.logf("peers: send refused (%s): %s", ipeers.ErrNotReady, detail)
			writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrNotReady, Detail: detail, Partial: true}) // refuseUnaudited cannot set Partial
		case errors.Is(err, ipeers.ErrLegacyCC):
			refuseUnaudited(http.StatusNotFound, ipeers.ErrPeerNotFound, err.Error())
		default:
			refuseUnaudited(http.StatusNotFound, ipeers.ErrPeerNotFound, fmt.Sprintf("no session %q on %q; %s", session, entry.Alias, peerNotFoundHint))
		}
		return
	}
	if !target.Deliverable || target.Agent == nil || target.Agent.Type != "cc" {
		// Reason is the remote's text: bounded before it is echoed or logged.
		reason := boundRemoteText(target.Reason)
		if reason == "" {
			reason = "not a deliverable Claude Code session"
		}
		refuseUnaudited(http.StatusConflict, ipeers.ErrNotDeliverable, reason)
		return
	}
	to := ipeers.WireTo{AgentSessionID: target.Agent.SessionID, PID: target.Agent.PID, ProcStart: target.Agent.ProcStart}
	toAddress := target.Address
	if toAddress == "" { // normalizeRemoteRows blanks an address that does not parse
		toAddress = entry.Alias + "/" + session
	}

	// 7. The outbound request, validated here against the same wire
	// contract the receiver enforces, so a tuple the remote reported in a
	// shape the receiver would refuse (or an origin label over the limit)
	// is a local 400 with nothing recorded and no msg_id sent. The
	// validation error may quote remote text (a proc_start that does not
	// parse), so it is bounded. Then the audit row: a fresh msg_id per
	// attempt (a reused id would be refused as a duplicate by the
	// receiver), recorded before anything leaves this host.
	dreq := ipeers.DeliverRequest{MsgID: m.newMsgID(), From: from, To: to, Text: req.Text}
	if err := dreq.Validate(); err != nil {
		refuseUnaudited(http.StatusBadRequest, ipeers.ValidationCode(err), "outbound request invalid: "+boundRemoteText(err.Error()))
		return
	}
	msgID := dreq.MsgID
	if m.stopCtx.Err() != nil {
		refuseUnaudited(http.StatusServiceUnavailable, ipeers.ErrNotReady, "daemon is stopping")
		return
	}
	if m.audit == nil {
		refuseUnaudited(http.StatusServiceUnavailable, ipeers.ErrAuditUnavailable, "audit store is not available")
		return
	}
	id, err := m.audit.Insert(store.PeerMessage{
		MsgID:         msgID,
		Direction:     store.DirOut,
		TS:            m.now(),
		FromHostID:    snap.hostID,
		FromSessionID: from.AgentSessionID,
		ToHostID:      entry.HostID,
		ToSessionID:   to.AgentSessionID,
		DeclaredMode:  mode,
		Bytes:         len(req.Text),
	})
	if err != nil {
		m.logf("peers: send %s to %q: audit insert: %v", msgID, entry.Alias, err)
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrAuditUnavailable, Detail: "audit insert failed"})
		return
	}

	// 8. The outbound call, under stopCtx rather than the request context:
	// a caller that leaves mid-call must not turn a delivery the receiver
	// already made into an unrecorded one.
	postCtx, cancelPost := context.WithTimeout(m.stopCtx, ipeers.InterDaemonTimeout)
	defer cancelPost()
	resp, remote, err := m.post(postCtx, m.deliverClient, entry.URL, entry.Token, dreq)
	switch {
	case err != nil:
		text := boundRemoteText(err.Error())
		m.setResult(id, "", "", text)
		m.logf("peers: send %s to %q: deliver call failed: %s", msgID, entry.Alias, text)
		writeWireError(w, http.StatusBadGateway, ipeers.APIError{
			Error:  ipeers.ErrRemoteError,
			Detail: "the deliver call to the peer failed",
			Remote: &ipeers.RemoteError{Status: 0, Error: text},
		})
		return
	case remote != nil:
		m.setResult(id, "", remote.Error, remote.Detail)
		m.logf("peers: send %s to %q refused by peer: %d %s: %s", msgID, entry.Alias, remote.Status, remote.Error, remote.Detail)
		writeWireError(w, http.StatusBadGateway, ipeers.APIError{
			Error:  ipeers.ErrRemoteError,
			Detail: "the peer refused the delivery",
			Remote: remote,
		})
		return
	}

	// resp.Result / resp.EffectiveMode are the receiver's text, admitted by
	// postDeliver only as values this daemon itself would produce.
	errText := ""
	if resp.OneWay {
		errText = ipeers.ErrNoReturnRoute
	}
	m.setResult(id, resp.EffectiveMode, resp.Result, errText)
	_ = json.NewEncoder(w).Encode(ipeers.SendResponse{
		MsgID:         msgID,
		ToHostID:      entry.HostID,
		ToAddress:     toAddress,
		To:            to,
		Result:        resp.Result,
		EffectiveMode: resp.EffectiveMode,
		OneWay:        resp.OneWay,
	})
}
