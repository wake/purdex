package agent

import "encoding/json"

// AgentProvider is the core interface that all agent providers must implement.
type AgentProvider interface {
	Type() string
	DisplayName() string
	IconHint() string
	Claim(ctx ClaimContext) bool
	Identify(proc ProcessInfo) bool
	DeriveStatus(eventName string, rawEvent json.RawMessage) DeriveResult
	IsAlive(tmuxTarget string) bool
}

// ClaimContext provides information for agent detection.
type ClaimContext struct {
	HookEvent   *HookEvent
	ProcessName string // Legacy foreground-command hint; avoid new dependencies.
	TmuxTarget  string // tmux target for detailed detection (e.g. "mySession:")
}

// HookEvent is the raw hook event received from pdx hook CLI.
type HookEvent struct {
	TmuxSession string          `json:"tmux_session"`
	EventName   string          `json:"event_name"`
	RawEvent    json.RawMessage `json:"raw_event"`
	AgentType   string          `json:"agent_type"`
}

// --- Optional capabilities ---

// HookInstaller can install/remove/check hook configurations for a specific agent.
//
// Events() is the single source of truth (SSoT) for the provider's classified
// hook event catalog. It supersedes any package-local *HookEvents slice; do not
// introduce a parallel string list. Installer iteration, CheckHooks reporting,
// and template parity must derive their installable subset with
// IsInstallableHookSpec rather than assuming every catalog entry is wired.
// SupportedStatuses derivation still reads the same declaration. Any new
// HookInstaller implementation is required to implement Events().
type HookInstaller interface {
	InstallHooks(pdxPath string) error
	RemoveHooks(pdxPath string) error
	CheckHooks() (HookStatus, error)
	Events() []HookEventSpec
}

// HookEventSpec declares one upstream hook event, the Status set DeriveStatus
// may emit from that event when it is installable, and a short human-readable
// blurb for the Inspector UI. It is the build-time declaration contract;
// runtime hook handling stays per-agent (policy dispersal, plumbing shared).
//
// Fields:
//   - EmitsStatus: non-empty Status values (Status != "") that DeriveStatus
//     may return for this event across all sub-branches. Empty slice (not nil)
//     means the event is detail-only (DeriveStatus returns Valid=true with
//     Status=""); SubagentStart/Stop are the canonical examples. Polymorphic
//     events (e.g. cc Notification) list the union of every sub-branch.
//   - Description: short English sentence for Inspector display. No trailing
//     period, no emoji; keep it under roughly 70 characters.
//   - FutureOnly: installer/checker-facet flag ONLY. When true, the hook is
//     declared (installer writes it, DeriveStatus parses it) but the current
//     CLI path is not guaranteed to emit it; CheckHooks therefore tolerates
//     an absent hooks.json entry (legacy user who installed before the
//     expansion) while still surfacing present-but-broken entries. The flag
//     does NOT affect DeriveStatus-capability declarations:
//     DeriveSupportedStatuses (and thus StatusSupporter.SupportedStatuses)
//     unions every spec's EmitsStatus regardless of FutureOnly — proxy paths
//     and future CLI versions may legitimately emit the event. Default false
//     means "installer-required; missing is an issue". See fix plan §1.1.
//   - Handling: classifies whether this catalog entry is installed/parsed.
//     Empty values preserve the legacy defaults via EffectiveHookHandling:
//     specs with EmitsStatus default to status, and empty-status specs default
//     to detail. Newly added non-installable upstream entries must set this
//     explicitly to ignored or unsupported.
type HookHandling string

const (
	HookHandlingStatus      HookHandling = "status"
	HookHandlingDetail      HookHandling = "detail"
	HookHandlingIgnored     HookHandling = "ignored"
	HookHandlingUnsupported HookHandling = "unsupported"
)

type HookEventSpec struct {
	// PurdexName is the daemon-internal stable identifier for this catalog
	// entry. Always prefixed with "Pdx". Used as:
	//   - DeriveStatus switch case label
	//   - CLI `pdx hook --agent <agent> <PurdexName>` positional argument
	//   - HTTP EventRequest.PurdexName payload value
	//   - NormalizedEvent.PurdexName / TraceStore record key
	// Daemon code MUST use PurdexName for all internal lookups and matching.
	PurdexName string

	// UpstreamKeys lists the raw event names that the agent's upstream hook
	// system fires when this catalog entry should match. Always non-empty
	// post-migration (installable / unsupported / ignored alike).
	//
	// Used at the installer/plugin boundary:
	//   - cc: written as ~/.claude/settings.json "hooks" map key
	//   - codex: written as ~/.codex/hooks.json matcher-group key
	//   - opencode: matched against Bus event name in plugin demux switch
	//
	// For cc/codex this is normally a single-element slice. For opencode
	// installable entries, multiple upstream Bus events may map to the same
	// PurdexName (e.g., permission.asked + question.asked → PdxPermissionRequest).
	// For opencode unsupported/ignored entries, UpstreamKeys is a
	// single-element slice equal to the raw upstream event name.
	UpstreamKeys []string

	// Lifecycle classifies the daemon-internal side effect kind for this
	// catalog entry. Used by frame_ops / handler so that lifecycle handling
	// (frame reset, subagent membership, frame delete, error guard whitelist)
	// can be done via catalog metadata lookup instead of hardcoded event-name
	// string comparison. See lifecycle.go for the value table.
	Lifecycle LifecycleEventKind

	EmitsStatus []Status
	Description string
	FutureOnly  bool
	Handling    HookHandling
}

