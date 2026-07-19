package dispatch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newFixtureClient starts a server serving fixture file and returns a client
// plus a pointer to the last received request (updated per call).
func newFixtureClient(t *testing.T, file string) (*Client, func() *http.Request) {
	t.Helper()
	f := loadFixture(t, file)
	var last *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.HTTPStatus)
		_, _ = w.Write(f.Payload)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "test-token")
	return c, func() *http.Request { return last }
}

func TestClient_PollPending_Success(t *testing.T) {
	c, lastReq := newFixtureClient(t, "e1_pending_list.success.json")

	pendings, err := c.PollPending(context.Background())
	if err != nil {
		t.Fatalf("PollPending: %v", err)
	}
	if len(pendings) != 2 {
		t.Fatalf("want 2 pending, got %d: %+v", len(pendings), pendings)
	}
	if pendings[0].DispatchID != "dsp_a1" || pendings[0].IssueID != "iss_42" {
		t.Errorf("pending[0] = %+v", pendings[0])
	}
	if pendings[1].DispatchID != "dsp_a2" || pendings[1].IssueID != "iss_43" {
		t.Errorf("pending[1] = %+v", pendings[1])
	}

	// E1 wire shape: GET /daemon/dispatches?status=pending with Bearer auth.
	req := lastReq()
	if req.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", req.Method)
	}
	if got := req.URL.Path; got != "/daemon/dispatches" {
		t.Errorf("path = %s", got)
	}
	if got := req.URL.Query().Get("status"); got != "pending" {
		t.Errorf("status query = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("auth header = %q", got)
	}
}

func TestClient_Claim_Success(t *testing.T) {
	c, lastReq := newFixtureClient(t, "e2_claim.success.json")

	res, err := c.Claim(context.Background(), "dsp_a1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if res.DispatchID != "dsp_a1" || res.Status != "claimed" {
		t.Errorf("claim result = %+v", res)
	}
	if res.ExecutionID != "" {
		t.Errorf("first claim should carry no execution_id, got %q", res.ExecutionID)
	}

	req := lastReq()
	if req.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.Method)
	}
	if req.URL.Path != "/daemon/dispatches/dsp_a1/claim" {
		t.Errorf("path = %s", req.URL.Path)
	}
}

func TestClient_Claim_DuplicateSameDaemon(t *testing.T) {
	c, _ := newFixtureClient(t, "e2_claim.duplicate_same_daemon.json")

	res, err := c.Claim(context.Background(), "dsp_a1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if res.Status != "claimed" {
		t.Errorf("status = %q, want claimed", res.Status)
	}
	if res.ExecutionID != "exc_9" {
		t.Errorf("idempotent re-claim should echo execution_id exc_9, got %q", res.ExecutionID)
	}
}

func TestClient_Claim_ConflictOtherDaemon(t *testing.T) {
	c, _ := newFixtureClient(t, "e2_claim.conflict_other_daemon.json")

	_, err := c.Claim(context.Background(), "dsp_a1")
	if !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("want ErrAlreadyClaimed, got %v", err)
	}
}

func TestClient_Claim_DispatchNotFound(t *testing.T) {
	// The dispatch_not_found fixture (E3) is contract-shared with E2 claim.
	c, _ := newFixtureClient(t, "e3_fetch.dispatch_not_found.json")

	_, err := c.Claim(context.Background(), "dsp_missing")
	if !errors.Is(err, ErrDispatchNotFound) {
		t.Fatalf("want ErrDispatchNotFound, got %v", err)
	}
}

func TestClient_Fetch_Success(t *testing.T) {
	c, lastReq := newFixtureClient(t, "e3_fetch.success.json")

	d, err := c.Fetch(context.Background(), "dsp_a1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if d.DispatchID != "dsp_a1" {
		t.Errorf("dispatch_id = %q", d.DispatchID)
	}
	if d.Issue.IssueID != "iss_42" || d.Issue.Title == "" || d.Issue.Body == "" {
		t.Errorf("issue = %+v", d.Issue)
	}
	if d.RepoLocation.ProjectID != "prj_1" || d.RepoLocation.LocalDir != "/Users/wake/Workspace/wake/example" {
		t.Errorf("repo_location = %+v", d.RepoLocation)
	}
	if !d.RepoLocation.IsOrigin {
		t.Errorf("is_origin should be true")
	}
	if d.SandboxProfile != "workspace-write" {
		t.Errorf("sandbox_profile = %q", d.SandboxProfile)
	}

	req := lastReq()
	if req.Method != http.MethodGet || req.URL.Path != "/daemon/dispatches/dsp_a1" {
		t.Errorf("fetch wire = %s %s", req.Method, req.URL.Path)
	}
}

func TestClient_Fetch_DispatchNotFound(t *testing.T) {
	c, _ := newFixtureClient(t, "e3_fetch.dispatch_not_found.json")

	_, err := c.Fetch(context.Background(), "dsp_missing")
	if !errors.Is(err, ErrDispatchNotFound) {
		t.Fatalf("want ErrDispatchNotFound, got %v", err)
	}
}

func TestClient_SchemaIncompatible(t *testing.T) {
	c, _ := newFixtureClient(t, "x_schema_incompatible.json")

	_, err := c.PollPending(context.Background())
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Fatalf("want ErrSchemaIncompatible, got %v", err)
	}
}

func TestClient_Unauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		c := NewClient(srv.URL, "bad-token")
		_, err := c.PollPending(context.Background())
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("status %d: want ErrUnauthorized, got %v", status, err)
		}
		srv.Close()
	}
}

func TestClient_ServerError_Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-token")

	_, err := c.PollPending(context.Background())
	if err == nil {
		t.Fatal("want error on 503")
	}
	if !Retryable(err) {
		t.Errorf("5xx should be Retryable, got %v", err)
	}
	// 5xx must not masquerade as a benign skip sentinel.
	if errors.Is(err, ErrDispatchNotFound) || errors.Is(err, ErrAlreadyClaimed) {
		t.Errorf("5xx misclassified as skip sentinel: %v", err)
	}
}

// A 200 body whose schema_version is not 1 must be rejected, never silently
// mis-parsed (defensive read side of the schema contract).
func TestClient_SuccessBodyWrongSchemaVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"schema_version":2,"dispatches":[]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-token")

	_, err := c.PollPending(context.Background())
	if !errors.Is(err, ErrSchemaIncompatible) {
		t.Fatalf("want ErrSchemaIncompatible, got %v", err)
	}
}
