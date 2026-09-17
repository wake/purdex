// cmd/pdx/msg.go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
)

// msgSendTimeout is the client timeout for `pdx msg send`'s POST
// /api/peers/send — longer than peersRequestTimeout because the daemon's
// handler makes its own outbound call to a peer host (up to
// ipeers.InterDaemonTimeout) before answering.
const msgSendTimeout = 15 * time.Second

// msgDefaultLogTail is `pdx msg log`'s default --tail, mirroring
// internal/module/peers.defaultLogTail (cmd/pdx must not import that
// daemon package; the wire types themselves are shared via
// internal/peers).
const msgDefaultLogTail = 50

// msgUsage is the generic grammar-rejection message for `pdx msg`:
// printed to stderr (exit 2) for every malformed invocation except an
// unrecognized flag, which gets its own more specific message (see
// runMsgCmd). `selftest`'s body lives in msg_selftest.go.
//
// The <canonical> line states the v3 address rule to the reader who most
// needs it: someone who has just claimed a label and is about to type that
// label as an address. A label never resolves (spec D3), so the usage text
// offers only the two forms that do — the canonical head and the explicit
// tmux form — and names the two commands that print a live address.
const msgUsage = "usage: pdx msg send [--mode prompting|bypass] [--json] [--config <path>] [--] <host>/<canonical>[:<suffix>] | <host>/tmux:<name> <text>\n" +
	"           (-- ends the options: use it before text that starts with -)\n" +
	"           (<canonical> is a session's permanent address head, derived from its sessionId\n" +
	"            — mini-lab/_3k9f2mq4. A label claimed with `pdx msg name` is never an address:\n" +
	"            it is how you choose a peer, not how you reach one. `pdx msg whoami` prints your\n" +
	"            own address, `pdx peers --all` prints every host's.)\n" +
	"       pdx msg log [--tail N] [--json] [--config <path>]\n" +
	"       pdx msg deliver <on|off|status> [--json] [--config <path>]\n" +
	"       pdx msg selftest [--timeout <dur>] [--config <path>]\n" +
	"       pdx msg name <label> | --release [--json] [--config <path>]\n" +
	"       pdx msg whoami [--json] [--config <path>]"

// runMsg is the `pdx msg` switch target.
func runMsg(args []string) {
	os.Exit(runMsgCmd(args, os.Getenv, os.Stdout, os.Stderr))
}

// runMsgCmd implements the full `pdx msg` grammar and returns the process
// exit code, so tests can drive it without os.Exit. Every grammar
// rejection returns 2 having made no config load or HTTP request. getenv
// is injectable so `send`'s CLAUDE_CODE_MESSAGING_SOCKET lookup can be
// tested without touching the real environment.
func runMsgCmd(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	inv, unknownFlag, ok := parseMsgInvocation(args)
	if !ok {
		if unknownFlag != "" {
			fmt.Fprintf(stderr, "pdx msg: unknown flag %s (put -- before text that starts with -; see usage: [--] <host>/<canonical>[:<suffix>] | <host>/tmux:<name> <text>)\n", unknownFlag)
		} else {
			fmt.Fprintln(stderr, msgUsage)
		}
		return 2
	}

	switch inv.verb {
	case "send":
		return runMsgSend(inv, getenv, stdout, stderr)
	case "log":
		return runMsgLog(inv, stdout, stderr)
	case "deliver":
		return runMsgDeliver(inv, stdout, stderr)
	case "selftest":
		return runMsgSelftestCmd(inv, stdout, stderr)
	case "name":
		return runMsgName(inv, getenv, stdout, stderr)
	case "whoami":
		return runMsgWhoami(inv, getenv, stdout, stderr)
	default:
		// Unreachable: parseMsgInvocation only accepts known verbs.
		fmt.Fprintln(stderr, msgUsage)
		return 2
	}
}

// msgInvocation is the parsed, validated result of parseMsgInvocation.
// Only the fields relevant to inv.verb are meaningful.
type msgInvocation struct {
	cfgPath    string
	jsonOutput bool
	verb       string // send | log | deliver | selftest | name | whoami

	// send
	to   string
	text string
	mode string // "" | ipeers.ModePrompting | ipeers.ModeBypass

	// log
	tail int

	// deliver
	deliverArg string // on | off | status

	// selftest: raw --timeout, parsed by selftestTimeout
	timeout string

	// name
	label   string // the label to claim; "" when release is true
	release bool   // --release: DELETE the label instead of claiming one
}

