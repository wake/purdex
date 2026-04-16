package sync

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// handlePush accepts a raw JSON bundle from a client and stores it as the
// canonical bundle for that client's group.
//
// Query param: clientId
// Body:        raw JSON bundle (max 10 MB)
// Response:    204 No Content on success
func (m *SyncModule) handlePush(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	if clientID == "" {
		http.Error(w, "clientId required", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if err := m.store.PushBundle(clientID, string(body)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePull returns the current canonical bundle for the client's group.
//
// Query param: clientId
// Response:    JSON bundle, or null if no bundle has been pushed yet
func (m *SyncModule) handlePull(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	if clientID == "" {
		http.Error(w, "clientId required", http.StatusBadRequest)
		return
	}

	bundle, err := m.store.PullCanonical(clientID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if bundle == "" {
		w.Write([]byte("null"))
		return
	}
	w.Write([]byte(bundle))
}

// handleHistory returns the push history for the client's group.
//
// Query params: clientId, limit (optional, default/max 100)
// Response:    JSON array of history entries
func (m *SyncModule) handleHistory(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	if clientID == "" {
		http.Error(w, "clientId required", http.StatusBadRequest)
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 100 {
				n = 100
			}
			limit = n
		}
	}

	entries, err := m.store.ListHistorySummary(clientID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Return [] rather than null for an empty list.
	if entries == nil {
		entries = []HistorySummary{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

// handleGroupCreate creates a new sync group and adds the requesting client.
//
// Body:     {"clientId": "...", "device": "..."}
// Response: {"groupId": "..."}
func (m *SyncModule) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID string `json:"clientId"`
		Device   string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.ClientID == "" {
		http.Error(w, "clientId required", http.StatusBadRequest)
		return
	}

	groupID, err := generateGroupID()
	if err != nil {
		http.Error(w, "failed to generate group id", http.StatusInternalServerError)
		return
	}

	if err := m.store.CreateGroup(groupID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := m.store.AddClientToGroup(groupID, req.ClientID, req.Device); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"groupId": groupID})
}

// handleGroupJoin adds a client to an existing group.
//
// Body:     {"groupId": "...", "clientId": "...", "device": "..."}
// Response: 204 No Content
func (m *SyncModule) handleGroupJoin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID  string `json:"groupId"`
		ClientID string `json:"clientId"`
		Device   string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.GroupID == "" || req.ClientID == "" {
		http.Error(w, "groupId and clientId required", http.StatusBadRequest)
		return
	}

	if err := m.store.AddClientToGroup(req.GroupID, req.ClientID, req.Device); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePairCreate generates a pairing code for the client's group.
//
// Body:     {"clientId": "..."}
// Response: {"code": "XXXXXXXX"}
func (m *SyncModule) handlePairCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID string `json:"clientId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.ClientID == "" {
		http.Error(w, "clientId required", http.StatusBadRequest)
		return
	}

	code, err := m.store.CreatePairingCode(req.ClientID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"code": code})
}

// handlePairVerify validates a pairing code and adds the client to the group.
//
// Body:     {"code": "...", "clientId": "...", "device": "..."}
// Response: {"groupId": "..."} on success, 403 on failure
func (m *SyncModule) handlePairVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code     string `json:"code"`
		ClientID string `json:"clientId"`
		Device   string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Code == "" || req.ClientID == "" {
		http.Error(w, "code and clientId required", http.StatusBadRequest)
		return
	}

	groupID, err := m.store.VerifyPairingCode(req.Code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	if err := m.store.AddClientToGroup(groupID, req.ClientID, req.Device); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"groupId": groupID})
}

// handleGroupMembers returns the list of members in the client's group.
//
// Query param: clientId
// Response:    JSON array of {clientId, device, lastSeen}
func (m *SyncModule) handleGroupMembers(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	if clientID == "" {
		http.Error(w, "clientId required", http.StatusBadRequest)
		return
	}

	members, err := m.store.ListGroupMembers(clientID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Return [] rather than null for an empty group.
	if members == nil {
		members = []GroupMember{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(members)
}

// handleGroupRemoveMember removes a member from the requester's group.
//
// Query params: clientId (requester), targetId (member to remove)
// Response:     204 No Content
func (m *SyncModule) handleGroupRemoveMember(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	targetID := r.URL.Query().Get("targetId")
	if clientID == "" || targetID == "" {
		http.Error(w, "clientId and targetId required", http.StatusBadRequest)
		return
	}

	if err := m.store.RemoveClientFromGroup(clientID, targetID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
