package peers

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxTextBytes is the maximum size, in bytes, of a delivered message body.
const MaxTextBytes = 64 * 1024

// MaxLabelBytes bounds the sender-controlled labels that end up in a
// frame's from-name and in a helper's name — WireFrom.SessionName and
// WireFrom.PeerName — and the two identity fields that end up in audit
// rows, helper keys and proxies.json: WireFrom.AgentSessionID and
// WireTo.AgentSessionID (a real one is a 36-byte UUID).
const MaxLabelBytes = 256

// MaxProcStartBytes bounds a proc_start before it is parsed: the layout
// (ProcStartLayout) is 24 bytes; anything past this cap is refused
// without reaching the parser, and a parse failure quotes at most
// MaxQuotedBytes of it.
const MaxProcStartBytes = 64

// MaxHopChainBytes bounds the loop-detection token a relay carries
// through; like the labels it is written back into a frame's wrapper
// attribute, so it is printable UTF-8 too.
const MaxHopChainBytes = 1024

// MaxQuotedBytes bounds a sender-supplied value quoted inside a Validate
// error (msg_id, declared_mode): the receiver echoes that text to the
// peer and into its audit row, so the value is cut here, at the source.
const MaxQuotedBytes = 64

// Wire limits (spec §4): the receiving daemon's in-memory windows and the
// two socket/HTTP timeouts. Named here, next to MaxTextBytes, so every
// constant from the spec lives in one place.
const (
	// DedupWindow is how long a msg_id is refused as a duplicate after
	// first being delivered (in memory only — never the audit table, D10).
	DedupWindow = 10 * time.Minute
	// PairRateLimit is the most deliveries one (sender, receiver) process
	// pair may make within PairRateWindow.
	PairRateLimit = 30
	// PairRateWindow is the sliding window PairRateLimit applies over.
	PairRateWindow = time.Minute
	// HostRateLimit is the most /deliver requests one authenticated peer
	// host may make within HostRateWindow, whatever they carry: the
	// admission check runs before the body is decoded, so a paired host
	// cannot drive inventory builds, audit inserts or dedup scans at HTTP
	// rate with fresh msg_ids and rotating from tuples (which would also
	// sidestep the pair limiter and fill the helper cap). A refusal is
	// unaudited. Sized for PairRateLimit conversations of a few sessions.
	HostRateLimit = 120
	// HostRateWindow is the sliding window HostRateLimit applies over.
	HostRateWindow = time.Minute
	// InterDaemonTimeout bounds one daemon-to-daemon HTTP call (/deliver).
	InterDaemonTimeout = 10 * time.Second
	// SocketWriteTimeout bounds one frame write into a Claude Code inbox
	// socket, including the wait for the peer's EOF.
	SocketWriteTimeout = 5 * time.Second
)

// Validation sentinels: Validate (via ValidateText / ValidateMode) wraps
// one of these so a handler can map the failure to its wire error code
// with errors.Is — see ValidationCode — instead of matching error text.
var (
	// ErrTextInvalid: the text is empty or not valid UTF-8 (bad_request).
	ErrTextInvalid = errors.New("text invalid")
	// ErrTextOversized: the text exceeds MaxTextBytes (text_too_large).
	ErrTextOversized = errors.New("text too large")
	// ErrModeInvalid: the mode is not "", prompting or bypass (bad_mode).
	ErrModeInvalid = errors.New("mode invalid")
	// ErrFieldInvalid: a label (session_name, peer_name, hop_chain) is too
	// long, not valid UTF-8, or carries control characters (bad_request).
	ErrFieldInvalid = errors.New("field invalid")
	// ErrAddressInvalid: from.address does not match the wire grammar
	// (bad_address) — see ValidateWireAddress.
	ErrAddressInvalid = errors.New("address invalid")
)

// validateLabel checks a sender-controlled string: at most max bytes,
// valid UTF-8 and (when printable) free of control characters. "" is
// always fine — every label is optional.
func validateLabel(name, s string, max int, printable bool) error {
	if len(s) > max {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrFieldInvalid, name, max)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrFieldInvalid, name)
	}
	if printable {
		for _, r := range s {
			if unicode.IsControl(r) {
				return fmt.Errorf("%w: %s contains a control character", ErrFieldInvalid, name)
			}
		}
	}
	return nil
}

// validateProcStart checks a sender-supplied proc_start: at most
// MaxProcStartBytes (ErrFieldInvalid, before any parsing), then it must
// parse via ParseProcStart — the failure quotes the value bounded, never
// time.ParseError's own rendering of the raw input.
func validateProcStart(name, s string) error {
	if len(s) > MaxProcStartBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrFieldInvalid, name, MaxProcStartBytes)
	}
	if _, err := ParseProcStart(s); err != nil {
		return fmt.Errorf("%s does not parse: %s", name, quoteBounded(s))
	}
	return nil
}