// parseMsgInvocation parses pdx msg's full grammar in one pass: flags may
// appear anywhere in args, positionals are collected in order, and the
// first positional selects the verb. A bare "--" ends option parsing:
// everything after it is positional, so text that starts with "-" (or
// that spells one of these flags) can be sent. ok is false for any malformed input:
// no verb, an unknown verb, a flag missing its value, a flag not valid for
// the selected verb, wrong positional arity, an invalid --mode/--tail
// value, or an unrecognized deliver argument. unknownFlag is set (and ok
// is false) specifically when an unrecognized flag is seen, so the caller
// can report it by name; every other rejection leaves unknownFlag empty
// and the caller falls back to the generic usage message.
func parseMsgInvocation(args []string) (inv msgInvocation, unknownFlag string, ok bool) {
	inv.tail = msgDefaultLogTail

	var positionals []string
	var hasMode, hasTail, hasTimeout, hasRelease bool
	var modeRaw, tailRaw string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			positionals = append(positionals, args[i+1:]...)
			i = len(args)
		case a == "--config" || a == "-config":
			if i+1 >= len(args) {
				return msgInvocation{}, "", false
			}
			i++
			inv.cfgPath = args[i]
		case a == "--json":
			inv.jsonOutput = true
		case a == "--release":
			hasRelease = true
		case a == "--mode":
			if i+1 >= len(args) {
				return msgInvocation{}, "", false
			}
			i++
			modeRaw = args[i]
			hasMode = true
		case a == "--tail":
			if i+1 >= len(args) {
				return msgInvocation{}, "", false
			}
			i++
			tailRaw = args[i]
			hasTail = true
		case a == "--timeout":
			if i+1 >= len(args) {
				return msgInvocation{}, "", false
			}
			i++
			inv.timeout = args[i]
			hasTimeout = true
		case strings.HasPrefix(a, "-"):
			return msgInvocation{}, a, false
		default:
			positionals = append(positionals, a)
		}
	}

	if len(positionals) == 0 {
		return msgInvocation{}, "", false
	}
	inv.verb = positionals[0]
	rest := positionals[1:]

	switch inv.verb {
	case "send":
		if hasTail || hasTimeout || hasRelease {
			return msgInvocation{}, "", false
		}
		if len(rest) != 2 {
			return msgInvocation{}, "", false
		}
		inv.to, inv.text = rest[0], rest[1]
		if hasMode {
			switch modeRaw {
			case ipeers.ModePrompting, ipeers.ModeBypass:
				inv.mode = modeRaw
			default:
				return msgInvocation{}, "", false
			}
		}

	case "log":
		if hasMode || hasTimeout || hasRelease {
			return msgInvocation{}, "", false
		}
		if len(rest) != 0 {
			return msgInvocation{}, "", false
		}
		if hasTail {
			n, err := strconv.Atoi(tailRaw)
			if err != nil || n < 0 {
				return msgInvocation{}, "", false
			}
			inv.tail = n
		}

	case "deliver":
		if hasMode || hasTail || hasTimeout || hasRelease {
			return msgInvocation{}, "", false
		}
		if len(rest) != 1 {
			return msgInvocation{}, "", false
		}
		switch rest[0] {
		case "on", "off", "status":
			inv.deliverArg = rest[0]
		default:
			return msgInvocation{}, "", false
		}

	case "selftest":
		// --json is deliberately not part of this form.
		if hasMode || hasTail || inv.jsonOutput || hasRelease {
			return msgInvocation{}, "", false
		}
		if len(rest) != 0 {
			return msgInvocation{}, "", false
		}

	case "name":
		if hasMode || hasTail || hasTimeout {
			return msgInvocation{}, "", false
		}
		inv.release = hasRelease
		if hasRelease {
			if len(rest) != 0 {
				return msgInvocation{}, "", false
			}
		} else {
			if len(rest) != 1 {
				return msgInvocation{}, "", false
			}
			inv.label = rest[0]
		}

	case "whoami":
		if hasMode || hasTail || hasTimeout || hasRelease {
			return msgInvocation{}, "", false
		}
		if len(rest) != 0 {
			return msgInvocation{}, "", false
		}

	default:
		return msgInvocation{}, "", false
	}

	return inv, "", true
}

