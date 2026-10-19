package jobdb

import (
	"context"
	"strings"
)

// PerJob is the Manager as the job runner's PerJobDB (Create, Drop) and the loop's Sweeper (Sweep).
type PerJob struct{ m *Manager }

// NewPerJob wraps m.
func NewPerJob(m *Manager) *PerJob { return &PerJob{m: m} }

// Create makes the job's database and returns the connection settings under both names a task may read: DB_* and
// WP_TESTS_DB_* (at-inwin-plugin's phpunit config, whose guard also wants the WWD_JOB mark the runner sets). A task reads the
// set it needs; the other is unused. Both carry the same password.
func (p *PerJob) Create(ctx context.Context, key string) (map[string]string, error) {
	info, err := p.m.Create(ctx, key)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, set := range [][]string{info.Env(), info.WPTestsEnv()} {
		for _, kv := range set {
			k, v, _ := strings.Cut(kv, "=")
			env[k] = v
		}
	}
	return env, nil
}

// ValidUserHost says whether h may be a configured user host (see New): a plain host name or address that the routines accept.
func ValidUserHost(h string) bool { return cfgHostRe.MatchString(h) }

// Drop removes the job's database and users; one that is not there is not an error.
func (p *PerJob) Drop(ctx context.Context, key string) error { return p.m.Drop(ctx, key) }

// Sweep drops every per-job database whose job is not in live.
func (p *PerJob) Sweep(ctx context.Context, live []string) error {
	_, err := p.m.Sweep(ctx, live)
	return err
}
