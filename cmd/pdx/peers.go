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
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

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

// sanitizeCell renders s safely for the operator's terminal, escaping every
// rune that is not printable (unicode.IsPrint) or is DEL (\x7f) into its Go
// escape form (e.g. "\x1b", "\n", "\t") — the same form strconv.QuoteRune
// would put inside single quotes, without the quotes themselves. Every
// other rune, including non-ASCII printable text, passes through
// unchanged.
//
// This is applied at the CLI's output boundary to every table cell and
// every stderr message that echoes text which ultimately came from a peer
// (a remote daemon's response body, forwarded through the local daemon):
// without it, a peer could inject ANSI/OSC escape sequences into the
// operator's terminal via a crafted peer name, status, cwd, or error
// string. --json passthrough is deliberately exempt — it is machine
// output, read by tools rather than rendered by a terminal.
func sanitizeCell(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) && r != 0x7f {
			b.WriteRune(r)
			continue
		}
		q := strconv.QuoteRune(r)
		b.WriteString(q[1 : len(q)-1]) // strip the surrounding single quotes
	}
	return b.String()
}

// peersUsage is the generic grammar-rejection message: printed to stderr
// (exit 2) for every malformed invocation except an unrecognized flag,
// which gets its own more specific message (see runPeersCmd).
const peersUsage = "usage: pdx peers [--json] [--all] [--config <path>]\n" +
	"       pdx peers host add [<alias>] <url> [--token <t>] [--config <path>]\n" +
	"       pdx peers host set-token <alias> <token> [--allow-bypass=true|false] [--config <path>]\n" +
	"       pdx peers host verify <alias> [--json] [--config <path>]\n" +
	"       pdx peers host rename <alias> <new-alias> [--config <path>]\n" +
	"       pdx peers host rotate <alias> [--commit|--cancel] [--force] [--config <path>]\n" +
	"       pdx peers host remove <alias> [--config <path>]\n" +
	"       pdx peers host list [--config <path>]\n" +
	"       pdx peers alias [--config <path>]\n" +
	"       pdx peers alias <name> [--config <path>]\n" +
	"       pdx peers alias --clear [--config <path>]"

// runPeers is the `pdx peers` switch target.
func runPeers(args []string) {
	os.Exit(runPeersCmd(args, os.Stdout, os.Stderr))
}

// runPeersCmd implements the full `pdx peers` grammar — the top-level query
// form (`pdx peers [--json] [--all] [--config <path>]`), the `host`
// subcommand form (`pdx peers host <add|set-token|remove|list> ...`) and
// the `alias` form (`pdx peers alias [<name>|--clear]`). It does all the
// work and returns the process exit code, so tests can drive it without
// os.Exit. Every grammar rejection returns 2 having made no config load or
// HTTP request.
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
	if inv.aliasMode {
		return runPeersAliasCmd(inv, stdout, stderr)
	}
	return runPeersQueryCmd(inv, stdout, stderr)
}

// peersInvocation is the parsed, validated result of parsePeersInvocation:
// the top-level query form (all/jsonOutput/cfgPath, hostMode and aliasMode
// false), the "host" subcommand form (hostMode true; verb/positionals/
// token/allowBypass/cfgPath), or the "alias" form (aliasMode true;
// aliasSet/aliasValue/aliasClear/cfgPath) — never a mix.
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

	// aliasMode selects `pdx peers alias` (self-alias spec §4.2). Exactly
	// one of its three forms applies: aliasSet with aliasValue (`alias
	// <name>`), aliasClear (`alias --clear`), or neither (the query form).
	// parsePeersInvocation rejects --clear for every other form.
	aliasMode  bool
	aliasSet   bool
	aliasValue string
	aliasClear bool

	// rotateCommit/rotateCancel/rotateForce carry `host rotate`'s three
	// boolean flags (spec §6.6) — meaningful only when verb == "rotate";
	// parsePeersInvocation rejects them for every other verb and for the
	// query form.
	rotateCommit bool
	rotateCancel bool
	rotateForce  bool
}

// peersHostVerbArity is every known `pdx peers host` verb's accepted
// positional-argument counts. "add" accepts two forms because its alias is
// optional (spec §7.2): `<alias> <url>` names the host locally, and `<url>`
// alone lets the daemon adopt the alias the peer publishes for itself.
var peersHostVerbArity = map[string][]int{
	"add":       {1, 2},
	"set-token": {2},
	"verify":    {1},
	"rename":    {2},
	"rotate":    {1},
	"remove":    {1},
	"list":      {0},
}

