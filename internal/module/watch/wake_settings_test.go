package watchmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/watchsettings"
	"github.com/wake/purdex/internal/watchwake"
)

const wakeAt = "2026-10-22T09:00:00+08:00"

type wakeRig struct {
	m      *Module
	mux    *http.ServeMux
	rows   hostconfig.WakeRowStore
	events []core.HostEvent
	mu     sync.Mutex
	old    watchsettings.Settings // the old `watch` row the migration reads
}

// newWakeRig is a watch module with a real hostconfig behind it. alias is this host's own alias; the source is mlab unless the config says
// otherwise.
func newWakeRig(t *testing.T, alias string) *wakeRig {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), HostID: "82b11c521df16c12"}
	cfg.Peers.Alias = alias
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	hc := hostconfig.New()
	require.NoError(t, hc.Init(c))
	t.Cleanup(func() { _ = hc.Stop(context.Background()) })
	svc, ok := c.Registry.Get(hostconfig.WakeRowStoreKey)
	require.True(t, ok)

	r := &wakeRig{rows: svc.(hostconfig.WakeRowStore), old: watchsettings.Default()}
	m := New()
	m.logf = func(string, ...any) {}
	require.NoError(t, m.Init(c))
	m.settings = func() (watchsettings.Settings, error) { return r.old, nil }
	m.now = func() time.Time { t, _ := time.Parse(time.RFC3339, wakeAt); return t }
	m.localTZ = func() string { return "Asia/Taipei" }
	m.emit = func(ev core.HostEvent) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, ev)
	}
	m.manualDelivery = true
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	r.m, r.mux = m, mux
	return r
}

func (r *wakeRig) do(ctx context.Context, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/watch/wake-settings", strings.NewReader(body)).WithContext(ctx)
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, req)
	return rr
}

func (r *wakeRig) get(t *testing.T) map[string]any {
	t.Helper()
	rr := r.do(adminCtx, http.MethodGet, "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var v map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &v))
	return v
}

func (r *wakeRig) events_() []core.HostEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.HostEvent(nil), r.events...)
}

// putBody is a PUT body for a document with expect_etag.
func putBody(t *testing.T, s watchwake.Settings, etag string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"settings": json.RawMessage(watchwake.Marshal(s)), "expect_etag": etag})
	require.NoError(t, err)
	return string(b)
}

func TestWakeSettings_FirstReadMigratesTheWatchRowOnceAndLeavesEnabledBehind(t *testing.T) {
	r := newWakeRig(t, "mlab")
	r.old.Enabled = false // the old switch of the patrol: it must not turn anything off
	r.old.AutoWake = false
	r.old.PatrolPresentMin, r.old.PatrolBusyMin, r.old.PatrolCalmMin, r.old.PatrolRestMin, r.old.DiskLowGB = 41, 7, 19, 12, 22

	v := r.get(t)
	assert.Equal(t, float64(1), v["revision"])
	assert.Equal(t, "mlab", v["source"])
	assert.Equal(t, "82b11c521df16c12", v["source_id"])
	assert.Equal(t, true, v["is_source"])
	assert.Equal(t, false, v["stale"])
	assert.Equal(t, false, v["readonly"])
	assert.Equal(t, wakeAt, v["fetched_at"])
	s := v["settings"].(map[string]any)
	assert.Equal(t, true, s["enabled"], "watch.enabled did not travel")
	assert.Equal(t, false, s["auto_start"], "auto_wake did")
	p := s["rules"].(map[string]any)["patrol"].(map[string]any)
	assert.Equal(t, map[string]any{"resting": 12., "present": 41., "away": 41., "busy": 7., "calm": 19.}, p["interval_min"])
	assert.Equal(t, "Asia/Taipei", p["tz"])
	assert.Equal(t, 22., s["rules"].(map[string]any)["disk_low"].(map[string]any)["below_gb"])
	assert.Len(t, v["known_kinds"], len(watchwake.KnownKinds))
	assert.Equal(t, []any{}, v["suppressed"])
	assert.Equal(t, map[string]any{"last_ok_at": nil, "last_ok_host": nil, "last_fail_at": nil, "last_fail_code": nil, "last_fail_detail": nil}, v["patrol_status"])

	// once: the old row changing afterwards changes nothing, and the revision stays
	r.old.DiskLowGB = 99
	v2 := r.get(t)
	assert.Equal(t, v["etag"], v2["etag"])
	assert.Equal(t, float64(1), v2["revision"])
	assert.Empty(t, r.events_(), "a migration is not a change of the settings")
}

