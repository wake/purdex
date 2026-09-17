package nex

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"lab.protype.tw/wake/nexen"
	"lab.protype.tw/wake/nexen/api"
	nexconfig "lab.protype.tw/wake/nexen/config"
	"lab.protype.tw/wake/nexen/sandbox"

	pdxconfig "github.com/wake/purdex/internal/config"
)

// RoutePrefix is the path prefix pdx mounts the embedded Nexen module's
// HTTP API under (Nexen's api.Deps.PublicPrefix / nexen.Options.PublicPrefix).
// Pinned by TestRoutePrefixIsValidPublicPrefix via api.ValidatePublicPrefix.
const RoutePrefix = "/api/nex"

// ClientHeader is the optional request header a client sets to name
// itself within the host principal (spec §4.3). Its value must match
// clientIDPattern to be used; anything else is ignored.
const ClientHeader = "X-Pdx-Client"

// clientIDPattern bounds what a client may append to the principal: a
// short token of URL-safe characters, so it can neither smuggle
// separators into audit fields nor grow them unboundedly.
var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// buildOptions maps pdx's [nex] config (spec §4.2's mapping table) onto the
// nexen.Options Assemble needs. n must already be Expanded() — buildOptions
// performs no "~"-expansion or path cleaning of its own, it only carries
// values across the seam.
//
// It runs (*nexconfig.Config).Validate on the assembled config before
// returning, so the Options handed back already carry Nexen's own applied
// defaults (e.g. LeaseTTL, InterruptTimeout, ShutdownTimeout when their
// pdx-side string was "") — see I13.
//
// Errors carry no "nex:" prefix of their own: Module.Init wraps every
// buildOptions error as "nex: init: %w", and a prefix here too would read
// "nex: init: nex: …". A Nexen Validate failure (for example a negative
// duration, which pdx's own layer-1 validation does not reject) is
// returned as Nexen wrote it — its message already starts with "config:".
//
// hostID (whitespace-trimmed) both names the Nexen config's HostID and is
// the identity every request through this module authenticates as
// ("pdx:<hostID>", optionally "/<client>" — see principalAuth): pdx has no
// separate principal/token model of its own here, so a blank hostID is
// rejected outright rather than silently producing "pdx:" as a principal.
//
// shutdownBudget is copied into Config.ShutdownTimeout only because
// Nexen's Validate requires it; the field is consumed by the standalone
// `nex daemon` alone. In the embedded path the bound on Shutdown is the
// ctx Module.Stop receives from core.StopModules (spec §4.5 rule 1) —
// System.Shutdown honours that ctx, not this field.
func buildOptions(hostID, dataDir string, n pdxconfig.NexConfig, shutdownBudget time.Duration) (nexen.Options, error) {
	hostID = strings.TrimSpace(hostID)
	if hostID == "" {
		return nexen.Options{}, fmt.Errorf("host_id is empty; cannot derive principal")
	}

	leaseTTL, err := parseTimeout(n.Timeouts.LeaseTTL)
	if err != nil {
		return nexen.Options{}, fmt.Errorf("config: timeouts.lease_ttl: %w", err)
	}
	interrupt, err := parseTimeout(n.Timeouts.Interrupt)
	if err != nil {
		return nexen.Options{}, fmt.Errorf("config: timeouts.interrupt: %w", err)
	}
	turn, err := parseTimeout(n.Timeouts.Turn)
	if err != nil {
		return nexen.Options{}, fmt.Errorf("config: timeouts.turn: %w", err)
	}

	cfg := &nexconfig.Config{
		DataDir:      filepath.Join(dataDir, "nex"),
		RepoRoots:    n.RepoRoots,
		ServiceRoots: n.ServiceRoots,
		HostID:       hostID,
		Sandbox: sandbox.Policy{
			MaxProfile:     n.Sandbox.MaxProfile,
			DefaultProfile: n.Sandbox.DefaultProfile,
		},
		LeaseTTL:         nexconfig.Duration(leaseTTL),
		InterruptTimeout: nexconfig.Duration(interrupt),
		TurnTimeout:      nexconfig.Duration(turn),
		ShutdownTimeout:  nexconfig.Duration(shutdownBudget),
	}

	if err := cfg.Validate(); err != nil {
		// Nexen's own Validate error already starts with "config:".
		return nexen.Options{}, err
	}

	return nexen.Options{
		Config:       cfg,
		Auth:         principalAuth(hostID),
		PublicPrefix: RoutePrefix,
		ClaudeBin:    n.ClaudeBin,
	}, nil
}

// principalAuth is the Authenticator pdx hands Nexen (spec §4.3). The
// request reaching it has already passed pdx's outer chain (CORS →
// IPWhitelist → PairingGuard → TokenAuth), so it never checks credentials;
// it only names the caller:
//
//   - "pdx:<hostID>" by default — "someone holding this host's daemon
//     credential";
//   - "pdx:<hostID>/<client>" when the request carries ClientHeader with a
//     value matching clientIDPattern.
//
// The suffix exists because Nexen treats the same principal as the same
// writer: two clients (two SPA tabs, say) sharing the host token would
// otherwise silently re-mint each other's control lease. A P-B browser
// client sends a per-client id; `pdx nex` does NOT (attach and send run
// in different processes and must share one principal). An absent or
// malformed header falls back to the bare host principal — it is ignored,
// not rejected, and nothing is logged.
func principalAuth(hostID string) api.Authenticator {
	base := "pdx:" + hostID
	return api.AuthenticatorFunc(func(r *http.Request) (string, error) {
		if c := r.Header.Get(ClientHeader); c != "" && clientIDPattern.MatchString(c) {
			return base + "/" + c, nil
		}
		return base, nil
	})
}

// parseTimeout parses a pdx-side [nex.timeouts] string into a
// time.Duration. An empty string means "no preference" and yields the zero
// Duration, which nexconfig.Config.Validate then either defaults
// (LeaseTTL, InterruptTimeout, ShutdownTimeout) or leaves at its own
// meaningful zero (TurnTimeout: disabled).
func parseTimeout(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
