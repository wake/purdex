package peers

import (
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// MaxTextBytes is the maximum size, in bytes, of a delivered message body.
const MaxTextBytes = 64 * 1024

// Agent modes: how the receiving Claude Code session should treat a
// delivered message.
const (
	ModePrompting = "prompting"
	ModeBypass    = "bypass"
)

// Error codes: the "error" field of every 4xx/5xx JSON body on /send,
// /deliver and /log.
const (
	ErrBadRequest        = "bad_request"
	ErrTextTooLarge      = "text_too_large"
	ErrBadMode           = "bad_mode"
	ErrBadAddress        = "bad_address"
	ErrLocalTarget       = "local_target"
	ErrHostUnknown       = "host_unknown"
	ErrOriginUnknown     = "origin_unknown"
	ErrPeerNotFound      = "peer_not_found"
	ErrAmbiguous         = "ambiguous"
	ErrNotDeliverable    = "not_deliverable"
	ErrRemoteError       = "remote_error"
	ErrHostUnverified    = "host_unverified"
	ErrDeliverDisabled   = "deliver_disabled"
	ErrAdminNotAllowed   = "admin_not_allowed"
	ErrTargetGone        = "target_gone"
	ErrDuplicate         = "duplicate"
	ErrRateLimited       = "rate_limited"
	ErrAuditUnavailable  = "audit_unavailable"
	ErrProxyLimit        = "proxy_limit"
	ErrProxySpawnFailed  = "proxy_spawn_failed"
	ErrSocketWriteFailed = "socket_write_failed"
	ErrNotReady          = "not_ready"
	ErrReplierUnknown    = "replier_unknown"
	ErrProxyToProxy      = "proxy_to_proxy"
	ErrNoReturnRoute     = "no_return_route"
)

// Results: DeliverResponse.Result / SendResponse.Result / the audit result
// column.
const (
	ResultDelivered         = "delivered"
	ResultDeliveryUncertain = "delivery_uncertain"
)

// WireFrom identifies the sending side of a delivery: which host, which
// Claude Code process (the AgentSessionID/PID/ProcStart tuple), and which
// peer/tmux identity it presents as.
type WireFrom struct {
	HostID         string `json:"host_id"`
	AgentSessionID string `json:"agent_session_id"`
	PID            int    `json:"pid"`
	ProcStart      string `json:"proc_start"`
	PeerName       string `json:"peer_name"`     // registry name; may be ""
	SessionName    string `json:"session_name"`  // tmux session name, or "cc:<peer_name>" outside tmux
	DeclaredMode   string `json:"declared_mode"` // prompting | bypass
}

// WireTo identifies the receiving Claude Code process: the
// AgentSessionID/PID/ProcStart tuple, on the host that receives the
// request.
type WireTo struct {
	AgentSessionID string `json:"agent_session_id"`
	PID            int    `json:"pid"`
	ProcStart      string `json:"proc_start"`
}

// DeliverRequest is the body of POST /deliver: one message from a sender to
// one receiving Claude Code process, on the receiving host.
type DeliverRequest struct {
	MsgID    string   `json:"msg_id"`
	HopChain string   `json:"hop_chain,omitempty"`
	From     WireFrom `json:"from"`
	To       WireTo   `json:"to"`
	Text     string   `json:"text"`
}

// DeliverResponse is the body of a successful POST /deliver response.
type DeliverResponse struct {
	MsgID         string `json:"msg_id"`
	Result        string `json:"result"` // delivered | delivery_uncertain
	EffectiveMode string `json:"effective_mode"`
	OneWay        bool   `json:"one_way"` // receiver has no verified outbound route back to the sender (§4.3)
}

// SendRequest is the body of POST /send: the local caller's request to
// deliver text to a peer address, resolved and routed by the daemon.
type SendRequest struct {
	To          string `json:"to"` // "<host>/<session>"
	Text        string `json:"text"`
	Mode        string `json:"mode,omitempty"` // "" ⇒ prompting
	OriginInbox string `json:"origin_inbox"`
}

// SendResponse is the body of a successful POST /send response.
type SendResponse struct {
	MsgID         string `json:"msg_id"`
	ToHostID      string `json:"to_host_id"`
	ToAddress     string `json:"to_address"` // normalised "<alias>/<session>" (Task 5)
	To            WireTo `json:"to"`
	Result        string `json:"result"`
	EffectiveMode string `json:"effective_mode"`
	OneWay        bool   `json:"one_way"`
}

// APIError is the body of every 4xx/5xx JSON response on /send, /deliver
// and /log.
type APIError struct {
	Error      string       `json:"error"`
	Detail     string       `json:"detail,omitempty"`
	Candidates []string     `json:"candidates,omitempty"` // ambiguous: addresses
	Remote     *RemoteError `json:"remote,omitempty"`     // remote_error: the other daemon's answer
}

// RemoteError carries another daemon's answer when a local request fails
// because a remote hop returned an error.
type RemoteError struct {
	Status int    `json:"status"`
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

// OriginKey is the helper key, the proxies.json origin and the audit
// identity of a sender. JSON tags match spec §4.5 exactly.
type OriginKey struct {
	HostID         string `json:"host_id"`
	AgentSessionID string `json:"agent_session_id"`
	PID            int    `json:"pid"`
	ProcStart      string `json:"proc_start"`
}

// Key returns the OriginKey identifying f's process.
func (f WireFrom) Key() OriginKey {
	return OriginKey{
		HostID:         f.HostID,
		AgentSessionID: f.AgentSessionID,
		PID:            f.PID,
		ProcStart:      f.ProcStart,
	}
}

// Key returns the OriginKey identifying t's process, on hostID (WireTo
// itself carries no host, since it always describes the receiving host of
// the request it appears in).
func (t WireTo) Key(hostID string) OriginKey {
	return OriginKey{
		HostID:         hostID,
		AgentSessionID: t.AgentSessionID,
		PID:            t.PID,
		ProcStart:      t.ProcStart,
	}
}

// uuidPattern matches an 8-4-4-4-12 lowercase-hex UUID (36 characters).
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsUUID reports whether s is an 8-4-4-4-12 lowercase-hex UUID.
func IsUUID(s string) bool {
	return uuidPattern.MatchString(s)
}

// ValidateText reports whether s is a valid message body: non-empty, valid
// UTF-8, and at most MaxTextBytes bytes.
func ValidateText(s string) error {
	if s == "" {
		return errors.New("text is empty")
	}
	if len(s) > MaxTextBytes {
		return fmt.Errorf("text exceeds %d bytes", MaxTextBytes)
	}
	if !utf8.ValidString(s) {
		return errors.New("text is not valid UTF-8")
	}
	return nil
}

// ValidateMode normalises s ("" means prompting) and rejects anything
// other than "", ModePrompting or ModeBypass.
func ValidateMode(s string) (string, error) {
	switch s {
	case "":
		return ModePrompting, nil
	case ModePrompting, ModeBypass:
		return s, nil
	default:
		return "", fmt.Errorf("bad mode %q", s)
	}
}

// Validate checks a DeliverRequest against the wire contract: MsgID must be
// a UUID; the From and To tuples must each be complete (non-empty
// HostID/AgentSessionID, PID > 0, ProcStart parses via ParseProcStart);
// From.DeclaredMode must be a valid mode; and Text must pass ValidateText.
func (r DeliverRequest) Validate() error {
	if !IsUUID(r.MsgID) {
		return fmt.Errorf("msg_id is not a valid UUID: %q", r.MsgID)
	}

	if r.From.HostID == "" {
		return errors.New("from.host_id is empty")
	}
	if r.From.AgentSessionID == "" {
		return errors.New("from.agent_session_id is empty")
	}
	if r.From.PID <= 0 {
		return fmt.Errorf("from.pid must be > 0, got %d", r.From.PID)
	}
	if _, err := ParseProcStart(r.From.ProcStart); err != nil {
		return fmt.Errorf("from.proc_start: %w", err)
	}

	if r.To.AgentSessionID == "" {
		return errors.New("to.agent_session_id is empty")
	}
	if r.To.PID <= 0 {
		return fmt.Errorf("to.pid must be > 0, got %d", r.To.PID)
	}
	if _, err := ParseProcStart(r.To.ProcStart); err != nil {
		return fmt.Errorf("to.proc_start: %w", err)
	}

	if _, err := ValidateMode(r.From.DeclaredMode); err != nil {
		return err
	}

	if err := ValidateText(r.Text); err != nil {
		return err
	}

	return nil
}
