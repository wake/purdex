package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dialogue module is mounted for real (Init opens dialogue.db in the data dir): /api/info announces dialogue.v1 and
// none of the workbook or journal capabilities, and its routes answer through the daemon's own mux.
func TestRegisterServeModules_DialogueAnnouncedAndServing(t *testing.T) {
	c, _ := realDaemonMux(t)

	rec := callWith(t, c, http.MethodGet, "/api/info", "t", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var info struct {
		Capabilities []string `json:"capabilities"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.Contains(t, info.Capabilities, "dialogue.v1")
	for _, gone := range []string{"workbook.v1", "workbook.v2", "journal.v1", "journal.sessions.v1", "journal.confirm.v1", "journal.members.v1"} {
		assert.NotContains(t, info.Capabilities, gone)
	}

	c.CfgMu.RLock()
	dir := c.Cfg.DataDir
	c.CfgMu.RUnlock()
	_, err := os.Stat(filepath.Join(dir, "dialogue.db"))
	assert.NoError(t, err, "the dialogue store is opened in the data dir")

	rec = callWith(t, c, http.MethodGet, "/api/dialogue/00000000-0000-4000-8000-0000000000a0/recaps", "t", nil)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = callWith(t, c, http.MethodGet, "/api/dialogue/nope/recaps", "t", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
