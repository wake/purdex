// internal/module/peers/deliver_local.go
package peers

import (
	"context"
	"errors"
	"net/http"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
)

// localDelivery is one message for one live local Claude Code process, from a sender that is a process on a PEER host
// (the transport-neutral input of steps 7–10 of /deliver, spec §4.3): handleDeliver builds it from an inbound request, and
// the team notice seam (team_notice.go) builds it from a member host's own record of its lead. The caller has already
// authenticated and bound the sender and owns the audit row; this carries only what the target-side steps need.
type localDelivery struct {
	msgID, hopChain, text string
	from                  ipeers.WireFrom // the sender process; HostID/session/pid/proc_start are its origin key
	to                    ipeers.WireTo   // the receiving process
	senderAlias           string          // the sender host's alias in THIS daemon's config (the helper is named under it)
	effective             string          // the wrapper's from-mode (already clamped by the caller)
	oneWay                bool            // no verified route back to the sender (D12)
}

// deliveryRefusal is why steps 7–10 did not deliver: the status, code and wire detail /deliver answers with, and the
// detail the audit row and the log keep. clientGone is the one case with nothing to answer (the caller left while its
// helper was starting).
type deliveryRefusal struct {
	status      int
	code        string
	wireDetail  string
	auditDetail string
	clientGone  bool
}

func refusalOf(status int, code, detail string) *deliveryRefusal {
	return &deliveryRefusal{status: status, code: code, wireDetail: detail, auditDetail: detail}
}

// deliverToLocalTarget is steps 7–10 of /deliver, shared so that the security-relevant part (re-verifying the target
// against this daemon's own inventory, the pair limiter, the sender's helper and its reply socket, the frame, the write)
// has exactly one copy. It returns the result (delivered | delivery_uncertain) and the audit error text, or the refusal.
func (m *Module) deliverToLocalTarget(ctx context.Context, snap deliverSnapshot, d localDelivery) (result, errText string, ref *deliveryRefusal) {
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
	env := m.localEnvelope(ctx, snap.localHostID, snap.localAlias)
	if !env.OK {
		return "", "", &deliveryRefusal{status: http.StatusServiceUnavailable, code: ipeers.ErrNotReady,
			wireDetail: "inventory unavailable", auditDetail: "inventory unavailable: " + env.Error}
	}
	target, detail := findTarget(env.Peers, d.to)
	if detail != "" {
		if len(env.UnknownRegistryFiles) > 0 {
			return "", "", refusalOf(http.StatusServiceUnavailable, ipeers.ErrNotReady, detailInventoryPartial)
		}
		return "", "", refusalOf(http.StatusConflict, ipeers.ErrTargetGone, detail)
	}

	// 8. Per (sender, receiver) process pair rate limit.
	if !m.pairs.Allow(pairKey{From: d.from.Key(), To: d.to.Key(snap.localHostID)}) {
		return "", "", refusalOf(http.StatusTooManyRequests, ipeers.ErrRateLimited, "pair rate limit exceeded")
	}

	// 9. The sender's helper: its socket is the reply address the frame
	// carries. The wait is bounded by the request (and by Stop); the helper
	// itself is owned by the manager and outlives both (B1). Its name is
	// the sender's address under the peer's alias (spec §3.5): a v2 sender
	// names "<alias>/<label>:<suffix>" at its address_rev; a v1 sender
	// (no from.address) names "<alias>/<session_name>" with no revision,
	// so the first v2 request for the same origin renames the instance.
	spawnName, rev := d.senderAlias+"/"+d.from.SessionName, revUnapplied // v1 sender
	if d.from.Address != "" {
		spawnName, rev = d.senderAlias+"/"+d.from.Address, d.from.AddressRev
	}
	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	stopAfter := context.AfterFunc(m.stopCtx, cancelWait)
	defer stopAfter()
	h, err := m.helpers.Acquire(waitCtx, d.from.Key(), spawnName, rev)
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
			return "", "", &deliveryRefusal{status: http.StatusBadGateway, code: ipeers.ErrProxySpawnFailed, wireDetail: spawnDetail, auditDetail: err.Error()}
		case errors.Is(err, ErrProxyLimit):
			return "", "", refusalOf(http.StatusServiceUnavailable, ipeers.ErrProxyLimit, "helper cap reached")
		case errors.Is(err, ErrNotReady) || m.stopCtx.Err() != nil:
			return "", "", refusalOf(http.StatusServiceUnavailable, ipeers.ErrNotReady, "helper manager is not ready")
		case ctx.Err() != nil:
			// The caller is gone; nothing to answer. The helper keeps
			// starting under the manager for the retry.
			return "", "", &deliveryRefusal{clientGone: true}
		default:
			return "", "", &deliveryRefusal{status: http.StatusBadGateway, code: ipeers.ErrProxySpawnFailed, wireDetail: spawnDetail, auditDetail: err.Error()}
		}
	}

	// 10. The wrapper's from-name: for a v2 sender the helper follows the
	// address in place when this request's revision is newer than what the
	// instance carries (an existing instance named by an earlier request,
	// or a v1 spawn); an older revision, or a failed rewrite, keeps the
	// current name — and the delivery goes through either way. The name
	// is never read off the instance directly: ApplyAddress/Name hold
	// the manager lock.
	name := m.helpers.Name(h)
	if d.from.Address != "" {
		name = m.helpers.ApplyAddress(h, spawnName, rev)
	}

	// The frame, written under stopCtx (never the request context: a
	// caller that disconnects mid-write must not leave a half frame).
	line, err := ccuds.BuildFrame(d.msgID, h.sock, ccuds.Wrapper{
		From:     "uds:" + h.sock,
		FromName: name,
		FromMode: d.effective,
		HopChain: d.hopChain,
		Text:     d.text,
	})
	if err != nil {
		return "", "", refusalOf(http.StatusInternalServerError, ipeers.ErrSocketWriteFailed, "build frame: "+err.Error())
	}
	err = m.writeFrame(m.stopCtx, target.Agent.Inbox, line, m.sockWriteTimeout)
	switch {
	case err == nil:
		result = ipeers.ResultDelivered
		if d.oneWay {
			errText = ipeers.ErrNoReturnRoute
		}
	case errors.Is(err, ccuds.ErrPostWriteTimeout):
		// Fully written, but the peer never closed: it may or may not
		// have consumed the frame (spec §4.3).
		result = ipeers.ResultDeliveryUncertain
		errText = err.Error()
	default:
		return "", "", refusalOf(http.StatusBadGateway, ipeers.ErrSocketWriteFailed, err.Error())
	}
	m.helpers.Touch(h.key)
	return result, errText, nil
}
