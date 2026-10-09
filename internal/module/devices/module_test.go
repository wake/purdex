package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
)

// QP-1 tasks 2 and 9: the module's authenticator behind the real TokenAuth, and the management routes. The admin token is
// generated here; nothing prints a token.

const pairingA = "00000000-0000-4000-8000-00000000000a"
const pairingB = "00000000-0000-4000-8000-00000000000b"

type env struct {
	mod   *Module
	h     http.Handler // TokenAuthWith over the module's routes, as the daemon wires it
	admin string
	dir   string
}

type fakeTickets struct{ valid string }

func (f fakeTickets) Validate(ticket string) bool { return ticket != "" && ticket == f.valid }

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	adminTok, _ := devices.NewToken()
	adminTok = "adm_" + adminTok[5:] // an admin token has no device prefix
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir, Token: adminTok}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	h := middleware.TokenAuthWith(func() string { return adminTok }, fakeTickets{valid: "one-time"}, m)(mux)
	return &env{mod: m, h: h, admin: adminTok, dir: dir}
}

func (e *env) call(method, path, bearer string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func mintBody(over map[string]any) map[string]any {
	b := map[string]any{"pairing_id": pairingA, "label": "iPhone", "client": map[string]any{"kind": "app", "label": "Purdex.app"}}
	for k, v := range over {
		b[k] = v
	}
	return b
}

type minted struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	PairingID string `json:"pairing_id"`
	ProfileID string `json:"profile_id"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"created_at"`
	UseBy     int64  `json:"use_by"`
}

func (e *env) mint(t *testing.T, over map[string]any) minted {
	t.Helper()
	rec := e.call("POST", "/api/devices", e.admin, mintBody(over))
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	var m minted
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMintRoute_ReturnsTheTokenOnceAndItAuthenticates(t *testing.T) {
	e := newEnv(t)
	m := e.mint(t, map[string]any{"profile_id": "p_0123456789ab"})
	if !devices.IsDeviceToken(m.Token) || !devices.ValidID(m.ID) || m.PairingID != pairingA || m.ProfileID != "p_0123456789ab" || m.Label != "iPhone" {
		t.Fatalf("minted = %+v", m)
	}
	if d := m.UseBy - m.CreatedAt; d != 900_000 {
		t.Fatalf("default use window = %d ms, want 15 min", d)
	}
	// The token works as a bearer on a route that needs auth (GET /api/devices is admin-only: 403 proves it authenticated).
	if rec := e.call("GET", "/api/devices", m.Token, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("device bearer on a management route: %d", rec.Code)
	}
	// And it is not shown again.
	rec := e.call("GET", "/api/devices", e.admin, nil)
	if strings.Contains(rec.Body.String(), m.Token) || strings.Contains(rec.Body.String(), devices.Hash(m.Token)) {
		t.Fatal("the list carries the token or its hash")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a token response must not be cacheable")
	}
}

func TestMintRoute_UseWithinBounds(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		within int
		want   int
	}{{59, 400}, {60, 201}, {1200, 201}, {1201, 400}, {0, 400}, {-5, 400}} {
		rec := e.call("POST", "/api/devices", e.admin, mintBody(map[string]any{"use_within_s": c.within}))
		if rec.Code != c.want {
			t.Errorf("use_within_s %d -> %d, want %d", c.within, rec.Code, c.want)
		}
	}
	m := e.mint(t, map[string]any{"use_within_s": 300})
	if m.UseBy-m.CreatedAt != 300_000 {
		t.Fatalf("window = %d ms", m.UseBy-m.CreatedAt)
	}
}