func TestWakeSettings_ManyReadersOnAFreshHostMigrateOnce(t *testing.T) {
	r := newWakeRig(t, "mlab")
	var wg sync.WaitGroup
	etags := make([]any, 12)
	for i := range etags {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rr := r.do(adminCtx, http.MethodGet, "")
			var v map[string]any
			_ = json.Unmarshal(rr.Body.Bytes(), &v)
			etags[i] = v["etag"]
			if v["revision"] != float64(1) {
				t.Errorf("revision %v", v["revision"])
			}
		}(i)
	}
	wg.Wait()
	for _, e := range etags {
		assert.Equal(t, etags[0], e)
	}
	_, rev, ok, _ := r.rows.Get(hostconfig.KeyWatchWake)
	assert.True(t, ok)
	assert.Equal(t, int64(1), rev)
}

func TestWakeSettings_PutReplacesTheWholeDocumentAndAnnouncesIt(t *testing.T) {
	r := newWakeRig(t, "mlab")
	cur := r.get(t)
	s := watchwake.Default("Asia/Taipei")
	s.Rules.Patrol.Times = []string{"21:30", "09:00"} // unsorted on purpose
	s.Rules.Patrol.Prompt = "第一行\n第二行"
	s.Rules.WwdAlert.KindsOff = []string{"swap_growth"}
	rr := r.do(adminCtx, http.MethodPut, putBody(t, s, cur["etag"].(string)))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var put map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &put))
	assert.Equal(t, float64(2), put["revision"])
	got := r.get(t)
	assert.Equal(t, put["etag"], got["etag"], "what the PUT answers is what the GET answers next")
	times := got["settings"].(map[string]any)["rules"].(map[string]any)["patrol"].(map[string]any)["times"]
	assert.Equal(t, []any{"09:00", "21:30"}, times, "stored sorted")

	evs := r.events_()
	require.Len(t, evs, 1)
	assert.Equal(t, "watch.wake_settings.changed", evs[0].Type)
	assert.JSONEq(t, `{"revision":2,"etag":"`+put["etag"].(string)+`","source":"mlab"}`, evs[0].Value)
}

// The etag is of the normalized document: a body with its keys in another order, and a body that sorts to the same document, get the etag
// the GET gives.
func TestWakeSettings_TheEtagDoesNotDependOnTheOrderOfTheKeysOfTheBody(t *testing.T) {
	r := newWakeRig(t, "mlab")
	cur := r.get(t)
	reordered := `{"expect_etag":"` + cur["etag"].(string) + `","settings":{"rules":{"user_back":{"away_min":30,"prompt":"","enabled":true},"unattended_changed":{"prompt":"","enabled":true},"wwd_alert":{"kinds_off":[],"levels":["push","digest","watch"],"prompt":"","enabled":true},"disk_low":{"below_gb":14,"prompt":"","enabled":true},"patrol":{"tz":"Asia/Taipei","times":["10:00","09:00"],"max_gap_hours":24,"interval_min":{"calm":15,"busy":10,"away":30,"present":30,"resting":10},"prompt":""}},"auto_start":true,"enabled":true,"version":1}}`
	rr := r.do(adminCtx, http.MethodPut, reordered)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var put map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &put))

	same := watchwake.Default("Asia/Taipei")
	same.Rules.Patrol.Times = []string{"09:00", "10:00"}
	assert.Equal(t, watchwake.Etag(same), put["etag"])
	assert.Equal(t, put["etag"], r.get(t)["etag"])
}

