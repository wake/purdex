package peers

// POST /api/peers/deliver (spec §4.3): the receiving daemon's endpoint. A
// verified peer host delivers one message for one local Claude Code
// session; this daemon binds the caller to its configured identity,
// re-verifies the target against its own inventory, clamps the declared
// mode, records the attempt, acquires the per-origin helper whose socket
// is the reply address, and writes the frame into the target's inbox.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/store"
)

// maxDeliverBodyBytes bounds the request body: MaxTextBytes of text plus
// the envelope, with generous headroom for JSON escaping.
const maxDeliverBodyBytes = 1 << 20

// resultClientGone is the audit result of a delivery whose caller went
// away while it waited for its helper: nothing was written, nothing was
// answered.
const resultClientGone = "client_gone"

// clampMode is the receiver's mode policy: a declared bypass is honoured
// only for a sender whose host entry has AllowBypass; everything else
// (prompting or unknown) is prompting. Unknown is passed through unchanged
// so the receiver can treat it as a mismatch (issue #1124, Option 2).
func clampMode(declared string, allowBypass bool) string {
	if declared == ipeers.ModeBypass && allowBypass {
		return ipeers.ModeBypass
	}
	if declared == ipeers.ModeUnknown {
		return ipeers.ModeUnknown
	}
	return ipeers.ModePrompting
}

// findTarget picks the inventory row the sender named: a cc agent whose
// (session id, pid, proc_start) tuple matches to exactly, and that is
// deliverable. detail (when no row matches) names the field that failed
// on the closest candidate — never another row's data. Since every live,
// non-proxy registry entry has its own entry row (spec §3.4), a target
// that findTarget cannot fully match is a real verdict on its own — the
// only thing that can still hide a live target is an alive-but-undecodable
// registry file, which handleDeliver checks separately via
// env.UnknownRegistryFiles.
func findTarget(records []ipeers.PeerRecord, to ipeers.WireTo) (rec ipeers.PeerRecord, detail string) {
	detail = "no live cc session with that agent_session_id"
	for _, rec := range records {
		a := rec.Agent
		if a == nil || a.Type != "cc" || a.SessionID != to.AgentSessionID {
			continue
		}
		switch {
		case a.PID != to.PID:
			detail = "pid does not match the live session"
		case a.ProcStart != to.ProcStart:
			detail = "proc_start does not match the live session"
		case !rec.Deliverable || a.Inbox == "":
			detail = "session is not deliverable"
			if rec.Reason != "" {
				detail += ": " + rec.Reason
			}
		default:
			return rec, ""
		}
	}
	return ipeers.PeerRecord{}, detail
}

// detailInventoryPartial is the fixed wire detail (and audit error) of a
// 503 not_ready answered because the inventory is partial (spec §4.2):
// the target's session may be one whose owner lookup did not complete.
const detailInventoryPartial = "inventory partial"

// deliverSnapshot is the one config read a delivery makes, under RLock.
type deliverSnapshot struct {
	deliver     bool
	localHostID string
	localAlias  string
	entry       config.PeerHost
	found       bool
}

func (m *Module) snapshotForDeliver(alias string) deliverSnapshot {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	s := deliverSnapshot{
		deliver:     m.core.Cfg.Peers.Deliver,
		localHostID: m.core.Cfg.HostID,
		localAlias:  m.core.Cfg.PeerAlias(),
	}
	if idx := m.core.Cfg.Peers.FindPeerHostByAlias(alias); idx >= 0 {
		s.entry = m.core.Cfg.Peers.Hosts[idx]
		s.found = true
	}
	return s
}

// setResult records the outcome of audit row id; a failure is logged with
// the row id and otherwise ignored — the delivery outcome is already
// decided by the time this runs, and the response must not change.
func (m *Module) setResult(id int64, effectiveMode, result, errText string) {
	if err := m.audit.SetResult(id, effectiveMode, result, errText); err != nil {
		m.logf("peers: deliver: audit row %d: set result %q: %v", id, result, err)
	}
}