func TestMintRoute_Validation(t *testing.T) {
	e := newEnv(t)
	cases := map[string]map[string]any{
		"no pairing id":          {"pairing_id": ""},
		"pairing id not uuid":    {"pairing_id": "not-a-uuid"},
		"empty label":            {"label": ""},
		"blank label":            {"label": "   "},
		"label too long":         {"label": strings.Repeat("x", 65)},
		"control in label":       {"label": "a\x00b"},
		"newline in label":       {"label": "a\nb"},
		"bidi in label":          {"label": "a‮b"},
		"profile id too long":    {"profile_id": strings.Repeat("p", 65)},
		"profile id wrong shape": {"profile_id": "settings"},
		"profile id uppercase":   {"profile_id": "p_0123456789AB"},
		"profile id short":       {"profile_id": "p_0123"},
		"control in profile":     {"profile_id": "p_01234567\nab"},
		"client not app":         {"client": map[string]any{"kind": "cli", "label": "x"}},
		"client label missing":   {"client": map[string]any{"kind": "app", "label": ""}},
		"client missing":         {"client": nil},
	}
	for name, over := range cases {
		if rec := e.call("POST", "/api/devices", e.admin, mintBody(over)); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := e.call("POST", "/api/devices", e.admin, "{not json"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d", rec.Code)
	}
	if rec := e.call("POST", "/api/devices", e.admin, strings.Repeat("x", maxBody+1)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d", rec.Code)
	}
	if rows, _ := e.mod.store.List(); len(rows) != 0 {
		t.Fatalf("%d rows after only bad requests", len(rows))
	}
}

func TestMintRoute_LabelEdgesAndAUppercaseUUID(t *testing.T) {
	e := newEnv(t)
	m := e.mint(t, map[string]any{"label": strings.Repeat("機", 64), "pairing_id": strings.ToUpper(pairingA)})
	if m.Label != strings.Repeat("機", 64) || m.PairingID != pairingA {
		t.Fatalf("minted = %+v (64 runes fit; the pairing id is stored lowercase)", m)
	}
}

// Admin only: with no token at all, with a device token, with a wrong token. Mutation gate: drop adminOnly → red.
func TestManagementRoutes_AdminOnly(t *testing.T) {
	e := newEnv(t)
	dev := e.mint(t, nil)
	routes := []struct{ method, path string }{
		{"POST", "/api/devices"}, {"GET", "/api/devices"}, {"DELETE", "/api/devices/" + dev.ID}, {"DELETE", "/api/devices?pairing_id=" + pairingA},
	}
	for _, rt := range routes {
		if rec := e.call(rt.method, rt.path, "", mintBody(nil)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token: %d", rt.method, rt.path, rec.Code)
		}
		if rec := e.call(rt.method, rt.path, "wrong", mintBody(nil)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with a wrong token: %d", rt.method, rt.path, rec.Code)
		}
		if rec := e.call(rt.method, rt.path, dev.Token, mintBody(nil)); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with a device token: %d", rt.method, rt.path, rec.Code)
		}
	}
	// Nothing the device tried changed anything.
	rows, _ := e.mod.store.List()
	if len(rows) != 1 || rows[0].RevokedAt != 0 {
		t.Fatalf("rows after the device's attempts = %+v", rows)
	}
}

func TestListRoute_RowsAreLabelledAndNeverCarryTokenOrHash(t *testing.T) {
	e := newEnv(t)
	a := e.mint(t, nil)
	e.mint(t, map[string]any{"pairing_id": pairingB, "label": "iPad"})
	rec := e.call("GET", "/api/devices", e.admin, nil)
	var body struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 || len(body.Devices) != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	for _, d := range body.Devices {
		for k := range d {
			if k == "token" || k == "token_hash" || k == "hash" {
				t.Fatalf("the list has a %q field", k)
			}
		}
		for _, want := range []string{"id", "pairing_id", "profile_id", "label", "created_at", "created_by", "use_by", "first_used_at", "last_used_at", "revoked_at"} {
			if _, ok := d[want]; !ok {
				t.Fatalf("a row lacks %q", want)
			}
		}
	}
	if body.Devices[0]["id"] != a.ID || body.Devices[0]["created_by"] != "Purdex.app" {
		t.Fatalf("first row = %v", body.Devices[0])
	}
	// An empty store lists [], not null.
	e2 := newEnv(t)
	if raw := e2.call("GET", "/api/devices", e2.admin, nil).Body.String(); !strings.Contains(raw, `"devices":[]`) {
		t.Fatalf("empty list = %s", raw)
	}
}