func TestWakeSettings_PutRefusals(t *testing.T) {
	r := newWakeRig(t, "mlab")
	cur := r.get(t)
	etag := cur["etag"].(string)
	good := watchwake.Default("Asia/Taipei")

	// no expect_etag
	rr := r.do(adminCtx, http.MethodPut, `{"settings":`+string(watchwake.Marshal(good))+`}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.JSONEq(t, `{"error":"missing_expect"}`, rr.Body.String())

	// not JSON, and an unknown top-level field
	assert.Equal(t, http.StatusBadRequest, r.do(adminCtx, http.MethodPut, `{`).Code)
	assert.Equal(t, http.StatusBadRequest, r.do(adminCtx, http.MethodPut, `{"settings":{},"expect_etag":"x","extra":1}`).Code)

	// every problem at once
	bad := `{"settings":{"version":1,"enabled":"yes","auto_start":true,"rules":{"patrol":{"prompt":"","interval_min":{"resting":10,"present":30,"away":30,"busy":10,"calm":15},"max_gap_hours":0,"times":["9:00"],"tz":"Asia/Taipei","enabled":false},"disk_low":{"enabled":true,"prompt":"","below_gb":14},"wwd_alert":{"enabled":true,"prompt":"","levels":["push"],"kinds_off":[]},"unattended_changed":{"enabled":true,"prompt":""},"user_back":{"enabled":true,"prompt":"","away_min":30}}},"expect_etag":"` + etag + `"}`
	rr = r.do(adminCtx, http.MethodPut, bad)
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	var inv struct {
		Error  string                 `json:"error"`
		Fields []watchwake.FieldError `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &inv))
	assert.Equal(t, "invalid", inv.Error)
	paths := map[string]string{}
	for _, f := range inv.Fields {
		paths[f.Path] = f.Code
	}
	assert.Equal(t, map[string]string{"enabled": "bad_type", "rules.patrol.max_gap_hours": "out_of_range", "rules.patrol.times[0]": "bad_time", "rules.patrol.enabled": "unknown_field"}, paths)

	// someone else wrote first: 409 with the current document, which is the GET's body
	rr = r.do(adminCtx, http.MethodPut, putBody(t, good, "0000000000000000"))
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	var conflict struct {
		Error   string         `json:"error"`
		Current map[string]any `json:"current"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &conflict))
	assert.Equal(t, "etag_mismatch", conflict.Error)
	assert.Equal(t, etag, conflict.Current["etag"])
	assert.Equal(t, float64(1), conflict.Current["revision"])

	assert.Equal(t, float64(1), r.get(t)["revision"], "no refusal changed the stored document")
	assert.Empty(t, r.events_())
}

// Two writers with the same expect_etag: one wins, the other is told the document changed.
func TestWakeSettings_TwoConcurrentPutsHaveOneWinner(t *testing.T) {
	r := newWakeRig(t, "mlab")
	etag := r.get(t)["etag"].(string)
	const n = 8
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := watchwake.Default("Asia/Taipei")
			s.Rules.DiskLow.BelowGB = 20 + i
			codes[i] = r.do(adminCtx, http.MethodPut, putBody(t, s, etag)).Code
		}(i)
	}
	wg.Wait()
	wins, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			wins++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("status %d", c)
		}
	}
	assert.Equal(t, 1, wins)
	assert.Equal(t, n-1, conflicts)
	assert.Equal(t, float64(2), r.get(t)["revision"])
	assert.Len(t, r.events_(), 1)
}

// The first PUT on a host that never read: the migration and the PUT race for the row; either way exactly one document is stored.
func TestWakeSettings_AFirstPutRacingTheMigration(t *testing.T) {
	r := newWakeRig(t, "mlab")
	s := watchwake.Default("Asia/Taipei")
	s.Rules.DiskLow.BelowGB = 33
	etagOfMigrated := watchwake.Etag(watchwake.FromWatchRow(r.old, "Asia/Taipei"))
	var wg sync.WaitGroup
	var putCode int
	wg.Add(2)
	go func() { defer wg.Done(); putCode = r.do(adminCtx, http.MethodPut, putBody(t, s, etagOfMigrated)).Code }()
	go func() { defer wg.Done(); r.do(adminCtx, http.MethodGet, "") }()
	wg.Wait()
	assert.Equal(t, http.StatusOK, putCode, "the PUT is based on the migrated document it can only have read after the migration")
	v := r.get(t)
	assert.Equal(t, float64(2), v["revision"])
	assert.Equal(t, 33., v["settings"].(map[string]any)["rules"].(map[string]any)["disk_low"].(map[string]any)["below_gb"])
}

func TestWakeSettings_ANewerVersionIsServedAsItIsAndReadOnly(t *testing.T) {
	r := newWakeRig(t, "mlab")
	_, err := r.rows.Put(hostconfig.KeyWatchWake, []byte(`{"version":2,"future":{"x":1}}`), 0)
	require.NoError(t, err)
	v := r.get(t)
	assert.Equal(t, true, v["readonly"])
	assert.Equal(t, map[string]any{"version": 2., "future": map[string]any{"x": 1.}}, v["settings"])
	assert.Equal(t, float64(1), v["revision"])

	rr := r.do(adminCtx, http.MethodPut, putBody(t, watchwake.Default("UTC"), v["etag"].(string)))
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"bad_version"`)
	assert.Equal(t, float64(1), r.get(t)["revision"], "nothing was written")
}

