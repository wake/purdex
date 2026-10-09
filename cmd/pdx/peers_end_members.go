// cmd/pdx/peers_end_members.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// teamURL is a route of this host's own daemon.
func teamURL(cfg config.Config, route string) string {
	return fmt.Sprintf("http://%s:%d%s", cfg.Bind, cfg.Port, route)
}

// listRemoteMembers is GET /api/team/remote-members.
func listRemoteMembers(cfg config.Config) ([]team.RemoteMemberView, error) {
	result, err := doPeersRequest(http.MethodGet, teamURL(cfg, team.RemoteMembersRoute), nil, cfg.Token, peersRequestTimeout)
	if err != nil {
		return nil, err
	}
	if result.status != http.StatusOK {
		return nil, fmt.Errorf("%s", sanitizeCell(extractPeersErrorMessage(result.body)))
	}
	var resp team.RemoteMembersResponse
	if err := json.Unmarshal(result.body, &resp); err != nil {
		return nil, fmt.Errorf("invalid response")
	}
	return resp.Members, nil
}

// remoteMemberCounts counts the live remote members per lead host id, for `host list`. nil, err when the daemon
// cannot say (an older daemon has no such route): the list still prints, the column shows "-".
func remoteMemberCounts(cfg config.Config) (map[string]int, error) {
	members, err := listRemoteMembers(cfg)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, m := range members {
		counts[m.LeadHostID]++
	}
	return counts, nil
}

// endMembersOf ends every live remote member of the lead host that row names (`allow-team <alias> off
// --end-members`, spec §3.2). The switch is already off when this runs, so no new command lands meanwhile. A member
// that is no longer live (409) is already what was asked; any other failure is reported, the rest are still tried,
// and the exit code is 1.
func endMembersOf(cfg config.Config, row cliHostRow, stdout, stderr io.Writer) int {
	members, err := listRemoteMembers(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "pdx peers: allow-team is off, but listing its members failed: %v\n", err)
		return 1
	}
	ended, already, failed := 0, 0, 0
	for _, m := range members {
		if row.HostID == "" || m.LeadHostID != row.HostID {
			continue
		}
		body, _ := json.Marshal(team.RemoteMemberEndRequest{MK: m.MK})
		result, err := doPeersRequest(http.MethodPost, teamURL(cfg, team.RemoteMembersEndRoute), body, cfg.Token, peersRequestTimeout)
		switch {
		case err != nil:
			fmt.Fprintf(stderr, "pdx peers: allow-team is off, but ending %s failed: %v\n", sanitizeCell(m.MK), err)
			failed++
		case result.status == http.StatusOK:
			ended++
		case result.status == http.StatusConflict:
			already++
		default:
			fmt.Fprintf(stderr, "pdx peers: allow-team is off, but ending %s failed: %s\n", sanitizeCell(m.MK), sanitizeCell(extractPeersErrorMessage(result.body)))
			failed++
		}
	}
	line := fmt.Sprintf("ended %d member(s)", ended)
	if already > 0 {
		line += fmt.Sprintf("  (%d already ended)", already)
	}
	fmt.Fprintln(stdout, line)
	if failed > 0 {
		return 1
	}
	return 0
}
