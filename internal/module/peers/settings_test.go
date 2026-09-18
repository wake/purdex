// internal/module/peers/settings_test.go
package peers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
)

func TestHandleGetSettings_ReturnsDeliverAndAlias(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	c.Cfg.Peers.Deliver = true
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/settings", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.SettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.Deliver {
		t.Errorf("Deliver = false, want true")
	}
	if got.Alias != "mini-lab" {
		t.Errorf("Alias = %q, want mini-lab", got.Alias)
	}
}

func TestHandleGetSettings_HostPrincipal_Forbidden(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/settings", nil, hostPrincipal("air"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleGetSettings_NoPrincipal_Forbidden(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/settings", nil, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlePutSettings_PersistsToCfgPath(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	deliver := true
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Deliver: &deliver}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.SettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.Deliver {
		t.Errorf("response Deliver = false, want true")
	}
	if got.Alias != "mini-lab" {
		t.Errorf("response Alias = %q, want mini-lab", got.Alias)
	}

	// Persisted to disk at CfgPath, not just in memory.
	onDisk := loadCfg(t, cfgPath)
	if !onDisk.Peers.Deliver {
		t.Errorf("on-disk Peers.Deliver = false, want true")
	}

	// And the in-memory config reflects it too.
	c.CfgMu.RLock()
	inMemory := c.Cfg.Peers.Deliver
	c.CfgMu.RUnlock()
	if !inMemory {
		t.Errorf("in-memory Peers.Deliver = false, want true")
	}
}

// TestHandlePutSettings_OmittedDeliver_LeavesValueUnchanged pins that
// Deliver is a pointer field: an absent "deliver" key must not reset it to
// false.
func TestHandlePutSettings_OmittedDeliver_LeavesValueUnchanged(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	c.Cfg.Peers.Deliver = true
	if err := config.WriteFile(cfgPath, *c.Cfg); err != nil {
		t.Fatalf("seed cfgPath: %v", err)
	}
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got ipeers.SettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.Deliver {
		t.Errorf("Deliver = false, want true (unchanged)")
	}
}

func TestHandlePutSettings_HostPrincipal_Forbidden(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	deliver := true
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Deliver: &deliver}, hostPrincipal("air"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}

	onDisk := loadCfg(t, cfgPath)
	if onDisk.Peers.Deliver {
		t.Errorf("on-disk Peers.Deliver = true, want false (host principal must not persist anything)")
	}
}

func TestHandlePutSettings_NoPrincipal_Forbidden(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	deliver := true
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Deliver: &deliver}, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlePutSettings_InvalidJSON_BadRequest(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPut, "/api/peers/settings", strings.NewReader("{not json"))
	ctx := middleware.WithPrincipal(context.Background(), *adminPrincipal())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req.WithContext(ctx))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandlePutSettings_BodyBounded pins that the PUT body is capped like
// the other handlers' (1 MiB): an oversized body is a 400 and changes
// nothing.
func TestHandlePutSettings_BodyBounded(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	body := `{"deliver":true,"pad":"` + strings.Repeat("x", maxSettingsBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/peers/settings", strings.NewReader(body))
	req = req.WithContext(middleware.WithPrincipal(context.Background(), *adminPrincipal()))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	c.CfgMu.RLock()
	deliver := c.Cfg.Peers.Deliver
	c.CfgMu.RUnlock()
	if deliver {
		t.Error("Deliver flipped to true by an oversized body")
	}
}

// ---- self alias (#1196, spec S-1…S-3, S-6) ----

// decodeSettings unmarshals a 200 body into the wire shape, failing the
// test on a non-200 or a malformed body.
func decodeSettings(t *testing.T, rr *httptest.ResponseRecorder) ipeers.SettingsResponse {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got ipeers.SettingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	return got
}

// decodeError unmarshals a {error} body and returns the message.
func decodeError(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal error body: %v; body=%s", err, rr.Body.String())
	}
	return got["error"]
}

func strp(s string) *string { return &s }

