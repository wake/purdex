package push

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/push"
)

// keyDirOf writes a throwaway APNs directory (a key generated here, nothing read from the machine).
func keyDirOf(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, "AuthKey_K1.p8"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "config.env"), []byte("APNS_KEY_ID=K1\nAPNS_TEAM_ID=T1\nAPNS_KEY_FILE=AuthKey_K1.p8\n"), 0o600))
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type env struct {
	mod  *Module
	mux  *http.ServeMux
	logs *bytes.Buffer
	data string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	data := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: data, Push: &config.PushConfig{APNsDir: keyDirOf(t)}}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(nil) })
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &env{mod: m, mux: mux, logs: logs, data: data}
}

func (e *env) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func reqBody(token string, mut func(*push.DeviceRequest)) string {
	r := push.DeviceRequest{Token: token, BundleID: push.BundleID, Env: "sandbox", Platform: "ios", DeviceName: "iPhone 8", HostLabel: "mlab", Locale: "zh-TW",
		Prefs: push.Prefs{Tabs: []string{"c1"}}}
	if mut != nil {
		mut(&r)
	}
	b, _ := json.Marshal(r)
	return string(b)
}

func TestPost_UpsertTwiceIsOneRowWithTheSameDeviceID(t *testing.T) {
	e := newEnv(t)
	r1 := e.do("POST", "/api/push/devices", reqBody(strings.ToUpper(tokA), nil))
	if r1.Code != 200 {
		t.Fatalf("first: %d %s", r1.Code, r1.Body)
	}
	r2 := e.do("POST", "/api/push/devices", reqBody(tokA, func(r *push.DeviceRequest) { r.DeviceName = "Renamed"; r.Prefs.Tabs = []string{"a", "b"} }))
	if r2.Code != 200 {
		t.Fatalf("second: %d %s", r2.Code, r2.Body)
	}
	var v1, v2 push.DeviceView
	must(t, json.Unmarshal(r1.Body.Bytes(), &v1))
	must(t, json.Unmarshal(r2.Body.Bytes(), &v2))
	if v1.DeviceID != push.DeviceID(tokA) || v2.DeviceID != v1.DeviceID {
		t.Fatalf("ids: %s / %s", v1.DeviceID, v2.DeviceID)
	}
	if v2.DeviceName != "Renamed" || v2.TabsCount != 2 || v2.CreatedAt != v1.CreatedAt {
		t.Fatalf("second view = %+v", v2)
	}
	var list struct{ Devices []push.DeviceView }
	must(t, json.Unmarshal(e.do("GET", "/api/push/devices", "").Body.Bytes(), &list))
	if len(list.Devices) != 1 {
		t.Fatalf("devices = %d", len(list.Devices))
	}
}