// addArgs splits `host add`'s positionals into alias and URL. The alias is
// the optional one, so a lone positional is the URL — never a host named
// after it. Only meaningful once parsePeersInvocation has accepted the
// invocation; any other arity yields two empty strings.
func (inv peersInvocation) addArgs() (alias, hostURL string) {
	switch len(inv.positionals) {
	case 1:
		return "", inv.positionals[0]
	case 2:
		return inv.positionals[0], inv.positionals[1]
	}
	return "", ""
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
		case a == "--commit":
			inv.rotateCommit = true
		case a == "--cancel":
			inv.rotateCancel = true
		case a == "--force":
			inv.rotateForce = true
		case a == "--clear":
			inv.aliasClear = true
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

	// --clear belongs to `alias` alone; every other form rejects it.
	if inv.aliasClear && (len(positionals) == 0 || positionals[0] != "alias") {
		return peersInvocation{}, "", false
	}

	if len(positionals) == 0 || (positionals[0] != "host" && positionals[0] != "alias") {
		// Top-level query form: no positionals at all, and none of the
		// host-only flags (--token, --allow-bypass, --commit/--cancel/
		// --force — the last three are rotate-only).
		if len(positionals) != 0 || inv.hasToken || inv.allowBypass != nil ||
			inv.rotateCommit || inv.rotateCancel || inv.rotateForce {
			return peersInvocation{}, "", false
		}
		return inv, "", true
	}

	if positionals[0] == "alias" {
		// alias form (self-alias spec §4.2): `alias`, `alias <name>` or
		// `alias --clear`. Exclusive with --all/--json (query-form output
		// switches with nothing to switch here) and with every host-only
		// flag; <name> together with --clear would be two instructions.
		inv.aliasMode = true
		if inv.all || inv.jsonOutput || inv.hasToken || inv.allowBypass != nil ||
			inv.rotateCommit || inv.rotateCancel || inv.rotateForce {
			return peersInvocation{}, "", false
		}
		switch len(positionals) {
		case 1:
		case 2:
			if inv.aliasClear {
				return peersInvocation{}, "", false
			}
			inv.aliasSet = true
			inv.aliasValue = positionals[1]
			// Same client-side "/" refusal as the host verbs' alias.
			if strings.Contains(inv.aliasValue, "/") {
				return peersInvocation{}, "", false
			}
		default:
			return peersInvocation{}, "", false
		}
		return inv, "", true
	}

	// host subcommand form.
	inv.hostMode = true
	if inv.all {
		return peersInvocation{}, "", false
	}
	if len(positionals) < 2 {
		return peersInvocation{}, "", false
	}
	inv.verb = positionals[1]
	inv.positionals = positionals[2:]

	arities, known := peersHostVerbArity[inv.verb]
	if !known || !slices.Contains(arities, len(inv.positionals)) {
		return peersInvocation{}, "", false
	}

	// --json is a query-form flag; among host verbs only verify has a
	// JSON body worth passing through.
	if inv.jsonOutput && inv.verb != "verify" {
		return peersInvocation{}, "", false
	}

	switch inv.verb {
	case "add":
		if inv.allowBypass != nil || inv.rotateCommit || inv.rotateCancel || inv.rotateForce {
			return peersInvocation{}, "", false
		}
		// The alias is what "add" may omit, so a lone positional has to
		// look like the URL it stands in for. Without this, "host add
		// air" would parse as the one-arg form and POST "air" as a URL
		// instead of being reported as the missing url it is. Only the
		// ambiguous arity is checked: with both positionals present the
		// URL's own validation stays the daemon's job.
		if len(inv.positionals) == 1 && !strings.Contains(inv.positionals[0], "://") {
			return peersInvocation{}, "", false
		}
	case "set-token":
		if inv.hasToken || inv.rotateCommit || inv.rotateCancel || inv.rotateForce {
			return peersInvocation{}, "", false
		}
	case "rotate":
		if inv.hasToken || inv.allowBypass != nil || inv.jsonOutput {
			return peersInvocation{}, "", false
		}
		if inv.rotateCommit && inv.rotateCancel {
			return peersInvocation{}, "", false
		}
		if inv.rotateForce && !inv.rotateCommit && !inv.rotateCancel {
			return peersInvocation{}, "", false
		}
	default: // verify, rename, remove, list
		if inv.hasToken || inv.allowBypass != nil || inv.rotateCommit || inv.rotateCancel || inv.rotateForce {
			return peersInvocation{}, "", false
		}
	}

	// Refuse "/" in the alias client-side (the server also validates it,
	// but this catches the obviously-wrong case before any request is
	// made). Every verb with a positional puts the alias first EXCEPT
	// "add", whose alias is optional — asking addArgs keeps a URL in the
	// first slot from being mistaken for an alias full of slashes.
	alias := ""
	if inv.verb == "add" {
		alias, _ = inv.addArgs()
	} else if len(inv.positionals) > 0 {
		alias = inv.positionals[0]
	}
	if strings.Contains(alias, "/") {
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
		fmt.Fprintf(stderr, "pdx peers: HTTP %d: %s\n", resp.StatusCode, sanitizeCell(detail))
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
		fmt.Fprintf(stderr, "pdx peers: %s\n", sanitizeCell(peersResp.Error))
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
		fmt.Fprintf(stderr, "pdx peers: %s\n", sanitizeCell(errMsg))
		return 1
	}

	fmt.Fprint(stdout, formatPeersAllTable(allResp))
	return 0
}