// TestHandleGetSettings_AliasSource_HostID: an unset [peers] alias is
// derived from host_id and reported as such (S-3).
func TestHandleGetSettings_AliasSource_HostID(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	got := decodeSettings(t, doHostsRequest(t, m, http.MethodGet, "/api/peers/settings", nil, adminPrincipal()))
	if got.Alias != "mini-lab" {
		t.Errorf("Alias = %q, want mini-lab (derived)", got.Alias)
	}
	if got.AliasSource != "host_id" {
		t.Errorf("AliasSource = %q, want host_id", got.AliasSource)
	}
}

// TestHandleGetSettings_AliasSource_Config: a configured alias is reported
// as "config" (S-3).
func TestHandleGetSettings_AliasSource_Config(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "mlab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	got := decodeSettings(t, doHostsRequest(t, m, http.MethodGet, "/api/peers/settings", nil, adminPrincipal()))
	if got.Alias != "mlab" {
		t.Errorf("Alias = %q, want mlab", got.Alias)
	}
	if got.AliasSource != "config" {
		t.Errorf("AliasSource = %q, want config", got.AliasSource)
	}
}

// TestHandlePutSettings_Alias_SetsAndPersists: PUT {alias:"mlab"} stores
// it verbatim (S-2), persists it, answers mlab/config, and leaves Deliver
// alone.
func TestHandlePutSettings_Alias_SetsAndPersists(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "", "admin-tok", nil)
	c.Cfg.Peers.Deliver = true
	if err := config.WriteFile(cfgPath, *c.Cfg); err != nil {
		t.Fatalf("seed cfgPath: %v", err)
	}
	m := newHostsTestModule(t, c, nil)

	got := decodeSettings(t, doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Alias: strp("mlab")}, adminPrincipal()))
	if got.Alias != "mlab" || got.AliasSource != "config" {
		t.Errorf("response = %+v, want Alias=mlab AliasSource=config", got)
	}
	if !got.Deliver {
		t.Errorf("response Deliver = false, want true (unchanged)")
	}

	onDisk := loadCfg(t, cfgPath)
	if onDisk.Peers.Alias != "mlab" {
		t.Errorf("on-disk Peers.Alias = %q, want mlab", onDisk.Peers.Alias)
	}
	if !onDisk.Peers.Deliver {
		t.Errorf("on-disk Peers.Deliver = false, want true (unchanged)")
	}

	c.CfgMu.RLock()
	inMem := c.Cfg.Peers.Alias
	c.CfgMu.RUnlock()
	if inMem != "mlab" {
		t.Errorf("in-memory Peers.Alias = %q, want mlab", inMem)
	}
}

// TestHandlePutSettings_Alias_Verbatim: no trim, no case fold (S-2) —
// "MLab" is stored as "MLab".
func TestHandlePutSettings_Alias_Verbatim(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	got := decodeSettings(t, doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Alias: strp("MLab")}, adminPrincipal()))
	if got.Alias != "MLab" {
		t.Errorf("response Alias = %q, want MLab verbatim", got.Alias)
	}
	if onDisk := loadCfg(t, cfgPath); onDisk.Peers.Alias != "MLab" {
		t.Errorf("on-disk Peers.Alias = %q, want MLab verbatim", onDisk.Peers.Alias)
	}
}

// TestHandlePutSettings_Alias_EmptyClears: PUT {alias:""} on a configured
// core clears Peers.Alias (S-2) and the effective alias falls back to the
// host_id-derived one.
func TestHandlePutSettings_Alias_EmptyClears(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mlab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	got := decodeSettings(t, doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Alias: strp("")}, adminPrincipal()))
	if got.Alias != "mini-lab" || got.AliasSource != "host_id" {
		t.Errorf("response = %+v, want Alias=mini-lab AliasSource=host_id", got)
	}
	if onDisk := loadCfg(t, cfgPath); onDisk.Peers.Alias != "" {
		t.Errorf("on-disk Peers.Alias = %q, want \"\"", onDisk.Peers.Alias)
	}
}

