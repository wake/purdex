package resourcesmod

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

func amendReq(cid string) resources.LeaseRequest {
	return resources.LeaseRequest{ClientID: cid, Weight: 10, HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026"}
}

// Mode, command and cwd are stored on the row (advise records rows too, so
// pdx lease ls shows the commands there as well); the command is cut to 200
// bytes and the control characters become spaces.
func TestLeaseCreate_StoresModeCommandCwd(t *testing.T) {
	f := newRouteFix(t, resources.ModeAdvise)
	req := amendReq(cidA)
	req.Mode = resources.CallQueue
	req.Command = strings.Repeat("x", 250) + "\n"
	req.Cwd = "/work/tree"
	rec := f.do(http.MethodPost, "/api/resources/leases", req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	r := f.row(decodeLease(t, rec).ID)
	assert.Equal(t, resources.CallQueue, r.Mode)
	assert.Equal(t, strings.Repeat("x", resources.MaxCommandBytes), r.Command)
	assert.Equal(t, "/work/tree", r.Cwd)
	assert.Equal(t, "", r.PositionID, "this PR creates no positions")

	req2 := amendReq(cidB)
	req2.Command = "a\tb\nc"
	r2 := f.row(decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", req2)).ID)
	assert.Equal(t, "a b c", r2.Command)
	assert.Equal(t, "", r2.Mode, "no mode reads as wait")

	req3 := amendReq(cidC)
	req3.Cwd = strings.Repeat("d", 1100)
	r3 := f.row(decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", req3)).ID)
	assert.Len(t, r3.Cwd, resources.MaxCwdBytes)
}

func TestLeaseCreate_BadModeIs400(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	req := amendReq(cidA)
	req.Mode = "park"
	rec := f.do(http.MethodPost, "/api/resources/leases", req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, resources.ErrBadRequest, apiCode(rec))
	for i, ok := range []string{resources.CallQueue, resources.CallAsk, resources.CallWait} {
		good := amendReq("4444444" + string(rune('0'+i)) + "-4444-4444-8444-444444444444")
		good.Mode = ok
		assert.Less(t, f.do(http.MethodPost, "/api/resources/leases", good).Code, 300, ok)
	}
}

// The mode is part of what a client id was made for.
func TestLeaseCreate_ReplayWithAnotherModeIs409(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	req := amendReq(cidA)
	req.Mode = resources.CallQueue
	require.Less(t, f.do(http.MethodPost, "/api/resources/leases", req).Code, 300)
	same := f.do(http.MethodPost, "/api/resources/leases", req)
	assert.Equal(t, http.StatusOK, same.Code, "the same request is a replay")
	req.Mode = resources.CallAsk
	rec := f.do(http.MethodPost, "/api/resources/leases", req)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, resources.ErrClientIDReused, apiCode(rec))
}

// A client that still sends parkable is not refused (POST rejects unknown
// fields); the field is ignored.
func TestLeaseCreate_ParkableStillAccepted(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	req := amendReq(cidA)
	req.Parkable = true
	rec := f.do(http.MethodPost, "/api/resources/leases", req)
	assert.Less(t, rec.Code, 300, rec.Body.String())
}
