package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// pdx watch wake-settings (WK-4, docs/specs/2026-10-21-watcher-wake-settings-spec.md §5 and §7): the CLI against the contract fixtures WK-1a put
// in internal/wwd/wire/testdata, served by an httptest daemon.

func wakeFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "wwd", "wire", "testdata", "watch_wake_settings_"+name+".json"))
	require.NoError(t, err)
	return b
}

// fakeWakeDaemon answers GET /api/watch/wake-settings with get and PUT with putStatus/put, and records what came.
type fakeWakeDaemon struct {
	t          *testing.T
	get        []byte
	getStatus  int
	putStatus  int
	put        []byte
	gets, puts int
	auth       string
	putBody    map[string]json.RawMessage
	other      []string // requests for any other path, as "METHOD path"
}

func (f *fakeWakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/watch/wake-settings" {
		f.other = append(f.other, r.Method+" "+r.URL.Path)
		http.NotFound(w, r)
		return
	}
	f.auth = r.Header.Get("Authorization")
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		f.gets++
		if f.getStatus != 0 {
			w.WriteHeader(f.getStatus)
		}
		_, _ = w.Write(f.get)
	case http.MethodPut:
		f.puts++
		raw, _ := io.ReadAll(r.Body)
		f.putBody = nil
		require.NoError(f.t, json.Unmarshal(raw, &f.putBody))
		w.WriteHeader(f.putStatus)
		_, _ = w.Write(f.put)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func newWakeDaemon(t *testing.T) (*fakeWakeDaemon, string) {
	t.Helper()
	f := &fakeWakeDaemon{t: t, get: wakeFixture(t, "get"), putStatus: http.StatusOK, put: wakeFixture(t, "put_200")}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, watchCfgFile(t, srv, "tok")
}

