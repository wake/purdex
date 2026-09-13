package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/peers"
)

// maxPeersErrorBodyBytes bounds how much of a non-200 response body is read
// for the error detail printed to stderr — the body could be arbitrarily
// large (a proxy's HTML error page, say), and only a short prefix is useful
// in an error message.
const maxPeersErrorBodyBytes = 4 * 1024

// maxPeersOKBodyBytes bounds how much of a 200 response body is read before
// it is rejected as too large. GET /api/peers' body is a small JSON object
// in normal operation; 16 MiB is generous headroom while still bounding one
// misbehaving or compromised daemon's cost to a fixed amount of memory.
const maxPeersOKBodyBytes = 16 * 1024 * 1024

// errPeersResponseTooLarge is returned by doPeersRequest when a response
// body exceeds the bound for its status class (maxPeersOKBodyBytes for a
// 2xx, maxPeersErrorBodyBytes otherwise).
var errPeersResponseTooLarge = errors.New("response too large")

// peersUsage is the generic grammar-rejection message: printed to stderr
// (exit 2) for every malformed invocation except an unrecognized flag,
// which gets its own more specific message (see runPeersCmd).
const peersUsage = "usage: pdx peers [--json] [--all] [--config <path>]\n" +
	"       pdx peers host add <alias> <url> [--token <t>] [--config <path>]\n" +
	"       pdx peers host set-token <alias> <token> [--allow-bypass=true|false] [--config <path>]\n" +
	"       pdx peers host remove <alias> [--config <path>]\n" +
	"       pdx peers host list [--config <path>]"

// runPeers is the `pdx peers` switch target.
func runPeers(args []string) {
	os.Exit(runPeersCmd(args, os.Stdout, os.Stderr))
}

// runPeersCmd implements the full `pdx peers` grammar — the top-level query
// form (`pdx peers [--json] [--all] [--config <path>]`) and the `host`
// subcommand form (`pdx peers host <add|set-token|remove|list> ...`). It
// does all the work and returns the process exit code, so tests can drive
// it without os.Exit. Every grammar rejection returns 2 having made no
// config load or HTTP request.
func runPeersCmd(args []string, stdout, stderr io.Writer) int {
	inv, unknownFlag, ok := parsePeersInvocation(args)
	if !ok {
		if unknownFlag != "" {
			fmt.Fprintf(stderr, "pdx peers: unknown flag %s\n", unknownFlag)
		} else {
			fmt.Fprintln(stderr, peersUsage)
		}
		return 2
	}

	if inv.hostMode {
		return runPeersHostCmd(inv, stdout, stderr)
	}
	return runPeersQueryCmd(inv, stdout, stderr)
}

// peersInvocation is the parsed, validated result of parsePeersInvocation:
// either the top-level query form (all/jsonOutput/cfgPath, hostMode false)
// or the "host" subcommand form (hostMode true; verb/positionals/token/
// allowBypass/cfgPath), never a mix of both.
type peersInvocation struct {
	cfgPath    string
	jsonOutput bool
	all        bool

	hostMode    bool
	verb        string
	positionals []string
	token       string
	hasToken    bool
	allowBypass *bool
}

// peersHostVerbArity is every known `pdx peers host` verb's exact
// positional-argument count.
var peersHostVerbArity = map[string]int{
	"add":       2,
	"set-token": 2,
	"remove":    1,
	"list":      0,
}