// --- send: POST /api/peers/send --------------------------------------------

// runMsgSend implements `pdx msg send <host>/<session> <text> [--mode ...]
// [--json] [--config <path>]`.
func runMsgSend(inv msgInvocation, getenv func(string) string, stdout, stderr io.Writer) int {
	originInbox := getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if originInbox == "" {
		renderMsgAPIError(ipeers.APIError{
			Error:  ipeers.ErrOriginUnknown,
			Detail: "CLAUDE_CODE_MESSAGING_SOCKET is unset — run inside a Claude Code session",
		}, "", "", stderr)
		return 1
	}

	cfg, err := config.Load(inv.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: %v\n", err)
		return 1
	}

	reqBody, err := json.Marshal(ipeers.SendRequest{
		To:          inv.to,
		Text:        inv.text,
		Mode:        inv.mode,
		OriginInbox: originInbox,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: %v\n", err)
		return 1
	}

	base := fmt.Sprintf("http://%s:%d", cfg.Bind, cfg.Port)
	result, err := doPeersRequest(http.MethodPost, base+"/api/peers/send", reqBody, cfg.Token, msgSendTimeout)
	if err != nil {
		return reportMsgTransportErr(err, stderr)
	}

	if inv.jsonOutput {
		return writeMsgJSONPassthrough(result, stdout)
	}

	host, session, _ := ipeers.SplitAddress(inv.to)

	if result.status != http.StatusOK {
		ae, ok := decodeMsgAPIError(result.body)
		if !ok {
			fmt.Fprintln(stderr, "pdx msg: invalid response")
			return 1
		}
		renderMsgAPIError(ae, host, session, stderr)
		return 1
	}

	var sr ipeers.SendResponse
	if err := json.Unmarshal(result.body, &sr); err != nil {
		fmt.Fprintln(stderr, "pdx msg: invalid response")
		return 1
	}

	line := fmt.Sprintf("sent %s → %s (%s, mode %s",
		sanitizeCell(sr.MsgID), sanitizeCell(sr.ToAddress), sanitizeCell(sr.Result), sanitizeCell(sr.EffectiveMode))
	if sr.OneWay {
		line += ", one-way"
	}
	line += ")"
	fmt.Fprintln(stdout, line)
	return 0
}

// decodeMsgAPIError unmarshals body as an ipeers.APIError, ok false when
// it doesn't decode or carries no error code (not the expected shape).
func decodeMsgAPIError(body []byte) (ipeers.APIError, bool) {
	var ae ipeers.APIError
	if err := json.Unmarshal(body, &ae); err != nil || ae.Error == "" {
		return ipeers.APIError{}, false
	}
	return ae, true
}

// renderMsgAPIError prints one send/log/deliver/name/whoami API error to
// stderr, sanitized: the generic form is "pdx msg: <error>[: <detail>]";
// remote_error additionally names the host part the caller typed and the
// remote's own error/detail; ambiguous prints the session part the caller
// typed followed by one indented line per candidate (see
// msgCandidateLine). Every other error, not_ready included, prints the
// generic line alone: not_ready used to append the registry files an
// inventory could not classify, but no daemon path sets that list any more
// — the claim gate that reported it went with the label_taken refusal.
func renderMsgAPIError(ae ipeers.APIError, host, session string, stderr io.Writer) {
	switch ae.Error {
	case ipeers.ErrRemoteError:
		remoteMsg := ae.Detail
		if ae.Remote != nil {
			remoteMsg = ae.Remote.Error
			if ae.Remote.Detail != "" {
				remoteMsg += ": " + ae.Remote.Detail
			}
		}
		fmt.Fprintf(stderr, "pdx msg: %s: %s\n", sanitizeCell(host), sanitizeCell(remoteMsg))

	case ipeers.ErrAmbiguous:
		fmt.Fprintf(stderr, "pdx msg: ambiguous: %s\n", sanitizeCell(session))
		for _, c := range ae.Candidates {
			fmt.Fprintln(stderr, msgCandidateLine(c))
		}

	default:
		fmt.Fprintln(stderr, msgGenericAPIErrorLine(ae))
	}
}

// msgCandidateLine renders one ambiguity candidate as an indented line:
// the address, then whatever the daemon knew that distinguishes it —
// agent name, pid, cwd. The extras are what make the refusal legible as
// a name collision rather than a broken address (spec §4.1), so they are
// printed whenever present and quietly skipped when the row had none.
func msgCandidateLine(c ipeers.AmbiguousCandidate) string {
	line := "  " + sanitizeCell(c.Address)
	if c.AgentName != "" {
		line += "  agent " + sanitizeCell(c.AgentName)
	}
	if c.PID != 0 {
		line += "  pid " + strconv.Itoa(c.PID)
	}
	if c.Cwd != "" {
		line += "  cwd " + sanitizeCell(c.Cwd)
	}
	return line
}

// msgGenericAPIErrorLine renders the shared "pdx msg: <error>[: <detail>]"
// line used by every error renderMsgAPIError does not give a shape of its
// own.
func msgGenericAPIErrorLine(ae ipeers.APIError) string {
	line := fmt.Sprintf("pdx msg: %s", sanitizeCell(ae.Error))
	if ae.Detail != "" {
		line += ": " + sanitizeCell(ae.Detail)
	}
	return line
}

// reportMsgTransportErr prints a transport-level failure (connection
// error, oversized body) to stderr and returns exit code 1. Mirrors
// reportPeersTransportErr with `pdx msg`'s own prefix — that helper
// hardcodes "pdx peers:", so it is not reused verbatim here.
func reportMsgTransportErr(err error, stderr io.Writer) int {
	if errors.Is(err, errPeersResponseTooLarge) {
		fmt.Fprintln(stderr, "pdx msg: response too large")
		return 1
	}
	fmt.Fprintf(stderr, "pdx msg: %v\n", err)
	return 1
}

// writeMsgJSONPassthrough writes result.body to stdout verbatim,
// unsanitized (machine output, not rendered by a terminal), returning 0
// iff the response was 200.
func writeMsgJSONPassthrough(result peersHTTPResult, stdout io.Writer) int {
	stdout.Write(result.body)
	if result.status == http.StatusOK {
		return 0
	}
	return 1
}

// --- log: GET /api/peers/log ------------------------------------------------
//
// The response shape is ipeers.LogResponse / ipeers.LogEntry, shared with
// the daemon module (cmd/pdx must not import internal/module/peers, which
// would pull a daemon package into the CLI binary; internal/peers is a
// leaf).

// runMsgLog implements `pdx msg log [--tail N] [--json] [--config
// <path>]`.
func runMsgLog(inv msgInvocation, stdout, stderr io.Writer) int {
	cfg, err := config.Load(inv.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: %v\n", err)
		return 1
	}

	reqURL := fmt.Sprintf("http://%s:%d/api/peers/log?tail=%d", cfg.Bind, cfg.Port, inv.tail)
	result, err := doPeersRequest(http.MethodGet, reqURL, nil, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportMsgTransportErr(err, stderr)
	}

	if inv.jsonOutput {
		return writeMsgJSONPassthrough(result, stdout)
	}

	if result.status != http.StatusOK {
		ae, ok := decodeMsgAPIError(result.body)
		if !ok {
			fmt.Fprintln(stderr, "pdx msg: invalid response")
			return 1
		}
		renderMsgAPIError(ae, "", "", stderr)
		return 1
	}

	var lr ipeers.LogResponse
	if err := json.Unmarshal(result.body, &lr); err != nil {
		fmt.Fprintln(stderr, "pdx msg: invalid response")
		return 1
	}

	fmt.Fprint(stdout, formatMsgLogTable(lr.Messages, time.Local))
	return 0
}

// formatMsgLogTable renders entries as a text/tabwriter table with columns
// TIME DIR MSG_ID FROM TO MODE BYTES RESULT ERROR. TIME is entries[i].TS
// (RFC 3339 with milliseconds, UTC) parsed and rendered in loc as
// "15:04:05"; MSG_ID/session IDs in FROM/TO are truncated to their first 8
// characters; MODE is "decl→eff" using each mode's first letter
// (p→p/b→p/b→b).
func formatMsgLogTable(entries []ipeers.LogEntry, loc *time.Location) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tDIR\tMSG_ID\tFROM\tTO\tMODE\tBYTES\tRESULT\tERROR")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			sanitizeCell(formatMsgLogTime(e.TS, loc)),
			sanitizeCell(e.Direction),
			sanitizeCell(msgTruncateID(e.MsgID)),
			sanitizeCell(msgFromToField(e.FromHostID, e.FromSessionID)),
			sanitizeCell(msgFromToField(e.ToHostID, e.ToSessionID)),
			sanitizeCell(msgModeField(e.DeclaredMode, e.EffectiveMode)),
			e.Bytes,
			sanitizeCell(e.Result),
			sanitizeCell(e.Error),
		)
	}
	w.Flush()
	return buf.String()
}

