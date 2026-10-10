// cmd/pdx/peers_allow_team.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/config"
)

// runPeersHostAllowTeam implements `pdx peers host allow-team <alias> on|off` with, optionally, one of two ways to change
// the roots (#2340). `--root <dir>...` sets the roots to exactly these, as it always did, but reads the set's revision
// first and sends it: if someone changed the roots in between, the daemon answers 409 team_roots_conflict and nothing is
// overwritten. `--add-root <dir>...` / `--remove-root <dir>...` are atomic edits of the current set (add_team_roots /
// remove_team_roots, no revision needed). Without any of them the roots are left alone. The API takes absolute paths only,
// so the CLI expands ~ and makes a relative root absolute; existence is the daemon's to check.
func runPeersHostAllowTeam(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	alias := inv.positionals[0]
	on := inv.positionals[1] == "on"
	req := cliPutHostRequest{AllowTeam: &on}
	var err error
	if inv.roots != nil {
		whole, err := absTeamRoots(inv.roots)
		if err != nil {
			fmt.Fprintf(stderr, "pdx peers: %v\n", err)
			return 1
		}
		req.TeamRoots = &whole
		rev, code := listedTeamRootsRev(cfg, base, alias, stderr)
		if code != 0 {
			return code
		}
		req.TeamRootsRev = rev
	}
	if req.AddTeamRoots, err = absTeamRoots(inv.addRoots); err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}
	if req.RemoveTeamRoots, err = absTeamRoots(inv.removeRoots); err != nil {
		fmt.Fprintf(stderr, "pdx peers: %v\n", err)
		return 1
	}
	reqBody, err := json.Marshal(req)
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
	state, roots := "off", "none"
	if row.AllowTeam {
		state = "on"
	}
	if len(row.TeamRoots) > 0 {
		roots = strings.Join(row.TeamRoots, ", ")
	}
	fmt.Fprintf(stdout, "%s: allow-team %s  roots: %s\n", sanitizeCell(row.Alias), state, sanitizeCell(roots))
	if inv.endMembers {
		return endMembersOf(cfg, row, stdout, stderr)
	}
	return 0
}

// listedTeamRootsRev reads the host list and returns the team_roots_rev of alias (matched like the daemon does, ignoring
// case). A host that is not listed gives nil: the PUT then answers its own 404.
func listedTeamRootsRev(cfg config.Config, base, alias string, stderr io.Writer) (*int64, int) {
	result, err := doPeersRequest(http.MethodGet, base, nil, cfg.Token, peersRequestTimeout)
	if err != nil {
		return nil, reportPeersTransportErr(err, stderr)
	}
	if result.status != http.StatusOK {
		return nil, reportPeersAPIError(result, stderr)
	}
	var list cliHostsListResponse
	if err := json.Unmarshal(result.body, &list); err != nil {
		fmt.Fprintln(stderr, "pdx peers: invalid response")
		return nil, 1
	}
	for _, h := range list.Hosts {
		if strings.EqualFold(h.Alias, alias) {
			rev := h.TeamRootsRev
			return &rev, 0
		}
	}
	return nil, 0
}

// absTeamRoots is absTeamRoot over a list; nil in, nil out.
func absTeamRoots(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(in))
	for _, r := range in {
		abs, err := absTeamRoot(r)
		if err != nil {
			return nil, err
		}
		out = append(out, abs)
	}
	return out, nil
}

// absTeamRoot expands a leading ~ with the user's home (tmux and the API do
// not) and makes a relative path absolute against the working directory.
func absTeamRoot(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", fmt.Errorf("cannot expand %q: no home directory", p)
		}
		p = filepath.Join(home, p[1:])
	}
	return filepath.Abs(p)
}
