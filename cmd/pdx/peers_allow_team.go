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

// runPeersHostAllowTeam implements `pdx peers host allow-team <alias>
// on|off [--root <dir>]...`: a PUT with allow_team, plus team_roots when
// --root was given (the whole set is replaced; without it the roots are
// left alone). The API takes absolute paths only, so the CLI expands ~ and
// makes a relative root absolute; existence is the daemon's to check.
func runPeersHostAllowTeam(cfg config.Config, base string, inv peersInvocation, stdout, stderr io.Writer) int {
	alias := inv.positionals[0]
	on := inv.positionals[1] == "on"
	req := cliPutHostRequest{AllowTeam: &on}
	if inv.roots != nil {
		roots := make([]string, 0, len(inv.roots))
		for _, r := range inv.roots {
			abs, err := absTeamRoot(r)
			if err != nil {
				fmt.Fprintf(stderr, "pdx peers: %v\n", err)
				return 1
			}
			roots = append(roots, abs)
		}
		req.TeamRoots = &roots
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
	return 0
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