// formatMsgLogTime parses ts (RFC 3339, milliseconds, UTC) and renders it
// in loc as "15:04:05". An unparseable ts is passed through unchanged
// rather than hiding a server/CLI contract mismatch.
func formatMsgLogTime(ts string, loc *time.Location) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	if loc != nil {
		t = t.In(loc)
	}
	return t.Format("15:04:05")
}

// msgTruncateID returns s's first 8 characters, or s unchanged when
// shorter.
func msgTruncateID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// msgFromToField renders one FROM/TO cell: "<host_id>/<session_id[:8]>".
func msgFromToField(hostID, sessionID string) string {
	return hostID + "/" + msgTruncateID(sessionID)
}

// msgModeField renders MODE: "<decl><eff>" joined by "→", each mode
// reduced to its first letter ("-" for an empty mode).
func msgModeField(declared, effective string) string {
	return msgModeLetter(declared) + "→" + msgModeLetter(effective)
}

func msgModeLetter(mode string) string {
	if mode == "" {
		return "-"
	}
	return string(mode[0])
}

// --- deliver: GET/PUT /api/peers/settings -----------------------------------
//
// The bodies are ipeers.SettingsResponse (GET and PUT) and
// ipeers.PutSettingsRequest (PUT), shared with the daemon module.

// runMsgDeliver implements `pdx msg deliver <on|off|status> [--json]
// [--config <path>]`.
func runMsgDeliver(inv msgInvocation, stdout, stderr io.Writer) int {
	cfg, err := config.Load(inv.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: %v\n", err)
		return 1
	}

	reqURL := fmt.Sprintf("http://%s:%d/api/peers/settings", cfg.Bind, cfg.Port)

	var result peersHTTPResult
	if inv.deliverArg == "status" {
		result, err = doPeersRequest(http.MethodGet, reqURL, nil, cfg.Token, peersRequestTimeout)
	} else {
		want := inv.deliverArg == "on"
		payload, merr := json.Marshal(ipeers.PutSettingsRequest{Deliver: &want})
		if merr != nil {
			fmt.Fprintf(stderr, "pdx msg: %v\n", merr)
			return 1
		}
		result, err = doPeersRequest(http.MethodPut, reqURL, payload, cfg.Token, peersRequestTimeout)
	}
	if err != nil {
		return reportMsgTransportErr(err, stderr)
	}

	if inv.jsonOutput {
		return writeMsgJSONPassthrough(result, stdout)
	}

	if result.status != http.StatusOK {
		ae, ok := decodeMsgAPIError(result.body)
		if !ok {
			fmt.Fprintln(stderr, "pdx msg: invalid response")
			return 1
		}
		renderMsgAPIError(ae, "", "", stderr)
		return 1
	}

	var sr ipeers.SettingsResponse
	if err := json.Unmarshal(result.body, &sr); err != nil {
		fmt.Fprintln(stderr, "pdx msg: invalid response")
		return 1
	}

	fmt.Fprintf(stdout, "deliver: %s\n", msgOnOff(sr.Deliver))
	return 0
}

func msgOnOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// --- name: PUT/DELETE /api/peers/self/label ---------------------------------
// --- whoami: POST /api/peers/self --------------------------------------------
//
// Both verbs attribute the caller to a live registry entry via
// CLAUDE_CODE_MESSAGING_SOCKET (Peer Address v2 §3.6 entry attribution,
// not /send's deliverable-row origin rule), exactly like `send`. The
// response shape is ipeers.PeerRecord, shared with the daemon module
// (cmd/pdx must not import internal/module/peers).

// msgOriginInbox reads CLAUDE_CODE_MESSAGING_SOCKET, reporting
// origin_unknown to stderr and returning ok=false when it is unset —
// shared by `name` and `whoami`, mirroring runMsgSend's own check.
func msgOriginInbox(getenv func(string) string, stderr io.Writer) (inbox string, ok bool) {
	inbox = getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if inbox == "" {
		renderMsgAPIError(ipeers.APIError{
			Error:  ipeers.ErrOriginUnknown,
			Detail: "CLAUDE_CODE_MESSAGING_SOCKET is unset — run inside a Claude Code session",
		}, "", "", stderr)
		return "", false
	}
	return inbox, true
}

// renderSelfRecord prints one ipeers.PeerRecord as the block shared by
// `name` and `whoami`'s text output: address, canonical id, label (with
// source and revision), host (with host ID), and — when the caller has a
// live agent — session ID and PID.
//
// The canonical line exists because an agent cannot work its own out: it
// is a hash of a sessionId the agent never handles (spec §4.1), so asking
// is the only way to learn it. It is printed unconditionally rather than
// only when non-empty — a blank canonical on a self route means the caller
// was not attributed to a live cc entry, and hiding the line would hide
// that.
func renderSelfRecord(rec ipeers.PeerRecord, stdout io.Writer) {
	fmt.Fprintf(stdout, "address:    %s\n", sanitizeCell(rec.Address))
	fmt.Fprintf(stdout, "canonical:  %s\n", sanitizeCell(rec.Ref))
	fmt.Fprintf(stdout, "label:      %s (%s, rev %d)\n", sanitizeCell(rec.Label), sanitizeCell(rec.LabelSource), rec.LabelRev)
	fmt.Fprintf(stdout, "host:       %s (%s)\n", sanitizeCell(rec.Host), sanitizeCell(rec.HostID))
	if rec.Agent != nil {
		fmt.Fprintf(stdout, "session:    %s pid %d\n", sanitizeCell(rec.Agent.SessionID), rec.Agent.PID)
	}
}

