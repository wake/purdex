package peers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchRemote_OK(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/peers" {
			t.Errorf("path = %q, want /api/peers", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"host_id":"remote:abc","ok":true,"partial":false,"peers":[]}`))
	}))
	defer srv.Close()

	client := newRemoteClient()
	env, err := fetchRemote(context.Background(), client, srv.URL, "pdxp_secret")
	if err != nil {
		t.Fatalf("fetchRemote: %v", err)
	}
	if gotAuth != "Bearer pdxp_secret" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer pdxp_secret")
	}
	if env.HostID != "remote:abc" || !env.OK {
		t.Errorf("env = %+v, want host_id=remote:abc ok=true", env)
	}
}

func TestFetchRemote_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := newRemoteClient()
	_, err := fetchRemote(context.Background(), client, srv.URL, "bad-token")
	if err == nil {
		t.Fatalf("fetchRemote: want error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want it to mention 401", err.Error())
	}
}

func TestFetchRemote_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"host_id":"remote","ok":true,"partial":false,"peers":[]}`))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 50 * time.Millisecond}
	_, err := fetchRemote(context.Background(), client, srv.URL, "token")
	if err == nil {
		t.Fatalf("fetchRemote: want error, got nil")
	}
}

func TestFetchRemote_OversizeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		padding := strings.Repeat("x", (16*1024*1024)+1024)
		w.Write([]byte(`{"host_id":"remote","ok":true,"partial":false,"peers":[],"pad":"` + padding + `"}`))
	}))
	defer srv.Close()

	client := newRemoteClient()
	_, err := fetchRemote(context.Background(), client, srv.URL, "token")
	if err == nil {
		t.Fatalf("fetchRemote: want error, got nil")
	}
}

func TestFetchRemote_RedirectNotFollowed(t *testing.T) {
	secondServerCalled := false
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondServerCalled = true
		w.Write([]byte(`{"host_id":"second","ok":true,"partial":false,"peers":[]}`))
	}))
	defer second.Close()

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL+"/api/peers", http.StatusFound)
	}))
	defer first.Close()

	client := newRemoteClient()
	_, err := fetchRemote(context.Background(), client, first.URL, "token")
	if err == nil {
		t.Fatalf("fetchRemote: want error, got nil")
	}
	if !strings.Contains(err.Error(), "302") {
		t.Errorf("error = %q, want it to mention 302", err.Error())
	}
	if secondServerCalled {
		t.Errorf("second server was called; redirect must not be followed")
	}
}

func TestFetchRemote_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	client := newRemoteClient()
	_, err := fetchRemote(context.Background(), client, srv.URL, "token")
	if err == nil {
		t.Fatalf("fetchRemote: want error, got nil")
	}
}
