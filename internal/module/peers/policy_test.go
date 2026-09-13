// internal/module/peers/policy_test.go
package peers

import (
	"net/http/httptest"
	"testing"
)

func TestHostRoutePolicy(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		query  string
		want   bool
	}{
		{"GET /api/peers no scope", "GET", "/api/peers", "", true},
		{"GET /api/peers scope=local", "GET", "/api/peers", "scope=local", true},
		{"GET /api/peers scope=all", "GET", "/api/peers", "scope=all", false},
		{"GET /api/peers scope=bogus", "GET", "/api/peers", "scope=bogus", false},
		{"GET /api/peers/hosts", "GET", "/api/peers/hosts", "", false},
		{"GET /api/peers/", "GET", "/api/peers/", "", false},
		{"POST /api/peers", "POST", "/api/peers", "", false},
		{"DELETE /api/peers", "DELETE", "/api/peers", "", false},
		{"GET /api/peersx", "GET", "/api/peersx", "", false},
		{"GET /api/other", "GET", "/api/other", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := tc.path
			if tc.query != "" {
				url += "?" + tc.query
			}
			req := httptest.NewRequest(tc.method, url, nil)
			got := HostRoutePolicy(req)
			if got != tc.want {
				t.Errorf("HostRoutePolicy(%s %s) = %v, want %v", tc.method, url, got, tc.want)
			}
		})
	}
}
