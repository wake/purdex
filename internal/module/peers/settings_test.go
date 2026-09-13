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
)

func TestHandleGetSettings_ReturnsDeliverAndAlias(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	c.Cfg.Peers.Deliver = true
	m := newHostsTestModule(c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/settings", nil, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got settingsResponse
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
	m := newHostsTestModule(c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/settings", nil, hostPrincipal("air"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleGetSettings_NoPrincipal_Forbidden(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(c, nil)

	rr := doHostsRequest(t, m, http.MethodGet, "/api/peers/settings", nil, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlePutSettings_PersistsToCfgPath(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(c, nil)

	deliver := true
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", putSettingsRequest{Deliver: &deliver}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got settingsResponse
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
	m := newHostsTestModule(c, nil)

	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", putSettingsRequest{}, adminPrincipal())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var got settingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body.String())
	}
	if !got.Deliver {
		t.Errorf("Deliver = false, want true (unchanged)")
	}
}

func TestHandlePutSettings_HostPrincipal_Forbidden(t *testing.T) {
	c, cfgPath := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(c, nil)

	deliver := true
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", putSettingsRequest{Deliver: &deliver}, hostPrincipal("air"))
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
	m := newHostsTestModule(c, nil)

	deliver := true
	rr := doHostsRequest(t, m, http.MethodPut, "/api/peers/settings", putSettingsRequest{Deliver: &deliver}, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlePutSettings_InvalidJSON_BadRequest(t *testing.T) {
	c, _ := newHostsTestCore(t, "mini-lab:abc123", "mini-lab", "admin-tok", nil)
	m := newHostsTestModule(c, nil)

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
