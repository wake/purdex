// internal/module/peers/bind_test.go
package peers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	ipeers "github.com/wake/purdex/internal/peers"
)

func TestBindHostPrincipal(t *testing.T) {
	entries := map[string]config.PeerHost{"lead": {Alias: "lead", HostID: "lead:1", AllowTeam: true}}
	entryOf := func(alias string) (config.PeerHost, bool) { e, ok := entries[alias]; return e, ok }
	host := func(alias, id string) *middleware.Principal {
		return &middleware.Principal{Kind: middleware.PrincipalHost, Alias: alias, HostID: id}
	}
	cases := []struct {
		name string
		p    *middleware.Principal
		code string // "" = bound
	}{
		{"bound", host("lead", "lead:1"), ""},
		{"no principal", nil, ipeers.ErrHostUnverified},
		{"admin", &middleware.Principal{Kind: middleware.PrincipalAdmin}, ipeers.ErrAdminNotAllowed},
		{"unverified entry", host("lead", ""), ipeers.ErrHostUnverified},
		{"entry gone", host("gone", "gone:1"), ipeers.ErrHostUnverified},
		{"entry re-created for another host", host("lead", "other:9"), ipeers.ErrHostUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.p != nil {
				r = r.WithContext(middleware.WithPrincipal(r.Context(), *tc.p))
			}
			p, e, berr := BindHostPrincipal(r, "team commands", entryOf)
			if tc.code == "" {
				if berr != nil || e.HostID != "lead:1" || p.Alias != "lead" {
					t.Fatalf("p=%+v e=%+v err=%+v", p, e, berr)
				}
				return
			}
			if berr == nil || berr.Status != http.StatusForbidden || berr.Code != tc.code {
				t.Fatalf("err = %+v, want 403 %s", berr, tc.code)
			}
		})
	}
}

// The exported admission pair is what the team routes use: limit first, then the capped decode.
func TestAdmitDecodeExported(t *testing.T) {
	lim := NewHostLimiter(1, time.Minute, time.Now)
	var v struct{ ID string }
	do := func(body string) int {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		return AdmitDecode(httptest.NewRecorder(), r, lim, "hostA", 1024, &v)
	}
	if st := do(`{"ID":"a"}`); st != 0 || v.ID != "a" {
		t.Fatalf("first st=%d v=%+v", st, v)
	}
	if st := do(`not json`); st != http.StatusTooManyRequests {
		t.Fatalf("second st=%d, want 429 before any decode", st)
	}
}