// parsePeersInvocation parses pdx peers' full grammar in one pass: flags
// may appear anywhere in args, positionals are collected in order, and the
// first positional (if any) selects the mode — "host" for the subcommand
// form, anything else is rejected (the query form takes no positionals at
// all).
//
// ok is false for any malformed input: a flag missing its value, a flag
// valid only for the other form, wrong positional arity or count, an
// unknown host verb, or an alias containing "/". unknownFlag is set (and ok
// is false) specifically when an unrecognized flag is seen, so the caller
// can report it by name; every other rejection leaves unknownFlag empty and
// the caller falls back to a generic usage message.
func parsePeersInvocation(args []string) (inv peersInvocation, unknownFlag string, ok bool) {
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-config":
			if i+1 >= len(args) {
				return peersInvocation{}, "", false
			}
			i++
			inv.cfgPath = args[i]
		case a == "--json":
			inv.jsonOutput = true
		case a == "--all":
			inv.all = true
		case a == "--token":
			if i+1 >= len(args) {
				return peersInvocation{}, "", false
			}
			i++
			inv.token = args[i]
			inv.hasToken = true
		case strings.HasPrefix(a, "--allow-bypass="):
			v := strings.TrimPrefix(a, "--allow-bypass=")
			b, valid := parseStrictBool(v)
			if !valid {
				return peersInvocation{}, "", false
			}
			inv.allowBypass = &b
		case strings.HasPrefix(a, "-"):
			return peersInvocation{}, a, false
		default:
			positionals = append(positionals, a)
		}
	}

	if len(positionals) == 0 || positionals[0] != "host" {
		// Top-level query form: no positionals at all, and none of the
		// host-only flags (--token, --allow-bypass).
		if len(positionals) != 0 || inv.hasToken || inv.allowBypass != nil {
			return peersInvocation{}, "", false
		}
		return inv, "", true
	}

	// host subcommand form.
	inv.hostMode = true
	if inv.all || inv.jsonOutput {
		return peersInvocation{}, "", false
	}
	if len(positionals) < 2 {
		return peersInvocation{}, "", false
	}
	inv.verb = positionals[1]
	inv.positionals = positionals[2:]

	arity, known := peersHostVerbArity[inv.verb]
	if !known || len(inv.positionals) != arity {
		return peersInvocation{}, "", false
	}

	switch inv.verb {
	case "add":
		if inv.allowBypass != nil {
			return peersInvocation{}, "", false
		}
	case "set-token":
		if inv.hasToken {
			return peersInvocation{}, "", false
		}
	default: // remove, list
		if inv.hasToken || inv.allowBypass != nil {
			return peersInvocation{}, "", false
		}
	}

	// Every verb with a positional puts the alias first; refuse "/" in it
	// client-side (the server also validates it, but this catches the
	// obviously-wrong case before any request is made).
	if len(inv.positionals) > 0 && strings.Contains(inv.positionals[0], "/") {
		return peersInvocation{}, "", false
	}

	return inv, "", true
}

func parseStrictBool(s string) (bool, bool) {
	switch s {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

// --- top-level query form: GET /api/peers[?scope=all] ---------------------

// runPeersQueryCmd implements `pdx peers [--json] [--all] [--config
// <path>]`.
func runPeersQueryCmd(inv peersInvocation, stdout, stderr io.Writer) int {
	cfg, err := config.Load(inv.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}

	reqURL := fmt.Sprintf("http://%s:%d/api/peers", cfg.Bind, cfg.Port)
	if inv.all {
		reqURL += "?scope=all"
	}

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	// The status code decides how much of the body is worth reading before
	// anything else happens: an error response's body could be arbitrarily
	// large (a proxy's HTML error page, a misbehaving server), so only a
	// bounded prefix is read for the error detail, never the whole thing.
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxPeersErrorBodyBytes))
		detail := strings.TrimSpace(string(errBody))
		if detail == "" {
			detail = "<no body>"
		}
		fmt.Fprintf(stderr, "pdx peers: HTTP %d: %s\n", resp.StatusCode, detail)
		return 1
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPeersOKBodyBytes+1))
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}
	if len(body) > maxPeersOKBodyBytes {
		fmt.Fprintln(stderr, "pdx peers: response too large")
		return 1
	}

	if inv.all {
		return renderPeersAll(body, inv.jsonOutput, stdout, stderr)
	}
	return renderPeersLocal(body, inv.jsonOutput, stdout, stderr)
}

// renderPeersLocal handles a scope-unset GET /api/peers body: JSON
// passthrough or the single-host table, exit 0 iff peersResp.OK.
func renderPeersLocal(body []byte, jsonOutput bool, stdout, stderr io.Writer) int {
	var peersResp peers.Envelope
	if err := json.Unmarshal(body, &peersResp); err != nil {
		fmt.Fprintf(stderr, "pdx peers: invalid response\n")
		return 1
	}

	if jsonOutput {
		fmt.Fprint(stdout, string(body))
		if peersResp.OK {
			return 0
		}
		return 1
	}

	if !peersResp.OK {
		fmt.Fprintf(stderr, "pdx peers: %s\n", peersResp.Error)
		return 1
	}

	fmt.Fprint(stdout, formatPeersTable(peersResp))
	return 0
}

