package watchmod

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/watchwake"
)

// maxWakePut bounds a PUT of the wake settings (the document is a few KB; the prompts are 2000 bytes each).
const maxWakePut = 64 << 10

// wakeSuppressed is one wake-up the settings stopped (spec section 5). Nothing records them yet (WK-2), so the list is empty.
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
}

// wakeView is the document as the routes answer it.
func (m *Module) wakeView(d wakeDoc, source string) wakeSettingsView {
	now := m.now()
	return wakeSettingsView{
		Settings: d.settings, Etag: d.etag, Revision: d.revision,
		Source: source, SourceID: m.wakeHostID(), IsSource: true, Readonly: d.readonly,
		FetchedAt:  &now,
		KnownKinds: watchwake.KnownKinds, Suppressed: []wakeSuppressed{}, PatrolStatus: wakePatrolStatus{},
	}
}

// notSourceView is the answer of a host that is not the source and has nothing from it yet: the defaults, stale, never fetched.
func (m *Module) notSourceView(source string) wakeSettingsView {
	def := watchwake.Default(m.zone())
	return wakeSettingsView{
		Settings: watchwake.Marshal(def), Etag: watchwake.Etag(def), Revision: 0,
		Source: source, SourceID: "", IsSource: false, Stale: true,
		KnownKinds: watchwake.KnownKinds, Suppressed: []wakeSuppressed{}, PatrolStatus: wakePatrolStatus{},
	}
}

func (m *Module) wakeReadErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoWakeRows):
		m.writeErr(w, http.StatusServiceUnavailable, "unavailable", "the host config module is not running")
	case errors.Is(err, errMigrateRead):
		m.writeErr(w, http.StatusInternalServerError, "migration_failed", err.Error())
	default:
		m.writeErr(w, http.StatusInternalServerError, "wake_settings", err.Error())
	}
}

// handleGetWake: any token that reaches it may read (the admin, the App, a paired phone).
func (m *Module) handleGetWake(w http.ResponseWriter, r *http.Request) {
	source, isSource := m.wakeSourceAlias()
	if !isSource {
		m.writeJSON(w, http.StatusOK, m.notSourceView(source))
		return
	}
	d, err := m.readWakeSource()
	if err != nil {
		m.wakeReadErr(w, err)
		return
	}
	m.writeJSON(w, http.StatusOK, m.wakeView(d, source))
}

type wakePutBody struct {
	Settings   json.RawMessage `json:"settings"`
	ExpectEtag string          `json:"expect_etag"`
}

// handlePutWake replaces the whole document: the admin token only; the body names the etag it was based on.
func (m *Module) handlePutWake(w http.ResponseWriter, r *http.Request) {
	if !m.requireAdmin(w, r, "changes the wake settings") {
		return
	}
	source, isSource := m.wakeSourceAlias()
	if !isSource { // WK-1b forwards the write to the source; until then a host that is not the source cannot reach it from here
		m.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "source_unreachable", "source": source})
		return
	}
	// The whole body is read under the limit (what comes after the object counts), and the object is the only thing in it.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWakePut))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			m.writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "the body is over the limit")
			return
		}
		m.writeErr(w, http.StatusBadRequest, "bad_request", "the body could not be read")
		return
	}
	var body wakePutBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || len(body.Settings) == 0 || dec.More() {
		m.writeErr(w, http.StatusBadRequest, "bad_request", `body must be one object {"settings": {...}, "expect_etag": "..."}`)
		return
	}
	if _, err := dec.Token(); err != io.EOF { // nothing but white space may follow it
		m.writeErr(w, http.StatusBadRequest, "bad_request", `body must be one object {"settings": {...}, "expect_etag": "..."}`)
		return
	}
	if body.ExpectEtag == "" {
		m.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_expect"})
		return
	}
	cur, err := m.readWakeSource()
	if err != nil {
		m.wakeReadErr(w, err)
		return
	}
	if cur.readonly { // a newer build wrote it: this one does not write over what it cannot read
		m.writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "fields": []watchwake.FieldError{{Path: "version", Code: watchwake.CodeBadVersion, Message: "the stored document is of a newer version than this build reads"}}})
		return
	}
	next, errs := watchwake.Normalize(body.Settings)
	if len(errs) > 0 {
		m.writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "fields": errs})
		return
	}
	if body.ExpectEtag != cur.etag {
		m.writeConflict(w, cur, source)
		return
	}
	rows, err := m.wakeRows()
	if err != nil {
		m.wakeReadErr(w, err)
		return
	}
	if m.beforeWakeCommit != nil {
		m.beforeWakeCommit()
	}
	rev, nowSource, err := m.commitWake(rows, source, next, cur.revision)
	if nowSource != "" { // this host is no longer the source (peers.alias or wake_source changed while the PUT was on its way): nothing was written
		m.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "source_unreachable", "source": nowSource})
		return
	}
	if errors.Is(err, hostconfig.ErrConflict) { // another PUT got in between the read and the swap
		if again, rerr := m.readWakeSource(); rerr == nil {
			m.writeConflict(w, again, source)
			return
		}
	}
	if err != nil {
		m.wakeReadErr(w, err)
		return
	}
	d := wakeDoc{settings: watchwake.Marshal(next), etag: watchwake.Etag(next), revision: rev}
	m.wakeChanged(source, d)
	m.writeJSON(w, http.StatusOK, m.wakeView(d, source))
}

// commitWake writes the document under the config's read lock, after checking again that this host is still the source it was when the PUT
// began. A change of peers.alias or [watch] wake_source takes the config's write lock, so it cannot come between the check and the write
// (and a write that began first finishes before the change takes effect). When this host is no longer the source, nothing is written and the
// source as it is now comes back as nowSource.
func (m *Module) commitWake(rows hostconfig.WakeRowStore, source string, next watchwake.Settings, base int64) (rev int64, nowSource string, err error) {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	if cur := m.core.Cfg.WakeSource(); cur != source || m.core.Cfg.PeerAlias() != cur {
		return 0, cur, nil
	}
	if m.duringWakeCommit != nil {
		m.duringWakeCommit()
	}
	rev, err = rows.Put(hostconfig.KeyWatchWake, watchwake.Marshal(next), base)
	return rev, "", err
}

func (m *Module) writeConflict(w http.ResponseWriter, current wakeDoc, source string) {
	m.writeJSON(w, http.StatusConflict, map[string]any{"error": "etag_mismatch", "current": m.wakeView(current, source)})
}
