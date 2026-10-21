package watch

import (
	"context"
	"fmt"
	"regexp"
)

// The service other modules call to wake the watcher with a reason (host-load-control plan W3b-5): the team module when the unattended
// switch changes, the daemon's own patrol. The watcher is found (or woken, or started,
// as auto_wake allows) and told a one-line [負載守門] note saying why.

// WakerKey is the service-registry key of the watch module's Waker.
const WakerKey = "watch.waker"

// Waker wakes the watcher. reason is a short lower-case name (unattended_changed, patrol, disk_low); detail is one
// line (at most 500 bytes, no control characters) saying what changed, and may be empty.
type Waker interface {
	WakeWatcher(ctx context.Context, reason, detail string) error
}

var reasonRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidateWake checks the arguments of WakeWatcher.
func ValidateWake(reason, detail string) error {
	if !reasonRe.MatchString(reason) {
		return fmt.Errorf("the reason must be lower-case letters, digits and _ (up to 64), got %q", reason)
	}
	if detail != "" {
		if err := ValidateNote(detail); err != nil {
			return fmt.Errorf("detail: %w", err)
		}
	}
	return nil
}

// reasonLabel is what the note calls a reason; one this table does not know is shown as it is.
func reasonLabel(reason string) string {
	switch reason {
	case "unattended_changed":
		return "無人值守開關改變"
	case "patrol":
		return "例行巡檢"
	case "disk_low":
		return "磁碟空間不足"
	case "wwd_alert":
		return "wwd 警報"
	}
	return reason
}

// WakeText is the note the watcher reads.
func WakeText(reason, detail string) string {
	s := messagePrefix + " 叫醒：" + reasonLabel(reason)
	if detail != "" {
		s += "｜" + detail
	}
	return s
}