// renderPeersAll handles a scope=all GET /api/peers body: JSON passthrough
// or the multi-host table. Exit 0 iff the local row (Hosts[0], see
// internal/peers/envelope.go and internal/module/peers.allEnvelope) is ok —
// remote failures are rows in the output, not errors.
func renderPeersAll(body []byte, jsonOutput bool, stdout, stderr io.Writer) int {
	var allResp peers.AllEnvelope
	if err := json.Unmarshal(body, &allResp); err != nil {
		fmt.Fprintf(stderr, "pdx peers: invalid response\n")
		return 1
	}

	localOK := len(allResp.Hosts) > 0 && allResp.Hosts[0].OK

	if jsonOutput {
		fmt.Fprint(stdout, string(body))
		if localOK {
			return 0
		}
		return 1
	}

	if !localOK {
		errMsg := ""
		if len(allResp.Hosts) > 0 {
			errMsg = allResp.Hosts[0].Error
		}
		fmt.Fprintf(stderr, "pdx peers: %s\n", errMsg)
		return 1
	}

	fmt.Fprint(stdout, formatPeersAllTable(allResp))
	return 0
}

// formatPeersTable renders resp.Peers as a text/tabwriter table with columns
// ADDRESS AGENT NAME STATUS DELIVERABLE CWD, followed by a partial-resolution
// summary line when any record's owner lookup did not run.
func formatPeersTable(resp peers.Envelope) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ADDRESS\tAGENT\tNAME\tSTATUS\tDELIVERABLE\tCWD")

	unresolved := 0
	for _, rec := range resp.Peers {
		if rec.Agent == nil && rec.Reason == "" {
			unresolved++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			rec.Address,
			agentField(rec),
			nameField(rec),
			statusField(rec),
			deliverableField(rec),
			rec.Cwd,
		)
	}
	w.Flush()

	if unresolved > 0 {
		fmt.Fprintf(&buf, "(partial: %d sessions not resolved within budget)\n", unresolved)
	}

	return buf.String()
}

// formatPeersAllTable renders a scope=all response as a text/tabwriter
// table with a leading HOST column (the row's host alias), one row per
// peer record across every host whose fetch succeeded, followed by one
// line per host whose fetch failed: "<alias>  (unreachable: <error>)".
func formatPeersAllTable(resp peers.AllEnvelope) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tADDRESS\tAGENT\tNAME\tSTATUS\tDELIVERABLE\tCWD")

	for _, h := range resp.Hosts {
		if !h.OK {
			continue
		}
		for _, rec := range h.Peers {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				h.Alias,
				rec.Address,
				agentField(rec),
				nameField(rec),
				statusField(rec),
				deliverableField(rec),
				rec.Cwd,
			)
		}
	}
	w.Flush()

	for _, h := range resp.Hosts {
		if !h.OK {
			fmt.Fprintf(&buf, "%s  (unreachable: %s)\n", h.Alias, h.Error)
		}
	}

	return buf.String()
}

func agentField(rec peers.PeerRecord) string {
	if rec.Agent == nil || rec.Agent.Type == "" {
		return "-"
	}
	return rec.Agent.Type
}

func nameField(rec peers.PeerRecord) string {
	if rec.Agent == nil || rec.Agent.PeerName == "" {
		return "-"
	}
	return rec.Agent.PeerName
}

func statusField(rec peers.PeerRecord) string {
	if rec.Agent == nil || rec.Agent.Status == "" {
		return "-"
	}
	return rec.Agent.Status
}

// deliverableField renders DELIVERABLE: "yes" when the row is usable,
// otherwise its Reason, or "-" for a row that is neither (unresolved: no
// agent and no reason, i.e. owner lookup did not run within budget).
func deliverableField(rec peers.PeerRecord) string {
	if rec.Deliverable {
		return "yes"
	}
	if rec.Reason != "" {
		return rec.Reason
	}
	return "-"
}

