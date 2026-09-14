package nex

import (
	"fmt"
	"net/http"
	"path/filepath"
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

// buildOptions maps pdx's [nex] config (spec §4.2's mapping table) onto the
// nexen.Options Assemble needs. n must already be Expanded() — buildOptions
// performs no "~"-expansion or path cleaning of its own, it only carries
// values across the seam.
//
// It runs (*nexconfig.Config).Validate on the assembled config before
// returning, so the Options handed back already carry Nexen's own applied
// defaults (e.g. LeaseTTL, InterruptTimeout, ShutdownTimeout when their
// pdx-side string was "") — see I13. A Validate failure (for example a
// negative duration, which pdx's own layer-1 validation does not reject)
// is wrapped with the "nex: config:" prefix.
//
// hostID both names the Nexen config's HostID and is the sole identity
// every request through this module authenticates as ("pdx:<hostID>"):
// pdx has no separate principal/token model of its own here, so an empty
// hostID is rejected outright rather than silently producing "pdx:" as a
// principal.
func buildOptions(hostID, dataDir string, n pdxconfig.NexConfig, shutdownBudget time.Duration) (nexen.Options, error) {
	if hostID == "" {
		return nexen.Options{}, fmt.Errorf("nex: host_id is empty; cannot derive principal")
	}

	leaseTTL, err := parseTimeout(n.Timeouts.LeaseTTL)
	if err != nil {
		return nexen.Options{}, fmt.Errorf("nex: config: timeouts.lease_ttl: %w", err)
	}
	interrupt, err := parseTimeout(n.Timeouts.Interrupt)
	if err != nil {
		return nexen.Options{}, fmt.Errorf("nex: config: timeouts.interrupt: %w", err)
	}
	turn, err := parseTimeout(n.Timeouts.Turn)
	if err != nil {
		return nexen.Options{}, fmt.Errorf("nex: config: timeouts.turn: %w", err)
	}

	cfg := &nexconfig.Config{
		DataDir:      filepath.Join(dataDir, "nex"),
		RepoRoots:    n.RepoRoots,
		ServiceRoots: n.ServiceRoots,
		CswapBin:     n.CswapBin,
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
		return nexen.Options{}, fmt.Errorf("nex: config: %w", err)
	}

	auth := api.AuthenticatorFunc(func(*http.Request) (string, error) {
		return "pdx:" + hostID, nil
	})

	return nexen.Options{
		Config:       cfg,
		Auth:         auth,
		PublicPrefix: RoutePrefix,
		ClaudeBin:    n.ClaudeBin,
	}, nil
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
