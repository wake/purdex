package watchmod

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/module/hostconfig"
)

// A GET with the lists WK-2 and WK-3 will fill: suppressed wake-ups from two hosts (one a failed patrol) and a patrol that failed. Nothing
// produces these yet, so the answer is built from the view's own types: the fixture is the shape the App reads, and this test is the proof
// that the Go types decode it and encode it back to the same document.
func TestWakeFixture_GetFull(t *testing.T) {
	r := newWakeRig(t, "mlab")
	d, err := r.m.readWakeSource()
	require.NoError(t, err)
	v := r.m.wakeView(d, "mlab")
	at := func(s string) time.Time {
		x, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return x
	}
	v.Suppressed = []wakeSuppressed{
		{At: at("2026-10-22T08:41:10+08:00"), Host: "air26", Reason: "wwd_alert", Why: "level_off", Detail: "air26：記憶體壓力偏高｜available 1.2 GB"},
		{At: at("2026-10-22T08:12:03+08:00"), Host: "mlab", Reason: "wwd_alert", Why: "kind_off", Detail: "mlab：swap 持續增加"},
		{At: at("2026-10-22T07:30:00+08:00"), Host: "air26", Reason: "patrol", Why: "patrol_failed", Detail: "targets_unreachable"},
	}
	okAt, failAt := at("2026-10-22T08:30:00+08:00"), at("2026-10-22T07:30:00+08:00")
	okHost, code, detail := "mlab", "targets_unreachable", "targets 全部連不上：mlab、air26"
	v.PatrolStatus = wakePatrolStatus{LastOKAt: &okAt, LastOKHost: &okHost, LastFailAt: &failAt, LastFailCode: &code, LastFailDetail: &detail}
	body, err := json.Marshal(v)
	require.NoError(t, err)
	checkWakeFixture(t, "watch_wake_settings_get_full.json", http.StatusOK, okRecorder{}, body)

	// what the file says decodes into the view's types and encodes back to the same document
	raw, err := os.ReadFile(filepath.Join(wakeFixtureDir, "watch_wake_settings_get_full.json"))
	require.NoError(t, err)
	var back wakeSettingsView
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&back))
	require.Len(t, back.Suppressed, 3)
	require.NotNil(t, back.PatrolStatus.LastFailCode)
	assert.Equal(t, "targets_unreachable", *back.PatrolStatus.LastFailCode)
	again, err := json.Marshal(back)
	require.NoError(t, err)
	assert.JSONEq(t, string(body), string(again))
}

// A document of a newer version, as the GET serves it: read-only, and the settings as they were stored.
func TestWakeFixture_GetReadonly(t *testing.T) {
	r := newWakeRig(t, "mlab")
	future := `{"version":2,"enabled":true,"auto_start":true,"rules":{"patrol":{"prompt":"","interval_min":{"resting":10,"present":30,"away":30,"busy":10,"calm":15},"max_gap_hours":24,"times":[],"tz":"Asia/Taipei","skip_holidays":true}}}`
	_, err := r.rows.Put(hostconfig.KeyWatchWake, []byte(future), 0)
	require.NoError(t, err)
	rr := r.do(adminCtx, http.MethodGet, "")
	checkWakeFixture(t, "watch_wake_settings_get_readonly.json", http.StatusOK, rr, rr.Body.Bytes())
	var v map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &v))
	assert.Equal(t, true, v["readonly"])
	assert.Equal(t, float64(2), v["settings"].(map[string]any)["version"])
}

// okRecorder stands in for a recorder whose status is 200, for a body that no handler served.
type okRecorder struct{}

func (okRecorder) Result() *http.Response { return &http.Response{StatusCode: http.StatusOK} }