// renderSelfWarning prints a self-route 200's advisory to stderr, one
// indented line per other holder and per live label. It is a warning, not
// an error: the caller's exit code is unaffected, and the record block
// still goes to stdout — the label really was set. label_in_use's live
// labels are listed because picking the next free serial is what the
// reader is expected to do next (spec §4.2).
func renderSelfWarning(w *ipeers.SelfWarning, stderr io.Writer) {
	if w == nil {
		return
	}
	line := fmt.Sprintf("pdx msg: warning: %s", sanitizeCell(w.Code))
	if w.Detail != "" {
		line += ": " + sanitizeCell(w.Detail)
	}
	fmt.Fprintln(stderr, line)
	for _, h := range w.Holders {
		fmt.Fprintf(stderr, "  also held by: %s\n", sanitizeCell(h.Address))
	}
	for _, l := range w.LiveLabels {
		fmt.Fprintf(stderr, "  live label: %s\n", sanitizeCell(l))
	}
}

// doSelfRequest issues one request on the /api/peers/self* routes and
// handles everything `name` and `whoami` share after that: config.Load and
// the base URL, doPeersRequest and its transport-error reporting, the
// --json passthrough, decoding a non-200 into ipeers.APIError and
// rendering it, and decoding a 200 into the ipeers.SelfResponse envelope —
// whose warning, if any, is rendered here so all three routes report one
// the same way. A 200 that is not envelope-shaped is a pre-v3 daemon and
// is refused, not decoded; see the check itself for why compatibility is
// the wrong answer there. done is true whenever the caller should return exit
// immediately without printing anything more (a --json passthrough, an
// error of any kind); done is false only on a decoded 200, when rec is
// populated and the caller still owes its own success-line output
// (`named:`/`released:` for `name`, nothing for `whoami`) before printing
// the shared renderSelfRecord block.
func doSelfRequest(method, path string, body []byte, inv msgInvocation, stdout, stderr io.Writer) (rec ipeers.PeerRecord, exit int, done bool) {
	cfg, err := config.Load(inv.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: %v\n", err)
		return ipeers.PeerRecord{}, 1, true
	}

	base := fmt.Sprintf("http://%s:%d", cfg.Bind, cfg.Port)
	result, err := doPeersRequest(method, base+path, body, cfg.Token, peersRequestTimeout)
	if err != nil {
		return ipeers.PeerRecord{}, reportMsgTransportErr(err, stderr), true
	}

	if inv.jsonOutput {
		return ipeers.PeerRecord{}, writeMsgJSONPassthrough(result, stdout), true
	}

	if result.status != http.StatusOK {
		ae, ok := decodeMsgAPIError(result.body)
		if !ok {
			fmt.Fprintln(stderr, "pdx msg: invalid response")
			return ipeers.PeerRecord{}, 1, true
		}
		renderMsgAPIError(ae, "", "", stderr)
		return ipeers.PeerRecord{}, 1, true
	}

	// Check the SHAPE before decoding it. A daemon from before v3 answers
	// these routes 200 with a bare PeerRecord, and encoding/json ignores
	// unknown root fields — so that body unmarshals happily into a zero
	// SelfResponse, and this used to print a block of empty strings and
	// exit 0: an agent asking who it is was told, successfully, that it is
	// nobody. The shape is the version: an envelope has "peer".
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(result.body, &shape); err != nil {
		fmt.Fprintln(stderr, "pdx msg: invalid response")
		return ipeers.PeerRecord{}, 1, true
	}
	if _, ok := shape["peer"]; !ok {
		// Deliberately not made compatible with the bare form. A v2
		// record's address head is a LABEL, and everything else in this
		// binary treats a head as a canonical id, so accepting it would
		// hand the caller a v2-semantics address to use as a v3 one — a
		// wrong answer delivered confidently. In practice this is the
		// daemon not having been restarted after pdx was updated, which is
		// what the message says to do.
		fmt.Fprintln(stderr, "pdx msg: the daemon is older than this pdx and answered with a pre-v3 record; restart it (pdx stop && pdx start) so both run the same version")
		return ipeers.PeerRecord{}, 1, true
	}

	var resp ipeers.SelfResponse
	if err := json.Unmarshal(result.body, &resp); err != nil {
		fmt.Fprintln(stderr, "pdx msg: invalid response")
		return ipeers.PeerRecord{}, 1, true
	}
	renderSelfWarning(resp.Warning, stderr)

	return resp.Peer, 0, false
}

