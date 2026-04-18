package dev

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleDaemonCheck_ReturnsHashes(t *testing.T) {
	t.Setenv("PDX_DEV_UPDATE", "1")
	m := New(".") // repoRoot = cwd (git worktree)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dev/daemon/check")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	var body daemonCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.LatestHash == "" {
		t.Error("latest_hash empty — expected a git commit short hash")
	}
}

func TestHandleDaemonCheck_AvailableFlag(t *testing.T) {
	old := BakedInHash
	defer func() { BakedInHash = old }()

	BakedInHash = "definitely-not-a-real-hash-0000"
	t.Setenv("PDX_DEV_UPDATE", "1")
	m := New(".")
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/dev/daemon/check")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body daemonCheckResponse
	json.NewDecoder(resp.Body).Decode(&body)
	if !body.Available {
		t.Errorf("expected Available=true when BakedInHash differs, got %+v", body)
	}
}
