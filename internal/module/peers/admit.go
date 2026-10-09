// internal/module/peers/admit.go
package peers

import (
	"encoding/json"
	"errors"
	"net/http"
)

// admitDecode is the inbound half of §3.1 rule 8, shared by the team
// routes: the per-host rate limit is spent BEFORE the body is read, then
// the body is capped at maxBytes and decoded into v. It returns 0 on
// success, else the HTTP status the route should answer (429, 413, 400);
// the caller writes the answer. The same limiter type serves /deliver
// (deliver.go step 2b).
func admitDecode(w http.ResponseWriter, r *http.Request, lim *hostLimiter, hostID string, maxBytes int64, v any) int {
	if !lim.Allow(hostID) {
		return http.StatusTooManyRequests
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes)).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return http.StatusRequestEntityTooLarge
		}
		return http.StatusBadRequest
	}
	return 0
}
