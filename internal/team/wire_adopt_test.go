package team

import (
	"encoding/json"
	"strings"
	"testing"
)

// The names an older or newer peer matches on (adopt spec D-U24-2, D-U24-3;
// plan PL-1a): the approval kind, the five 409 codes, the member state and
// origins, the two notice kinds, the outbox's give-up bound and the two
// notice texts.
func TestWireAdopt_LiteralsArePinned(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{string(KindAdopt), "adopt"},
		{ErrAdoptSelf, "adopt_self"},
		{ErrAdoptTargetIsLead, "adopt_target_is_lead"},
		{ErrAdoptAlreadyMember, "adopt_already_member"},
		{ErrAdoptTargetNotFound, "adopt_target_not_found"},
		{ErrAdoptTargetAmbiguous, "adopt_target_ambiguous"},
		{ErrKillFailed, "kill_failed"},
		{ErrRemoteUnsupported, "remote_unsupported"},
		{string(MemberReleased), "released"},
		{MemberOriginSpawned, "spawned"},
		{MemberOriginAdopted, "adopted"},
		{NoticeAdopted, "adopted"},
		{NoticeReleased, "released"},
		{AdoptNoticeFmt, "[pdx team] 你已成為 %s 的 member（team %s）。自我接力已關閉，接力由 lead 安排；回報請送 %s。"},
		{ReleaseNoticeFmt, "[pdx team] %s 已讓你離開 team %s：你現在是一般 session，自我接力依這台主機的設定。"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if NoticeGiveUpS != 600 {
		t.Errorf("NoticeGiveUpS = %d, want 600", NoticeGiveUpS)
	}
}

// ReleaseRequest is KillRequest under another name: one body shape for two
// routes, so a client builds either the same way.
func TestWireAdopt_ReleaseRequestIsKillRequest(t *testing.T) {
	b, err := json.Marshal(ReleaseRequest{OriginInbox: "/tmp/in.sock", Target: "_abc123"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"origin_inbox":"/tmp/in.sock","target":"_abc123"}` {
		t.Fatalf("release request = %s", b)
	}
}

// The JSON of the adopt additions, both with every optional field set and
// with it left out, so that each key name and each omitempty is pinned.
func TestWireAdopt_JSONShapes(t *testing.T) {
	adopted := Member{SessionID: "s", Ref: "_abc123", Address: "mlab/_abc123", TeamID: "t", HostID: "h",
		Cwd: "/w/r", TmuxSession: "main:@1.%2", State: MemberActive, Origin: MemberOriginAdopted,
		AdoptRequest: "req-1", CreatedAt: 5, EndedAt: 9}
	spawned := Member{SessionID: "s", Ref: "_abc123", Address: "mlab/_abc123", TeamID: "t", HostID: "h",
		Cwd: "/w/r", TmuxSession: "tm-0123456789", State: MemberActive, Origin: MemberOriginSpawned,
		SpawnOp: "op", CreatedAt: 5}
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"AdoptPayload full", AdoptPayload{TeamID: "t", LeadSessionID: "ls", TargetRef: "_abc123", TargetSessionID: "ts",
			Title: "worker", TargetName: "w-one", TargetAddress: "mlab/w-one", TargetCwd: "/w/r", TargetTmux: "main:@1.%2"},
			`{"team_id":"t","lead_session_id":"ls","target_ref":"_abc123","target_session_id":"ts","title":"worker","target_name":"w-one","target_address":"mlab/w-one","target_cwd":"/w/r","target_tmux":"main:@1.%2"}`},
		{"AdoptPayload minimal", AdoptPayload{},
			`{"team_id":"","lead_session_id":"","target_ref":"","target_session_id":""}`},

		{"Approval with close_reason", Approval{State: StateCancelled, CloseReason: ErrAdoptSelf},
			`{"id":"","kind":"","host_id":"","origin":{"session_id":"","ref":"","name":"","pid":0,"proc_start":"","cwd":"","tmux":""},"payload":null,"state":"cancelled","created_at":0,"deadline_at":0,"lease_until":0,"close_reason":"adopt_self"}`},
		{"Approval without close_reason", Approval{},
			`{"id":"","kind":"","host_id":"","origin":{"session_id":"","ref":"","name":"","pid":0,"proc_start":"","cwd":"","tmux":""},"payload":null,"state":"","created_at":0,"deadline_at":0,"lease_until":0}`},

		{"CreateApprovalRequest adopt", CreateApprovalRequest{ID: "id", Kind: KindAdopt, OriginInbox: "/tmp/in.sock", Target: "_abc123"},
			`{"id":"id","kind":"adopt","origin_inbox":"/tmp/in.sock","reason":"","target":"_abc123"}`},
		{"CreateApprovalRequest lead has no target", CreateApprovalRequest{ID: "id", Kind: KindLead, OriginInbox: "/tmp/in.sock"},
			`{"id":"id","kind":"lead","origin_inbox":"/tmp/in.sock","reason":""}`},

		{"Member adopted (spawn_op empty, adopt_request, ended_at)", adopted,
			`{"session_id":"s","ref":"_abc123","address":"mlab/_abc123","team_id":"t","host_id":"h","cwd":"/w/r","tmux_session":"main:@1.%2","state":"active","origin":"adopted","spawn_op":"","adopt_request":"req-1","created_at":5,"ended_at":9}`},
		{"Member spawned", spawned,
			`{"session_id":"s","ref":"_abc123","address":"mlab/_abc123","team_id":"t","host_id":"h","cwd":"/w/r","tmux_session":"tm-0123456789","state":"active","origin":"spawned","spawn_op":"op","created_at":5}`},
		{"Member zero still carries origin", Member{},
			`{"session_id":"","ref":"","address":"","team_id":"","host_id":"","cwd":"","tmux_session":"","state":"","origin":"","spawn_op":"","created_at":0}`},
		{"Member released", Member{State: MemberReleased, Origin: MemberOriginAdopted, EndedAt: 7},
			`{"session_id":"","ref":"","address":"","team_id":"","host_id":"","cwd":"","tmux_session":"","state":"released","origin":"adopted","spawn_op":"","created_at":0,"ended_at":7}`},
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

// AdoptPayloadOf is the daemon's strict read of an adopt approval: the right
// kind, no key it does not know, one JSON value and nothing after it.
func TestAdoptPayloadOf(t *testing.T) {
	want := AdoptPayload{TeamID: "t", LeadSessionID: "ls", TargetRef: "_abc123", TargetSessionID: "ts", Title: "worker", TargetTmux: "main:@1.%2"}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := AdoptPayloadOf(Approval{Kind: KindAdopt, Payload: raw})
	if err != nil || got != want {
		t.Fatalf("round trip = %+v, %v; want %+v", got, err, want)
	}
	for _, c := range []struct {
		name string
		a    Approval
		in   string // substring of the error
	}{
		{"wrong kind", Approval{Kind: KindLead, Payload: raw}, "kind"},
		{"unknown field", Approval{Kind: KindAdopt, Payload: json.RawMessage(`{"team_id":"t","extra":1}`)}, "extra"},
		{"bad JSON", Approval{Kind: KindAdopt, Payload: json.RawMessage(`{"team_id":`)}, ""},
		{"no payload", Approval{Kind: KindAdopt}, ""},
		{"null", Approval{Kind: KindAdopt, Payload: json.RawMessage(`null`)}, ""},
		{"wrong type", Approval{Kind: KindAdopt, Payload: json.RawMessage(`{"team_id":3}`)}, ""},
		{"trailing value", Approval{Kind: KindAdopt, Payload: json.RawMessage(`{"team_id":"t"} {}`)}, "trailing"},
	} {
		if _, err := AdoptPayloadOf(c.a); err == nil || !strings.Contains(err.Error(), c.in) {
			t.Errorf("%s: err = %v, want one mentioning %q", c.name, err, c.in)
		}
	}
}
