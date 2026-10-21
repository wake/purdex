package notify

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/push"
)

// PUT /api/notify/group-labels
//
//	{"device": "<the App's client identity>", "labels": [{"root_session_id": "...", "group": "..."}, ...]}
//
// The body is the whole set this device has for this host: a write replaces the device's earlier one (what it leaves out is gone,
// so an empty list clears the device). 200 {"count": n} with n the labels kept; 400 for a bad body; 403 for anything but the
// admin token (a paired phone's device token is refused by the scope middleware before it gets here, and again here); 413
// `too_many_labels` when the list is over maxLabelsPerDevice — the whole write is refused, nothing is cut. `labels` must be there
// (a list; null or missing is 400, so a malformed request cannot clear a device). A group is at most 512 characters in, and 40 kept.
// The body is at most 8 MiB. Capability notify.group_labels.v1.

const (
	maxLabelsPerDevice = 2000
	maxDeviceRunes     = 128
	maxRootRunes       = 128
	maxGroupRunes      = 40 // the length a group has inside a longer title (push.maxNamedRunes)
	// maxRawGroupRunes bounds what comes in as a group, before the rules cut it to maxGroupRunes: the body limit needs a worst case.
	maxRawGroupRunes = 512
	// maxBody holds maxLabelsPerDevice entries of the longest root (128 four-byte characters) and the longest raw group (512
	// characters, each written as a six-byte \uXXXX escape at worst) with their JSON: about 3.7 KB each, 7.5 MB for 2000, so 8 MiB.
	maxBody = 8 << 20
)

type putLabel struct {
	Root  string `json:"root_session_id"`
	Group string `json:"group"`
}

type putRequest struct {
	Device string `json:"device"`
	// Labels is a pointer so that a body without the key (or with null) is told from an explicit empty list: only the latter clears.
	Labels *[]putLabel `json:"labels"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]string{"error": code, "detail": detail})
}

// plain is a non-empty text of at most max runes with no control characters (an id, not prose).
func plain(s string, max int) bool {
	n := utf8.RuneCountInString(s)
	if n < 1 || n > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < ' ' || r == 0x7f {
			return false
		}
	}
	return strings.TrimSpace(s) != ""
}

func (m *Module) handlePut(w http.ResponseWriter, r *http.Request) {
	if !devices.IsAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin_only", "the notification groups are written with the admin token")
		return
	}
	st := m.live()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "the notify store is not open")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "")
		} else {
			writeError(w, http.StatusBadRequest, "bad_body", "")
		}
		return
	}
	var req putRequest
	if !utf8.Valid(body) || json.Unmarshal(body, &req) != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not the expected JSON")
		return
	}
	device := strings.TrimSpace(req.Device)
	if !plain(device, maxDeviceRunes) {
		writeError(w, http.StatusBadRequest, "bad_request", "device must be 1-128 characters with no control characters")
		return
	}
	if req.Labels == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "labels is required: a list, empty to clear this device")
		return
	}
	if len(*req.Labels) > maxLabelsPerDevice {
		writeError(w, http.StatusRequestEntityTooLarge, "too_many_labels", "at most 2000 labels for one device; nothing was written")
		return
	}
	labels := make([]Label, 0, len(*req.Labels))
	for _, l := range *req.Labels {
		if !plain(l.Root, maxRootRunes) {
			writeError(w, http.StatusBadRequest, "bad_request", "root_session_id must be 1-128 characters with no control characters")
			return
		}
		if utf8.RuneCountInString(l.Group) > maxRawGroupRunes {
			writeError(w, http.StatusBadRequest, "bad_request", "group must be at most 512 characters")
			return
		}
		group := push.Normalise(l.Group, maxGroupRunes) // the notification text rules: plain, one line, cut
		if group == "" {
			continue // nothing left of it: not a label
		}
		labels = append(labels, Label{Root: l.Root, Group: group})
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready { // stopped between the check above and the lock
		writeError(w, http.StatusServiceUnavailable, "not_ready", "the notify store is not open")
		return
	}
	if err := st.Replace(device, labels); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", "the labels could not be written")
		return
	}
	if err := m.load(st); err != nil {
		// the file has the write; the table will have it at the next load (a restart or a sweep). Say so rather than report a clean write.
		writeError(w, http.StatusInternalServerError, "store_error", "the labels were written but could not be read back")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": len(labels)})
}