// handleDeliver serves POST /api/peers/deliver. The step order is the
// contract (spec §4.3): refusals before the audit insert (steps 1–4:
// principal, entry binding and deliver flag, per-host admission, decode
// and validation, dedup) are unattributable, configuration state, a host
// over its admission limit, malformed, or a retransmit whose first
// attempt already has a row, and are logged rather than audited;
// everything from the insert on is audited.
func (m *Module) handleDeliver(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if m.stopCtx.Err() != nil {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrNotReady, Detail: "daemon is stopping"})
		return
	}

	// 1. The caller must be a verified peer host.
	principal, ok := middleware.PrincipalFrom(r.Context())
	refuseUnaudited := func(status int, code, detail string) {
		m.logf("peers: deliver refused (%s) for host %q: %s", code, principal.Alias, detail)
		writeWireError(w, status, ipeers.APIError{Error: code, Detail: detail})
	}
	switch {
	case ok && principal.Kind == middleware.PrincipalAdmin:
		refuseUnaudited(http.StatusForbidden, ipeers.ErrAdminNotAllowed, "deliver is for peer hosts, not the admin token")
		return
	case !ok || principal.Kind != middleware.PrincipalHost:
		refuseUnaudited(http.StatusForbidden, ipeers.ErrHostUnverified, "no host principal")
		return
	case principal.HostID == "":
		refuseUnaudited(http.StatusForbidden, ipeers.ErrHostUnverified, "host entry is unverified")
		return
	}

	// 2. Bind the principal to the config entry it was authenticated for
	// (M1): the alias must still name the same host_id, or a host entry
	// deleted and recreated for another host between auth and handling
	// would inherit this request. AllowBypass and the return-route token
	// come from this same entry and nowhere else.
	snap := m.snapshotForDeliver(principal.Alias)
	if !snap.found || snap.entry.HostID == "" || snap.entry.HostID != principal.HostID {
		refuseUnaudited(http.StatusForbidden, ipeers.ErrHostUnverified, "host entry no longer matches the authenticated host")
		return
	}
	if !snap.deliver {
		refuseUnaudited(http.StatusForbidden, ipeers.ErrDeliverDisabled, "inbound delivery is disabled on this host")
		return
	}

	// 2b. Per-host admission (ipeers.HostRateLimit), keyed by the
	// authenticated host id and checked BEFORE the body is decoded: from
	// here on every step costs something a peer could otherwise drive at
	// HTTP rate — dedup scans, audit inserts, the inventory build (tmux,
	// registry, ps) and, with rotating from tuples, helper spawns up to
	// the cap. Unaudited: one warn line, nothing of the request in it.
	if !m.hostLimit.Allow(principal.HostID) {
		refuseUnaudited(http.StatusTooManyRequests, ipeers.ErrRateLimited, "host rate limit exceeded")
		return
	}

	// 3. Decode and validate; the sender must claim its own host_id.
	var req ipeers.DeliverRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDeliverBodyBytes)).Decode(&req); err != nil {
		refuseUnaudited(http.StatusBadRequest, ipeers.ErrBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		refuseUnaudited(http.StatusBadRequest, ipeers.ValidationCode(err), err.Error())
		return
	}
	if req.From.HostID != principal.HostID {
		refuseUnaudited(http.StatusForbidden, ipeers.ErrHostUnverified, "from.host_id is not the authenticated host")
		return
	}

	// 4. Retransmit of a message already handled (D5).
	if m.dedup.Seen(req.MsgID) {
		refuseUnaudited(http.StatusConflict, ipeers.ErrDuplicate, "msg_id already delivered")
		return
	}

	// 5. Policy from the bound entry only.
	declared, _ := ipeers.ValidateMode(req.From.DeclaredMode) // validated above; "" ⇒ prompting
	effective := clampMode(declared, snap.entry.AllowBypass)
	oneWay := snap.entry.Token == "" // D12: no verified route back to the sender

	// 6. The audit row: from here on every outcome is recorded (M8).
	if m.audit == nil {
		refuseUnaudited(http.StatusServiceUnavailable, ipeers.ErrAuditUnavailable, "audit store is not available")
		return
	}
	id, err := m.audit.Insert(store.PeerMessage{
		MsgID:         req.MsgID,
		Direction:     store.DirIn,
		TS:            m.now(),
		FromHostID:    req.From.HostID,
		FromSessionID: req.From.AgentSessionID,
		ToHostID:      snap.localHostID,
		ToSessionID:   req.To.AgentSessionID,
		DeclaredMode:  declared,
		EffectiveMode: effective,
		Bytes:         len(req.Text),
	})
	if err != nil {
		m.logf("peers: deliver %s from %q: audit insert: %v", req.MsgID, principal.Alias, err)
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrAuditUnavailable, Detail: "audit insert failed"})
		return
	}
	// refuseWith records auditDetail (local, may name paths and internal
	// errors) and answers the peer with wireDetail; refuse uses one text
	// for both.
	refuseWith := func(status int, code, wireDetail, auditDetail string) {
		m.setResult(id, "", code, auditDetail)
		m.logf("peers: deliver %s from %q refused (%s): %s", req.MsgID, principal.Alias, code, auditDetail)
		writeWireError(w, status, ipeers.APIError{Error: code, Detail: wireDetail})
	}
	refuse := func(status int, code, detail string) { refuseWith(status, code, detail, detail) }

	// 7. Re-verify the target against this daemon's own inventory: the
	// sender's view may be stale, and a session that restarted, whose inbox
	// died, or that is a proxy row is not this target. An inventory that
	// could not be built at all says nothing about the target: it is this
	// daemon's own trouble, answered 503 not_ready (never target_gone,
	// which the origin takes as a verdict and reaps the sender's helper on)
	// with a fixed detail — the error text is local (tmux, registry paths)
	// and stays in the audit row and the log. Peer Address v2 gives every
	// live, non-proxy registry entry its own entry row (spec §3.4)
	// regardless of tmux owner resolution, so a merely PARTIAL inventory
	// (an owner lookup timed out, failed, or never started, or the label
	// store read failed) no longer hides a live target: only an
	// alive-but-undecodable registry file (env.UnknownRegistryFiles,
	// Diagnosis's "unknown" class) can, because that file could be exactly
	// the entry that would have superseded whatever mismatched row
	// findTarget did resolve (a restart racing the registry write). Its
	// mere presence is not_ready, never a verdict, overriding even a
	// genuine candidate row; anything else findTarget reports is a real
	// verdict, target_gone.
	env := m.localEnvelope(r.Context(), snap.localHostID, snap.localAlias)
	if !env.OK {
		refuseWith(http.StatusServiceUnavailable, ipeers.ErrNotReady, "inventory unavailable", "inventory unavailable: "+env.Error)
		return
	}
	target, detail := findTarget(env.Peers, req.To)
	if detail != "" {
		if len(env.UnknownRegistryFiles) > 0 {
			refuse(http.StatusServiceUnavailable, ipeers.ErrNotReady, detailInventoryPartial)
			return
		}
		refuse(http.StatusConflict, ipeers.ErrTargetGone, detail)
		return
	}

	// 8. Per (sender, receiver) process pair rate limit.
	if !m.pairs.Allow(pairKey{From: req.From.Key(), To: req.To.Key(snap.localHostID)}) {
		refuse(http.StatusTooManyRequests, ipeers.ErrRateLimited, "pair rate limit exceeded")
		return
	}

	// 9. The sender's helper: its socket is the reply address the frame
	// carries. The wait is bounded by the request (and by Stop); the helper
	// itself is owned by the manager and outlives both (B1). Its name is
	// the sender's address under the peer's alias (spec §3.5): a v2 sender
	// names "<alias>/<label>:<suffix>" at its address_rev; a v1 sender
	// (no from.address) names "<alias>/<session_name>" with no revision,
	// so the first v2 request for the same origin renames the instance.
	spawnName, rev := principal.Alias+"/"+req.From.SessionName, revUnapplied // v1 sender
	if req.From.Address != "" {
		spawnName, rev = principal.Alias+"/"+req.From.Address, req.From.AddressRev
	}
	waitCtx, cancelWait := context.WithCancel(r.Context())
	defer cancelWait()
	stopAfter := context.AfterFunc(m.stopCtx, cancelWait)
	defer stopAfter()
	h, err := m.helpers.Acquire(waitCtx, req.From.Key(), spawnName, rev)
	if err != nil {
		// The manager's typed errors are classified first, by sentinel:
		// a spawn failure wraps its cause, and that cause must never be
		// mistaken for the caller leaving. Only then is "the caller is
		// gone" decided — by the request's own context, not by the shape
		// of the error — and anything else is a spawn failure. A spawn
		// error names local paths (the registry dir, proxies.json): the
		// peer gets a fixed detail, the audit row and the log keep the
		// cause.
		const spawnDetail = "helper could not be started"
		switch {
		case errors.Is(err, ErrProxySpawnFailed):
			refuseWith(http.StatusBadGateway, ipeers.ErrProxySpawnFailed, spawnDetail, err.Error())
		case errors.Is(err, ErrProxyLimit):
			refuse(http.StatusServiceUnavailable, ipeers.ErrProxyLimit, "helper cap reached")
		case errors.Is(err, ErrNotReady) || m.stopCtx.Err() != nil:
			refuse(http.StatusServiceUnavailable, ipeers.ErrNotReady, "helper manager is not ready")
		case r.Context().Err() != nil:
			// The caller is gone; nothing to answer. The helper keeps
			// starting under the manager for the retry.
			m.setResult(id, "", resultClientGone, "")
			m.logf("peers: deliver %s from %q: caller gone while waiting for its helper", req.MsgID, principal.Alias)
		default:
			refuseWith(http.StatusBadGateway, ipeers.ErrProxySpawnFailed, spawnDetail, err.Error())
		}
		return
	}

	// 10. The wrapper's from-name: for a v2 sender the helper follows the
	// address in place when this request's revision is newer than what the
	// instance carries (an existing instance named by an earlier request,
	// or a v1 spawn); an older revision, or a failed rewrite, keeps the
	// current name — and the delivery goes through either way. The name
	// is never read off the instance directly: ApplyAddress/Name hold
	// the manager lock.
	name := m.helpers.Name(h)
	if req.From.Address != "" {
		name = m.helpers.ApplyAddress(h, spawnName, rev)
	}

	// The frame, written under stopCtx (never the request context: a
	// caller that disconnects mid-write must not leave a half frame).
	line, err := ccuds.BuildFrame(req.MsgID, h.sock, ccuds.Wrapper{
		From:     "uds:" + h.sock,
		FromName: name,
		FromMode: effective,
		HopChain: req.HopChain,
		Text:     req.Text,
	})
	if err != nil {
		refuse(http.StatusInternalServerError, ipeers.ErrSocketWriteFailed, "build frame: "+err.Error())
		return
	}
	err = m.writeFrame(m.stopCtx, target.Agent.Inbox, line, m.sockWriteTimeout)
	var (
		result  string
		errText string
	)
	switch {
	case err == nil:
		result = ipeers.ResultDelivered
		if oneWay {
			errText = ipeers.ErrNoReturnRoute
		}
	case errors.Is(err, ccuds.ErrPostWriteTimeout):
		// Fully written, but the peer never closed: it may or may not
		// have consumed the frame (spec §4.3).
		result = ipeers.ResultDeliveryUncertain
		errText = err.Error()
	default:
		refuse(http.StatusBadGateway, ipeers.ErrSocketWriteFailed, err.Error())
		return
	}
	m.setResult(id, "", result, errText)
	m.helpers.Touch(h.key)
	_ = json.NewEncoder(w).Encode(ipeers.DeliverResponse{
		MsgID:         req.MsgID,
		Result:        result,
		EffectiveMode: effective,
		OneWay:        oneWay,
	})
}
