// internal/module/peers/policy.go
package peers

import "net/http"

// HostRoutePolicy says which requests a host principal may make: GET
// /api/peers (exact path) with no scope query parameter, or scope=local;
// and POST /api/peers/deliver (a peer host delivering an inbound message,
// Task 8). Everything else — hosts routes, settings, scope=all, any other
// method — is admin-only (false here means "refuse the host principal";
// admin principals bypass this policy entirely).
func HostRoutePolicy(r *http.Request) bool {
	if r.Method == http.MethodPost && r.URL.Path == "/api/peers/deliver" {
		return true
	}
	if r.Method != http.MethodGet || r.URL.Path != "/api/peers" {
		return false
	}
	switch r.URL.Query().Get("scope") {
	case "", "local":
		return true
	default:
		return false
	}
}
