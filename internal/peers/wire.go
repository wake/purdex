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
	ModeUnknown   = "unknown" // sender cannot determine caller's mode
)

// Error codes: the "error" field of every 4xx/5xx JSON body on /send,
// /deliver and /log.
const (
	ErrForbidden         = "forbidden" // the principal may not use this route (admin-only routes)
	ErrBadRequest        = "bad_request"
	ErrTextTooLarge      = "text_too_large"
	ErrBadMode           = "bad_mode"
	ErrBadAddress        = "bad_address"
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

	// ErrCodeRemoteTooOld is /send's answer when Resolve came back with
	// ErrRemoteTooOld: the target host still runs a daemon from before
	// Peer Address v4, so its rows carry no ref and no address can be
	// resolved against them. Distinct from peer_not_found because
	// the two ask for opposite things — one says check the address, the
	// other says upgrade the other host — and the refusal is only useful
	// if it says which. (The Go sentinel lives in address.go; this is the
	// wire string, prefixed like ErrCodeTitleInvalid to keep the two
	// apart in one package.)
	ErrCodeRemoteTooOld = "remote_too_old"

	// ErrCodeNameMismatch is /send's answer when Resolve came back with
	// ErrNameMismatch: the combined form's name is not the ref's current
	// name. The detail carries all three values, because distinguishing a
	// peer that renamed itself from an address someone doctored is the
	// operator's call and they cannot make it from the code alone.
	ErrCodeNameMismatch = "name_mismatch"

	// ErrSelfTarget is /send's answer when the address resolved to the
	// sending session itself (spec §4.5). Only local delivery can reach it:
	// a remote target is on another host by construction. Refused rather
	// than delivered because the frame would arrive labelled as being from
	// its own receiver, with that receiver's own socket as the reply
	// address, and a native reply goes back over that socket without
	// touching pdx — so neither HopChain nor the pair limit is in the path
	// of the loop it invites.
	ErrSelfTarget = "self_target"

	// The self routes' refusals: whoami, claim, release. Nothing returns
	// ErrCodeTitleReserved since v4 dropped the reserved words — a title
	// routes nowhere, so it has nothing to shadow.
	ErrCodeTitleInvalid  = "title_invalid"
	ErrCodeTitleReserved = "title_reserved"
	ErrStoreUnavailable  = "store_unavailable"
)