// formatPeersTable renders resp.Peers as a text/tabwriter table with columns
// TITLE ADDRESS AGENT STATUS DELIVERABLE TMUX CWD, followed by the
// host's partial-cause lines (writeHostDiagnostics) and a trailer line
// naming this daemon's version.
//
// TITLE comes first because that is the order the table is used in (v4 spec
// §5.7): a reader scans the titles to find the conversation they want,
// then copies that row's ADDRESS to reach it. A row with no title renders
// a BLANK cell rather than "-" — a dash reads as a value, and an unnamed
// conversation has nothing to show there; it is still perfectly
// addressable, which is exactly what the ADDRESS beside it says.
//
// Two v3 columns are gone and one is new. NAME went because it IS the
// address's second segment, and HOST — which only the --all form ever had —
// because it is the address's first. TMUX arrives because the tmux name left
// the address along with the suffix, and without a column of its own it would
// not be on screen anywhere.
func formatPeersTable(resp peers.Envelope) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TITLE\tADDRESS\tAGENT\tSTATUS\tDELIVERABLE\tTMUX\tCWD")

	for _, rec := range resp.Peers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			sanitizeCell(rec.Title),
			addressField(rec),
			sanitizeCell(agentField(rec)),
			sanitizeCell(statusField(rec)),
			sanitizeCell(deliverableField(rec)),
			sanitizeCell(tmuxField(rec)),
			sanitizeCell(rec.Cwd),
		)
	}
	w.Flush()

	writeHostDiagnostics(&buf, "", resp.Peers, resp.UnknownRegistryFiles, resp.TitlesUnavailable)
	fmt.Fprintf(&buf, "daemon %s\n", daemonVersionField(resp.DaemonVersion))

	return buf.String()
}

// countUnresolved counts the rows whose owner lookup neither produced an
// agent nor a reason — the "sessions not resolved within budget" figure
// shown in the partial trailer.
func countUnresolved(recs []peers.PeerRecord) int {
	n := 0
	for _, rec := range recs {
		if rec.Agent == nil && rec.Reason == "" {
			n++
		}
	}
	return n
}

// daemonVersionField renders a host's daemon_version trailer value:
// "(unknown)" when the field is blank (the daemon predates this field, or
// the row is a local fetch failure that never reached a daemon), sanitized
// verbatim otherwise.
func daemonVersionField(v string) string {
	if v == "" {
		return "(unknown)"
	}
	return sanitizeCell(v)
}

// writeHostDiagnostics prints one host's partial-cause lines (spec §3.3,
// §3.6), the same renderer for the single-host table and for every host
// of --all. Each of the three causes has its own explicit signal in the
// envelope and its own line, printed whenever that signal is set — never
// inferred from the others' absence, never suppressed by another — in
// this order:
//
//	(partial: N sessions not resolved within budget)   N = countUnresolved(peers) > 0
//	(partial: unknown registry files: a, b)            unknownFiles non-empty; each path through sanitizeCell
//	(partial: title store unavailable)                 titlesUnavailable
//
// prefix is "" for the single-host table and "<alias>  " for --all,
// matching the unreachable/daemon trailer lines. Nothing is printed when
// no signal is set.
func writeHostDiagnostics(buf *strings.Builder, prefix string, peerRows []peers.PeerRecord, unknownFiles []string, titlesUnavailable bool) {
	if unresolved := countUnresolved(peerRows); unresolved > 0 {
		fmt.Fprintf(buf, "%s(partial: %d sessions not resolved within budget)\n", prefix, unresolved)
	}
	if len(unknownFiles) > 0 {
		names := make([]string, len(unknownFiles))
		for i, f := range unknownFiles {
			names[i] = sanitizeCell(f)
		}
		fmt.Fprintf(buf, "%s(partial: unknown registry files: %s)\n", prefix, strings.Join(names, ", "))
	}
	if titlesUnavailable {
		fmt.Fprintf(buf, "%s(partial: title store unavailable)\n", prefix)
	}
}

