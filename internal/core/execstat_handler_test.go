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

	rec := httptest.NewRecorder()
	handleExecstat(rec, httptest.NewRequest(http.MethodGet, "/api/debug/execstat", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	var got struct {
		Tags []execstat.TagStat `json:"tags"`
		Sum  map[string]int64   `json:"sum"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 2 || got.Tags[0].Tag != "a.y" || got.Tags[0].Total != 2 || got.Tags[0].Last1 != 2 || got.Tags[0].Last5 != 2 || got.Tags[0].Last60 != 2 {
		t.Fatalf("tags = %+v", got.Tags)
	}
	if got.Tags[1].Tag != "b.x" || got.Tags[1].TotalMS != 5 {
		t.Fatalf("tags = %+v", got.Tags)
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