// Warning codes: SelfWarning.Code, the advisory a successful self-route
// answer may carry (Peer Address v3 spec §6.3).
const (
	// WarnTitleInUse: the title was set, and other live sessions hold it
	// too. A warning rather than the refusal v2 gave (`label_taken`, now
	// gone from the vocabulary) because under v3 nothing routes on a
	// title, so nothing needs it to be unique — spec D5/D7. The agent is
	// still asked to add a serial number; SelfWarning.LiveTitles is what
	// lets it pick one without a second round trip.
	WarnTitleInUse = "title_in_use"
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
	Address        string `json:"address,omitempty"`     // "_<ref>" from a v4 sender, which sets no suffix (v4 spec §5.6); "<label>:<suffix>" from a legacy sender; "" from a v1 sender
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
// /api/peers/self/title: the caller's own inbox, attributed to a live,
// non-proxy registry entry (entry attribution, Peer Address v2 spec
// §3.6 — not /send's deliverable-row origin rule).
type SelfRequest struct {
	OriginInbox string `json:"origin_inbox"`
}

// ClaimTitleRequest is the body of PUT /api/peers/self/title: the
// caller's own inbox plus the free-text title it wants to claim.
type ClaimTitleRequest struct {
	OriginInbox string `json:"origin_inbox"`
	Title       string `json:"title"`
}

// SelfResponse is the 200 body of all three self routes: POST
// /api/peers/self, PUT and DELETE /api/peers/self/title (spec §6.3).
//
// The record used to be encoded bare. It moved inside an envelope because
// a 200 now has something to say beyond the record itself — a claim that
// landed on a title someone else holds succeeds *and* warns — and there is
// no room for that beside a bare PeerRecord. All three routes carry the
// envelope, not just the claim: the CLI decodes them through one function
// (doSelfRequest), so one shape is less churn than one exception.
type SelfResponse struct {
	Peer    PeerRecord   `json:"peer"`
	Warning *SelfWarning `json:"warning,omitempty"`
}

// SelfWarning is an advisory on an answer that SUCCEEDED: the route did
// what was asked, and this is what the caller should know about the state
// it landed in. Absent whenever there is nothing to say. WarnTitleInUse is
// the only code today.
type SelfWarning struct {
	Code       string       `json:"code"`
	Detail     string       `json:"detail,omitempty"`
	Holders    []PeerRecord `json:"holders,omitempty"`     // title_in_use: the OTHER live sessions holding the title
	LiveTitles []string     `json:"live_titles,omitempty"` // title_in_use: every title held by a live session (sorted), the caller's own included
}

// APIError is the body of every 4xx/5xx JSON response on /send, /deliver,
// /log and the three self routes (/api/peers/self, /api/peers/self/title).
type APIError struct {
	Error      string               `json:"error"`
	Detail     string               `json:"detail,omitempty"`
	Candidates []AmbiguousCandidate `json:"candidates,omitempty"` // ambiguous: the rows that share the address
	Remote     *RemoteError         `json:"remote,omitempty"`     // remote_error: the other daemon's answer
	Partial    bool                 `json:"partial,omitempty"`    // not_ready from Resolve: the inventory that produced it was partial
}

// AmbiguousCandidate is one of the rows an `ambiguous` refusal could not
// choose between. It exists because a SAFE failure has to be legible as
// one (spec §4.1): the daemon refuses rather than guessing, but if the
// caller only sees the address — which by definition every candidate
// shares — the refusal reads as "my address stopped working" and sends
// the operator after a bug that is not there. The three extra fields are
// what actually tells two live processes of one conversation apart, and
// each is omitempty: a candidate the daemon knows only by address still
// belongs in the list, it just says less.
//
// Ref is the one field that is not merely advisory. Under v4 the rows that
// actually collide are two conversations sharing a registry name, and those
// have IDENTICAL Address and IDENTICAL AgentName — pid and cwd tell them
// apart but are not address forms, so a refusal without the ref names no
// way to reach either. It is still omitempty: Ref is empty on any row with
// no live cc agent (record.go), and such a row still belongs in the list.
type AmbiguousCandidate struct {
	Address   string `json:"address"`
	Ref       string `json:"ref,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
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
// Alias is the effective self alias (config.Config.PeerAlias) and
// AliasSource says where it comes from (self-alias spec S-3, #1196):
// "config" when [peers] alias is set, "host_id" when it is derived from
// host_id. A daemon older than alpha.401 omits AliasSource — clients use
// its absence to detect that a PUT {alias} was ignored (S-5).
type SettingsResponse struct {
	Deliver     bool   `json:"deliver"`
	Alias       string `json:"alias"`
	AliasSource string `json:"alias_source"`
}

// PutSettingsRequest is PUT /api/peers/settings' body. Both fields are
// pointers: absent (nil) means "leave unchanged", present sets the value.
// Alias "" clears [peers] alias back to the host_id default; anything
// else is validated by config.ValidateSelfAlias and stored verbatim
// (self-alias spec S-2, #1196).
type PutSettingsRequest struct {
	Deliver *bool   `json:"deliver"`
	Alias   *string `json:"alias"`
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

// ValidateMode normalises s ("" means unknown) and rejects anything
// other than "", ModePrompting, ModeBypass or ModeUnknown.
// ModeUnknown is used when the sender cannot determine the caller's mode
// (issue #1124, Option 2): the receiver treats unknown as mismatch.
func ValidateMode(s string) (string, error) {
	switch s {
	case "":
		return ModeUnknown, nil
	case ModePrompting, ModeBypass, ModeUnknown:
		return s, nil
	default:
		return "", fmt.Errorf("%w: bad mode %s", ErrModeInvalid, quoteBounded(s))
	}
}

// legacyV3Head matches the 8-digit canonical id v3 used as an address head.
//
// It exists because the peers on the other end of the wire upgrade on their
// own schedule: a v3 daemon still announces an 8-digit head, and a receiver
// that refused it would not be enforcing v4 — it would be dropping real
// traffic from hosts nobody has updated yet. Resolve still refuses such a
// batch (spec §8.3), so accepting the head here only changes which error the
// operator sees.
//
// It is deliberately 8 and only 8, never a 6-8 range: no version of the
// address scheme has ever minted a 7-digit head, so a range would admit a
// format that does not exist — a string nothing can have generated and
// nothing can resolve.
//
// TODO(v5): delete this arm, the v2 arm that IsRef now covers, and their rows
// in TestValidateWireAddress_Matrix once every peer that can reach this daemon
// speaks v4 or later. Both legacy classes are accepted for exactly one release
// so a single upgrade window does not have to carry two incompatibilities at
// once.
var legacyV3Head = regexp.MustCompile(`^_[0-9a-z]{8}$`)

// ValidateWireAddress checks from.address (Peer Address v4 spec §5.6): "" is a
// v1 sender and always passes; otherwise the head (up to the first ':') must be
// a v4 ref (IsRef), the 8-digit canonical id a v3 sender still announces
// (legacyV3Head), or a user label (which a v2 sender may still present as a
// head); and, when a ':' is present at all, the rest must match the suffix wire
// grammar (suffixWirePattern), including an explicitly empty suffix
// ("purdex-tester:"), which is rejected. Reserved heads ("cc", "tmux") never
// pass, via ValidateUserLabel.
//
// IsRef also covers v2's legacy head: v2's default label was "_" plus six
// base36 digits, the same shape a v4 ref has. That is a coincidence of
// format, not of meaning, and it is harmless here because this function
// only checks grammar. Routing tells them apart — a v2 or v3 peer's rows
// carry no ref at all, so Resolve refuses the whole batch (spec §8.3)
// rather than matching one.
func ValidateWireAddress(s string) error {
	if s == "" {
		return nil
	}
	head, rest := SplitSession(s)
	if !IsRef(head) && !legacyV3Head.MatchString(head) {
		if err := ValidateUserLabel(head); err != nil {
			return fmt.Errorf("%w: head: %w", ErrAddressInvalid, err)
		}
	}
	// A v4 sender sets no suffix, but a legacy one does, and a legacy suffix is
	// still validated rather than waved through: relaxing a receiver's grammar
	// while retiring a sender's is how a field stops being checked at all.
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
