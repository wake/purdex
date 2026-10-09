package profiles

import (
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/wake/purdex/internal/profilehash"
)

// The paired phone's one write (QR pairing spec §5.2): it appends tabs to the tabs.<ws> section of its own profile. The
// handler holds gate 1 (where); deviceAppendGuard runs gates 2-4 inside PutSection, on the live row whose rev equals baseRev.

// deviceClientID is the clientId every device write must name: "c_" + the 12 hex of the device id ("d_" + 12 hex).
func deviceClientID(deviceID string) string {
	if len(deviceID) < 2 {
		return "c_"
	}
	return "c_" + deviceID[2:]
}

func forbidAppend(detail string) error {
	return &GuardError{Status: http.StatusForbidden, Code: "device_append_only", Detail: detail}
}

// deviceAppendGuard: gate 2 (shape unchanged), gate 3 (append only), gate 4 (the announced hash is the canonical hash of the
// payload — a Mac fast-forwards without pulling a section whose announced hash equals the one it holds, so a wrong hash
// would be invisible to the Macs and later overwritten).
func deviceAppendGuard(stored, in Section) error {
	if in.Fingerprint != stored.Fingerprint || in.Ordinal != stored.Ordinal {
		return forbidAppend("fingerprint and ordinal must equal the stored section's")
	}
	old, ok := decodeTabsPayload(stored.Payload)
	if !ok {
		return forbidAppend("the stored section is not a tabs payload")
	}
	next, ok := decodeTabsPayload(in.Payload)
	if !ok {
		return forbidAppend("the payload must be an object with an order array of strings and a tabs object")
	}
	if len(next.order) <= len(old.order) {
		return forbidAppend("order must gain at least one tab")
	}
	for i, id := range old.order {
		if next.order[i] != id {
			return forbidAppend("order must keep the stored order and add at the end")
		}
	}
	added := next.order[len(old.order):]
	seen := make(map[string]bool, len(added))
	for _, id := range added {
		_, inOld := old.tabs[id]
		if seen[id] || inOld || old.has(id) {
			return forbidAppend("a new tab id repeats or reuses an existing one")
		}
		seen[id] = true
	}
	if len(next.tabs) != len(old.tabs)+len(added) {
		return forbidAppend("tabs must hold exactly the stored tabs plus one per new id")
	}
	for id, v := range old.tabs {
		got, present := next.tabs[id]
		if !present || !reflect.DeepEqual(got, v) {
			return forbidAppend("an existing tab was removed or changed")
		}
	}
	for _, id := range added {
		v, present := next.tabs[id]
		if _, isObj := v.(map[string]any); !present || !isObj {
			return forbidAppend("a new tab must be a JSON object")
		}
	}
	for k, v := range old.rest {
		if got, present := next.rest[k]; !present || !reflect.DeepEqual(got, v) {
			return forbidAppend("a top-level member other than order and tabs changed")
		}
	}
	if len(next.rest) != len(old.rest) {
		return forbidAppend("a top-level member other than order and tabs was added")
	}
	sum, err := profilehash.Sum(in.Payload)
	if err != nil || sum != in.Hash {
		return &GuardError{Status: http.StatusBadRequest, Code: "hash_mismatch", Detail: "hash is not the canonical hash of the payload"}
	}
	return nil
}

type tabsPayload struct {
	order []string
	tabs  map[string]any
	rest  map[string]any // every other top-level member
}

func (t tabsPayload) has(id string) bool {
	for _, o := range t.order {
		if o == id {
			return true
		}
	}
	return false
}

// decodeTabsPayload reads a tabs payload as parsed JSON (numbers as float64, as JavaScript reads them).
func decodeTabsPayload(raw json.RawMessage) (tabsPayload, bool) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return tabsPayload{}, false
	}
	var out tabsPayload
	orderAny, ok := top["order"].([]any)
	if !ok {
		return tabsPayload{}, false
	}
	out.order = make([]string, 0, len(orderAny))
	for _, v := range orderAny {
		s, isStr := v.(string)
		if !isStr {
			return tabsPayload{}, false
		}
		out.order = append(out.order, s)
	}
	if out.tabs, ok = top["tabs"].(map[string]any); !ok {
		return tabsPayload{}, false
	}
	out.rest = make(map[string]any, len(top))
	for k, v := range top {
		if k != "order" && k != "tabs" {
			out.rest[k] = v
		}
	}
	return out, true
}
