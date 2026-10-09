package main

import (
	"encoding/json"
	"net/http"
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

	// WB-2: the capability is announced, and the admin and a paired phone both reach the read routes
	info := callWith(t, c, http.MethodGet, "/api/info", "t", nil)
	require.Equal(t, http.StatusOK, info.Code)
	var body struct{ Capabilities []string }
	require.NoError(t, json.Unmarshal(info.Body.Bytes(), &body))
	assert.Contains(t, body.Capabilities, "workbook.v1")

	assert.Equal(t, http.StatusOK, callWith(t, c, http.MethodGet, "/api/workbook/entries", "t", nil).Code)
	assert.Equal(t, http.StatusNotFound, callWith(t, c, http.MethodGet, "/api/workbook/conversations/claude/nobody", "t", nil).Code)
	_, phone := mintAndUse(t, c, "00000000-0000-4000-8000-00000000000a", "p_0123456789ab")
	assert.Equal(t, http.StatusOK, callWith(t, c, http.MethodGet, "/api/workbook/entries", phone, nil).Code, "a paired phone reads the workbook")
	assert.Equal(t, http.StatusNotFound, callWith(t, c, http.MethodGet, "/api/workbook/conversations/claude/nobody", phone, nil).Code)
}