// --- host subcommand form: /api/peers/hosts[/{alias}] ----------------------
//
// cmd/pdx must not import internal/module/peers (that would pull a daemon
// package into the CLI binary), so the JSON shapes of its handlers'
// request/response bodies are mirrored here as small local structs instead
// of shared types. Keep field names/tags in sync with
// internal/module/peers/hosts.go if that file's wire format changes.

// cliHostRow mirrors internal/module/peers.hostRow: the never-secret view
// of a configured host served by GET /api/peers/hosts and as PUT
// /api/peers/hosts/{alias}'s response body.
type cliHostRow struct {
	Alias           string `json:"alias"`
	URL             string `json:"url"`
	HostID          string `json:"host_id"`
	Verified        bool   `json:"verified"`
	HasToken        bool   `json:"has_token"`
	HasInboundToken bool   `json:"has_inbound_token"`
	AllowBypass     bool   `json:"allow_bypass"`
}

// cliHostsListResponse mirrors GET /api/peers/hosts' body.
type cliHostsListResponse struct {
	Hosts []cliHostRow `json:"hosts"`
}

// cliAddHostRequest mirrors POST /api/peers/hosts' body.
type cliAddHostRequest struct {
	Alias string `json:"alias"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

// cliAddHostResponse mirrors POST /api/peers/hosts' 201 body — the only
// response that ever carries a live inbound-token value.
type cliAddHostResponse struct {
	Alias        string `json:"alias"`
	URL          string `json:"url"`
	HostID       string `json:"host_id"`
	InboundToken string `json:"inbound_token"`
	Verified     bool   `json:"verified"`
}

// cliPutHostRequest mirrors PUT /api/peers/hosts/{alias}'s body.
type cliPutHostRequest struct {
	Token       string `json:"token"`
	AllowBypass *bool  `json:"allow_bypass,omitempty"`
}

// runPeersHostCmd dispatches to the four `pdx peers host` verbs. inv.verb
// and inv.positionals' arity are already validated by parsePeersInvocation.
func runPeersHostCmd(inv peersInvocation, stdout, stderr io.Writer) int {
	cfg, err := config.Load(inv.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}
	base := fmt.Sprintf("http://%s:%d/api/peers/hosts", cfg.Bind, cfg.Port)

	switch inv.verb {
	case "list":
		return runPeersHostList(cfg, base, stdout, stderr)
	case "add":
		return runPeersHostAdd(cfg, base, inv, stdout, stderr)
	case "set-token":
		return runPeersHostSetToken(cfg, base, inv, stdout, stderr)
	case "remove":
		return runPeersHostRemove(cfg, base, inv, stdout, stderr)
	default:
		// Unreachable: parsePeersInvocation only accepts known verbs.
		fmt.Fprintln(stderr, peersUsage)
		return 2
	}
}

func runPeersHostList(cfg config.Config, base string, stdout, stderr io.Writer) int {
	result, err := doPeersRequest(http.MethodGet, base, nil, cfg.Token)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status != http.StatusOK {
		return reportPeersAPIError(result, stderr)
	}

	var listResp cliHostsListResponse
	if err := json.Unmarshal(result.body, &listResp); err != nil {
		fmt.Fprintln(stderr, "pdx peers: invalid response")
		return 1
	}

	fmt.Fprint(stdout, formatHostsTable(listResp.Hosts))
	return 0
}

func runPeersHostAdd(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	alias, hostURL := inv.positionals[0], inv.positionals[1]
	reqBody, err := json.Marshal(cliAddHostRequest{Alias: alias, URL: hostURL, Token: inv.token})
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}

	result, err := doPeersRequest(http.MethodPost, base, reqBody, cfg.Token)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status != http.StatusCreated {
		return reportPeersAPIError(result, stderr)
	}

	var addResp cliAddHostResponse
	if err := json.Unmarshal(result.body, &addResp); err != nil {
		fmt.Fprintln(stderr, "pdx peers: invalid response")
		return 1
	}

	fmt.Fprintf(stdout, "added %s (%s)  verified: %s\n", addResp.Alias, addResp.URL, yesNo(addResp.Verified))
	fmt.Fprintf(stdout, "inbound token for %s to use when adding this host:\n", addResp.Alias)
	fmt.Fprintf(stdout, "  %s\n", addResp.InboundToken)
	return 0
}

func runPeersHostSetToken(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	alias, token := inv.positionals[0], inv.positionals[1]
	reqBody, err := json.Marshal(cliPutHostRequest{Token: token, AllowBypass: inv.allowBypass})
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}

	result, err := doPeersRequest(http.MethodPut, base+"/"+url.PathEscape(alias), reqBody, cfg.Token)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status != http.StatusOK {
		return reportPeersAPIError(result, stderr)
	}

	var row cliHostRow
	if err := json.Unmarshal(result.body, &row); err != nil {
		fmt.Fprintln(stderr, "pdx peers: invalid response")
		return 1
	}

	fmt.Fprint(stdout, formatHostsTable([]cliHostRow{row}))
	return 0
}

func runPeersHostRemove(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	alias := inv.positionals[0]

	result, err := doPeersRequest(http.MethodDelete, base+"/"+url.PathEscape(alias), nil, cfg.Token)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status != http.StatusNoContent {
		return reportPeersAPIError(result, stderr)
	}

	fmt.Fprintf(stdout, "removed %s\n", alias)
	return 0
}

// formatHostsTable renders hosts as a text/tabwriter table with columns
// ALIAS URL HOST_ID VERIFIED TOKEN INBOUND ALLOW_BYPASS (the last four
// rendered as yes/no).
func formatHostsTable(hosts []cliHostRow) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ALIAS\tURL\tHOST_ID\tVERIFIED\tTOKEN\tINBOUND\tALLOW_BYPASS")
	for _, h := range hosts {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			h.Alias,
			h.URL,
			h.HostID,
			yesNo(h.Verified),
			yesNo(h.HasToken),
			yesNo(h.HasInboundToken),
			yesNo(h.AllowBypass),
		)
	}
	w.Flush()
	return buf.String()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// peersHTTPResult is one hosts-route HTTP response, already bounded-read
// into memory by doPeersRequest.
type peersHTTPResult struct {
	status int
	body   []byte
}

// doPeersRequest issues one hosts-route HTTP request (method/url/payload,
// payload nil for a bodyless request) with the admin bearer token, and
// reads the response body bounded by status class: maxPeersOKBodyBytes for
// a 2xx, maxPeersErrorBodyBytes otherwise (mirroring runPeersQueryCmd's
// bounded reads for GET /api/peers) — a body exceeding its bound yields
// errPeersResponseTooLarge rather than being read in full.
func doPeersRequest(method, url string, payload []byte, token string) (peersHTTPResult, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return peersHTTPResult{}, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return peersHTTPResult{}, err
	}
	defer resp.Body.Close()

	limit := int64(maxPeersOKBodyBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limit = maxPeersErrorBodyBytes
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return peersHTTPResult{status: resp.StatusCode}, err
	}
	if int64(len(raw)) > limit {
		return peersHTTPResult{status: resp.StatusCode}, errPeersResponseTooLarge
	}
	return peersHTTPResult{status: resp.StatusCode, body: raw}, nil
}

// reportPeersTransportErr prints a transport-level failure (connection
// error, oversized body) to stderr and returns exit code 1.
func reportPeersTransportErr(err error, stderr io.Writer) int {
	if errors.Is(err, errPeersResponseTooLarge) {
		fmt.Fprintln(stderr, "pdx peers: response too large")
		return 1
	}
	fmt.Fprintf(stderr, "pdx peers: %v\n", err)
	return 1
}

// reportPeersAPIError prints a non-2xx hosts-route response's server
// `error` field (or a fallback) to stderr and returns exit code 1.
func reportPeersAPIError(result peersHTTPResult, stderr io.Writer) int {
	fmt.Fprintf(stderr, "pdx peers: %s\n", extractPeersErrorMessage(result.body))
	return 1
}

// extractPeersErrorMessage pulls the `error` field out of a hosts-route
// error body ({"error":"..."}, per internal/module/peers/hosts.go's
// writeJSONError), falling back to the trimmed raw body, or "<no body>",
// when it isn't that shape.
func extractPeersErrorMessage(body []byte) string {
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error != "" {
		return errResp.Error
	}
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return "<no body>"
	}
	return detail
}