// formatPeersAllTable renders a scope=all response as a text/tabwriter
// table with a leading HOST column (the row's host alias) and then the
// single-host order, TITLE before ADDRESS (v4 spec §5.7). HOST stays first
// because neither of the other two columns means anything until you know
// which host the row lives on — the address carries its own host segment,
// but a column you can scan down beats one you have to read across.
// One row per peer record across every host
// whose fetch succeeded, followed by one line per host whose fetch failed:
// "<alias>  (unreachable: <error>)", then one line per host whose
// self-reported name disagrees with ours (aliasDriftField), followed by, for
// every host whose
// fetch succeeded, that host's "<alias>  (partial: …)" cause lines (spec
// §3.3, writeHostDiagnostics — the same lines the single-host table
// prints) and a "<alias>  daemon <version>" trailer line.
func formatPeersAllTable(resp peers.AllEnvelope) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tTITLE\tADDRESS\tAGENT\tSTATUS\tDELIVERABLE\tTMUX\tCWD")

	for _, h := range resp.Hosts {
		if !h.OK {
			continue
		}
		for _, rec := range h.Peers {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				sanitizeCell(h.Alias),
				sanitizeCell(rec.Title),
				addressField(rec),
				sanitizeCell(agentField(rec)),
				sanitizeCell(statusField(rec)),
				sanitizeCell(deliverableField(rec)),
				sanitizeCell(tmuxField(rec)),
				sanitizeCell(rec.Cwd),
			)
		}
	}
	w.Flush()

	for _, h := range resp.Hosts {
		if !h.OK {
			fmt.Fprintf(&buf, "%s  (unreachable: %s)\n", sanitizeCell(h.Alias), sanitizeCell(h.Error))
		}
	}
	for _, h := range resp.Hosts {
		if self := aliasDriftField(h); self != "" {
			fmt.Fprintf(&buf, "%s  (alias drift: peer calls itself %s)\n", sanitizeCell(h.Alias), sanitizeCell(self))
		}
	}
	for _, h := range resp.Hosts {
		if !h.OK {
			continue
		}
		alias := sanitizeCell(h.Alias)
		writeHostDiagnostics(&buf, alias+"  ", h.Peers, h.UnknownRegistryFiles, h.TitlesUnavailable)
		fmt.Fprintf(&buf, "%s  daemon %s\n", alias, daemonVersionField(h.DaemonVersion))
	}

	return buf.String()
}

// aliasDriftField returns the peer's self-reported name when it disagrees
// with the name this host has that peer configured under, and "" when there
// is nothing to say: the two agree, or the peer never reported one (an old
// daemon, or a fetch that never reached one — see HostResult.SelfAlias).
// The comparison is case-insensitive because everything that routes on an
// alias already is (config.ValidateAlias, config.FindPeerHostByAlias), so
// "MLAB" and "mlab" reach the same host and are not a disagreement.
//
// Drift is surfaced, never followed. h.Alias is what every address on this
// host resolves against; adopting the peer's rename here would move every
// address out from under whoever had written one down. The line reports that
// the two disagree and stops — renaming stays a deliberate `pdx peers host`
// edit by an operator who has decided to.
//
// This lives on --all and not on `pdx peers host list` on purpose (spec
// §7.4): that route renders local config and contacts nobody, so the best it
// could show is a value remembered at pairing time. The fan-out refetches
// every peer's envelope on every call, which makes this the one place the
// comparison is live rather than stale.
func aliasDriftField(h peers.HostResult) string {
	if h.SelfAlias == "" || strings.EqualFold(h.SelfAlias, h.Alias) {
		return ""
	}
	return h.SelfAlias
}

// displayAddress renders one peer row's address through addressWithRef.
func displayAddress(rec peers.PeerRecord) string {
	return addressWithRef(rec.Address, rec.Ref)
}