// A stored document that does not read (damage) is served as the defaults, and a PUT of a whole document heals it.
func TestWakeSettings_ADamagedDocumentReadsAsTheDefaultsAndAPutHealsIt(t *testing.T) {
	r := newWakeRig(t, "mlab")
	_, err := r.rows.Put(hostconfig.KeyWatchWake, []byte(`{"version":1,"enabled":"nope"}`), 0)
	require.NoError(t, err)
	v := r.get(t)
	assert.Equal(t, false, v["readonly"])
	assert.Equal(t, watchwake.Etag(watchwake.Default("Asia/Taipei")), v["etag"])
	assert.Equal(t, float64(1), v["revision"])

	s := watchwake.Default("Asia/Taipei")
	s.Rules.UserBack.AwayMin = 45
	rr := r.do(adminCtx, http.MethodPut, putBody(t, s, v["etag"].(string)))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, float64(2), r.get(t)["revision"])
}

// A host that is not the source has no document of its own yet (WK-1b makes it follow the source): it says so, and a write cannot reach the
// source from here.
func TestWakeSettings_ANonSourceHostSaysItIsNotAndDoesNotWrite(t *testing.T) {
	r := newWakeRig(t, "air26")
	v := r.get(t)
	assert.Equal(t, "mlab", v["source"])
	assert.Equal(t, false, v["is_source"])
	assert.Equal(t, true, v["stale"])
	assert.Nil(t, v["fetched_at"])
	assert.Equal(t, float64(0), v["revision"])
	assert.Equal(t, watchwake.Etag(watchwake.Default("Asia/Taipei")), v["etag"])

	rr := r.do(adminCtx, http.MethodPut, putBody(t, watchwake.Default("Asia/Taipei"), v["etag"].(string)))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.JSONEq(t, `{"error":"source_unreachable","source":"mlab"}`, rr.Body.String())
	_, _, ok, _ := r.rows.Get(hostconfig.KeyWatchWake)
	assert.False(t, ok, "a non-source host writes no source row")
	assert.Empty(t, r.events_())
}

// The source can change while a PUT is on its way (peers.alias or [watch] wake_source is edited): a host that has stopped being the source
// does not commit the write, and does not say 200 for it.
func TestWakeSettings_APutDoesNotCommitOnAHostThatStoppedBeingTheSource(t *testing.T) {
	r := newWakeRig(t, "mlab")
	etag := r.get(t)["etag"].(string)
	r.m.beforeWakeCommit = func() { // after the PUT has read and checked everything, before it writes
		r.m.core.CfgMu.Lock()
		r.m.core.Cfg.Watch = &config.WatchConfig{WakeSource: "air26"}
		r.m.core.CfgMu.Unlock()
	}
	s := watchwake.Default("Asia/Taipei")
	s.Rules.DiskLow.BelowGB = 77
	rr := r.do(adminCtx, http.MethodPut, putBody(t, s, etag))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"error":"source_unreachable","source":"air26"}`, rr.Body.String(), "it names the source as it is now")
	_, rev, _, _ := r.rows.Get(hostconfig.KeyWatchWake)
	assert.Equal(t, int64(1), rev, "nothing was written")
	assert.Empty(t, r.events_())
}

// The source can also change between the check and the write: the write may land on the row of the host that was the source, so the answer is
// not a 200 (the caller reads again, as for any 503 of a source it could not reach) and no change is announced. The config lock is not held
// across the write: a config change never waits for the store.
func TestWakeSettings_ASourceChangeDuringTheWriteIsNotAnOK(t *testing.T) {
	r := newWakeRig(t, "mlab")
	etag := r.get(t)["etag"].(string)
	r.m.duringWakeCommit = func() {
		// a config change gets the write lock while the PUT is writing: it must not have to wait for the PUT
		done := make(chan struct{})
		go func() {
			r.m.core.CfgMu.Lock()
			r.m.core.Cfg.Peers.Alias = "air26"
			r.m.core.CfgMu.Unlock()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("the config write lock was held while the PUT wrote")
		}
	}
	rr := r.do(adminCtx, http.MethodPut, putBody(t, watchwake.Default("Asia/Taipei"), etag))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"error":"source_unreachable","source":"mlab"}`, rr.Body.String())
	assert.Empty(t, r.events_(), "no change is announced for a write that is not an answer of the source")
}

func TestLocalZoneName_TZWinsAndANonNameIsSkipped(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	assert.Equal(t, "America/New_York", localZoneName())
	t.Setenv("TZ", "Not/AZone")
	got := localZoneName()
	assert.NotEqual(t, "Not/AZone", got)
	assert.True(t, watchwake.ValidZone(got), "whatever it falls back to is a name the document accepts: %q", got)
}