// TestHandlePutSettings_Alias_AbsentLeavesUnchanged: an absent "alias" key
// is "unchanged" (S-2), with and without a deliver in the same body.
func TestHandlePutSettings_Alias_AbsentLeavesUnchanged(t *testing.T) {
	t.Run("with deliver", func(t *testing.T) {
		c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mlab", "admin-tok", nil)
		m := newHostsTestModule(t, c, nil)

		deliver := true
		got := decodeSettings(t, doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Deliver: &deliver}, adminPrincipal()))
		if got.Alias != "mlab" || got.AliasSource != "config" {
			t.Errorf("response = %+v, want Alias=mlab AliasSource=config", got)
		}
		if !got.Deliver {
			t.Errorf("response Deliver = false, want true")
		}
		onDisk := loadCfg(t, cfgPath)
		if onDisk.Peers.Alias != "mlab" {
			t.Errorf("on-disk Peers.Alias = %q, want mlab (unchanged)", onDisk.Peers.Alias)
		}
		if !onDisk.Peers.Deliver {
			t.Errorf("on-disk Peers.Deliver = false, want true")
		}
	})

	t.Run("empty body object", func(t *testing.T) {
		c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mlab", "admin-tok", nil)
		c.Cfg.Peers.Deliver = true
		if err := config.WriteFile(cfgPath, *c.Cfg); err != nil {
			t.Fatalf("seed cfgPath: %v", err)
		}
		m := newHostsTestModule(t, c, nil)

		got := decodeSettings(t, doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{}, adminPrincipal()))
		if got.Alias != "mlab" || got.AliasSource != "config" || !got.Deliver {
			t.Errorf("response = %+v, want Alias=mlab AliasSource=config Deliver=true (nothing changes)", got)
		}
		onDisk := loadCfg(t, cfgPath)
		if onDisk.Peers.Alias != "mlab" || !onDisk.Peers.Deliver {
			t.Errorf("on-disk = alias %q deliver %v, want mlab/true (nothing changes)", onDisk.Peers.Alias, onDisk.Peers.Deliver)
		}
	})
}

// TestHandlePutSettings_Alias_Reserved_BadRequest: ".." → 400 with
// ValidateSelfAlias's text (S-6), nothing written.
func TestHandlePutSettings_Alias_Reserved_BadRequest(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mlab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Alias: strp("..")}, adminPrincipal())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if msg := decodeError(t, rr); !strings.Contains(msg, "reserved") {
		t.Errorf("error = %q, want it to contain \"reserved\"", msg)
	}
	if onDisk := loadCfg(t, cfgPath); onDisk.Peers.Alias != "mlab" {
		t.Errorf("on-disk Peers.Alias = %q, want mlab (nothing written)", onDisk.Peers.Alias)
	}
}

// TestHandlePutSettings_Alias_BadPattern_BadRequest: a space fails the
// pattern → 400 (S-6), nothing written.
func TestHandlePutSettings_Alias_BadPattern_BadRequest(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mlab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Alias: strp("bad alias")}, adminPrincipal())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if msg := decodeError(t, rr); !strings.Contains(msg, "must match") {
		t.Errorf("error = %q, want it to contain \"must match\"", msg)
	}
	if onDisk := loadCfg(t, cfgPath); onDisk.Peers.Alias != "mlab" {
		t.Errorf("on-disk Peers.Alias = %q, want mlab (nothing written)", onDisk.Peers.Alias)
	}
}

// TestHandlePutSettings_Alias_CollidesWithHost_Conflict: a
// case-insensitive match with a configured [[peers.hosts]] alias → 409
// with the exact S-6 text, nothing written.
func TestHandlePutSettings_Alias_CollidesWithHost_Conflict(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "air26", URL: "http://air:7860", Token: "pdxp_" + strings.Repeat("a", 32)}}
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mlab", "admin-tok", hosts)
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Alias: strp("AIR26")}, adminPrincipal())
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if msg, want := decodeError(t, rr), `alias "AIR26" is already used by a peer host`; msg != want {
		t.Errorf("error = %q, want %q", msg, want)
	}
	if onDisk := loadCfg(t, cfgPath); onDisk.Peers.Alias != "mlab" {
		t.Errorf("on-disk Peers.Alias = %q, want mlab (nothing written)", onDisk.Peers.Alias)
	}
}

