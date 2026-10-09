package team

import (
	"encoding/json"
	"testing"
)

// Every literal of the relay contract, pinned one by one: the CLI, the SPA
// and the mod compare these strings, so a rename here is a silent
// cross-component break (PR #1700 attacker finding).
func TestRelayContract_Literals(t *testing.T) {
	for name, got := range map[string]string{
		"RelayKindSelf":   string(RelayKindSelf),
		"RelayKindMember": string(RelayKindMember),

		"RelayAwaitingApproval": string(RelayAwaitingApproval),
		"RelayRequested":        string(RelayRequested),
		"RelayClaimed":          string(RelayClaimed),
		"RelayWriting":          string(RelayWriting),
		"RelayWritten":          string(RelayWritten),
		"RelayCleared":          string(RelayCleared),
		"RelayDone":             string(RelayDone),
		"RelayFailed":           string(RelayFailed),
		"RelayCancelled":        string(RelayCancelled),

		"RelayReasonHandoffIncomplete":  RelayReasonHandoffIncomplete,
		"RelayReasonMemberUnresponsive": RelayReasonMemberUnresponsive,
		"RelayReasonMemberGone":         RelayReasonMemberGone,
		"RelayReasonDaemonUnavailable":  RelayReasonDaemonUnavailable,
		"RelayReasonDenied":             RelayReasonDenied,
		"RelayReasonTimeout":            RelayReasonTimeout,
		"RelayReasonCompacted":          RelayReasonCompacted,
		"RelayReasonAbandoned":          RelayReasonAbandoned,

		"ErrMemberRelayIsLeads": ErrMemberRelayIsLeads,
		"ErrSelfRelayOff":       ErrSelfRelayOff,
		"ErrSelfRelayPaused":    ErrSelfRelayPaused,
		"ErrRelayOpen":          ErrRelayOpen,
		"ErrUnknownSession":     ErrUnknownSession,
		"ErrBadTransition":      ErrBadTransition,

		"LineageReaderKey": LineageReaderKey, "ApprovalFeedKey": ApprovalFeedKey,
		"RelayDir": RelayDir,
	} {
		want := map[string]string{
			"RelayKindSelf": "self", "RelayKindMember": "member",
			"RelayAwaitingApproval": "awaiting_approval", "RelayRequested": "requested", "RelayClaimed": "claimed",
			"RelayWriting": "writing", "RelayWritten": "written", "RelayCleared": "cleared",
			"RelayDone": "done", "RelayFailed": "failed", "RelayCancelled": "cancelled",
			"RelayReasonHandoffIncomplete": "handoff_incomplete", "RelayReasonMemberUnresponsive": "member_unresponsive",
			"RelayReasonMemberGone": "member_gone", "RelayReasonDaemonUnavailable": "daemon_unavailable",
			"RelayReasonDenied": "denied", "RelayReasonTimeout": "timeout", "RelayReasonCompacted": "compacted",
			"RelayReasonAbandoned":  "abandoned",
			"ErrMemberRelayIsLeads": "member_relay_is_leads", "ErrSelfRelayOff": "self_relay_off",
			"ErrSelfRelayPaused": "self_relay_paused", "ErrRelayOpen": "relay_open",
			"ErrUnknownSession": "unknown_session", "ErrBadTransition": "bad_transition",
			"LineageReaderKey": "team.lineage", "ApprovalFeedKey": "team.approval-feed", "RelayDir": "relay",
		}[name]
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if RelayThresholdPct != 70 || RelayMinGrowth != 20000 || SelfRelayDeadlineS != 600 {
		t.Errorf("limits = %d / %d / %d, want 70 / 20000 / 600", RelayThresholdPct, RelayMinGrowth, SelfRelayDeadlineS)
	}
}

// The JSON of every DTO, both with every field set and with the optional
// ones left out, so that each key name and each omitempty is pinned.
func TestRelayContract_DTOJSON(t *testing.T) {
	pct := 0.0
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"RelayOp full", RelayOp{ID: "op", Kind: RelayKindMember, HostID: "h", SessionID: "s", NewSessionID: "s2", Ref: "_a", NewRef: "_b",
			TeamID: "t", RequestID: "r", State: RelayFailed, Reason: RelayReasonMemberGone, HandoffPath: "/p", Pruned: true, UsedPercentage: &pct, CreatedAt: 1, UpdatedAt: 2},
			`{"id":"op","kind":"member","host_id":"h","session_id":"s","new_session_id":"s2","ref":"_a","new_ref":"_b","team_id":"t","request_id":"r","state":"failed","reason":"member_gone","handoff_path":"/p","pruned":true,"used_percentage":0,"created_at":1,"updated_at":2}`},
		{"RelayOp minimal (optional fields omitted, required zero values kept)", RelayOp{},
			`{"id":"","kind":"","host_id":"","session_id":"","ref":"","state":"","handoff_path":"","created_at":0,"updated_at":0}`},
		{"SelfRelayPayload full", SelfRelayPayload{OpID: "op", UsedPercentage: 71.5, Window: 200000, ModelID: "m", Effort: "low"},
			`{"op_id":"op","used_percentage":71.5,"window":200000,"model_id":"m","effort":"low"}`},
		{"SelfRelayPayload without model/effort", SelfRelayPayload{OpID: "op", UsedPercentage: 71.5, Window: 200000},
			`{"op_id":"op","used_percentage":71.5,"window":200000}`},
		{"RelayHelloRequest full", RelayHelloRequest{SessionID: "s", ModVersion: "1", Agent: "cc"},
			`{"session_id":"s","mod_version":"1","agent":"cc"}`},
		{"RelayHelloRequest minimal", RelayHelloRequest{SessionID: "s"}, `{"session_id":"s"}`},
		{"RelayHelloResponse", RelayHelloResponse{OK: true, Role: "none", SelfRelay: "on", Threshold: RelayThresholdPct, MinGrowth: RelayMinGrowth},
			`{"ok":true,"role":"none","self_relay":"on","threshold":70,"min_growth":20000}`},
		{"RelayBeginRequest", RelayBeginRequest{SessionID: "s", Self: true, UsedPercentage: 72.4, Window: 200000},
			`{"session_id":"s","self":true,"used_percentage":72.4,"window":200000}`},
		{"RelayBeginRequest with request_id", RelayBeginRequest{SessionID: "s", Self: true, UsedPercentage: 72.4, Window: 200000, RequestID: "r"},
			`{"session_id":"s","self":true,"used_percentage":72.4,"window":200000,"request_id":"r"}`},
		{"RelayBeginResponse", RelayBeginResponse{Op: RelayOp{ID: "op", Kind: RelayKindSelf, State: RelayAwaitingApproval}, RequestID: "r"},
			`{"op":{"id":"op","kind":"self","host_id":"","session_id":"","ref":"","state":"awaiting_approval","handoff_path":"","created_at":0,"updated_at":0},"request_id":"r"}`},
		{"RelaySelfRequest", RelaySelfRequest{SessionID: "s", Action: "status"}, `{"session_id":"s","action":"status"}`},
		{"RelaySelfResponse", RelaySelfResponse{SelfRelay: "paused", HostSwitch: true, Member: false},
			`{"self_relay":"paused","host_switch":true,"member":false}`},
		{"RelayReportRequest cleared", RelayReportRequest{State: RelayCleared, NewSessionID: "s2"},
			`{"state":"cleared","new_session_id":"s2"}`},
		{"RelayReportRequest failed", RelayReportRequest{State: RelayFailed, Error: RelayReasonHandoffIncomplete},
			`{"state":"failed","error":"handoff_incomplete"}`},
		{"RelayReportRequest bare", RelayReportRequest{State: RelayWriting}, `{"state":"writing"}`},
		{"APIError with op only", APIError{Error: ErrBadTransition, Op: &RelayOp{ID: "op", State: RelayDone}},
			`{"error":"bad_transition","op":{"id":"op","kind":"","host_id":"","session_id":"","ref":"","state":"done","handoff_path":"","created_at":0,"updated_at":0}}`},
		{"APIError with approval only", APIError{Error: ErrRequestOpen, Approval: &Approval{ID: "a"}},
			`{"error":"request_open","approval":` + mustJSON(t, Approval{ID: "a"}) + `}`},
		{"APIError bare", APIError{Error: ErrBadRequest}, `{"error":"bad_request"}`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(b) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, b, c.want)
		}
	}
}

// used_percentage is a pointer on RelayOp so that "not measured" (absent)
// and "measured 0" (present) stay distinguishable to every consumer.
func TestRelayContract_UsedPercentageNilVsZero(t *testing.T) {
	var absent RelayOp
	if err := json.Unmarshal([]byte(`{"id":"op"}`), &absent); err != nil || absent.UsedPercentage != nil {
		t.Fatalf("absent: %+v err=%v", absent.UsedPercentage, err)
	}
	var zero RelayOp
	if err := json.Unmarshal([]byte(`{"id":"op","used_percentage":0}`), &zero); err != nil || zero.UsedPercentage == nil || *zero.UsedPercentage != 0 {
		t.Fatalf("zero: %+v err=%v", zero.UsedPercentage, err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
