package gatemod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
)

var t0 = time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

func line(i int, class string) string {
	return fmt.Sprintf(`{"time":%q,"argv":["go","test"],"class":%q,"profile":"","outcome":"slot","wait_s":0,"exit":0,"duration_s":1,"cpu_s":2.5}`,
		t0.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), class)
}

func writeFile(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644))
}

func get(t *testing.T, m *Module, query string, admin bool) (int, DecisionsReply) {
	t.Helper()
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/gate/decisions"+query, nil)
	if admin {
		req = req.WithContext(devices.WithAdmin(req.Context()))
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var out DecisionsReply
	if rr.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&out))
	}
	return rr.Code, out
}

func rig(t *testing.T) (*Module, string) {
	dir := t.TempDir()
	t.Setenv("PDX_GATE_DIR", dir)
	return New(), dir
}

// Newest first across the two files: the current file's lines (last line first), then the rotated one.
func TestDecisions_NewestFirstAcrossTheRotation(t *testing.T) {
	m, dir := rig(t)
	writeFile(t, dir, "decisions.jsonl.1", line(0, "heavy"), line(1, "heavy"))
	writeFile(t, dir, "decisions.jsonl", line(2, "heavy"), line(3, "heavy"))
	code, r := get(t, m, "", true)
	require.Equal(t, 200, code)
	require.Len(t, r.Records, 4)
	for i, rec := range r.Records {
		assert.True(t, rec.Time.Equal(t0.Add(time.Duration(3-i)*time.Minute)), "record %d at %s", i, rec.Time)
	}
	require.NotNil(t, r.Records[0].CPUS)
	assert.Equal(t, 2.5, *r.Records[0].CPUS)
}

func TestDecisions_ClassSinceLimit(t *testing.T) {
	m, dir := rig(t)
	writeFile(t, dir, "decisions.jsonl", line(0, "heavy"), line(1, "light"), line(2, "heavy"), line(3, "light"), line(4, "heavy"))
	_, r := get(t, m, "?class=light", true)
	assert.Len(t, r.Records, 2)
	_, r = get(t, m, "?since="+t0.Add(2*time.Minute).Format(time.RFC3339), true)
	assert.Len(t, r.Records, 3, "since is inclusive")
	_, r = get(t, m, "?limit=2", true)
	require.Len(t, r.Records, 2)
	assert.True(t, r.Records[0].Time.Equal(t0.Add(4*time.Minute)), "the newest two")
	for _, q := range []string{"?class=x", "?since=yesterday", "?limit=0", "?limit=abc"} {
		code, _ := get(t, m, q, true)
		assert.Equal(t, 400, code, q)
	}
}

func TestDecisions_LimitIsCappedAtAThousand(t *testing.T) {
	m, dir := rig(t)
	ls := make([]string, 1100)
	for i := range ls {
		ls[i] = line(i, "heavy")
	}
	writeFile(t, dir, "decisions.jsonl", ls...)
	_, r := get(t, m, "?limit=5000", true)
	assert.Len(t, r.Records, 1000)
	_, r = get(t, m, "", true)
	assert.Len(t, r.Records, 200, "the default")
}

func TestDecisions_AMalformedLineIsSkippedAndCounted(t *testing.T) {
	m, dir := rig(t)
	writeFile(t, dir, "decisions.jsonl.1", `{"time":"2026-10-17T10:0`, line(1, "heavy"))
	writeFile(t, dir, "decisions.jsonl", line(2, "heavy"), `not json`, line(3, "heavy"))
	_, r := get(t, m, "", true)
	assert.Len(t, r.Records, 3)
	assert.Equal(t, 2, r.Skipped)
}

// WWD-9 9b-2: the assist keys (host, tried, transfer_s, run_s, reclaimed, assist_why) left Decision. A record an assist run wrote earlier
// still reads: the keys it no longer knows are ignored, not skipped as a malformed line.
func TestDecisions_ARecordWithTheOldAssistKeysStillReads(t *testing.T) {
	m, dir := rig(t)
	old := fmt.Sprintf(`{"time":%q,"argv":["go","test"],"class":"heavy","outcome":"assist","host":"air26","tried":"air26","transfer_s":2.5,"run_s":40,"reclaimed":true,"assist_why":"ssh failed","exit":0,"duration_s":1}`, t0.Format(time.RFC3339))
	writeFile(t, dir, "decisions.jsonl", old, line(1, "heavy"))
	_, r := get(t, m, "", true)
	require.Len(t, r.Records, 2)
	assert.Equal(t, 0, r.Skipped)
	assert.Equal(t, "assist", r.Records[1].Outcome)
}

func TestDecisions_NoFilesIsAnEmptyList(t *testing.T) {
	m, _ := rig(t)
	code, r := get(t, m, "", true)
	assert.Equal(t, 200, code)
	assert.NotNil(t, r.Records)
	assert.Empty(t, r.Records)
}

func TestDecisions_OnlyTheAdminToken(t *testing.T) {
	m, dir := rig(t)
	writeFile(t, dir, "decisions.jsonl", line(0, "heavy"))
	code, _ := get(t, m, "", false)
	assert.Equal(t, 403, code, "no admin mark")
	// a device token: the device principal, even if the admin mark were somehow there
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/gate/decisions", nil)
	ctx := devices.WithAdmin(devices.WithPrincipal(req.Context(), devices.Principal{ID: "d_1"}))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req.WithContext(ctx))
	assert.Equal(t, 403, rr.Code)
	code, _ = get(t, m, "", true)
	assert.Equal(t, 200, code)
}

// The real module registered in a real core, asked through GET /api/info (the lesson of fleet.v1).
func TestInfo_AnnouncesGateDecisionsV1ForTheRealModule(t *testing.T) {
	m, _ := rig(t)
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	c.AddModule(m)
	require.NoError(t, m.Init(c))
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	caps := func() []any {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		list, _ := body["capabilities"].([]any)
		return list
	}
	require.Contains(t, caps(), "gate.decisions.v1")
	require.NoError(t, m.Stop(context.Background()))
	require.NotContains(t, caps(), "gate.decisions.v1")
}
