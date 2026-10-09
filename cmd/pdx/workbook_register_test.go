package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// WB-1a-ii: the workbook module is always mounted, its dependencies resolve, and its Status is readable through the
// core (what moduleReady reads for the capability that WB-2 adds).
func TestRegisterServeModules_WorkbookMounted(t *testing.T) {
	dataDir := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dataDir, Token: "t"}})
	require.NoError(t, registerServeModules(c, nil, nil))
	assert.True(t, c.Mounted("workbook"))
	require.NoError(t, c.InitModules())

	st, ok := c.ModuleStatus("workbook")
	require.True(t, ok, "workbook has no Status")
	assert.Equal(t, true, st["ready"])

	fi, err := os.Stat(filepath.Join(dataDir, "workbook.db"))
	require.NoError(t, err)
	assert.Zero(t, fi.Mode().Perm()&0o077, "workbook.db must be owner-only, is %o", fi.Mode().Perm())
}