// LookupByPurdexName scans specs for the catalog entry whose PurdexName
// matches purdexName. The empty string never matches even if a (legacy /
// not-yet-migrated) entry has an empty PurdexName, which keeps malformed
// payloads from accidentally landing on a real catalog row.
//
// Intended caller: daemon-internal lookups (handler routing, frame_ops
// dispatch, DeriveStatus). Slice scan is O(N) with N ≤ ~11; an index would
// add cache invalidation without measurable benefit.
func LookupByPurdexName(specs []HookEventSpec, purdexName string) (HookEventSpec, bool) {
	if purdexName == "" {
		return HookEventSpec{}, false
	}
	for _, s := range specs {
		if s.PurdexName == purdexName {
			return s, true
		}
	}
	return HookEventSpec{}, false
}

// LookupByUpstreamKey scans specs for the catalog entry whose UpstreamKeys
// contains upstreamKey. Used for installer/checker boundary inspection and
// for test assertions when verifying the cc/codex 1:1 PurdexName ↔
// UpstreamKey mapping.
//
// Caveat: NOT suitable for opencode runtime routing of filter-based events
// (`session.status`, `tool.execute.before`, `tool.execute.after`). Those
// upstream keys require a `type` / `tool` filter to determine the correct
// PurdexName, and catalog UpstreamKeys does not encode filter conditions —
// hitting `session.status` here would resolve to PdxStop even for busy /
// retry sub-states. opencode plugin demux remains the authority for that
// runtime path.
func LookupByUpstreamKey(specs []HookEventSpec, upstreamKey string) (HookEventSpec, bool) {
	if upstreamKey == "" {
		return HookEventSpec{}, false
	}
	for _, s := range specs {
		for _, k := range s.UpstreamKeys {
			if k == upstreamKey {
				return s, true
			}
		}
	}
	return HookEventSpec{}, false
}

func EffectiveHookHandling(spec HookEventSpec) HookHandling {
	if spec.Handling != "" {
		return spec.Handling
	}
	if len(spec.EmitsStatus) > 0 {
		return HookHandlingStatus
	}
	return HookHandlingDetail
}

func IsInstallableHookSpec(spec HookEventSpec) bool {
	switch EffectiveHookHandling(spec) {
	case HookHandlingStatus, HookHandlingDetail:
		return true
	default:
		return false
	}
}

// HookStatus reports the installation state of hooks for an agent.
//
// Managed and UpgradesAvailable are the Finding #2 / #4 contract
// extensions (PR #616 review): the SPA needs to distinguish (a) "pdx
// left artifacts on disk" from "all required events are installed"
// so the Remove button stays enabled on drifted-but-managed state,
// and (b) "install is valid but 6 new FutureOnly events are
// available" so the Install button / upgrade hint is not dead.
type HookStatus struct {
	Installed         bool                     `json:"installed"`
	Managed           bool                     `json:"managed"`
	UpgradesAvailable []string                 `json:"upgradesAvailable,omitempty"`
	Events            map[string]HookEventInfo `json:"events"`
	Issues            []string                 `json:"issues"`
	AgentVersion      string                   `json:"agentVersion,omitempty"`
	SupportedVersion  string                   `json:"supportedVersion,omitempty"`
	ExceedsSupport    bool                     `json:"exceedsSupport,omitempty"`
}

// HookEventInfo describes the state of a single hook event.
//
// FutureOnly mirrors HookEventSpec.FutureOnly so the UI can render
// "tolerated absent" (grey FutureOnly badge, no red error) vs
// "missing installer-required event" (hard red).
type HookEventInfo struct {
	Installed  bool   `json:"installed"`
	Command    string `json:"command"`
	FutureOnly bool   `json:"futureOnly,omitempty"`
}

// StatuslineInstaller manages CC's statusLine.command in ~/.claude/settings.json.
type StatuslineInstaller interface {
	CheckStatusline() (StatuslineState, error)
	InstallStatuslinePdx(pdxPath string) error
	InstallStatuslineWrap(pdxPath, inner string) error
	RemoveStatusline() error
}

// StatuslineState describes the current state of an agent's statusLine config.
type StatuslineState struct {
	Mode         string `json:"mode"` // "none" | "pdx" | "wrapped" | "unmanaged"
	Installed    bool   `json:"installed"`
	Inner        string `json:"innerCommand,omitempty"`
	RawCommand   string `json:"rawCommand,omitempty"`
	SettingsPath string `json:"settingsPath"`
}

// HistoryProvider can retrieve conversation history for a session.
type HistoryProvider interface {
	GetHistory(cwd string, sessionID string) ([]map[string]any, error)
}

// StreamCapable marks a provider that supports stream mode handoff.
// Reserved for future implementation.
type StreamCapable interface {
	ExtractState(tmuxTarget string) (SessionState, error)
	ExitInteractive(tmuxTarget string) error
	RelayArgs(state SessionState) []string
	ResumeCommand(state SessionState) string
}

// StatusSupporter is an optional capability for providers to declare which
// Status values they can emit. Providers that do not implement this interface
// are treated as "not declared" by the Coverage helper. Returning an empty
// slice is a valid declaration meaning "supports no Status values" and is
// distinct from not implementing the interface at all.
//
// Scope: this declares the full status set DeriveStatus is *capable* of
// returning, independent of which hook events the agent's hook installer
// currently wires. Declaration may legitimately exceed the installer's
// current event coverage (e.g. when the provider is preparing for a future
// CLI version, or when events arrive via proxy / parent agents). The drift
// test (internal/agent/drift_test.go) keeps DeriveStatus implementations
// honest against this declaration; alignment with the hook installer event
// catalog is a separate concern tracked by per-agent installer phases.
type StatusSupporter interface {
	SupportedStatuses() []Status
}

// SessionState holds agent session state for stream handoff.
type SessionState struct {
	SessionID string
	Cwd       string
}
