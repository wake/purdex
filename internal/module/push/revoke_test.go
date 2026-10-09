package push

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	devicesmod "github.com/wake/purdex/internal/module/devices"
	"github.com/wake/purdex/internal/push"
)

// A paired phone's push registrations live and die with the phone: revoking it (by id or by pairing, live or while push was
// down) drops them; the admin's are never touched. Real devices module, real push module.

type revokeEnv struct {
	core *core.Core
	push *Module
	dev  *devicesmod.Module
	mux  *http.ServeMux
}

func newRevokeEnv(t *testing.T) *revokeEnv {
	t.Helper()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "adm", Push: &config.PushConfig{APNsDir: keyDirOf(t)}}})
	dm, pm := devicesmod.New(), New()
	must(t, dm.Init(c))
	must(t, pm.Init(c))
	t.Cleanup(func() { pm.Stop(context.Background()); dm.Stop(context.Background()) })
	mux := http.NewServeMux()
	dm.RegisterRoutes(mux)
	pm.RegisterRoutes(mux)
	return &revokeEnv{core: c, push: pm, dev: dm, mux: mux}
}

func (e *revokeEnv) call(p *devices.Principal, method, path string, body any) *httptest.ResponseRecorder {
	var raw []byte
	if s, ok := body.(string); ok {
		raw = []byte(s)
	} else if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	ctx := req.Context()
	if p != nil {
		ctx = devices.WithPrincipal(ctx, *p)
	} else {
		ctx = devices.WithAdmin(ctx)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// pair mints a phone through the real route and authenticates it once (first use), returning its principal.
func (e *revokeEnv) pair(t *testing.T, pairing string) devices.Principal {
	t.Helper()
	rec := e.call(nil, "POST", "/api/devices", map[string]any{
		"pairing_id": pairing, "label": "iPhone", "client": map[string]any{"kind": "app", "label": "Purdex.app"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	var m struct{ Token string }
	must(t, json.Unmarshal(rec.Body.Bytes(), &m))
	p, ok := e.dev.AuthenticateToken(m.Token)
	if !ok {
		t.Fatal("first use refused")
	}
	return p
}

func (e *revokeEnv) register(t *testing.T, p *devices.Principal, tok string) {
	t.Helper()
	if rec := e.call(p, "POST", "/api/push/devices", regBody(tok)); rec.Code != http.StatusOK {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
}

func (e *revokeEnv) adminSees(t *testing.T) []string {
	return listed(t, e.call(nil, "GET", "/api/push/devices", nil))
}

const (
	pairA = "00000000-0000-4000-8000-00000000000a"
	pairB = "00000000-0000-4000-8000-00000000000b"
)

func TestRevoke_DropsTheRegistrationsOfThePhoneAtOnce(t *testing.T) {
	e := newRevokeEnv(t)
	must(t, e.push.Start(context.Background()))
	pa, pb1, pb2 := e.pair(t, pairA), e.pair(t, pairB), e.pair(t, pairB)
	adminTok := strings.Repeat("d4", 32)
	e.register(t, &pa, tokA)
	e.register(t, &pb1, tokB)
	e.register(t, &pb2, strings.Repeat("c3", 32))
	e.register(t, nil, adminTok)
	if n := len(e.adminSees(t)); n != 4 {
		t.Fatalf("registered %d", n)
	}

	if rec := e.call(nil, "DELETE", "/api/devices/"+pa.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	got := e.adminSees(t)
	if len(got) != 3 || contains(got, push.DeviceID(tokA)) {
		t.Fatalf("after revoking one phone: %v", got)
	}
	if _, ok := e.push.Get(push.DeviceID(tokA)); ok {
		t.Fatal("the revoked phone's registration is still in the send cache")
	}

	if rec := e.call(nil, "DELETE", "/api/devices?pairing_id="+pairB, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke pairing: %d", rec.Code)
	}
	got = e.adminSees(t)
	if len(got) != 1 || got[0] != push.DeviceID(adminTok) {
		t.Fatalf("after revoking the pairing only the admin's should stay: %v", got)
	}
	if rows, _ := e.push.store.List(); len(rows) != 1 {
		t.Fatalf("the store still holds %d rows", len(rows))
	}
}

// A phone revoked while push was not listening (a restart in between) is dropped when push starts.
func TestStart_DropsTheRegistrationsOfAPhoneRevokedMeanwhile(t *testing.T) {
	e := newRevokeEnv(t) // push is not started: it hears nothing of the revoke
	pa, pb := e.pair(t, pairA), e.pair(t, pairB)
	e.register(t, &pa, tokA)
	e.register(t, &pb, tokB)
	e.register(t, nil, strings.Repeat("d4", 32))
	if rec := e.call(nil, "DELETE", "/api/devices/"+pa.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if n := len(e.adminSees(t)); n != 3 {
		t.Fatalf("before Start %d", n)
	}
	must(t, e.push.Start(context.Background()))
	got := e.adminSees(t)
	if len(got) != 2 || contains(got, push.DeviceID(tokA)) {
		t.Fatalf("after Start: %v", got)
	}
}

// With no devices module a phone-owned registration cannot be verified: dropped at Start. The admin's stays.
func TestStart_WithoutTheDevicesModuleThePhonesRegistrationsAreDropped(t *testing.T) {
	e := newEnv(t)
	e.mod.store.Upsert(func() push.Device { d := dev(tokA); d.OwnerDeviceID = "d_aaaaaaaaaaaa"; return d }())
	e.mod.store.Upsert(dev(tokB))
	e.mod.mu.Lock()
	e.mod.devices = map[string]push.Device{}
	rows, _ := e.mod.store.List()
	for _, d := range rows {
		e.mod.devices[d.DeviceID] = d
	}
	e.mod.mu.Unlock()
	must(t, e.mod.Start(context.Background()))
	got := listed(t, e.as(nil, "GET", "/api/push/devices", ""))
	if len(got) != 1 || got[0] != push.DeviceID(tokB) {
		t.Fatalf("after Start: %v", got)
	}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// "" names the admin's registrations; no list of owners, however it was built, may delete those.
func TestStore_DeleteByOwnersNeverDeletesTheAdminsRows(t *testing.T) {
	s := newStore(t)
	s.Upsert(dev(tokA))
	if gone, err := s.DeleteByOwners([]string{""}); err != nil || len(gone) != 0 {
		t.Fatalf("gone %v err %v", gone, err)
	}
	if rows, _ := s.List(); len(rows) != 1 {
		t.Fatalf("the admin's row was deleted: %v", rows)
	}
}
