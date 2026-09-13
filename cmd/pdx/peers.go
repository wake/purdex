package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// peersResponse mirrors GET /api/peers' JSON body. It is cmd/pdx's own copy
// (this package must not import internal/module/peers), sharing only the
// PeerRecord row type with the daemon's public wire format.
type peersResponse struct {
	HostID  string             `json:"host_id"`
	OK      bool               `json:"ok"`
	Error   string             `json:"error"`
	Partial bool               `json:"partial"`
	Peers   []peers.PeerRecord `json:"peers"`
}

// runPeers is the `pdx peers` switch target.
func runPeers(args []string) {
	os.Exit(runPeersCmd(args, os.Stdout, os.Stderr))
}

// runPeersCmd implements `pdx peers [--json] [--config <path>]`. It does all
// the work and returns the process exit code, so tests can drive it without
// os.Exit.
func runPeersCmd(args []string, stdout, stderr io.Writer) int {
	cfgPath, jsonOutput, unknownFlag, ok := parsePeersArgs(args)
	if !ok {
		fmt.Fprintf(stderr, "pdx peers: unknown flag %s\n", unknownFlag)
		return 2
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}

	url := fmt.Sprintf("http://%s:%d/api/peers", cfg.Bind, cfg.Port)
	req, err := http.NewRequest(http.MethodGet, url, nil)
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

	var peersResp peersResponse
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

// parsePeersArgs extracts --config <path> and --json from args. It does not
// use the package's parseConfigPath (that helper calls log.Fatalf on a bad
// config, which would bypass runPeersCmd's exit-code contract).
//
// Any argument starting with "-" that is not --config/-config (with a
// following value) or --json is rejected: ok is false and unknownFlag names
// the offending argument, so the caller can print an error and exit 2
// before loading config or making any request.
func parsePeersArgs(args []string) (cfgPath string, jsonOutput bool, unknownFlag string, ok bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-config":
			if i+1 < len(args) {
				cfgPath = args[i+1]
				i++
			}
		case "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", false, args[i], false
			}
		}
	}
	return cfgPath, jsonOutput, "", true
}

// formatPeersTable renders resp.Peers as a text/tabwriter table with columns
// ADDRESS AGENT NAME STATUS DELIVERABLE CWD, followed by a partial-resolution
// summary line when any record's owner lookup did not run.
func formatPeersTable(resp peersResponse) string {
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
