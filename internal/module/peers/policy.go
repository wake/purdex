// internal/module/peers/policy.go
package peers

import "net/http"

// HostRoutePolicy says which requests a host principal may make in P2:
// GET /api/peers (exact path) with no scope query parameter, or
// scope=local. Everything else — hosts routes, scope=all, any other
// method — is admin-only (false here means "refuse the host principal";
// admin principals bypass this policy entirely).
func HostRoutePolicy(r *http.Request) bool {
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
