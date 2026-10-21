package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
)

// WorkerView is the answer of GET /v1/worker: the settings in force and their etag. A reader that predates the etag decodes it as
// WorkerSettings and skips the field.
type WorkerView struct {
	WorkerSettings
	// Etag names the whole setting (WorkerSettings.Etag). It is computed from the content, never stored: a wwd that goes back to an
	// earlier release has nothing to undo.
	Etag string `json:"etag"`
}

// WorkerPut is the body of PUT /v1/worker. ExpectEtag is optional: when it is set the change is made only if it still names the
// setting in force (409 otherwise, nothing changed); absent, the PUT replaces the setting as it always did.
type WorkerPut struct {
	WorkerSettings
	ExpectEtag string `json:"expect_etag,omitempty"`
}

// WorkerEtagMismatch is the 409 body of a PUT whose expect_etag no longer names the setting in force: the etag and the setting now there,
// so that whoever asked can decide again without another round trip.
type WorkerEtagMismatch struct {
	Reason string     `json:"reason"`
	Etag   string     `json:"etag"`
	Worker WorkerView `json:"worker"`
}

// WorkerEtagMismatchReason is WorkerEtagMismatch.Reason.
const WorkerEtagMismatchReason = "etag_mismatch"

// Etag is a hash of the whole setting (enabled, slots, owner rule, disk floor), over the values and not over any JSON spelling: the
// same setting has the same etag whatever the field order or whether the floor was written out or left to the default. It tells two
// settings apart; it carries no version, so a setting changed and changed back has its old etag again.
func (w WorkerSettings) Etag() string {
	floor := DefaultDiskMinGB
	if w.DiskMinGB != nil {
		floor = *w.DiskMinGB
	}
	canon := fmt.Sprintf("enabled=%t\nheavy=%d\nlight=%d\nowner_rule=%q\ndisk_min_gb=%s", w.Enabled, w.Slots.Heavy, w.Slots.Light, w.OwnerRule, strconv.FormatFloat(floor, 'g', -1, 64))
	sum := sha256.Sum256([]byte(canon))
	return hex.EncodeToString(sum[:8])
}

// View is the settings with their etag.
func (w WorkerSettings) View() WorkerView { return WorkerView{WorkerSettings: w, Etag: w.Etag()} }
