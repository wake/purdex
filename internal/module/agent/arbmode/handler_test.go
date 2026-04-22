package arbmode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSnapshotter is a mock Snapshotter that records call counts.
type fakeSnapshotter struct {
	snap      Snapshot
	callCount int
}

func (f *fakeSnapshotter) Snapshot() Snapshot {
	f.callCount++
	return f.snap
}

// ── TestArbModeHandler_Get_Snapshot_Shape ────────────────────────────────────

func TestArbModeHandler_Get_Snapshot_Shape(t *testing.T) {
	fake := &fakeSnapshotter{
		snap: Snapshot{
			Current:   ModePassthrough,
			Pending:   ModeAuthoritative,
			EnvLocked: false,
		},
	}
	h := NewHandler(fake)

	req := httptest.NewRequest(http.MethodGet, "/api/agent/arbitrator/mode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got struct {
		Current   ArbMode `json:"current"`
		Pending   ArbMode `json:"pending"`
		EnvLocked bool    `json:"env_locked"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Current != ModePassthrough {
		t.Errorf("current = %q, want %q", got.Current, ModePassthrough)
	}
	if got.Pending != ModeAuthoritative {
		t.Errorf("pending = %q, want %q", got.Pending, ModeAuthoritative)
	}
	if got.EnvLocked {
		t.Error("env_locked = true, want false")
	}
}

// ── TestArbModeHandler_Get_Method_Disallowed ─────────────────────────────────

func TestArbModeHandler_Get_Method_Disallowed(t *testing.T) {
	h := NewHandler(&fakeSnapshotter{})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/agent/arbitrator/mode", strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
			}
			allow := rec.Header().Get("Allow")
			if allow != http.MethodGet {
				t.Errorf("Allow header = %q, want %q", allow, http.MethodGet)
			}
		})
	}
}

// ── TestArbModeHandler_Get_Snapshot_UsesSingleCall ───────────────────────────

func TestArbModeHandler_Get_Snapshot_UsesSingleCall(t *testing.T) {
	fake := &fakeSnapshotter{
		snap: Snapshot{
			Current:   ModePassthrough,
			Pending:   ModePassthrough,
			EnvLocked: false,
		},
	}
	h := NewHandler(fake)

	req := httptest.NewRequest(http.MethodGet, "/api/agent/arbitrator/mode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if fake.callCount != 1 {
		t.Errorf("Snapshot() called %d times, want exactly 1", fake.callCount)
	}
}

// ── TestArbModeHandler_ContentType_JSON ──────────────────────────────────────

func TestArbModeHandler_ContentType_JSON(t *testing.T) {
	h := NewHandler(&fakeSnapshotter{
		snap: Snapshot{
			Current: ModePassthrough,
			Pending: ModePassthrough,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/agent/arbitrator/mode", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
