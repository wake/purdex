// internal/module/peers/admit_test.go
package peers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdmitDecode_LimitBeforeDecode(t *testing.T) {
	now := time.Unix(0, 0)
	lim := newHostLimiter(1, time.Minute, func() time.Time { return now })
	var v struct{ ID string }
	do := func(body string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		return admitDecode(w, r, lim, "hostA", 64<<10, &v)
	}
	if st := do(`{"ID":"a"}`); st != 0 || v.ID != "a" {
		t.Fatalf("first st=%d v=%+v", st, v)
	}
	// The second body is invalid; the limiter must answer before any decode.
	if st := do(`not json`); st != http.StatusTooManyRequests {
		t.Fatalf("second st=%d", st)
	}
}

func TestAdmitDecode_BodyCap(t *testing.T) {
	lim := newHostLimiter(10, time.Minute, time.Now)
	var v struct{ ID string }
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ID":"`+strings.Repeat("a", 200)+`"}`))
	if st := admitDecode(w, r, lim, "hostA", 64, &v); st != http.StatusRequestEntityTooLarge {
		t.Fatalf("st=%d", st)
	}
}

// A valid first value followed by more data must not get past the cap or
// the syntax check (codex R1).
func TestAdmitDecode_TrailingData(t *testing.T) {
	lim := newHostLimiter(10, time.Minute, time.Now)
	run := func(body string, max int64) int {
		var v struct{ ID string }
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		return admitDecode(w, r, lim, "hostA", max, &v)
	}
	if st := run(`{"ID":"a"}`+strings.Repeat(" ", 200)+`{}`, 64); st != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized tail st=%d", st)
	}
	if st := run(`{"ID":"a"} {}`, 1024); st != http.StatusBadRequest {
		t.Fatalf("second value st=%d", st)
	}
	if st := run(`{"ID":"a"}`+"\n", 1024); st != 0 {
		t.Fatalf("trailing newline st=%d", st)
	}
}

func TestAdmitDecode_BadJSON(t *testing.T) {
	lim := newHostLimiter(10, time.Minute, time.Now)
	var v struct{ ID string }
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`nope`))
	if st := admitDecode(w, r, lim, "hostA", 1024, &v); st != http.StatusBadRequest {
		t.Fatalf("st=%d", st)
	}
}
