package jobsapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/fault"
	"github.com/wake/purdex/internal/wwd/wire"
)

func putLog(r *rig, id, claimID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PUT", "/v1/jobs/"+id+"/log?claim_id="+url.QueryEscape(claimID), strings.NewReader("log"))
	w := httptest.NewRecorder()
	r.peer.ServeHTTP(w, req)
	return w
}

// H1 refuses the log and nothing else; without it the same upload is taken.
func TestFaultLogUploadFailRefusesTheLogNotTheResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    *fault.Hooks
		want int
	}{
		{"no hook", nil, http.StatusOK},
		{"enabled, hook not named", fault.New(fault.Config{Enabled: true, Zone: "danger"}, nil), http.StatusOK},
		{"log_upload_fail", fault.New(fault.Config{Enabled: true, LogUploadFail: true}, nil), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			a := New(r.q).WithFault(tc.h)
			a.WorkerRoutes(r.peer.(*http.ServeMux), true)
			a.WorkerRoutes(r.local.(*http.ServeMux), false)
			id := r.submit(t)
			c := r.claim(t, r.peer, id, "air26")
			require.Equal(t, 200, call(r.peer, "POST", "/v1/jobs/"+id+"/start", claimBody(c.ClaimID)).Code)
			assert.Equal(t, tc.want, putLog(r, id, c.ClaimID).Code)
			res := call(r.peer, "POST", "/v1/jobs/"+id+"/result", wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded})
			assert.Equal(t, 200, res.Code, res.Body.String())
		})
	}
}