func runWake(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := watchMain(append([]string{"wake-settings"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

// putSettings is the settings object the CLI sent, decoded to a generic tree so a test can compare it with the fixture's.
func putSettings(t *testing.T, f *fakeWakeDaemon) map[string]any {
	t.Helper()
	require.Equal(t, 1, f.puts, "one PUT")
	var s map[string]any
	require.NoError(t, json.Unmarshal(f.putBody["settings"], &s))
	return s
}

func fixtureSettings(t *testing.T, name string) map[string]any {
	t.Helper()
	var v struct{ Settings map[string]any }
	require.NoError(t, json.Unmarshal(wakeFixture(t, name), &v))
	return v.Settings
}

func at(t *testing.T, tree map[string]any, path ...string) any {
	t.Helper()
	var cur any = tree
	for _, p := range path {
		m, ok := cur.(map[string]any)
		require.True(t, ok, "%v is not an object at %s", cur, p)
		cur = m[p]
	}
	return cur
}

// ---- show ----

func TestWakeSettingsShow_SaysWhatTheDocumentAndTheSourceAre(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	code, out, errb := runWake("--config", cfg)
	require.Equal(t, 0, code, errb)
	require.Equal(t, "Bearer tok", f.auth)
	for _, want := range []string{
		"正本：mlab", "revision 1", "064f942514dbe8fa",
		"巡檢", "不能關", "休息 10", "在電腦前 30", "吃緊 10", "平穩 15", "最長不巡上限 24", "Asia/Taipei",
		"disk_low", "14", "wwd_alert", "push", "digest", "watch", "unattended_changed", "user_back", "30",
		"stale：否", "唯讀：否",
	} {
		require.Contains(t, out, want, out)
	}
	require.Empty(t, f.other)
}

func TestWakeSettingsShow_SubcommandShowIsTheSame(t *testing.T) {
	_, cfg := newWakeDaemon(t)
	_, a, _ := runWake("--config", cfg)
	_, b, _ := runWake("show", "--config", cfg)
	require.Equal(t, a, b)
}

func TestWakeSettingsShow_JSONIsTheDaemonsAnswerAsItCame(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	code, out, errb := runWake("show", "--json", "--config", cfg)
	require.Equal(t, 0, code, errb)
	require.JSONEq(t, string(f.get), out)
}

func TestWakeSettingsShow_AStaleCopyOfTheSourceIsSaid(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	f.get = wakeFixture(t, "get_stale")
	code, out, errb := runWake("--config", cfg)
	require.Equal(t, 0, code, errb)
	require.Contains(t, out, "stale：是")
	require.Contains(t, out, "從未取到")
	require.Contains(t, out, "mlab")
}

func TestWakeSettingsShow_PatrolStatusAndTheSuppressedAreListed(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	var v map[string]any
	require.NoError(t, json.Unmarshal(f.get, &v))
	v["patrol_status"] = map[string]any{"last_ok_at": "2026-10-22T08:00:00+08:00", "last_ok_host": "mlab", "last_fail_at": "2026-10-22T08:30:00+08:00", "last_fail_code": "start_failed", "last_fail_detail": "no agent command"}
	v["suppressed"] = []any{map[string]any{"at": "2026-10-22T08:40:00+08:00", "host": "air26", "reason": "wwd_alert", "why": "level_off", "detail": "air26：記憶體壓力偏高"}}
	f.get, _ = json.Marshal(v)
	code, out, errb := runWake("--config", cfg)
	require.Equal(t, 0, code, errb)
	for _, want := range []string{"start_failed", "no agent command", "air26", "level_off", "wwd_alert", "記憶體壓力偏高"} {
		require.Contains(t, out, want, out)
	}
}

// A settings object whose fields have the wrong types is not a document either: show would print its zero values as if they were the settings
// (a "no" for an enabled that came as the string "yes"), and a PUT 200 of that shape is not a confirmed write (codex critic on #3956).
func TestWakeSettings_AnObjectWithFieldsOfTheWrongTypeIsNotADocument(t *testing.T) {
	for name, settings := range map[string]string{
		"enabled as a string":    `{"enabled":"yes"}`,
		"a number for a rule":    `{"rules":{"disk_low":5}}`,
		"below_gb as a string":   `{"rules":{"disk_low":{"below_gb":"14"}}}`,
		"levels as a string":     `{"rules":{"wwd_alert":{"levels":"push"}}}`,
		"times as numbers":       `{"rules":{"patrol":{"times":[8,9]}}}`,
		"interval_min as a list": `{"rules":{"patrol":{"interval_min":[1,2]}}}`,
	} {
		doc := `{"settings":` + settings + `,"etag":"x","revision":2}`
		// the read
		f, cfg := newWakeDaemon(t)
		f.get = []byte(doc)
		code, out, errb := runWake("--config", cfg)
		require.Equal(t, 1, code, "read %s", name)
		require.Empty(t, out, "read %s: nothing is shown as the settings", name)
		require.NotEmpty(t, errb, name)
		// the 200 of a write
		f, cfg = newWakeDaemon(t)
		f.put = []byte(doc)
		code, out, errb = runWake("set", "enabled=false", "--config", cfg)
		require.Equal(t, 1, code, "write %s", name)
		require.Empty(t, out, "write %s", name)
		require.Contains(t, errb, "無法確認", name)
	}
}

// The same check holds for the read: a document that is not an object is not shown, and `set` does not build on it.
func TestWakeSettings_AReadThatIsNotADocumentIsRefused(t *testing.T) {
	for name, body := range map[string]string{"null": `{"settings":null,"etag":"x"}`, "array": `{"settings":[],"etag":"x"}`, "no etag": `{"settings":{}}`, "html": "<html>"} {
		f, cfg := newWakeDaemon(t)
		f.get = []byte(body)
		for _, args := range [][]string{{"--config", cfg}, {"set", "enabled=false", "--config", cfg}} {
			code, out, errb := runWake(args...)
			require.Equal(t, 1, code, "%s %v", name, args)
			require.Empty(t, out, "%s %v", name, args)
			require.NotEmpty(t, errb, "%s %v", name, args)
		}
		require.Equal(t, 0, f.puts, name)
	}
}

func TestWakeSettingsShow_ADaemonThatCannotBeReadIsAFailure(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	f.getStatus, f.get = http.StatusServiceUnavailable, []byte(`{"error":"unavailable","detail":"the host config module is not running"}`)
	code, _, errb := runWake("--config", cfg)
	require.Equal(t, 1, code)
	require.Contains(t, errb, "unavailable")
}

// ---- set: reading, changing, writing back ----

func TestWakeSettingsSet_ReadsChangesAndWritesBackWithTheEtagItRead(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	code, out, errb := runWake("set", "rules.disk_low.below_gb=20", "--config", cfg)
	require.Equal(t, 0, code, errb)
	require.Equal(t, 1, f.gets, "read first")
	require.JSONEq(t, `"064f942514dbe8fa"`, string(f.putBody["expect_etag"]), "the etag of the read goes with the write")
	got, want := putSettings(t, f), fixtureSettings(t, "get")
	require.EqualValues(t, 20, at(t, got, "rules", "disk_low", "below_gb"))
	// everything else is what was read, whole (a PUT replaces the document)
	want["rules"].(map[string]any)["disk_low"].(map[string]any)["below_gb"] = float64(20)
	require.Equal(t, want, got)
	require.Contains(t, out, "revision 2")
}

func TestWakeSettingsSet_SeveralPathsAndEveryKindOfValue(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	code, _, errb := runWake("set",
		"enabled=false",
		"auto_start=false",
		"rules.patrol.interval_min.present=45",
		"rules.patrol.interval_min.resting=0",
		"rules.patrol.max_gap_hours=48",
		"rules.patrol.times=08:30,21:00",
		"rules.patrol.tz=America/New_York",
		"rules.wwd_alert.levels=push,watch",
		"rules.wwd_alert.kinds_off=thrash,orphans",
		"rules.user_back.enabled=true",
		"rules.user_back.prompt=歡迎回來",
		"--config", cfg)
	require.Equal(t, 0, code, errb)
	got := putSettings(t, f)
	require.Equal(t, false, at(t, got, "enabled"))
	require.Equal(t, false, at(t, got, "auto_start"))
	require.EqualValues(t, 45, at(t, got, "rules", "patrol", "interval_min", "present"))
	require.EqualValues(t, 0, at(t, got, "rules", "patrol", "interval_min", "resting"))
	require.EqualValues(t, 48, at(t, got, "rules", "patrol", "max_gap_hours"))
	require.Equal(t, []any{"08:30", "21:00"}, at(t, got, "rules", "patrol", "times"))
	require.Equal(t, "America/New_York", at(t, got, "rules", "patrol", "tz"))
	require.Equal(t, []any{"push", "watch"}, at(t, got, "rules", "wwd_alert", "levels"))
	require.Equal(t, []any{"thrash", "orphans"}, at(t, got, "rules", "wwd_alert", "kinds_off"))
	require.Equal(t, "歡迎回來", at(t, got, "rules", "user_back", "prompt"))
	require.EqualValues(t, 30, at(t, got, "rules", "user_back", "away_min"), "what was not named is what it was")
}

func TestWakeSettingsSet_AnEmptyValueMakesAnEmptyArrayAndAnEmptyString(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	code, _, errb := runWake("set", "rules.wwd_alert.levels=", "rules.patrol.prompt=", "--config", cfg)
	require.Equal(t, 0, code, errb)
	got := putSettings(t, f)
	require.Equal(t, []any{}, at(t, got, "rules", "wwd_alert", "levels"), "[] and not null")
	require.Equal(t, "", at(t, got, "rules", "patrol", "prompt"))
}

func TestWakeSettingsSet_AnElementOfAnArrayByIndex(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	var v map[string]any
	require.NoError(t, json.Unmarshal(f.get, &v))
	v["settings"].(map[string]any)["rules"].(map[string]any)["patrol"].(map[string]any)["times"] = []any{"08:00", "20:00"}
	f.get, _ = json.Marshal(v)
	code, _, errb := runWake("set", "rules.patrol.times[1]=22:15", "--config", cfg)
	require.Equal(t, 0, code, errb)
	require.Equal(t, []any{"08:00", "22:15"}, at(t, putSettings(t, f), "rules", "patrol", "times"))
}

func TestWakeSettingsSet_AFileFillsAMultiLinePrompt(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	prompt := "第一行\n\t縮排的第二行\n第三行\n"
	p := filepath.Join(t.TempDir(), "prompt.txt")
	require.NoError(t, os.WriteFile(p, []byte(prompt), 0o600))
	code, _, errb := runWake("set", "rules.patrol.prompt=@"+p, "--config", cfg)
	require.Equal(t, 0, code, errb)
	require.Equal(t, prompt, at(t, putSettings(t, f), "rules", "patrol", "prompt"), "as the file has it")
}

func TestWakeSettingsSet_ADoubleAtIsALiteralAt(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	code, _, errb := runWake("set", "rules.disk_low.prompt=@@mlab 注意", "--config", cfg)
	require.Equal(t, 0, code, errb)
	require.Equal(t, "@mlab 注意", at(t, putSettings(t, f), "rules", "disk_low", "prompt"))
}

func TestWakeSettingsSet_JSONPrintsTheDaemonsAnswer(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	code, out, errb := runWake("set", "enabled=false", "--json", "--config", cfg)
	require.Equal(t, 0, code, errb)
	require.JSONEq(t, string(f.put), out)
}

// ---- set: what is refused before anything is sent ----

func TestWakeSettingsSet_WhatIsWrongHereNeverReachesTheDaemon(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.txt")
	for name, args := range map[string][]string{
		"no path at all":                    {"set"},
		"no equals":                         {"set", "enabled"},
		"an unknown path":                   {"set", "rules.disk_low.bellow_gb=3"},
		"an unknown rule":                   {"set", "rules.nothing.enabled=true"},
		"the patrol has no switch":          {"set", "rules.patrol.enabled=false"},
		"a path that is a whole object":     {"set", "rules.disk_low=3"},
		"a path through a leaf":             {"set", "enabled.x=1"},
		"a bool that is not":                {"set", "enabled=maybe"},
		"a number that is not":              {"set", "rules.disk_low.below_gb=lots"},
		"a number with a fraction":          {"set", "rules.disk_low.below_gb=1.5"},
		"an index on something that is not": {"set", "rules.disk_low.below_gb[0]=1"},
		"an index past the end":             {"set", "rules.patrol.times[3]=08:00"},
		"a bad index":                       {"set", "rules.patrol.times[x]=08:00"},
		"an unclosed index":                 {"set", "rules.patrol.times[0=08:00"},
		"a file that is not there":          {"set", "rules.patrol.prompt=@" + missing},
	} {
		f, cfg := newWakeDaemon(t)
		code, _, errb := runWake(append(args, "--config", cfg)...)
		require.Equal(t, 2, code, "%s: %s", name, errb)
		require.NotEmpty(t, errb, name)
		require.Equal(t, 0, f.puts, "%s: nothing is written", name)
	}
}

func TestWakeSettingsSet_AnUnknownPathNamesIt(t *testing.T) {
	_, cfg := newWakeDaemon(t)
	_, _, errb := runWake("set", "rules.disk_low.bellow_gb=3", "--config", cfg)
	require.Contains(t, errb, "rules.disk_low.bellow_gb")
	require.Contains(t, errb, "no such path", "it says the path is not in the document, not some later complaint about its type")
	_, _, errb = runWake("set", "rules.nothing.enabled=true", "--config", cfg)
	require.Contains(t, errb, "no such path")
}

// ---- set: what the daemon answers ----

func TestWakeSettingsSet_AConflictIsExitThreeAndSaysToRunItAgain(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	f.putStatus, f.put = http.StatusConflict, wakeFixture(t, "put_409")
	code, out, errb := runWake("set", "enabled=false", "--config", cfg)
	require.Equal(t, 3, code)
	require.Contains(t, errb, "有人先改了")
	require.Contains(t, errb, "重跑")
	require.Empty(t, out, "nothing is reported as done")
}

func TestWakeSettingsSet_InvalidIsExitFourWithEveryFieldNamed(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	f.putStatus, f.put = http.StatusBadRequest, wakeFixture(t, "put_400")
	code, _, errb := runWake("set", "enabled=false", "--config", cfg)
	require.Equal(t, 4, code)
	for _, want := range []string{
		"rules.patrol.enabled", "unknown_field", "is not a field of this document",
		"rules.patrol.max_gap_hours", "out_of_range", "must be between 1 and 72",
		"rules.patrol.times[0]", "bad_time",
	} {
		require.Contains(t, errb, want, errb)
	}
}

func TestWakeSettingsSet_AMissingExpectIsExitFour(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	f.putStatus, f.put = http.StatusBadRequest, wakeFixture(t, "put_400_missing_expect")
	code, _, errb := runWake("set", "enabled=false", "--config", cfg)
	require.Equal(t, 4, code)
	require.Contains(t, errb, "missing_expect")
}

func TestWakeSettingsSet_ASourceThatCannotBeReachedNamesIt(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	f.putStatus, f.put = http.StatusServiceUnavailable, wakeFixture(t, "put_503")
	code, _, errb := runWake("set", "enabled=false", "--config", cfg)
	require.Equal(t, 1, code)
	require.Contains(t, errb, "mlab")
	require.Contains(t, errb, "正本連不上")
}

func TestWakeSettingsSet_ADeviceTokenIsToldItReadsOnly(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	f.putStatus, f.put = http.StatusForbidden, wakeFixture(t, "put_403")
	code, _, errb := runWake("set", "enabled=false", "--config", cfg)
	require.Equal(t, 1, code)
	require.Contains(t, errb, "唯讀")
	require.Contains(t, errb, "device_forbidden")
}

func TestWakeSettingsSet_ADaemonThatCannotBeReadIsNotWrittenTo(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	f.getStatus, f.get = http.StatusServiceUnavailable, []byte(`{"error":"unavailable"}`)
	code, _, _ := runWake("set", "enabled=false", "--config", cfg)
	require.Equal(t, 1, code)
	require.Equal(t, 0, f.puts)
}

// ---- codex R2 on #3956 ----

// A file bigger than the daemon would take is refused here, before it is read into memory and before anything is sent.
func TestWakeSettingsSet_AFileOverTheLimitIsRefusedBeforeItIsRead(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	p := filepath.Join(t.TempDir(), "big.txt")
	require.NoError(t, os.WriteFile(p, bytes.Repeat([]byte("x"), wakeFileMax+1), 0o600))
	code, _, errb := runWake("set", "rules.patrol.prompt=@"+p, "--config", cfg)
	require.Equal(t, 2, code, errb)
	require.Contains(t, errb, "too large")
	require.Equal(t, 0, f.puts)
	// the limit itself still goes (the daemon decides about the 2000 bytes of a prompt)
	require.NoError(t, os.WriteFile(p, bytes.Repeat([]byte("x"), wakeFileMax), 0o600))
	code, _, errb = runWake("set", "rules.patrol.prompt=@"+p, "--config", cfg)
	require.Equal(t, 0, code, errb)
}

// Whatever the daemon sends is printed through sanitizeCell, the fixed times too.
func TestWakeSettingsShow_NothingTheDaemonSendsReachesTheTerminalRaw(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	var v map[string]any
	require.NoError(t, json.Unmarshal(f.get, &v))
	patrol := v["settings"].(map[string]any)["rules"].(map[string]any)["patrol"].(map[string]any)
	patrol["times"] = []any{"08:00\x1b]0;pwned\x07", "09:00\nfake line"}
	patrol["tz"] = "UTC\x1b[31m"
	v["source"] = "mlab\x1b[2J"
	f.get, _ = json.Marshal(v)
	code, out, errb := runWake("--config", cfg)
	require.Equal(t, 0, code, errb)
	require.NotContains(t, out, "\x1b")
	require.NotContains(t, out, "\x07")
	require.NotContains(t, out, "\nfake line")
}

// A 200 that is not a settings document is not a success: the write may have happened, and the CLI says it cannot tell.
func TestWakeSettingsSet_A200ThatIsNotADocumentIsNotConfirmed(t *testing.T) {
	for name, body := range map[string]string{
		"html": "<html>proxy</html>", "empty": "", "no etag": `{"settings":{}}`, "no settings": `{"etag":"x","revision":3}`,
		"settings null": `{"settings":null,"etag":"x"}`, "settings array": `{"settings":[],"etag":"x"}`,
		"settings string": `{"settings":"x","etag":"x"}`, "settings number": `{"settings":5,"etag":"x"}`,
	} {
		f, cfg := newWakeDaemon(t)
		f.put = []byte(body)
		for _, extra := range [][]string{nil, {"--json"}} {
			code, out, errb := runWake(append([]string{"set", "enabled=false", "--config", cfg}, extra...)...)
			require.Equal(t, 1, code, "%s %v", name, extra)
			require.Empty(t, out, "%s %v: nothing is printed as the daemon's answer", name, extra)
			require.Contains(t, errb, "無法確認", "%s %v", name, extra)
		}
	}
}

// ---- dispatch ----

// `pdx watch wake` is the manual wake-up and stays so (the name wake-settings must not swallow it).
func TestWatchWake_StillWakesByHandAndNeverTouchesTheSettings(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"host":"mlab","state":"awake","session_id":"s1"}`))
	}))
	defer srv.Close()
	var out, errb bytes.Buffer
	require.Equal(t, 0, watchMain([]string{"wake", "--prompt", "看一下", "--config", watchCfgFile(t, srv, "tok")}, &out, &errb), errb.String())
	require.Equal(t, []string{"POST /api/watch/wake"}, paths)
}

func TestWatchUsage_ListsWakeSettingsAndKeepsWake(t *testing.T) {
	require.Contains(t, watchUsage, "pdx watch wake-settings")
	require.Contains(t, watchUsage, "pdx watch wake [--prompt")
	var out, errb bytes.Buffer
	require.Equal(t, 2, watchMain(nil, &out, &errb))
	require.True(t, strings.Contains(errb.String(), "wake-settings"))
}

func TestWakeSettings_AnUnknownSubcommandIsAUsageError(t *testing.T) {
	f, cfg := newWakeDaemon(t)
	code, _, errb := runWake("frobnicate", "--config", cfg)
	require.Equal(t, 2, code)
	require.Contains(t, errb, "wake-settings")
	require.Equal(t, 0, f.gets+f.puts)
}