// A host that never set peers.alias goes by the alias its host id gives (Config.PeerAlias): mlab:… is mlab, the default source.
func TestWakeSettings_TheAliasComesFromTheHostIDWhenNoneIsSet(t *testing.T) {
	r := newWakeRig(t, "mlab")
	r.m.core.Cfg.Peers.Alias = ""
	r.m.core.Cfg.HostID = "mlab:82b11c521df16c12"
	v := r.get(t)
	assert.Equal(t, true, v["is_source"])
	assert.Equal(t, false, v["stale"])
	assert.Equal(t, float64(1), v["revision"])
	rr := r.do(adminCtx, http.MethodPut, putBody(t, watchwake.Default("Asia/Taipei"), v["etag"].(string)))
	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	r.m.core.Cfg.HostID = "air26:cccccccccccccccc" // another host by id, no alias set: not the source
	assert.Equal(t, false, r.get(t)["is_source"])
}

// The body is one JSON object and no longer than the limit: nothing after it, and not more than maxWakePut bytes.
func TestWakeSettings_PutBodyIsOneObjectWithinTheLimit(t *testing.T) {
	r := newWakeRig(t, "mlab")
	etag := r.get(t)["etag"].(string)
	good := putBody(t, watchwake.Default("Asia/Taipei"), etag)

	for name, body := range map[string]string{"a second object": good + `{}`, "garbage": good + ` x`, "a second document": good + good} {
		rr := r.do(adminCtx, http.MethodPut, body)
		assert.Equal(t, http.StatusBadRequest, rr.Code, name)
	}
	assert.Equal(t, float64(1), r.get(t)["revision"], "none of them changed the document")

	pad := strings.Repeat(" ", maxWakePut+10)
	assert.Equal(t, http.StatusRequestEntityTooLarge, r.do(adminCtx, http.MethodPut, good+pad).Code, "trailing blanks do not get past the limit")
	assert.Equal(t, http.StatusRequestEntityTooLarge, r.do(adminCtx, http.MethodPut, `{"settings":{"x":"`+strings.Repeat("a", maxWakePut)+`"},"expect_etag":"x"}`).Code)
	assert.Equal(t, http.StatusOK, r.do(adminCtx, http.MethodPut, good+"  \n").Code, "trailing white space inside the limit is fine")
}

func TestWakeSettings_WakeSourceInTheConfigMovesTheSource(t *testing.T) {
	r := newWakeRig(t, "air26")
	r.m.core.Cfg.Watch = &config.WatchConfig{WakeSource: "air26"}
	v := r.get(t)
	assert.Equal(t, "air26", v["source"])
	assert.Equal(t, true, v["is_source"])
	assert.Equal(t, float64(1), v["revision"])
}

func TestWakeSettings_OnlyTheAdminTokenWritesAndAnyTokenReads(t *testing.T) {
	r := newWakeRig(t, "mlab")
	dev := devices.WithPrincipal(context.Background(), devices.Principal{ID: "d_1"})
	assert.Equal(t, http.StatusOK, r.do(dev, http.MethodGet, "").Code, "a paired phone reads")
	etag := r.get(t)["etag"].(string)
	body := putBody(t, watchwake.Default("Asia/Taipei"), etag)
	assert.Equal(t, http.StatusForbidden, r.do(dev, http.MethodPut, body).Code)
	assert.Equal(t, http.StatusForbidden, r.do(context.Background(), http.MethodPut, body).Code)
	assert.Equal(t, float64(1), r.get(t)["revision"])
}

// watch.wake_settings.v1 is announced for the real module registered in a real core (when it is ready), and goes with it.
func TestInfo_AnnouncesWatchWakeSettingsV1ForTheRealModule(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), HostID: "h"}
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	m := New()
	m.logf = func(string, ...any) {}
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
	require.Contains(t, caps(), "watch.wake_settings.v1")
	require.NotContains(t, caps(), "watch.wake_rules.v1", "the wake-ups do not read the new document yet: that capability is WK-2/WK-3's")
	require.NoError(t, m.Stop(context.Background()))
	require.NotContains(t, caps(), "watch.wake_settings.v1")
}

func TestWakeSettings_WithoutTheHostConfigRowsTheRoutesAre503(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), HostID: "h"}
	cfg.Peers.Alias = "mlab" // the source, with no host config module behind it
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	m := New()
	m.logf = func(string, ...any) {}
	require.NoError(t, m.Init(c))
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/watch/wake-settings", nil).WithContext(adminCtx))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}