// quoteBounded renders s for an error message as %q would, but never more
// than MaxQuotedBytes of it: a longer value is cut on a rune boundary and
// marked with "…" inside the quotes. Validate's callers echo these errors
// to the peer and into audit rows, so a sender-supplied value must not
// travel whole.
func quoteBounded(s string) string {
	if len(s) <= MaxQuotedBytes {
		return fmt.Sprintf("%q", s)
	}
	cut := MaxQuotedBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%q", s[:cut]+"…")
}

// ValidationCode maps a Validate error to its wire error code:
// ErrTextOversized ⇒ ErrTextTooLarge, ErrModeInvalid ⇒ ErrBadMode, any
// other non-nil error ⇒ ErrBadRequest, nil ⇒ "".
func ValidationCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrTextOversized):
		return ErrTextTooLarge
	case errors.Is(err, ErrModeInvalid):
		return ErrBadMode
	case errors.Is(err, ErrAddressInvalid):
		return ErrBadAddress
	default:
		return ErrBadRequest
	}
}

// Agent modes: how the receiving Claude Code session should treat a
// delivered message.
const (
	ModePrompting = "prompting"
	ModeBypass    = "bypass"
)

// Error codes: the "error" field of every 4xx/5xx JSON body on /send,
// /deliver and /log.
const (
	ErrForbidden         = "forbidden" // the principal may not use this route (admin-only routes)
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

	// Peer Address v2 self routes (Task 7): whoami, claim, release.
	ErrCodeLabelInvalid  = "label_invalid"
	ErrCodeLabelReserved = "label_reserved"
	ErrLabelTaken        = "label_taken"
	ErrStoreUnavailable  = "store_unavailable"
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
	PeerName       string `json:"peer_name"`             // registry name; may be ""
	SessionName    string `json:"session_name"`          // tmux session name, or "cc:<peer_name>" outside tmux
	DeclaredMode   string `json:"declared_mode"`         // prompting | bypass
	Address        string `json:"address,omitempty"`     // "<label>:<suffix>" (Peer Address v2 spec §3.5); "" from a v1 sender
	AddressRev     int64  `json:"address_rev,omitempty"` // the label row's revision when Address is set
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

// SelfRequest is the body of POST /api/peers/self and DELETE
// /api/peers/self/label: the caller's own inbox, attributed to a live,
// non-proxy registry entry (entry attribution, Peer Address v2 spec
// §3.6 — not /send's deliverable-row origin rule).
type SelfRequest struct {
	OriginInbox string `json:"origin_inbox"`
}

// ClaimLabelRequest is the body of PUT /api/peers/self/label: the
// caller's own inbox plus the user label it wants to claim.
type ClaimLabelRequest struct {
	OriginInbox string `json:"origin_inbox"`
	Label       string `json:"label"`
}

// APIError is the body of every 4xx/5xx JSON response on /send, /deliver,
// /log and the three self routes (/api/peers/self, /api/peers/self/label).
type APIError struct {
	Error      string       `json:"error"`
	Detail     string       `json:"detail,omitempty"`
	Candidates []string     `json:"candidates,omitempty"`  // ambiguous: addresses
	Remote     *RemoteError `json:"remote,omitempty"`      // remote_error: the other daemon's answer
	Partial    bool         `json:"partial,omitempty"`     // not_ready from Resolve: the inventory that produced it was partial
	Holder     *PeerRecord  `json:"holder,omitempty"`      // label_taken: the live session currently holding the label
	LiveLabels []string     `json:"live_labels,omitempty"` // label_taken: every label held by a live session (sorted), including the caller's own
	Skipped    []string     `json:"skipped,omitempty"`     // not_ready from claim: registry files that blocked the completeness proof
}

// RemoteError carries another daemon's answer when a local request fails
// because a remote hop returned an error.
type RemoteError struct {
	Status int    `json:"status"`
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

// LogEntry is one row of GET /api/peers/log: every peer_messages column,
// snake_case, TS as RFC 3339 with milliseconds in UTC (the daemon formats
// it; the CLI parses it). The wire shape is defined here — shared by the
// daemon module and cmd/pdx — not on store.PeerMessage.
type LogEntry struct {
	ID            int64  `json:"id"`
	MsgID         string `json:"msg_id"`
	NativeMsgID   string `json:"native_msg_id"`
	Direction     string `json:"direction"`
	TS            string `json:"ts"`
	FromHostID    string `json:"from_host_id"`
	FromSessionID string `json:"from_session_id"`
	ToHostID      string `json:"to_host_id"`
	ToSessionID   string `json:"to_session_id"`
	DeclaredMode  string `json:"declared_mode"`
	EffectiveMode string `json:"effective_mode"`
	Bytes         int    `json:"bytes"`
	Result        string `json:"result"`
	Error         string `json:"error"`
}

// LogResponse is the body of GET /api/peers/log.
type LogResponse struct {
	Messages []LogEntry `json:"messages"`
}

// SettingsResponse is the body of both GET and PUT /api/peers/settings.
type SettingsResponse struct {
	Deliver bool   `json:"deliver"`
	Alias   string `json:"alias"`
}

// PutSettingsRequest is PUT /api/peers/settings' body. Deliver is a
// pointer: absent (nil) means "leave unchanged", present sets the value.
type PutSettingsRequest struct {
	Deliver *bool `json:"deliver"`
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
		return fmt.Errorf("%w: text is empty", ErrTextInvalid)
	}
	if len(s) > MaxTextBytes {
		return fmt.Errorf("%w: text exceeds %d bytes", ErrTextOversized, MaxTextBytes)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: text is not valid UTF-8", ErrTextInvalid)
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
		return "", fmt.Errorf("%w: bad mode %s", ErrModeInvalid, quoteBounded(s))
	}
}

// canonicalWireHead accepts a v3 id (8) and, for as long as a v2 peer may
// still be sending, a v2 one (6). Never 7: that is not an id either version
// ever minted.
//
// The two widths are spelled out rather than written as a 6–8 range on
// purpose. A range would admit a 7-digit head, and no version of the
// address scheme has ever produced one, so admitting it would be admitting
// a format that does not exist — a string nothing can have generated and
// nothing can resolve. The 6-digit arm is not legacy tolerance for its own
// sake either: a v2 sender on the other end of the wire still announces
// itself with a 6-digit default label, and refusing it would drop real
// traffic from hosts that have not been updated yet.
var canonicalWireHead = regexp.MustCompile(`^_([0-9a-z]{6}|[0-9a-z]{8})$`)

// ValidateWireAddress checks from.address (Peer Address v3 spec §6.2): ""
// is a v1 sender and always passes; otherwise the head (up to the first
// ':') must be a canonical id (canonicalWireHead — 8 digits from a v3
// sender, 6 from a v2 one) or a user label (which a v2 sender may still
// present as a head), and — when a ':' is present at all — the rest must
// match the suffix wire grammar (suffixWirePattern), including an
// explicitly empty suffix ("purdex-tester:"), which is rejected. Reserved
// heads ("cc", "tmux") never pass, via ValidateUserLabel.
func ValidateWireAddress(s string) error {
	if s == "" {
		return nil
	}
	head, rest := SplitSession(s)
	if !canonicalWireHead.MatchString(head) {
		if err := ValidateUserLabel(head); err != nil {
			return fmt.Errorf("%w: head: %w", ErrAddressInvalid, err)
		}
	}
	if strings.Contains(s, ":") && !ValidSuffix(rest) {
		return fmt.Errorf("%w: suffix must match %s", ErrAddressInvalid, suffixWirePattern)
	}
	return nil
}

// Validate checks a DeliverRequest against the wire contract: MsgID must be
// a UUID; the From and To tuples must each be complete (non-empty HostID,
// AgentSessionID non-empty and at most MaxLabelBytes of printable UTF-8,
// PID > 0, ProcStart at most MaxProcStartBytes and parsing via
// ParseProcStart); From.DeclaredMode must be a valid mode;
// From.SessionName and From.PeerName are at most MaxLabelBytes and
// HopChain at most MaxHopChainBytes of printable UTF-8 (validateLabel);
// From.Address must pass ValidateWireAddress and From.AddressRev must be
// >= 0; and Text must pass ValidateText. An error that quotes a
// sender-supplied value (msg_id, declared_mode, proc_start) carries at
// most MaxQuotedBytes of it.
func (r DeliverRequest) Validate() error {
	if !IsUUID(r.MsgID) {
		return fmt.Errorf("msg_id is not a valid UUID: %s", quoteBounded(r.MsgID))
	}

	if r.From.HostID == "" {
		return errors.New("from.host_id is empty")
	}
	if r.From.AgentSessionID == "" {
		return errors.New("from.agent_session_id is empty")
	}
	if err := validateLabel("from.agent_session_id", r.From.AgentSessionID, MaxLabelBytes, true); err != nil {
		return err
	}
	if r.From.PID <= 0 {
		return fmt.Errorf("from.pid must be > 0, got %d", r.From.PID)
	}
	if err := validateProcStart("from.proc_start", r.From.ProcStart); err != nil {
		return err
	}

	if r.To.AgentSessionID == "" {
		return errors.New("to.agent_session_id is empty")
	}
	if err := validateLabel("to.agent_session_id", r.To.AgentSessionID, MaxLabelBytes, true); err != nil {
		return err
	}
	if r.To.PID <= 0 {
		return fmt.Errorf("to.pid must be > 0, got %d", r.To.PID)
	}
	if err := validateProcStart("to.proc_start", r.To.ProcStart); err != nil {
		return err
	}

	if _, err := ValidateMode(r.From.DeclaredMode); err != nil {
		return err
	}

	if err := validateLabel("from.session_name", r.From.SessionName, MaxLabelBytes, true); err != nil {
		return err
	}
	if err := validateLabel("from.peer_name", r.From.PeerName, MaxLabelBytes, true); err != nil {
		return err
	}
	if err := validateLabel("hop_chain", r.HopChain, MaxHopChainBytes, true); err != nil {
		return err
	}

	if err := ValidateWireAddress(r.From.Address); err != nil {
		return err
	}
	if r.From.AddressRev < 0 {
		return errors.New("from.address_rev must be >= 0")
	}

	if err := ValidateText(r.Text); err != nil {
		return err
	}

	return nil
}
