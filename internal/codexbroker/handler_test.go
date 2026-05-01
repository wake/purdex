package codexbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// newHandlerFromFixtures builds a Handler whose Scanner is wired to the
// canonical testdata fixtures. The result mirrors newScannerFromFixtures from
// inventory_test.go but exposes the scanner via *Handler.
func newHandlerFromFixtures(t *testing.T) *Handler {
	t.Helper()
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")
	lister := NewFakeProcessListerFromText(src)
	scanner := NewScanner(ScannerOpts{
		FS:              NewOsFS(),
		Lister:          lister,
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	return NewHandler(scanner)
}

// TestHandler_Get_200 verifies that a successful Scan returns 200 with a JSON
// body that includes every summary field listed in spec §4.3.
func TestHandler_Get_200(t *testing.T) {
	h := newHandlerFromFixtures(t)
	req := httptest.NewRequest(http.MethodGet, "/api/codex/brokers", nil)
	rec := httptest.NewRecorder()

	h.HandleBrokers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v\n%s", err, rec.Body.String())
	}
	for _, key := range []string{"scannedAt", "scanDurationMs", "deadlineMs", "partial", "brokers", "summary"} {
		if _, ok := body[key]; !ok {
			t.Errorf("response missing top-level field %q", key)
		}
	}

	var summary map[string]json.RawMessage
	if err := json.Unmarshal(body["summary"], &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	for _, key := range []string{
		"total",
		"withProcess",
		"withStateDir",
		"withSocket",
		"anomalyCount",
		"duplicateRuntimeCount",
		"scanSourceTimeouts",
	} {
		if _, ok := summary[key]; !ok {
			t.Errorf("summary missing field %q", key)
		}
	}
}

// TestHandler_Method_405 verifies that non-GET methods return 405.
func TestHandler_Method_405(t *testing.T) {
	h := newHandlerFromFixtures(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/codex/brokers", nil)
			rec := httptest.NewRecorder()
			h.HandleBrokers(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status for %s = %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

// TestHandler_PsFailure_503 verifies that ErrPsUnavailable from the Scanner
// (i.e. ps fail AND no state/socket inventory available) produces a 503 with
// a structured error body. Per spec §4.3 + R2 API-7 fix.
func TestHandler_PsFailure_503(t *testing.T) {
	emptyDir := t.TempDir() // forces state/socket scans to be empty
	lister := NewFakeProcessLister(nil).WithError(errors.New("ps not found"))
	scanner := NewScanner(ScannerOpts{
		FS:              NewOsFS(),
		Lister:          lister,
		PluginDataRoot:  emptyDir,
		SocketRoots:     []string{emptyDir},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	h := NewHandler(scanner)

	req := httptest.NewRequest(http.MethodGet, "/api/codex/brokers", nil)
	rec := httptest.NewRecorder()
	h.HandleBrokers(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v\n%s", err, rec.Body.String())
	}
	if _, ok := body["error"].(string); !ok {
		t.Errorf("503 body missing string error field; body = %v", body)
	}
}

// TestHandler_PartialResultIs200 verifies that partial scans (a source timed
// out) still return 200 with partial=true. AC8/AC9.
func TestHandler_PartialResultIs200(t *testing.T) {
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")
	// Heavy fs delay forces state-dir scan to exceed total deadline; ps still
	// returns instantly so the scanner produces a partial=true result.
	delayedFS := &slowFS{FS: NewOsFS(), prefix: stateAbs, delay: 500 * time.Millisecond}
	scanner := NewScanner(ScannerOpts{
		FS:              delayedFS,
		Lister:          NewFakeProcessListerFromText(src),
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   100 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	h := NewHandler(scanner)

	req := httptest.NewRequest(http.MethodGet, "/api/codex/brokers", nil)
	rec := httptest.NewRecorder()
	h.HandleBrokers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body struct {
		Partial bool `json:"partial"`
		Summary struct {
			ScanSourceTimeouts []string `json:"scanSourceTimeouts"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v\n%s", err, rec.Body.String())
	}
	if !body.Partial {
		t.Errorf("partial = false, want true under heavy fs delay")
	}
	if len(body.Summary.ScanSourceTimeouts) == 0 {
		t.Errorf("scanSourceTimeouts empty, want at least one entry")
	}
}

// TestHandler_RespectsClientDeadline ensures that a client-supplied request
// deadline shorter than the scanner deadline wins. We pass a 1ms ctx so the
// scan must abort early; either a partial 200 or a sentinel-503 is acceptable
// — the contract is that the handler does NOT block longer than the client
// allows.
func TestHandler_RespectsClientDeadline(t *testing.T) {
	stateAbs, _ := filepath.Abs(filepath.Join("testdata", "state"))
	cxcAbs, _ := filepath.Abs(filepath.Join("testdata", "cxc"))
	src := loadFixture(t, "one-broker.txt")
	lister := NewFakeProcessListerFromText(src).WithDelay(200 * time.Millisecond)
	scanner := NewScanner(ScannerOpts{
		FS:              NewOsFS(),
		Lister:          lister,
		PluginDataRoot:  stateAbs,
		SocketRoots:     []string{cxcAbs},
		TotalDeadline:   800 * time.Millisecond,
		StateDirBudget:  100 * time.Millisecond,
		SocketDirBudget: 50 * time.Millisecond,
	})
	h := NewHandler(scanner)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/codex/brokers", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.HandleBrokers(rec, req)
	elapsed := time.Since(start)

	if elapsed > 150*time.Millisecond {
		t.Errorf("handler ignored client deadline: elapsed=%v (lister delay 200ms)", elapsed)
	}
	// Either 200 partial or 503; what we forbid is the handler hanging past
	// the client deadline. Verify status is one of those two (not e.g. 500).
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 200 or 503", rec.Code)
	}
}
