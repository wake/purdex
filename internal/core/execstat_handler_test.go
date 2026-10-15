package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/execstat"
)

func TestHandleExecstatListsTagsWithTheirWindowsAndASum(t *testing.T) {
	execstat.ResetTags()
	defer execstat.ResetTags()
	execstat.Observe("b.x", 5*time.Millisecond)
	execstat.Observe("a.y", time.Millisecond)
	execstat.Observe("a.y", time.Millisecond)
	execstat.Event("hook.PdxPreToolUse")
	execstat.SetGauge("sessions", 15)
	execstat.SetGaugeText("resources-mode", "measure")

	rec := httptest.NewRecorder()
	handleExecstat(rec, httptest.NewRequest(http.MethodGet, "/api/debug/execstat", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	var got struct {
		Tags   []execstat.Series `json:"tags"`
		Sum    map[string]int64  `json:"sum"`
		Events []execstat.Series `json:"events"`
		Gauges []execstat.Gauge  `json:"gauges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 2 || got.Tags[0].Name != "a.y" || got.Tags[0].Total != 2 || got.Tags[0].Last1 != 2 || got.Tags[0].Last5 != 2 || got.Tags[0].Last60 != 2 {
		t.Fatalf("tags = %+v", got.Tags)
	}
	if got.Tags[1].Name != "b.x" || got.Tags[1].TotalMS != 5 {
		t.Fatalf("tags = %+v", got.Tags)
	}
	if len(got.Events) != 1 || got.Events[0].Name != "hook.PdxPreToolUse" || got.Events[0].Total != 1 {
		t.Fatalf("events = %+v", got.Events)
	}
	if len(got.Gauges) != 2 || got.Gauges[0].Name != "resources-mode" || got.Gauges[0].Text != "measure" || got.Gauges[1].Name != "sessions" || got.Gauges[1].Value != 15 {
		t.Fatalf("gauges = %+v", got.Gauges)
	}
	if got.Sum["total"] != 3 || got.Sum["last_1m"] != 3 || got.Sum["last_5m"] != 3 || got.Sum["last_60m"] != 3 {
		t.Fatalf("sum = %v", got.Sum)
	}
}

// The route is on the daemon's mux (GET only).
func TestExecstatRouteIsRegisteredByTheCoreRoutes(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/debug/execstat", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/debug/execstat", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rec.Code)
	}
}