// Revoking: a revoked token authenticates no more, even one in use; idempotent; unknown ids are a 204; the hook hears it.
func TestRevokeRoutes(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	var heard []string
	e.mod.SetOnRevoke(func(ids []string) { mu.Lock(); heard = append(heard, ids...); mu.Unlock() })
	a := e.mint(t, nil)
	b := e.mint(t, nil)
	c := e.mint(t, map[string]any{"pairing_id": pairingB})
	for _, d := range []minted{a, b, c} { // first use of each
		if rec := e.call("GET", "/api/devices", d.Token, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("first use: %d", rec.Code)
		}
	}
	if rec := e.call("DELETE", "/api/devices/"+a.ID, e.admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if rec := e.call("GET", "/api/devices", a.Token, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked token: %d", rec.Code)
	}
	if rec := e.call("GET", "/api/devices", b.Token, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("an unrelated token stopped working: %d", rec.Code)
	}
	for _, id := range []string{a.ID, "d_000000000000", "garbage"} { // again, unknown, malformed
		if rec := e.call("DELETE", "/api/devices/"+id, e.admin, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("revoke %s: %d", id, rec.Code)
		}
	}
	// By pairing: every token of pairingA that is left; pairingB untouched.
	if rec := e.call("DELETE", "/api/devices?pairing_id="+pairingA, e.admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke pairing: %d", rec.Code)
	}
	if rec := e.call("GET", "/api/devices", b.Token, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("b survived its pairing's revoke: %d", rec.Code)
	}
	if rec := e.call("GET", "/api/devices", c.Token, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("another pairing's token was revoked: %d", rec.Code)
	}
	if rec := e.call("DELETE", "/api/devices?pairing_id="+pairingA, e.admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke pairing again: %d", rec.Code)
	}
	for _, bad := range []string{"/api/devices", "/api/devices?pairing_id=", "/api/devices?pairing_id=zz"} {
		if rec := e.call("DELETE", bad, e.admin, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("DELETE %s: %d", bad, rec.Code)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !contains(heard, a.ID) || !contains(heard, b.ID) || contains(heard, c.ID) {
		t.Fatalf("the revoke hook heard %v", heard)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// PUT /api/devices/self: a device renames itself and only itself; the admin token has no device to rename.
func TestSelfRoute(t *testing.T) {
	e := newEnv(t)
	a := e.mint(t, nil)
	b := e.mint(t, nil)
	if rec := e.call("PUT", "/api/devices/self", a.Token, map[string]any{"label": "iPhone 8"}); rec.Code != 200 {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	rows, _ := e.mod.store.List()
	for _, r := range rows {
		if r.ID == a.ID && r.Label != "iPhone 8" || r.ID == b.ID && r.Label != "iPhone" {
			t.Fatalf("labels = %+v", rows)
		}
	}
	if rec := e.call("PUT", "/api/devices/self", e.admin, map[string]any{"label": "x"}); rec.Code != http.StatusForbidden {
		t.Fatalf("admin on self: %d", rec.Code)
	}
	if rec := e.call("PUT", "/api/devices/self", "", map[string]any{"label": "x"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 65), "a\nb", "a‮b"} {
		if rec := e.call("PUT", "/api/devices/self", a.Token, map[string]any{"label": bad}); rec.Code != http.StatusBadRequest {
			t.Fatalf("label %q: %d", bad, rec.Code)
		}
	}
	if rec := e.call("PUT", "/api/devices/self", a.Token, "{nope"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
}

// A device token is accepted only while live: unused past use_by it is refused at the middleware, with no sweep.
func TestAuthenticateToken_PastUseByAndUnknown(t *testing.T) {
	e := newEnv(t)
	m := e.mint(t, map[string]any{"use_within_s": 60})
	e.mod.store.now = func() int64 { return m.CreatedAt + 61_000 }
	if rec := e.call("PUT", "/api/devices/self", m.Token, map[string]any{"label": "x"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an unused token past use_by: %d", rec.Code)
	}
	unknown, _ := devices.NewToken()
	if rec := e.call("GET", "/api/devices", unknown, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an unknown device token: %d", rec.Code)
	}
}

// With the admin token empty, auth is off as before; device tokens add nothing there.
func TestEmptyAdminTokenLeavesAuthOffAsBefore(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	h := middleware.TokenAuthWith(func() string { return "" }, nil, m)(mux)
	req := httptest.NewRequest("GET", "/api/devices", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("with auth off: %d", rec.Code)
	}
}

// The module publishes its authenticator, the daemon's token middleware finds it there, and Status says ready.
func TestInit_PublishesTheAuthenticatorAndIsReady(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "adm_x"}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	svc, ok := c.Registry.Get(RegistryKey)
	if !ok {
		t.Fatal("authenticator not published")
	}
	if _, isAuth := svc.(devices.Authenticator); !isAuth {
		t.Fatalf("published %T", svc)
	}
	if st := m.Status(); st["ready"] != true || st["init_error"] != "" {
		t.Fatalf("status = %v", st)
	}
	if fi, err := os.Stat(filepath.Join(c.Cfg.DataDir, "devices.db")); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("devices.db: %v %v", fi, err)
	}
}

// A store that cannot be opened leaves the module off: no route, nothing authenticates, not ready, and Init does not fail
// (the daemon must stay up).
func TestInit_BrokenStoreSoftFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "devices.db"), 0o755); err != nil { // a directory where the file should be
		t.Fatal(err)
	}
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir, Token: "adm_x"}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if st := m.Status(); st["ready"] != false || st["init_error"] == "" {
		t.Fatalf("status = %v", st)
	}
	if _, ok := c.Registry.Get(RegistryKey); ok {
		t.Fatal("a soft-failed module published an authenticator")
	}
	tok, _ := devices.NewToken()
	if _, ok := m.AuthenticateToken(tok); ok {
		t.Fatal("a soft-failed module authenticated a token")
	}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest("GET", "/api/devices", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("route on a soft-failed module: %d", rec.Code)
	}
	if err := m.Start(context.Background()); err != nil || m.Stop(context.Background()) != nil {
		t.Fatal("start/stop of a soft-failed module")
	}
}

func TestStartSweepsAndStopIsClean(t *testing.T) {
	e := newEnv(t)
	m := e.mint(t, map[string]any{"use_within_s": 60})
	e.mod.store.now = func() int64 { return m.CreatedAt + 120_000 }
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.mod.store.List()
	if len(rows) != 0 {
		t.Fatalf("the boot sweep left %d dead row(s)", len(rows))
	}
	done := make(chan struct{})
	go func() { e.mod.Stop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop hangs")
	}
}

// A one-time WebSocket ticket authenticates a request with no principal, but it is not the admin: the management routes
// refuse it, even on a GET that has the shape of a WebSocket handshake. Mutation gate: adminOnly = "no principal" → red.
func TestManagementRoutes_ATicketIsNotTheAdmin(t *testing.T) {
	e := newEnv(t)
	dev := e.mint(t, nil)
	for _, rt := range []struct{ method, path string }{
		{"GET", "/api/devices?ticket=one-time"}, {"GET", "/api/devices/" + dev.ID + "?ticket=one-time"},
	} {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
			t.Errorf("%s with a ticket: %d %s", rt.path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), dev.PairingID) {
			t.Errorf("%s with a ticket listed a device", rt.path)
		}
	}
	// The same shape with the admin token still works, and a ticket cannot rename a device either.
	req := httptest.NewRequest("GET", "/api/devices", nil)
	req.Header.Set("Authorization", "Bearer "+e.admin)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("admin on a handshake-shaped GET: %d", rec.Code)
	}
	req = httptest.NewRequest("PUT", "/api/devices/self?ticket=one-time", strings.NewReader(`{"label":"x"}`))
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a ticket on a PUT: %d", rec.Code)
	}
}

// profile_id is the profiles module's id, or nothing: a token bound to anything else could never read a profile. Edge: the
// good id passes, empty means no profile.
func TestMintRoute_ProfileIDIsTheProfilesContract(t *testing.T) {
	e := newEnv(t)
	for id, want := range map[string]int{"p_0123456789ab": 201, "p_ffffffffffff": 201, "": 201, "p_0123456789abc": 400, "P_0123456789ab": 400, "p_0123456789ag": 400, "profile": 400} {
		over := map[string]any{"profile_id": id}
		if rec := e.call("POST", "/api/devices", e.admin, mintBody(over)); rec.Code != want {
			t.Errorf("profile_id %q -> %d, want %d", id, rec.Code, want)
		}
	}
}