// addressField renders displayAddress through sanitizeCell first, then — only
// for an entry row (RowKind == "entry", a live tmux/cc process outside any
// registered session) — prefixes it with two spaces, visually nesting it
// under the session rows above it. sanitizeCell passes plain spaces
// through unchanged (it only escapes non-printable runes), so any leading
// space in the address itself is trimmed first: otherwise it would be
// indistinguishable from our own indentation, or make a non-entry row
// with a stray leading space look indented when it is not.
func addressField(rec peers.PeerRecord) string {
	addr := strings.TrimLeft(sanitizeCell(displayAddress(rec)), " ")
	if rec.RowKind == "entry" {
		addr = "  " + addr
	}
	return addr
}

func agentField(rec peers.PeerRecord) string {
	if rec.Agent == nil || rec.Agent.Type == "" {
		return "-"
	}
	return rec.Agent.Type
}

// tmuxField renders the TMUX cell. An entry row's tmux name comes from the
// registry file, frozen when the agent started, so it can name a session
// since renamed or gone; a session row's comes from the live inventory. The
// '?' is the difference, and a reader deciding where to attach is the one who
// needs to know it.
//
// It renders TmuxName and never SessionName: SessionName is empty on an entry
// row, which is the whole reason TmuxName exists (v4 spec §5.3).
func tmuxField(rec peers.PeerRecord) string {
	if rec.TmuxName == "" {
		return "-"
	}
	if rec.RowKind == "entry" {
		return rec.TmuxName + "?"
	}
	return rec.TmuxName
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
	RotationPending bool   `json:"rotation_pending"`
	LastInboundAuth string `json:"last_inbound_auth"`
}

// cliRotateResponse mirrors internal/module/peers.rotateResponse — with
// POST 201, the only responses that carry a live inbound-token value.
type cliRotateResponse struct {
	Alias        string `json:"alias"`
	InboundToken string `json:"inbound_token"`
}

// cliRotateGateRequest mirrors the body POST .../rotate/commit and
// .../rotate/cancel accept.
type cliRotateGateRequest struct {
	Force bool `json:"force,omitempty"`
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

// cliPutHostRequest mirrors PUT /api/peers/hosts/{alias}'s body. Alias is
// the rename field (spec §4.2); omitted when empty so set-token bodies
// stay byte-identical to before.
type cliPutHostRequest struct {
	Token       string `json:"token"`
	AllowBypass *bool  `json:"allow_bypass,omitempty"`
	Alias       string `json:"alias,omitempty"`
}

// cliVerifyHostResponse mirrors internal/module/peers.verifyHostResponse:
// one scope=all row for one entry, minus its peer rows.
type cliVerifyHostResponse struct {
	Alias         string `json:"alias"`
	HostID        string `json:"host_id"`
	OK            bool   `json:"ok"`
	Error         string `json:"error,omitempty"`
	SelfAlias     string `json:"self_alias"`
	DaemonVersion string `json:"daemon_version"`
}

// runPeersHostVerify implements `pdx peers host verify <alias> [--json]`:
// POST /api/peers/hosts/{alias}/verify, exit 0 on ok, 1 otherwise. The
// drift line uses the wording `pdx peers --all` prints so the two agree
// word for word; every remote value passes through sanitizeCell because
// it is the peer's own text landing in a terminal.
func runPeersHostVerify(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	alias := inv.positionals[0]

	result, err := doPeersRequest(http.MethodPost, base+"/"+url.PathEscape(alias)+"/verify", nil, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status != http.StatusOK {
		return reportPeersAPIError(result, stderr)
	}

	var resp cliVerifyHostResponse
	if err := json.Unmarshal(result.body, &resp); err != nil {
		fmt.Fprintln(stderr, "pdx peers: invalid response")
		return 1
	}

	if inv.jsonOutput {
		// Re-encoded, not echoed: the daemon's body is trusted-shaped but
		// re-encoding guarantees one JSON document with a trailing newline.
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(stderr, "pdx peers: %v\n", err)
			return 1
		}
		if resp.OK {
			return 0
		}
		return 1
	}

	if !resp.OK {
		fmt.Fprintf(stdout, "%s  FAILED: %s\n", sanitizeCell(resp.Alias), sanitizeCell(resp.Error))
		return 1
	}
	fmt.Fprintf(stdout, "%s  ok  host_id %s  daemon %s\n",
		sanitizeCell(resp.Alias), sanitizeCell(resp.HostID), sanitizeCell(resp.DaemonVersion))
	if resp.SelfAlias != "" {
		fmt.Fprintf(stdout, "  self alias: %s\n", sanitizeCell(resp.SelfAlias))
	}
	if resp.SelfAlias != "" && !strings.EqualFold(resp.SelfAlias, resp.Alias) {
		fmt.Fprintf(stdout, "  alias drift: peer calls itself %s\n", sanitizeCell(resp.SelfAlias))
	}
	return 0
}

// runPeersHostCmd dispatches to the six `pdx peers host` verbs. inv.verb
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
	case "verify":
		return runPeersHostVerify(cfg, base, inv, stdout, stderr)
	case "rename":
		return runPeersHostRename(cfg, base, inv, stdout, stderr)
	case "rotate":
		return runPeersHostRotate(cfg, base, inv, stdout, stderr)
	case "remove":
		return runPeersHostRemove(cfg, base, inv, stdout, stderr)
	default:
		// Unreachable: parsePeersInvocation only accepts known verbs.
		fmt.Fprintln(stderr, peersUsage)
		return 2
	}
}

