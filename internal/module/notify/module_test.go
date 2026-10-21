package notify

import (
	"bytes"
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

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/notifylabel"
)

// ---- PUT /api/notify/group-labels (notify-title spec §3.2, plan N-2 #4) ----

type env struct {
	mod   *Module
	h     http.Handler
	admin string
	dir   string
	c     *core.Core
}

func newEnvIn(t *testing.T, dir string) *env {
	t.Helper()
	adminTok, _ := devices.NewToken()
	adminTok = "adm_" + adminTok[5:] // an admin token has no device prefix
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir, Token: adminTok}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()); m.Close() })
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	h := middleware.TokenAuthWith(func() string { return adminTok }, nil, nil)(mux)
	return &env{mod: m, h: h, admin: adminTok, dir: dir, c: c}
}

func newEnv(t *testing.T) *env { return newEnvIn(t, t.TempDir()) }

func (e *env) put(bearer string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	switch b := body.(type) {
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest("PUT", "/api/notify/group-labels", rd)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

type lab = map[string]string

func payload(device string, labels ...lab) map[string]any {
	ls := make([]map[string]string, 0, len(labels))
	for _, l := range labels {
		ls = append(ls, l)
	}
	return map[string]any{"device": device, "labels": ls}
}

func l(root, group string) lab { return lab{"root_session_id": root, "group": group} }

func TestPut_AdminWritesAndTheOverlayAnswers(t *testing.T) {
	e := newEnv(t)
	rec := e.put(e.admin, payload("dev-a", l("root-1", "Alpha"), l("root-2", "Beta")))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var resp struct{ Count int }
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Fatalf("count = %d, body %s", resp.Count, rec.Body.String())
	}
	if got := e.mod.GroupOf("root-1"); got != "Alpha" {
		t.Fatalf("GroupOf = %q", got)
	}
	if got := e.mod.GroupOf("nobody"); got != "" {
		t.Fatalf("GroupOf(unknown) = %q", got)
	}
}

// The module publishes itself as the overlay the agent module's labeler asks for.
func TestInit_RegistersTheOverlay(t *testing.T) {
	e := newEnv(t)
	svc, ok := e.c.Registry.Get(notifylabel.OverlayKey)
	if !ok {
		t.Fatal("no overlay in the registry")
	}
	ov, ok := svc.(notifylabel.Overlay)
	if !ok {
		t.Fatalf("%T is not a notifylabel.Overlay", svc)
	}
	e.put(e.admin, payload("dev-a", l("root-1", "Alpha")))
	if got := ov.GroupOf("root-1"); got != "Alpha" {
		t.Fatalf("registered overlay says %q", got)
	}
}

// A whole-set replacement: the second write drops what it leaves out, from the memory table too.
func TestPut_TheSecondWriteReplacesTheFirst(t *testing.T) {
	e := newEnv(t)
	e.put(e.admin, payload("dev-a", l("root-1", "Alpha"), l("root-2", "Beta")))
	e.put(e.admin, payload("dev-a", l("root-2", "Beta 2")))
	if got := e.mod.GroupOf("root-1"); got != "" {
		t.Fatalf("root-1 = %q: it was left out of the second write", got)
	}
	if got := e.mod.GroupOf("root-2"); got != "Beta 2" {
		t.Fatalf("root-2 = %q", got)
	}
	// an empty set clears the device
	e.put(e.admin, payload("dev-a"))
	if got := e.mod.GroupOf("root-2"); got != "" {
		t.Fatalf("root-2 = %q after an empty set", got)
	}
}

func TestPut_TwoDevicesTheLastWriteWinsAndTheOtherSurvivesAnOverwrite(t *testing.T) {
	e := newEnv(t)
	e.put(e.admin, payload("dev-a", l("root-1", "From A")))
	e.put(e.admin, payload("dev-b", l("root-1", "From B")))
	if got := e.mod.GroupOf("root-1"); got != "From B" {
		t.Fatalf("got %q", got)
	}
	e.put(e.admin, payload("dev-b")) // B stops claiming it: A's value shows again
	if got := e.mod.GroupOf("root-1"); got != "From A" {
		t.Fatalf("got %q, want A's value once B left it out", got)
	}
}

// 2000 is the most one device may write; one more is refused whole (413), nothing is cut, nothing is written.
func TestPut_TooManyLabelsIs413AndWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.put(e.admin, payload("dev-a", l("keep", "Kept")))
	var many []lab
	for i := 0; i < maxLabelsPerDevice+1; i++ {
		many = append(many, l(fmt.Sprintf("root-%d", i), "G"))
	}
	rec := e.put(e.admin, payload("dev-a", many...))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "too_many_labels") {
		t.Fatalf("body %s", rec.Body.String())
	}
	if got := e.mod.GroupOf("keep"); got != "Kept" {
		t.Fatalf("the refused write changed the device's set: %q", got)
	}
	if got := e.mod.GroupOf("root-0"); got != "" {
		t.Fatalf("the refused write was applied in part: %q", got)
	}
	// exactly the limit is accepted
	if rec := e.put(e.admin, payload("dev-a", many[:maxLabelsPerDevice]...)); rec.Code != http.StatusOK {
		t.Fatalf("the limit itself: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPut_BadRequests(t *testing.T) {
	e := newEnv(t)
	for name, body := range map[string]any{
		"not json":          "{",
		"no device":         map[string]any{"labels": []lab{}},
		"empty device":      payload("  ", l("r", "g")),
		"empty root":        payload("dev-a", l("", "g")),
		"a long device":     payload(strings.Repeat("d", maxDeviceRunes+1), l("r", "g")),
		"a long root":       payload("dev-a", l(strings.Repeat("r", maxRootRunes+1), "g")),
		"labels not a list": map[string]any{"device": "dev-a", "labels": "x"},
	} {
		if rec := e.put(e.admin, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if got := e.mod.GroupOf("r"); got != "" {
		t.Fatalf("a refused request wrote %q", got)
	}
}

// The group goes through the notification text rules (control and bidi characters dropped, whitespace one space, cut); one that is
// nothing after that is not a label.
func TestPut_TheGroupIsNormalised(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("長", 60)
	rec := e.put(e.admin, payload("dev-a",
		l("r1", "  Mei's\n\tgroup‮  "),
		l("r2", long),
		l("r3", "​ \n "),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if got := e.mod.GroupOf("r1"); got != "Mei's group" {
		t.Fatalf("r1 = %q", got)
	}
	if got := []rune(e.mod.GroupOf("r2")); len(got) != 41 || got[40] != '…' {
		t.Fatalf("r2 = %q (%d runes)", string(got), len(got))
	}
	if got := e.mod.GroupOf("r3"); got != "" {
		t.Fatalf("r3 = %q: a group with nothing left is not a label", got)
	}
	var resp struct{ Count int }
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Fatalf("count = %d: only the labels kept are counted (%s)", resp.Count, rec.Body.String())
	}
}

// A group with nothing left after the rules is not a claim on the root: it does not shadow what another device wrote.
func TestPut_ABlankGroupDoesNotShadowAnotherDevice(t *testing.T) {
	e := newEnv(t)
	e.put(e.admin, payload("dev-a", l("root-1", "Real")))
	e.put(e.admin, payload("dev-b", l("root-1", " \u200b\n ")))
	if got := e.mod.GroupOf("root-1"); got != "Real" {
		t.Fatalf("root-1 = %q: the blank write from the later device must not win", got)
	}
}

// A device token never reaches the route (the scope middleware refuses it first; this is the second wall).
func TestPut_ADevicePrincipalIs403(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("PUT", "/api/notify/group-labels", strings.NewReader(`{"device":"dev-a","labels":[]}`))
	req = req.WithContext(devices.WithPrincipal(req.Context(), devices.Principal{ID: "d_aaaaaaaaaaaa"}))
	rec := httptest.NewRecorder()
	e.mod.handlePut(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestPut_NoTokenIs401(t *testing.T) {
	e := newEnv(t)
	if rec := e.put("", payload("dev-a")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d", rec.Code)
	}
}

// What was written is still there after the daemon restarts: the memory table is rebuilt from notify.db.
func TestRestart_TheTableIsRebuiltFromTheFile(t *testing.T) {
	dir := t.TempDir()
	e := newEnvIn(t, dir)
	e.put(e.admin, payload("dev-a", l("root-1", "Alpha")))
	e.mod.Stop(context.Background())
	e.mod.Close()

	again := newEnvIn(t, dir)
	if got := again.mod.GroupOf("root-1"); got != "Alpha" {
		t.Fatalf("after the restart: %q", got)
	}
}

// A device that has not written for 30 days goes, with all its rows, from the file and from the table.
func TestSweep_RemovesTheQuietDevicesFromTheTableToo(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UnixMilli()
	e.mod.store.now = func() int64 { return now - int64(retention/time.Millisecond) - 1000 }
	e.put(e.admin, payload("old-dev", l("root-1", "Old")))
	e.mod.store.now = func() int64 { return now }
	e.put(e.admin, payload("fresh-dev", l("root-2", "Fresh")))
	if err := e.mod.sweep(now); err != nil {
		t.Fatal(err)
	}
	if got := e.mod.GroupOf("root-1"); got != "" {
		t.Fatalf("root-1 = %q", got)
	}
	if got := e.mod.GroupOf("root-2"); got != "Fresh" {
		t.Fatalf("root-2 = %q", got)
	}
}

// A store that cannot be opened turns the module off: no route, no overlay, ready=false, and the daemon goes on.
func TestInit_ASoftFailureLeavesNoRouteAndNoOverlay(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "notify.db")
	if err := os.MkdirAll(blocked, 0o755); err != nil { // a directory where the file should be
		t.Fatal(err)
	}
	e := newEnvIn(t, dir)
	st := e.mod.Status()
	if ready, _ := st["ready"].(bool); ready {
		t.Fatalf("status %v", st)
	}
	if msg, _ := st["init_error"].(string); msg == "" {
		t.Fatalf("status %v: the reason must be recorded", st)
	}
	if rec := e.put(e.admin, payload("dev-a")); rec.Code != http.StatusNotFound {
		t.Fatalf("the route exists: %d", rec.Code)
	}
	if _, ok := e.c.Registry.Get(notifylabel.OverlayKey); ok {
		t.Fatal("a module that is off published an overlay")
	}
	if got := e.mod.GroupOf("anything"); got != "" {
		t.Fatalf("GroupOf = %q", got)
	}
}

func TestStatus_ReadyWhenTheStoreIsOpen(t *testing.T) {
	e := newEnv(t)
	if ready, _ := e.mod.Status()["ready"].(bool); !ready {
		t.Fatalf("status %v", e.mod.Status())
	}
}
