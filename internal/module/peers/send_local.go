package peers

// The local arm of POST /api/peers/send: steps 7b and 8 of handleSend
// (send.go) for a target that lives on this very host. There is no second
// daemon to ask and no HTTP call to make — the frame goes straight into the
// target's inbox socket.
//
// The split is by transport, not by abstraction: handleSend still holds the
// honest `if isLocal` and the remote arm still sits inline there. What moved
// is only the half that talks to a socket, and it moved WHOLE — the pair
// limit, the frame, the write, the audit result and the response — so the
// one ordering this arm is pinned on (the rate-limited refusal is on the
// audited side of the step-7 boundary) is visible in a single function
// rather than spread across a branch of a much longer one.
//
// Everything up to and including the audit insert stays in handleSend: this
// function is entered only with a row already inserted, which is why it
// takes that row's id rather than an option to skip it.

import (
	"encoding/json"
	"errors"
	"net/http"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
)

// deliverLocal delivers dreq into a Claude Code inbox on this host and
// writes the /send response.
//
// The parameters are what the arm reads, and nothing else:
//
//   - dreq is the same validated DeliverRequest the remote arm posts — msg
//     id, from, to and text — so the two arms are visibly one request over
//     two transports.
//   - auditID is the step-7 row; every outcome below is recorded into it.
//   - mode is the caller's declared mode, which for a local delivery is also
//     the effective one (nothing negotiates it; there is no peer to answer).
//   - originInbox and originAddress are the SENDER's socket and display
//     address: the reply route and the name the receiving agent reads. They
//     are not on the wire types — from.Address is the sender's ref — so they
//     travel separately.
//   - targetInbox is the receiving session's socket, targetHostID keys the
//     pair limit and names the host in the response, toAddress is the
//     normalised address echoed back, and targetAlias appears only in logs.
func (m *Module) deliverLocal(
	w http.ResponseWriter,
	dreq ipeers.DeliverRequest,
	auditID int64,
	mode string,
	originInbox string,
	originAddress string,
	targetInbox string,
	targetHostID string,
	targetAlias string,
	toAddress string,
) {
	msgID := dreq.MsgID

	// 7b. The pair rate limit, which is the ONE /deliver policy that also
	// runs here (spec §4.3). Not because local callers are distrusted — they
	// hold this host's admin token — but because this limit protects the
	// RECEIVING session from being flooded, and that protection is as wanted
	// from next door as from another host. The key is built with the same
	// constructor /deliver uses (deliver.go), and OriginKey carries HostID, so
	// a local pair can never collide with a remote one.
	//
	// AFTER the insert, deliberately, mirroring /deliver: the refusal is on
	// the audited side of the step-7 boundary because it is an attempt this
	// daemon made, unlike the resolution failures above it, which are the
	// caller's address being wrong. That is why this function takes auditID
	// and not a decision about whether to audit.
	//
	// The two policies that are NOT here are decided, not overlooked:
	// dedup keys on a msg id the handler mints per attempt, so it could
	// never fire and a test of it would assert nothing; and the host limit
	// rations an external host's admission to this daemon, which the local
	// admin caller is not (spec §4.3).
	if !m.pairs.Allow(pairKey{From: dreq.From.Key(), To: dreq.To.Key(targetHostID)}) {
		const detail = "pair rate limit exceeded"
		m.setResult(auditID, "", ipeers.ErrRateLimited, detail)
		m.logf("peers: send %s to %q refused (%s): %s", msgID, targetAlias, ipeers.ErrRateLimited, detail)
		writeWireError(w, http.StatusTooManyRequests, ipeers.APIError{Error: ipeers.ErrRateLimited, Detail: detail})
		return
	}

	// 8. The frame goes straight into the target's inbox, with the SENDER's
	// own socket as its reply address — no helper stands in, so a native
	// reply goes back to the session that sent this (spec §4.2, L4).
	//
	// Two different `from`s are set, and they are not the same field:
	// BuildFrame's second argument becomes the NDJSON frame's top-level
	// from, which is what Claude Code replies to, while Wrapper.From is an
	// attribute inside the rendered content, which is what the receiving
	// agent reads. Both are the sender's own inbox.
	//
	// HopChain is "" rather than anything off the request: SendRequest
	// carries no hop chain, and a CLI-initiated send is by definition the
	// first hop.
	//
	// Written under stopCtx, for the same reason the remote call is: a
	// caller that leaves mid-write must not leave a half frame or an
	// unrecorded delivery.
	effective := mode
	line, err := ccuds.BuildFrame(msgID, originInbox, ccuds.Wrapper{
		From:     "uds:" + originInbox,
		FromName: originAddress,
		FromMode: effective,
		HopChain: "",
		Text:     dreq.Text,
	})
	if err != nil {
		// The result column carries the refusal CODE, as /deliver's
		// refuse() writes it (deliver.go): this daemon performed the
		// delivery itself, so it has an outcome of its own to record,
		// unlike the remote arms in send.go, whose result would have come
		// from the peer's answer and a failed call has none.
		m.setResult(auditID, "", ipeers.ErrSocketWriteFailed, err.Error())
		m.logf("peers: send %s to %q: build frame: %v", msgID, targetAlias, err)
		writeWireError(w, http.StatusInternalServerError, ipeers.APIError{Error: ipeers.ErrSocketWriteFailed, Detail: "build frame: " + err.Error()})
		return
	}
	// The same mapping /deliver uses (deliver.go): a clean write is
	// delivered, a write that completed but was never acknowledged is
	// delivery_uncertain, anything else is a failure.
	var result, errText string
	switch err := m.writeFrame(m.stopCtx, targetInbox, line, m.sockWriteTimeout); {
	case err == nil:
		result = ipeers.ResultDelivered
	case errors.Is(err, ccuds.ErrPostWriteTimeout):
		result = ipeers.ResultDeliveryUncertain
		errText = err.Error()
	default:
		// Result is the code, error the text — see the build-frame arm.
		m.setResult(auditID, "", ipeers.ErrSocketWriteFailed, err.Error())
		m.logf("peers: send %s to %q: inbox write failed: %v", msgID, targetAlias, err)
		writeWireError(w, http.StatusBadGateway, ipeers.APIError{Error: ipeers.ErrSocketWriteFailed, Detail: err.Error()})
		return
	}
	m.setResult(auditID, effective, result, errText)
	_ = json.NewEncoder(w).Encode(ipeers.SendResponse{
		MsgID:         msgID,
		ToHostID:      targetHostID,
		ToAddress:     toAddress,
		To:            dreq.To,
		Result:        result,
		EffectiveMode: effective,
		// A local delivery always has a return route: the reply
		// address is a socket in this very filesystem.
		OneWay: false,
	})
}