func runPeersHostList(cfg config.Config, base string, stdout, stderr io.Writer) int {
	result, err := doPeersRequest(http.MethodGet, base, nil, cfg.Token, peersRequestTimeout)
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
	// An empty alias is sent as such: the daemon then adopts the one the
	// peer publishes for itself, and the 201 reports what it settled on.
	alias, hostURL := inv.addArgs()
	reqBody, err := json.Marshal(cliAddHostRequest{Alias: alias, URL: hostURL, Token: inv.token})
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}

	result, err := doPeersRequest(http.MethodPost, base, reqBody, cfg.Token, peersRequestTimeout)
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

	result, err := doPeersRequest(http.MethodPut, base+"/"+url.PathEscape(alias), reqBody, cfg.Token, peersRequestTimeout)
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

// runPeersHostRename implements `pdx peers host rename <alias> <new-alias>`:
// a PUT with only the alias field. Validation and uniqueness are the
// daemon's; the CLI only carries the words.
func runPeersHostRename(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	oldAlias, newAlias := inv.positionals[0], inv.positionals[1]
	reqBody, err := json.Marshal(cliPutHostRequest{Alias: newAlias})
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}

	result, err := doPeersRequest(http.MethodPut, base+"/"+url.PathEscape(oldAlias), reqBody, cfg.Token, peersRequestTimeout)
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

	// An older daemon decodes the PUT body without an alias field, ignores
	// the key, and answers 200 with the entry unchanged. The row's alias is
	// the only evidence the rename landed. Comparison must be exact (not
	// case-insensitive) because a case-only rename is a real rename; an old
	// daemon echoing the old spelling must be refused just like any other
	// ignored rename.
	if row.Alias != newAlias {
		fmt.Fprintf(stderr, "pdx peers: daemon did not apply the rename (entry is still %q; daemon too old?)\n", sanitizeCell(row.Alias))
		return 1
	}

	fmt.Fprintf(stdout, "renamed %s -> %s\n", sanitizeCell(oldAlias), sanitizeCell(row.Alias))
	return 0
}

