package watchmod

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/watchwake"
)

// The contract fixtures of the wake settings API (spec section 5) are the answers of the real handler for a fixed host, a fixed clock and a
// fixed document, compared byte for byte: the App keeps byte copies (spa/src/lib/watch/__fixtures__, checked by fixtures-sync.guard.test.ts), so
// what they hold is the contract. To rewrite them after a change: WWD_UPDATE_FIXTURES=1 go test ./internal/module/watch -run WakeFixture.
// (The 403 is the device scope's, not this module's: cmd/pdx/device_scope_test.go checks watch_wake_settings_put_403.json.)
const wakeFixtureDir = "../../wwd/wire/testdata"

func checkWakeFixture(t *testing.T, name string, status int, rr interface {
	Result() *http.Response
}, body []byte) {
	t.Helper()
	require.Equal(t, status, rr.Result().StatusCode, string(body))
	var indented bytes.Buffer
	require.NoError(t, json.Indent(&indented, body, "", "  "))
	got := append(indented.Bytes(), '\n')
	path := filepath.Join(wakeFixtureDir, name)
	if os.Getenv("WWD_UPDATE_FIXTURES") != "" {
		require.NoError(t, os.WriteFile(path, got, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got), "%s differs from the handler's answer (WWD_UPDATE_FIXTURES=1 rewrites it)", name)
}

func TestWakeFixture_Get(t *testing.T) {
	r := newWakeRig(t, "mlab")
	rr := r.do(adminCtx, http.MethodGet, "")
	checkWakeFixture(t, "watch_wake_settings_get.json", http.StatusOK, rr, rr.Body.Bytes())
}

func TestWakeFixture_Put200(t *testing.T) {
	r := newWakeRig(t, "mlab")
	cur := r.get(t)
	s := watchwake.Default("Asia/Taipei")
	s.Rules.Patrol.Prompt = "巡檢時多看一下佇列。\n有疑問就問使用者。"
	s.Rules.Patrol.Times = []string{"09:00", "21:30"}
	s.Rules.Patrol.IntervalMin.Calm = 0
	s.Rules.WwdAlert.Levels = []string{"push", "watch"}
	s.Rules.WwdAlert.KindsOff = []string{"swap_growth"}
	rr := r.do(adminCtx, http.MethodPut, putBody(t, s, cur["etag"].(string)))
	checkWakeFixture(t, "watch_wake_settings_put_200.json", http.StatusOK, rr, rr.Body.Bytes())
}

// Three problems in one answer: a gap of 0, a time that is not HH:MM, and a field the patrol does not have (it has no switch).
func TestWakeFixture_Put400(t *testing.T) {
	r := newWakeRig(t, "mlab")
	cur := r.get(t)
	body := `{"settings":{"version":1,"enabled":true,"auto_start":true,"rules":{"patrol":{"prompt":"","interval_min":{"resting":10,"present":30,"away":30,"busy":10,"calm":15},"max_gap_hours":0,"times":["9:00"],"tz":"Asia/Taipei","enabled":false},"disk_low":{"enabled":true,"prompt":"","below_gb":14},"wwd_alert":{"enabled":true,"prompt":"","levels":["push","digest","watch"],"kinds_off":[]},"unattended_changed":{"enabled":true,"prompt":""},"user_back":{"enabled":true,"prompt":"","away_min":30}}},"expect_etag":"` + cur["etag"].(string) + `"}`
	rr := r.do(adminCtx, http.MethodPut, body)
	checkWakeFixture(t, "watch_wake_settings_put_400.json", http.StatusBadRequest, rr, rr.Body.Bytes())
}

func TestWakeFixture_Put400MissingExpect(t *testing.T) {
	r := newWakeRig(t, "mlab")
	rr := r.do(adminCtx, http.MethodPut, `{"settings":`+string(watchwake.Marshal(watchwake.Default("Asia/Taipei")))+`}`)
	checkWakeFixture(t, "watch_wake_settings_put_400_missing_expect.json", http.StatusBadRequest, rr, rr.Body.Bytes())
}

func TestWakeFixture_Put409(t *testing.T) {
	r := newWakeRig(t, "mlab")
	_ = r.get(t)
	rr := r.do(adminCtx, http.MethodPut, putBody(t, watchwake.Default("Asia/Taipei"), "0123456789abcdef"))
	checkWakeFixture(t, "watch_wake_settings_put_409.json", http.StatusConflict, rr, rr.Body.Bytes())
}

func TestWakeFixture_Put503(t *testing.T) {
	r := newWakeRig(t, "air26")
	rr := r.do(adminCtx, http.MethodPut, putBody(t, watchwake.Default("Asia/Taipei"), "0123456789abcdef"))
	checkWakeFixture(t, "watch_wake_settings_put_503.json", http.StatusServiceUnavailable, rr, rr.Body.Bytes())
}

// A host that is not the source answers with the defaults, stale, never fetched (WK-1b makes it follow the source).
func TestWakeFixture_GetNotSource(t *testing.T) {
	r := newWakeRig(t, "air26")
	rr := r.do(adminCtx, http.MethodGet, "")
	checkWakeFixture(t, "watch_wake_settings_get_stale.json", http.StatusOK, rr, rr.Body.Bytes())
}
