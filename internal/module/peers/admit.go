// internal/module/peers/admit.go
package peers

import (
	"encoding/json"
	"errors"
	"io"
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
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	err := dec.Decode(v)
	if err == nil {
		// One value, then EOF: reading on makes the cap cover the whole
		// body and refuses trailing values.
		if err = dec.Decode(&struct{}{}); err == io.EOF {
			return 0
		} else if err == nil {
			err = errors.New("trailing data")
		}
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