// runPeersHostRotate implements the three forms of `pdx peers host rotate`
// (spec §6.6). The plain form prints the new token the way add does — this
// is the value the PEER must be given (`pdx peers host set-token <us> <tok>`
// over there). --commit and --cancel hit the daemon's gates; a 409
// "rotation unconfirmed" is explained in terms of what to do next, because
// a CLI on this host cannot make the peer dial us (spec §6.4).
func runPeersHostRotate(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	alias := inv.positionals[0]
	target := base + "/" + url.PathEscape(alias) + "/rotate"

	if !inv.rotateCommit && !inv.rotateCancel {
		result, err := doPeersRequest(http.MethodPost, target, nil, cfg.Token, peersRequestTimeout)
		if err != nil {
			return reportPeersTransportErr(err, stderr)
		}
		if result.status != http.StatusOK {
			return reportPeersAPIError(result, stderr)
		}
		var resp cliRotateResponse
		if err := json.Unmarshal(result.body, &resp); err != nil || resp.InboundToken == "" {
			fmt.Fprintln(stderr, "pdx peers: invalid response")
			return 1
		}
		fmt.Fprintf(stdout, "rotated %s: both the old and the new inbound token are accepted until you --commit\n", sanitizeCell(resp.Alias))
		fmt.Fprintf(stdout, "new inbound token for %s to use (pdx peers host set-token <this host> <token> over there):\n", sanitizeCell(resp.Alias))
		fmt.Fprintf(stdout, "  %s\n", resp.InboundToken)
		return 0
	}

	verb := "commit"
	if inv.rotateCancel {
		verb = "cancel"
	}
	reqBody, err := json.Marshal(cliRotateGateRequest{Force: inv.rotateForce})
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}
	result, err := doPeersRequest(http.MethodPost, target+"/"+verb, reqBody, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status == http.StatusConflict && extractPeersErrorMessage(result.body) == "rotation unconfirmed" {
		if verb == "commit" {
			fmt.Fprintf(stderr, "pdx peers: rotation unconfirmed — the peer has not presented the NEW token yet. Give it the token (set-token over there), run `pdx peers host verify <this host's alias>` on the peer, then retry; or --force if the peer is gone for good.\n")
		} else {
			fmt.Fprintf(stderr, "pdx peers: rotation unconfirmed — the peer was not last seen on the OLD token (it may already hold the new one). Run `pdx peers host verify <this host's alias>` on the peer, then retry; or --force if you are sure.\n")
		}
		return 1
	}
	if result.status != http.StatusOK {
		return reportPeersAPIError(result, stderr)
	}
	var row cliHostRow
	if err := json.Unmarshal(result.body, &row); err != nil {
		fmt.Fprintln(stderr, "pdx peers: invalid response")
		return 1
	}
	if verb == "commit" {
		fmt.Fprintf(stdout, "committed %s: the old inbound token is revoked\n", sanitizeCell(row.Alias))
	} else {
		fmt.Fprintf(stdout, "cancelled rotation for %s: the old inbound token is the only one again\n", sanitizeCell(row.Alias))
	}
	return 0
}

