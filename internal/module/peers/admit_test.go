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

func TestAdmitDecode_BadJSON(t *testing.T) {
	lim := newHostLimiter(10, time.Minute, time.Now)
	var v struct{ ID string }
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`nope`))
	if st := admitDecode(w, r, lim, "hostA", 1024, &v); st != http.StatusBadRequest {
		t.Fatalf("st=%d", st)
	}
}
