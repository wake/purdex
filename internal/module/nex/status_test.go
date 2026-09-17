package nex

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen"
	nexconfig "lab.protype.tw/wake/nexen/config"
	"lab.protype.tw/wake/nexen/sandbox"

	pdxconfig "github.com/wake/purdex/internal/config"
)

// TestBuildStatus covers buildStatus directly (no Module needed): the not
// assembled / assembled and no error / assembled-with-error combinations,
// plus the ClaudeBin empty-vs-set rendering.
func TestBuildStatus(t *testing.T) {
	expanded := pdxconfig.NexConfig{
		RepoRoots:    []string{"/repo"},
		ServiceRoots: []string{"/svc"},
	}
	cfg := &nexconfig.Config{
		DataDir: "/data/nex",
		Sandbox: sandbox.Policy{
			MaxProfile:     "handoff",
			DefaultProfile: "trusted",
		},
		LeaseTTL:         nexconfig.Duration(90e9),  // 90s
		InterruptTimeout: nexconfig.Duration(5e9),   // 5s
		TurnTimeout:      nexconfig.Duration(600e9), // 10m
	}

	t.Run("not assembled, no error: effective nil, ready false", func(t *testing.T) {
		st := buildStatus(nil, nexen.Options{}, pdxconfig.NexConfig{}, "", false)
		assert.Equal(t, false, st["ready"])
		assert.Equal(t, "", st["init_error"])
		assert.Nil(t, st["effective"])
	})

	t.Run("init error recorded: ready false, effective nil", func(t *testing.T) {
		err := errors.New("nex: init: assembling engine: boom")
		st := buildStatus(err, nexen.Options{}, pdxconfig.NexConfig{}, "", false)
		assert.Equal(t, false, st["ready"])
		assert.Equal(t, err.Error(), st["init_error"])
		assert.Nil(t, st["effective"])
	})

	t.Run("assembled with claude_bin set: ready true, effective populated", func(t *testing.T) {
		opts := nexen.Options{Config: cfg, ClaudeBin: "/usr/local/bin/claude"}
		st := buildStatus(nil, opts, expanded, "/usr/local/bin", true)
		require.Equal(t, true, st["ready"])
		assert.Equal(t, "", st["init_error"])
		eff, ok := st["effective"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "/data/nex", eff["data_dir"])
		assert.Equal(t, "/usr/local/bin/claude", eff["claude_bin"])
		assert.Equal(t, "handoff", eff["max_profile"])
		assert.Equal(t, "trusted", eff["default_profile"])
		assert.Equal(t, []string{"/repo"}, eff["repo_roots"])
		assert.Equal(t, []string{"/svc"}, eff["service_roots"])
		assert.Equal(t, "/usr/local/bin", eff["path_prefix"])
		assert.Equal(t, "1m30s", eff["lease_ttl"])
		assert.Equal(t, "5s", eff["interrupt"])
		assert.Equal(t, "10m0s", eff["turn"])
	})

	t.Run("assembled with claude_bin empty (lazy PATH resolution)", func(t *testing.T) {
		opts := nexen.Options{Config: cfg, ClaudeBin: ""}
		st := buildStatus(nil, opts, expanded, "", true)
		eff, ok := st["effective"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "", eff["claude_bin"])
	})

	t.Run("initErr set but assembled somehow still reports not ready", func(t *testing.T) {
		// Defensive case: ready is initErr == nil && assembled, so a
		// non-nil initErr always wins even if assembled were true.
		err := errors.New("boom")
		opts := nexen.Options{Config: cfg}
		st := buildStatus(err, opts, expanded, "", true)
		assert.Equal(t, false, st["ready"])
	})
}