// runMsgName implements `pdx msg name <label> | --release [--json]
// [--config <path>]`: PUT /api/peers/self/label to claim inv.label, or
// DELETE /api/peers/self/label (inv.release) to clear the caller's label.
// Neither moves the caller's address, and both success lines say so.
func runMsgName(inv msgInvocation, getenv func(string) string, stdout, stderr io.Writer) int {
	originInbox, ok := msgOriginInbox(getenv, stderr)
	if !ok {
		return 1
	}

	var method string
	var reqBody []byte
	var err error
	if inv.release {
		method = http.MethodDelete
		reqBody, err = json.Marshal(ipeers.SelfRequest{OriginInbox: originInbox})
	} else {
		method = http.MethodPut
		reqBody, err = json.Marshal(ipeers.ClaimLabelRequest{OriginInbox: originInbox, Label: inv.label})
	}
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: %v\n", err)
		return 1
	}

	rec, exit, done := doSelfRequest(method, "/api/peers/self/label", reqBody, inv, stdout, stderr)
	if done {
		return exit
	}

	// Both lines name what changed and, beside it, what did not. Printing
	// the address alone (what this used to do) invited the exact misreading
	// this release is about: an agent that has just claimed a name is the
	// most likely reader to assume the name is now reachable, and the
	// cheapest place to refuse that is the line it reads on success.
	if inv.release {
		fmt.Fprintf(stdout, "released: the label (address unchanged: %s)\n", sanitizeCell(rec.Address))
	} else {
		fmt.Fprintf(stdout, "named: %s (address unchanged: %s)\n", sanitizeCell(rec.Label), sanitizeCell(rec.Address))
	}
	renderSelfRecord(rec, stdout)
	return 0
}

// runMsgWhoami implements `pdx msg whoami [--json] [--config <path>]`: POST
// /api/peers/self to resolve the caller's own peer record.
func runMsgWhoami(inv msgInvocation, getenv func(string) string, stdout, stderr io.Writer) int {
	originInbox, ok := msgOriginInbox(getenv, stderr)
	if !ok {
		return 1
	}

	reqBody, err := json.Marshal(ipeers.SelfRequest{OriginInbox: originInbox})
	if err != nil {
		fmt.Fprintf(stderr, "pdx msg: %v\n", err)
		return 1
	}

	rec, exit, done := doSelfRequest(http.MethodPost, "/api/peers/self", reqBody, inv, stdout, stderr)
	if done {
		return exit
	}

	renderSelfRecord(rec, stdout)
	return 0
}
