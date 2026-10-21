package watchmod

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	ipeers "github.com/wake/purdex/internal/peers"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/watchwake"
)

// maxWakePut bounds a PUT of the wake settings (the document is a few KB; the prompts are 2000 bytes each).
const maxWakePut = 64 << 10

type wakePutBody struct {
	Settings   json.RawMessage `json:"settings"`
	ExpectEtag string          `json:"expect_etag"`
}

// wakeReply is an answer before it is written: the status and the body, as a peer's /put envelope carries them.
type wakeReply struct {
	Status int
	Body   any
}

func replyCode(status int, code string) wakeReply {
	return wakeReply{status, map[string]string{"error": code}}
}

func (m *Module) writeReply(w http.ResponseWriter, r wakeReply) { m.writeJSON(w, r.Status, r.Body) }

// readWakePut reads the body of a PUT: the whole body under the limit (what comes after the object counts), and the object the only thing in
// it. It writes the refusal itself and returns false.
func (m *Module) readWakePut(w http.ResponseWriter, r *http.Request) (wakePutBody, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWakePut))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			m.writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "the body is over the limit")
			return wakePutBody{}, false
		}
		m.writeErr(w, http.StatusBadRequest, "bad_request", "the body could not be read")
		return wakePutBody{}, false
	}
	var body wakePutBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || len(body.Settings) == 0 || dec.More() {
		m.writeErr(w, http.StatusBadRequest, "bad_request", `body must be one object {"settings": {...}, "expect_etag": "..."}`)
		return wakePutBody{}, false
	}
	if _, err := dec.Token(); err != io.EOF { // nothing but white space may follow it
		m.writeErr(w, http.StatusBadRequest, "bad_request", `body must be one object {"settings": {...}, "expect_etag": "..."}`)
		return wakePutBody{}, false
	}
	return body, true
}

// handlePutWake replaces the whole document: the admin token only; the body names the etag it was based on. On the source host it writes;
// on any other it passes the PUT to the source and answers what the source answered.
func (m *Module) handlePutWake(w http.ResponseWriter, r *http.Request) {
	if !m.requireAdmin(w, r, "changes the wake settings") {
		return
	}
	body, ok := m.readWakePut(w, r)
	if !ok {
		return
	}
	source, isSource := m.wakeSourceAlias()
	if !isSource {
		m.writeReply(w, m.forwardWakePut(r.Context(), source, body))
		return
	}
	m.writeReply(w, m.putWakeSource(body))
}

func (m *Module) wakeReadReply(err error) wakeReply {
	switch {
	case errors.Is(err, errNoWakeRows):
		return wakeReply{http.StatusServiceUnavailable, ipeers.APIError{Error: "unavailable", Detail: "the host config module is not running"}}
	case errors.Is(err, errMigrateRead):
		return wakeReply{http.StatusInternalServerError, ipeers.APIError{Error: "migration_failed", Detail: err.Error()}}
	default:
		return wakeReply{http.StatusInternalServerError, ipeers.APIError{Error: "wake_settings", Detail: err.Error()}}
	}
}

// putWakeSource is the PUT on the source host: the checks in order (the etag was named, the stored document can be written over, the new one
// is valid, the etag is the stored one), then the swap. It answers with the status and body the route sends - also what a peer's /put carries.
func (m *Module) putWakeSource(body wakePutBody) wakeReply {
	source, isSource := m.wakeSourceAlias()
	if !isSource {
		return wakeReply{http.StatusServiceUnavailable, map[string]string{"error": "not_source", "source": source}}
	}
	if body.ExpectEtag == "" {
		return replyCode(http.StatusBadRequest, "missing_expect")
	}
	cur, err := m.readWakeSource()
	if err != nil {
		return m.wakeReadReply(err)
	}
	if cur.readonly { // a newer build wrote it: this one does not write over what it cannot read
		return wakeReply{http.StatusBadRequest, map[string]any{"error": "invalid", "fields": []watchwake.FieldError{{Path: "version", Code: watchwake.CodeBadVersion, Message: "the stored document is of a newer version than this build reads"}}}}
	}
	next, errs := watchwake.Normalize(body.Settings)
	if len(errs) > 0 {
		return wakeReply{http.StatusBadRequest, map[string]any{"error": "invalid", "fields": errs}}
	}
	if body.ExpectEtag != cur.etag {
		return m.conflictReply(cur, source)
	}
	rows, err := m.wakeRows()
	if err != nil {
		return m.wakeReadReply(err)
	}
	if m.beforeWakeCommit != nil {
		m.beforeWakeCommit()
	}
	if again, still := m.wakeSourceAlias(); !still || again != source { // peers.alias or wake_source changed while the PUT was on its way: nothing is written
		return wakeReply{http.StatusServiceUnavailable, map[string]string{"error": "source_unreachable", "source": again}}
	}
	rev, err := rows.Put(hostconfig.KeyWatchWake, watchwake.Marshal(next), cur.revision)
	if m.duringWakeCommit != nil {
		m.duringWakeCommit()
	}
	// The identity can change between the check above and the write, and no lock can span the write (a config change would wait for the store).
	// So it is checked once more after: if it changed, this write may have landed on the row of a host that is no longer the source, and the
	// answer is not a 200 - the same 503 as for a source that could not be reached ("it may have been written; read again"), and nothing is
	// announced. The row it left is read only if this host becomes the source again.
	if err == nil {
		if after, still := m.wakeSourceAlias(); !still || after != source {
			return wakeReply{http.StatusServiceUnavailable, map[string]string{"error": "source_unreachable", "source": after}}
		}
	}
	if errors.Is(err, hostconfig.ErrConflict) { // another PUT got in between the read and the swap
		if again, rerr := m.readWakeSource(); rerr == nil {
			return m.conflictReply(again, source)
		}
	}
	if err != nil {
		return m.wakeReadReply(err)
	}
	d := wakeDoc{settings: watchwake.Marshal(next), etag: watchwake.Etag(next), revision: rev}
	m.wakeChanged(source, d)
	m.tellFleetWakeChanged(d.revision)
	return wakeReply{http.StatusOK, m.wakeView(d, source)}
}

func (m *Module) conflictReply(current wakeDoc, source string) wakeReply {
	return wakeReply{http.StatusConflict, map[string]any{"error": "etag_mismatch", "current": m.wakeView(current, source)}}
}
