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
	cfgPath, jsonOutput := parsePeersArgs(args)

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}

	if resp.StatusCode != http.StatusOK {
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		fmt.Fprintf(stderr, "pdx peers: %s\n", detail)
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
func parsePeersArgs(args []string) (cfgPath string, jsonOutput bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-config":
			if i+1 < len(args) {
				cfgPath = args[i+1]
				i++
			}
		case "--json":
			jsonOutput = true
		}
	}
	return cfgPath, jsonOutput
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
