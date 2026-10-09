package conversation

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/convmodel/ccnorm"
)

// maxSubagentReads bounds the subagent files read at once: each read is bounded (ccnorm caps a file at 64 MiB), but
// they need no entry and no pin, so the count is limited here.
const maxSubagentReads = 4

type subagentJSON struct {
	Items   []convmodel.Item `json:"items"`
	Partial bool             `json:"partial"`
}

// handleSubagent answers GET /api/conversations/{provider}/{session_id}/subagents/{agent_id}: the items of one
// subagent's own transcript, `<session_id>/subagents/agent-<agent_id>.jsonl` beside the session's transcript. Nothing
// is opened before provider, session id and agent id are valid.
func (m *Module) handleSubagent(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("provider") != "claude" {
		writeError(w, http.StatusNotFound, "provider_unsupported")
		return
	}
	sid := r.PathValue("session_id")
	if !sessionIDRe.MatchString(sid) {
		writeError(w, http.StatusBadRequest, "bad_session_id")
		return
	}
	agentID := r.PathValue("agent_id")
	if !ccnorm.ValidAgentID(agentID) {
		writeError(w, http.StatusBadRequest, "bad_agent_id")
		return
	}
	select {
	case m.subSem <- struct{}{}:
		defer func() { <-m.subSem }()
	default:
		writeError(w, http.StatusServiceUnavailable, "busy")
		return
	}

	f, err := m.resolver.OpenSubagent(r.Context(), sid, agentID)
	if err != nil {
		switch {
		case r.Context().Err() != nil:
		case errors.Is(err, convfeed.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found")
		default:
			writeError(w, http.StatusInternalServerError, "resolve_failed")
		}
		return
	}
	defer f.Close()

	items, _, readErr := ccnorm.NormalizeSubagent(f, agentID)
	if readErr != nil && len(items) == 0 {
		writeError(w, http.StatusInternalServerError, "read_failed")
		return
	}
	if items == nil {
		items = []convmodel.Item{}
	}
	resp := subagentJSON{Items: items, Partial: readErr != nil}
	body, err := json.Marshal(resp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode_failed")
		return
	}
	if len(body) > m.maxBody {
		// Too big for one answer: keep the longest prefix that fits and say so.
		per := make([]int, len(items))
		for i, it := range items {
			b, _ := json.Marshal(it)
			per[i] = len(b) + 1
		}
		cum := make([]int, len(items)+1)
		for i := range items {
			cum[i+1] = cum[i] + per[i]
		}
		k := sort.Search(len(items), func(i int) bool { return cum[i+1]+envelopeSlack > m.maxBody })
		resp = subagentJSON{Items: items[:k], Partial: true}
		if body, err = json.Marshal(resp); err != nil || len(body) > m.maxBody {
			writeError(w, http.StatusInternalServerError, "too_large")
			return
		}
	}
	writeJSON(w, http.StatusOK, body)
}
