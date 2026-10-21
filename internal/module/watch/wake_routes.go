package watchmod

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/watchwake"
)

// wakeSuppressed is one wake-up the settings stopped (spec section 5). Nothing records them on this host yet (WK-2); a source also holds the
// ones the other hosts report (wake_suppressed.go).
type wakeSuppressed struct {
	At     time.Time `json:"at"`
	Host   string    `json:"host"`
	Reason string    `json:"reason"`
	Why    string    `json:"why"`
	Detail string    `json:"detail"`
}

// wakePatrolStatus is the last patrol and the last patrol failure of this host (spec section 5). Nothing records them yet (WK-3).
type wakePatrolStatus struct {
	LastOKAt       *time.Time `json:"last_ok_at"`
	LastOKHost     *string    `json:"last_ok_host"`
	LastFailAt     *time.Time `json:"last_fail_at"`
	LastFailCode   *string    `json:"last_fail_code"`
	LastFailDetail *string    `json:"last_fail_detail"`
}

// wakeSettingsView is the body of GET /api/watch/wake-settings, and of a successful PUT.
type wakeSettingsView struct {
	Settings     json.RawMessage  `json:"settings"`
	Etag         string           `json:"etag"`
	Revision     int64            `json:"revision"`
	Source       string           `json:"source"`
	SourceID     string           `json:"source_id"`
	IsSource     bool             `json:"is_source"`
	Stale        bool             `json:"stale"`
	Readonly     bool             `json:"readonly"`
	FetchedAt    *time.Time       `json:"fetched_at"`
	KnownKinds   []watchwake.Kind `json:"known_kinds"`
	Suppressed   []wakeSuppressed `json:"suppressed"`
	PatrolStatus wakePatrolStatus `json:"patrol_status"`
}

func (m *Module) registerWakeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/watch/wake-settings", m.handleGetWake)
	mux.HandleFunc("PUT /api/watch/wake-settings", m.handlePutWake)
	mux.HandleFunc("POST /api/peers/watch/wake-settings/get", m.handlePeerWakeGet)
	mux.HandleFunc("POST /api/peers/watch/wake-settings/put", m.handlePeerWakePut)
	mux.HandleFunc("POST /api/peers/watch/wake-settings/changed", m.handlePeerWakeChanged)
	mux.HandleFunc("POST /api/peers/watch/wake-suppressed", m.handlePeerWakeSuppressed)
}

// wakeView is the document as the source answers it.
func (m *Module) wakeView(d wakeDoc, source string) wakeSettingsView {
	now := m.now()
	return wakeSettingsView{
		Settings: d.settings, Etag: d.etag, Revision: d.revision,
		Source: source, SourceID: m.wakeHostID(), IsSource: true, Readonly: d.readonly,
		FetchedAt:  &now,
		KnownKinds: watchwake.KnownKinds, Suppressed: m.suppressed.list(), PatrolStatus: wakePatrolStatus{},
	}
}

// handleGetWake: any token that reaches it may read (the admin, the App, a paired phone).
func (m *Module) handleGetWake(w http.ResponseWriter, r *http.Request) {
	source, isSource := m.wakeSourceAlias()
	if !isSource {
		m.writeJSON(w, http.StatusOK, m.followerView(source))
		return
	}
	d, err := m.readWakeSource()
	if err != nil {
		m.writeReply(w, m.wakeReadReply(err))
		return
	}
	m.writeJSON(w, http.StatusOK, m.wakeView(d, source))
}