func runPeersHostRemove(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	alias := inv.positionals[0]

	result, err := doPeersRequest(http.MethodDelete, base+"/"+url.PathEscape(alias), nil, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status != http.StatusNoContent {
		return reportPeersAPIError(result, stderr)
	}

	fmt.Fprintf(stdout, "removed %s\n", alias)
	return 0
}

// --- pdx peers alias [<name>|--clear]: /api/peers/settings ----------------

// cliPutSettingsRequest is the body `pdx peers alias` PUTs. It is the CLI's
// own shape rather than peers.PutSettingsRequest because that shared type
// serialises its nil Deliver pointer as `"deliver":null`, and the daemon
// treats an explicit null as an instruction to interpret. The CLI has
// exactly one thing to say — the alias — so the body is exactly
// {"alias":"…"}: with omitempty, a nil pointer is left out while a pointer
// to "" (the --clear form) is still written.
type cliPutSettingsRequest struct {
	Alias *string `json:"alias,omitempty"`
}

// runPeersAliasCmd implements the three forms of `pdx peers alias`
// (self-alias spec §4.2): the query form GETs /api/peers/settings;
// `<name>` PUTs {"alias":"<name>"}; --clear PUTs {"alias":""}. Every form
// prints `alias: <alias> (<source>)` on success, and applies the S-5
// acceptance rule (tightened per codex F5) before calling anything a
// success — see acceptSettingsResponse.
func runPeersAliasCmd(inv peersInvocation, stdout, stderr io.Writer) int {
	cfg, err := config.Load(inv.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}
	settingsURL := fmt.Sprintf("http://%s:%d/api/peers/settings", cfg.Bind, cfg.Port)

	method := http.MethodGet
	var reqBody []byte
	if inv.aliasSet || inv.aliasClear {
		method = http.MethodPut
		value := ""
		if inv.aliasSet {
			value = inv.aliasValue
		}
		reqBody, err = json.Marshal(cliPutSettingsRequest{Alias: &value})
		if err != nil {
			fmt.Fprintf(stderr, "pdx peers: %v\n", err)
			return 1
		}
	}

	result, err := doPeersRequest(method, settingsURL, reqBody, cfg.Token, peersRequestTimeout)
	if err != nil {
		return reportPeersTransportErr(err, stderr)
	}
	if result.status != http.StatusOK {
		return reportPeersAPIError(result, stderr)
	}

	var settings peers.SettingsResponse
	if err := json.Unmarshal(result.body, &settings); err != nil {
		fmt.Fprintln(stderr, "pdx peers: invalid response")
		return 1
	}

	if problem := acceptSettingsResponse(inv, settings); problem != "" {
		fmt.Fprintf(stderr, "pdx peers: daemon did not apply the alias (%s; daemon too old?)\n", problem)
		return 1
	}

	fmt.Fprintf(stdout, "alias: %s (%s)\n", sanitizeCell(settings.Alias), settings.AliasSource)
	return 0
}

// acceptSettingsResponse is the S-5 acceptance rule for a 200 from
// /api/peers/settings, as tightened by codex F5. It returns "" when the
// response proves the request took effect, otherwise a short phrase naming
// what was wrong — the caller wraps it in the "did not apply" message.
//
// Every form requires alias_source to be one of the two values this
// version's daemon emits AND a non-empty alias: an older daemon decodes the
// PUT body without the alias key, ignores it, and answers 200 with the
// previous alias and no alias_source at all, so the status alone proves
// nothing. On top of that a set requires the echoed alias to equal the
// requested one EXACTLY (the alias is stored verbatim per S-2, so a
// case-only difference is a real difference) with source "config", and a
// clear requires source "host_id" — the daemon's derived default. The
// query form accepts either source.
func acceptSettingsResponse(inv peersInvocation, s peers.SettingsResponse) string {
	switch s.AliasSource {
	case "config", "host_id":
	case "":
		return "response carries no alias_source"
	default:
		return fmt.Sprintf("unknown alias_source %q", sanitizeCell(s.AliasSource))
	}
	if s.Alias == "" {
		return "response carries an empty alias"
	}
	switch {
	case inv.aliasSet:
		if s.Alias != inv.aliasValue {
			return fmt.Sprintf("alias is %q, not %q", sanitizeCell(s.Alias), sanitizeCell(inv.aliasValue))
		}
		if s.AliasSource != "config" {
			return fmt.Sprintf("alias_source is %q, not \"config\"", s.AliasSource)
		}
	case inv.aliasClear:
		if s.AliasSource != "host_id" {
			return fmt.Sprintf("alias_source is %q, not \"host_id\"", s.AliasSource)
		}
	}
	return ""
}

// formatHostsTable renders hosts as a text/tabwriter table with columns
// ALIAS URL HOST_ID VERIFIED TOKEN INBOUND ALLOW_BYPASS (rendered as
// yes/no) ROTATION (rotationCell: "-" | "pending" | "pending, confirmed").
func formatHostsTable(hosts []cliHostRow) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ALIAS\tURL\tHOST_ID\tVERIFIED\tTOKEN\tINBOUND\tALLOW_BYPASS\tROTATION")
	for _, h := range hosts {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			sanitizeCell(h.Alias),
			sanitizeCell(h.URL),
			sanitizeCell(h.HostID),
			yesNo(h.Verified),
			yesNo(h.HasToken),
			yesNo(h.HasInboundToken),
			yesNo(h.AllowBypass),
			rotationCell(h),
		)
	}
	w.Flush()
	return buf.String()
}

// rotationCell renders host list's ROTATION column: "-" when no rotation is
// pending, "pending, confirmed" once the peer has been seen presenting the
// new inbound token (LastInboundAuth == "current"), "pending" otherwise.
func rotationCell(h cliHostRow) string {
	switch {
	case !h.RotationPending:
		return "-"
	case h.LastInboundAuth == "current":
		return "pending, confirmed"
	default:
		return "pending"
	}
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

// peersRequestTimeout is doPeersRequest's client timeout for every
// hosts-route call and for `pdx msg log`/`pdx msg deliver` (cmd/pdx/msg.go)
// — a single local round trip to this daemon. `pdx msg send` uses its own,
// longer msgSendTimeout instead, since the daemon's handler makes its own
// outbound call to a peer host before answering.
const peersRequestTimeout = 10 * time.Second

// doPeersRequest issues one HTTP request (method/url/payload, payload nil
// for a bodyless request) with the admin bearer token and the given
// client timeout, and reads the response body bounded by status class:
// maxPeersOKBodyBytes for a 2xx, maxPeersErrorBodyBytes otherwise
// (mirroring runPeersQueryCmd's bounded reads for GET /api/peers) — a body
// exceeding its bound yields errPeersResponseTooLarge rather than being
// read in full.
func doPeersRequest(method, url string, payload []byte, token string, timeout time.Duration) (peersHTTPResult, error) {
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

	client := &http.Client{Timeout: timeout}
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
	fmt.Fprintf(stderr, "pdx peers: %s\n", sanitizeCell(extractPeersErrorMessage(result.body)))
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
