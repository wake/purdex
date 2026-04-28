package agent

import "fmt"

// LifecycleEventKind classifies a hook event by its daemon-side lifecycle
// effect. Each catalog entry tags its lifecycle kind so the daemon can
// dispatch frame mutations and error-guard transitions metadata-driven
// rather than via hard-coded event-name string comparisons.
type LifecycleEventKind int

const (
	// LifecycleNone marks events with no frame-mutation lifecycle effect
	// (e.g. PdxNotification, PdxPermissionRequest). Status is emitted via
	// DeriveStatus only.
	LifecycleNone LifecycleEventKind = iota
	LifecycleSessionStart
	LifecycleUserPromptSubmit
	LifecycleStop
	LifecycleStopFailure
	LifecycleSessionEnd
	LifecycleSubagentStart
	LifecycleSubagentStop
)

func (k LifecycleEventKind) String() string {
	switch k {
	case LifecycleNone:
		return "None"
	case LifecycleSessionStart:
		return "SessionStart"
	case LifecycleUserPromptSubmit:
		return "UserPromptSubmit"
	case LifecycleStop:
		return "Stop"
	case LifecycleStopFailure:
		return "StopFailure"
	case LifecycleSessionEnd:
		return "SessionEnd"
	case LifecycleSubagentStart:
		return "SubagentStart"
	case LifecycleSubagentStop:
		return "SubagentStop"
	default:
		return fmt.Sprintf("LifecycleEventKind(%d)", int(k))
	}
}
