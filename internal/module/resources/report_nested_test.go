package resourcesmod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// The report counts the nested answers of the leases of the period (P2f-3).
// (M: leave the sum out.)
func TestReport_CountsNestedAnswers(t *testing.T) {
	f := newNestedFix(t)
	outer := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "pnpm check --fast"))
	require.Equal(t, resources.StateHeld, outer.State)
	f.postNested(1100, resources.CallQueue)
	f.postNested(1100, resources.CallQueue)
	rec := f.do(http.MethodGet, "/api/resources/report?since=1h", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var rep resources.Report
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rep))
	assert.Equal(t, 2, rep.Positions.Nested)
}
