package nex

import (
	"time"

	"lab.protype.tw/wake/nexen"

	pdxconfig "github.com/wake/purdex/internal/config"
)

// Status is what GET /api/info reports under "nex" beyond configured/mounted
// (spec §4.4.2): whether the engine is serving, why not, and the expanded
// config it was assembled with. Read by core through core.StatusReporter so
// core never imports this package.
func (m *Module) Status() map[string]any {
	return buildStatus(m.initErr, m.opts, m.expanded, m.pathPrefix, m.sys.handler != nil)
}

// buildStatus is Status()'s pure core, taking every input as a parameter so
// it can be table-tested without assembling a Module.
func buildStatus(initErr error, opts nexen.Options, expanded pdxconfig.NexConfig, pathPrefix string, assembled bool) map[string]any {
	st := map[string]any{"ready": initErr == nil && assembled, "init_error": ""}
	if initErr != nil {
		st["init_error"] = initErr.Error()
	}
	if !assembled || opts.Config == nil {
		st["effective"] = nil
		return st
	}
	cfg := opts.Config
	st["effective"] = map[string]any{
		"data_dir":        cfg.DataDir,
		"claude_bin":      opts.ClaudeBin, // "" = Nexen resolves lazily from PATH
		"max_profile":     cfg.Sandbox.MaxProfile,
		"default_profile": cfg.Sandbox.DefaultProfile,
		"repo_roots":      expanded.RepoRoots,
		"service_roots":   expanded.ServiceRoots,
		"path_prefix":     pathPrefix,
		"lease_ttl":       time.Duration(cfg.LeaseTTL).String(), // nexconfig.Duration has no String()
		"interrupt":       time.Duration(cfg.InterruptTimeout).String(),
		"turn":            time.Duration(cfg.TurnTimeout).String(),
	}
	return st
}