// TestHandlePutSettings_Alias_Collision_DeliverNotWrittenEither: one
// closure, one transaction — a rejected alias aborts the deliver change
// sent in the same body.
func TestHandlePutSettings_Alias_Collision_DeliverNotWrittenEither(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "x", URL: "http://x:7860", Token: "pdxp_" + strings.Repeat("b", 32)}}
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "", "admin-tok", hosts)
	m := newHostsTestModule(t, c, nil)

	deliver := true
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Deliver: &deliver, Alias: strp("x")}, adminPrincipal())
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}

	onDisk := loadCfg(t, cfgPath)
	if onDisk.Peers.Deliver {
		t.Errorf("on-disk Peers.Deliver = true, want false (rejected alias must abort the whole closure)")
	}
	if onDisk.Peers.Alias != "" {
		t.Errorf("on-disk Peers.Alias = %q, want \"\"", onDisk.Peers.Alias)
	}
	c.CfgMu.RLock()
	inMemDeliver, inMemAlias := c.Cfg.Peers.Deliver, c.Cfg.Peers.Alias
	c.CfgMu.RUnlock()
	if inMemDeliver || inMemAlias != "" {
		t.Errorf("in-memory = deliver %v alias %q, want false/\"\"", inMemDeliver, inMemAlias)
	}
}

// TestHandlePutSettings_Alias_HostPrincipal_Forbidden: the admin-only gate
// covers the alias field too.
func TestHandlePutSettings_Alias_HostPrincipal_Forbidden(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Alias: strp("mlab")}, hostPrincipal("air"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if onDisk := loadCfg(t, cfgPath); onDisk.Peers.Alias != "" {
		t.Errorf("on-disk Peers.Alias = %q, want \"\" (host principal must not persist anything)", onDisk.Peers.Alias)
	}
}

// TestHandlePutSettings_Alias_NullLeavesUnchanged pins codex F6: a JSON
// `null` decodes to a nil pointer and so means "unchanged", exactly like an
// absent key. Raw body because the typed request struct cannot express
// null.
func TestHandlePutSettings_Alias_NullLeavesUnchanged(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mlab", "admin-tok", nil)
	m := newHostsTestModule(t, c, nil)

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPut, "/api/peers/settings", strings.NewReader(`{"alias":null,"deliver":true}`))
	req = req.WithContext(middleware.WithPrincipal(context.Background(), *adminPrincipal()))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	got := decodeSettings(t, rr)
	if got.Alias != "mlab" || got.AliasSource != "config" {
		t.Errorf("response = %+v, want Alias=mlab AliasSource=config (null = unchanged)", got)
	}
	if !got.Deliver {
		t.Errorf("response Deliver = false, want true (the other field still applies)")
	}
	onDisk := loadCfg(t, cfgPath)
	if onDisk.Peers.Alias != "mlab" {
		t.Errorf("on-disk Peers.Alias = %q, want mlab (null = unchanged)", onDisk.Peers.Alias)
	}
	if !onDisk.Peers.Deliver {
		t.Errorf("on-disk Peers.Deliver = false, want true")
	}
}

// TestHandlePutSettings_Alias_ClearCollidesWithHost_Conflict pins codex
// F1: clearing makes the host_id-derived alias effective, and a peer entry
// may carry exactly that name because a different self alias allowed it.
// The derived alias has to clear the same bar → 409, nothing written.
func TestHandlePutSettings_Alias_ClearCollidesWithHost_Conflict(t *testing.T) {
	hosts := []config.PeerHost{{Alias: "mini-lab", URL: "http://other:7860", Token: "pdxp_" + strings.Repeat("c", 32)}}
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc", "mlab", "admin-tok", hosts)
	m := newHostsTestModule(t, c, nil)

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", ipeers.PutSettingsRequest{Alias: strp("")}, adminPrincipal())
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if msg, want := decodeError(t, rr), `clearing the alias would make it "mini-lab", which is already used by a peer host`; msg != want {
		t.Errorf("error = %q, want %q", msg, want)
	}
	if onDisk := loadCfg(t, cfgPath); onDisk.Peers.Alias != "mlab" {
		t.Errorf("on-disk Peers.Alias = %q, want mlab (nothing written)", onDisk.Peers.Alias)
	}
	c.CfgMu.RLock()
	inMem := c.Cfg.Peers.Alias
	c.CfgMu.RUnlock()
	if inMem != "mlab" {
		t.Errorf("in-memory Peers.Alias = %q, want mlab (nothing written)", inMem)
	}
}