func TestGet_MasksTheToken(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/push/devices", reqBody(tokA, nil))
	rec := e.do("GET", "/api/push/devices", "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), tokA) {
		t.Fatalf("code %d, body carries the full token: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), push.MaskToken(tokA)) {
		t.Fatalf("masked token missing: %s", rec.Body)
	}
}

func TestDelete_ByDeviceIDIsIdempotent(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/push/devices", reqBody(tokA, nil))
	id := push.DeviceID(tokA)
	if rec := e.do("DELETE", "/api/push/devices/"+id, ""); rec.Code != 204 {
		t.Fatalf("first delete: %d", rec.Code)
	}
	if rec := e.do("DELETE", "/api/push/devices/"+id, ""); rec.Code != 204 {
		t.Fatalf("second delete: %d", rec.Code)
	}
	var list struct{ Devices []push.DeviceView }
	must(t, json.Unmarshal(e.do("GET", "/api/push/devices", "").Body.Bytes(), &list))
	if len(list.Devices) != 0 {
		t.Fatalf("devices = %d", len(list.Devices))
	}
	if list, _ := e.mod.store.List(); len(list) != 0 {
		t.Fatalf("store still has %d", len(list))
	}
}

func TestPost_BadRequests(t *testing.T) {
	e := newEnv(t)
	for name, body := range map[string]string{
		"not json":     "{",
		"empty":        "",
		"short token":  reqBody("abc", nil),
		"other bundle": reqBody(tokA, func(r *push.DeviceRequest) { r.BundleID = "com.x.y" }),
		"bad env":      reqBody(tokA, func(r *push.DeviceRequest) { r.Env = "dev" }),
	} {
		t.Run(name, func(t *testing.T) {
			rec := e.do("POST", "/api/push/devices", body)
			if rec.Code != 400 {
				t.Fatalf("code %d: %s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), tokA) {
				t.Fatal("the error body carries the token")
			}
		})
	}
	if list, _ := e.mod.store.List(); len(list) != 0 {
		t.Fatalf("a rejected request stored %d rows", len(list))
	}
}

func TestPost_BodyOverTheCapIs413(t *testing.T) {
	e := newEnv(t)
	big := reqBody(tokA, func(r *push.DeviceRequest) { r.DeviceName = strings.Repeat("x", 70<<10) })
	if rec := e.do("POST", "/api/push/devices", big); rec.Code != 413 {
		t.Fatalf("code %d, want 413", rec.Code)
	}
	just := reqBody(tokA, nil) + strings.Repeat(" ", 64<<10-len(reqBody(tokA, nil))-1)
	if rec := e.do("POST", "/api/push/devices", just); rec.Code != 200 {
		t.Fatalf("a body just under the cap: %d %s", rec.Code, rec.Body)
	}
}

// No response body and no log line, on any path, contains the full token (spec §4).
func TestNothingEverCarriesTheFullToken(t *testing.T) {
	e := newEnv(t)
	var all strings.Builder
	for _, call := range []struct{ m, p, b string }{
		{"POST", "/api/push/devices", reqBody(tokA, nil)},
		{"POST", "/api/push/devices", reqBody(tokA, func(r *push.DeviceRequest) { r.Env = "nope" })},
		{"POST", "/api/push/devices", `{"token":"` + tokA + `"`},
		{"POST", "/api/push/devices", reqBody(tokA, func(r *push.DeviceRequest) { r.DeviceName = strings.Repeat("x", 70<<10) })},
		{"GET", "/api/push/devices", ""},
		{"DELETE", "/api/push/devices/" + push.DeviceID(tokA), ""},
	} {
		rec := e.do(call.m, call.p, call.b)
		all.WriteString(rec.Body.String())
	}
	all.WriteString(e.logs.String())
	if strings.Contains(all.String(), tokA) {
		t.Fatal("a response or a log line carries the full token")
	}
}

func TestInit_ARefusedKeyFailsTheModuleWithoutKeyMaterial(t *testing.T) {
	secret := "SUPERSECRETPEMCONTENT0123456789"
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "config.env"), []byte("APNS_KEY_ID=K\nAPNS_TEAM_ID=T\nAPNS_KEY_FILE=k.p8\n"), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "k.p8"), []byte(secret), 0o600))
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Push: &config.PushConfig{APNsDir: dir}}})
	err := New().Init(c)
	if err == nil {
		t.Fatal("want an Init error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the error carries key file content: %v", err)
	}
}

func TestInit_ExpandsTheHomeInTheDirectory(t *testing.T) {
	home := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(home, ".config", "apns"), 0o700))
	src := keyDirOf(t)
	for _, n := range []string{"config.env", "AuthKey_K1.p8"} {
		b, _ := os.ReadFile(filepath.Join(src, n))
		must(t, os.WriteFile(filepath.Join(home, ".config", "apns", n), b, 0o600))
	}
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Push: &config.PushConfig{APNsDir: "~/.config/apns"}}})
	m := New()
	m.home = func() (string, error) { return home, nil }
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	m.Stop(nil)
}

// A restart keeps the devices: the cache is rebuilt from the store at Init.
func TestRestart_RebuildsTheCacheFromTheStore(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/push/devices", reqBody(tokA, nil))
	e.mod.Stop(nil)
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: e.data, Push: &config.PushConfig{APNsDir: keyDirOf(t)}}})
	m := New()
	must(t, m.Init(c))
	defer m.Stop(nil)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/push/devices", nil))
	if !strings.Contains(rec.Body.String(), push.DeviceID(tokA)) {
		t.Fatalf("device lost across restart: %s", rec.Body)
	}
}
