package team

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Every literal of the team contract, pinned one by one: the CLI maps the
// codes and reasons to exit codes, persists the states and steps, and
// prints the texts, so a rename here is a silent cross-component break.
func TestWireTeam_LiteralsArePinned(t *testing.T) {
	for name, c := range map[string]struct{ got, want string }{
		"ErrNotLead":         {ErrNotLead, "not_lead"},
		"ErrTeamFull":        {ErrTeamFull, "team_full"},
		"ErrCwdOutsideGrant": {ErrCwdOutsideGrant, "cwd_outside_grant"},
		"ErrNotYourMember":   {ErrNotYourMember, "not_your_member"},

		"SpawnReasonStartTimeout": {SpawnReasonStartTimeout, "member_start_timeout"},
		"SpawnReasonCreateFailed": {SpawnReasonCreateFailed, "session_create_failed"},
		"SpawnReasonLaunchFailed": {SpawnReasonLaunchFailed, "launch_failed"},
		"SpawnReasonNameTaken":    {SpawnReasonNameTaken, "tmux_name_taken"},
		"SpawnReasonAbandoned":    {SpawnReasonAbandoned, "abandoned"},

		"MemberActive": {string(MemberActive), "active"},
		"MemberKilled": {string(MemberKilled), "killed"},
		"MemberGone":   {string(MemberGone), "gone"},

		"SpawnRunning": {string(SpawnRunning), "running"},
		"SpawnDone":    {string(SpawnDone), "done"},
		"SpawnFailed":  {string(SpawnFailed), "failed"},

		"StepAccepted":       {StepAccepted, "accepted"},
		"StepSessionCreated": {StepSessionCreated, "session_created"},
		"StepLaunched":       {StepLaunched, "launched"},
		"StepRegistered":     {StepRegistered, "registered"},

		"TeamEndLeadGone":      {TeamEndLeadGone, "lead_gone"},
		"DefaultMemberCommand": {DefaultMemberCommand, "claude --dangerously-skip-permissions"},

		// spec §7.2 (the brief's first line) and U20 (b), (c), verbatim.
		"MemberBriefPrefixFmt": {MemberBriefPrefixFmt, "[pdx team] 你是 %s 的 member（team %s）。接力由 lead 決定，不要自己接力。"},
		"ReminderAtActivation": {ReminderAtActivation, "已成為 lead。預設模型不固定：spawn member 時請依工作需求用 --model 指定（例：--model sonnet 做機械性修改、--model opus 做設計）。"},
		"ReminderNoModel":      {ReminderNoModel, "提醒：沒有指定 --model，member 會用這台主機當下的預設模型。"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", name, c.got, c.want)
		}
	}
	if SpawnRegisterS != 20 || SpawnPollWaitS != 25 {
		t.Errorf("limits = %d / %d, want 20 / 25", SpawnRegisterS, SpawnPollWaitS)
	}
	// The brief prefix takes exactly the lead address, then the team id.
	if got, want := fmt.Sprintf(MemberBriefPrefixFmt, "mlab/n10", "t-1"),
		"[pdx team] 你是 mlab/n10 的 member（team t-1）。接力由 lead 決定，不要自己接力。"; got != want {
		t.Errorf("brief prefix = %q, want %q", got, want)
	}
}

// The JSON of every DTO, both with every field set and with the optional
// ones left out, so that each key name and each omitempty is pinned.
func TestWireTeam_JSONShapes(t *testing.T) {
	pct := 41.5
	zero := 0.0
	grant := Grant{MaxMembers: 3, Roots: []string{"/w"}}
	ctx := &MemberContext{UsedPercentage: &pct, Window: 200000, ModelID: "claude-sonnet-5", Effort: "low", At: 7}
	member := Member{SessionID: "s", Ref: "_abc123", Address: "mlab/_abc123", TeamID: "t", HostID: "h",
		Title: "worker", Cwd: "/w/r", TmuxSession: "tm-0123456789", State: MemberActive,
		Model: "sonnet", Effort: "low", Context: ctx, SpawnOp: "op", CreatedAt: 5}
	const memberFull = `{"session_id":"s","ref":"_abc123","address":"mlab/_abc123","team_id":"t","host_id":"h","title":"worker","cwd":"/w/r","tmux_session":"tm-0123456789","state":"active","model":"sonnet","effort":"low","context":{"used_percentage":41.5,"window":200000,"model_id":"claude-sonnet-5","effort":"low","at":7},"spawn_op":"op","created_at":5}`
	const memberMin = `{"session_id":"","ref":"","address":"","team_id":"","host_id":"","cwd":"","tmux_session":"","state":"","spawn_op":"","created_at":0}`
	const teamMin = `{"id":"","host_id":"","lead_session_id":"","lead_ref":"","grant":{"max_members":0,"roots":null},"request_id":"","created_at":0}`
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"Team full", Team{ID: "t", HostID: "h", LeadSessionID: "ls", LeadRef: "_lead01", Grant: grant, RequestID: "t",
			CreatedAt: 1, EndedAt: 2, EndReason: TeamEndLeadGone},
			`{"id":"t","host_id":"h","lead_session_id":"ls","lead_ref":"_lead01","grant":{"max_members":3,"roots":["/w"]},"request_id":"t","created_at":1,"ended_at":2,"end_reason":"lead_gone"}`},
		{"Team minimal (live: no ended_at, no end_reason)", Team{}, teamMin},

		{"MemberContext full", *ctx, `{"used_percentage":41.5,"window":200000,"model_id":"claude-sonnet-5","effort":"low","at":7}`},
		{"MemberContext with no percentage yet", MemberContext{Window: 200000, At: 7}, `{"used_percentage":null,"window":200000,"at":7}`},
		{"MemberContext measured 0", MemberContext{UsedPercentage: &zero, Window: 200000, At: 7}, `{"used_percentage":0,"window":200000,"at":7}`},

		{"Member full", member, memberFull},
		{"Member minimal", Member{}, memberMin},

		{"SpawnRequest full", SpawnRequest{ID: "id", OriginInbox: "/tmp/in.sock", Cwd: "/w/r", Title: "worker", Model: "opus[1m]", Effort: "high"},
			`{"id":"id","origin_inbox":"/tmp/in.sock","cwd":"/w/r","title":"worker","model":"opus[1m]","effort":"high"}`},
		{"SpawnRequest minimal", SpawnRequest{ID: "id", OriginInbox: "/tmp/in.sock"}, `{"id":"id","origin_inbox":"/tmp/in.sock"}`},

		{"SpawnOp done", SpawnOp{ID: "op", TeamID: "t", HostID: "h", State: SpawnDone, Step: StepRegistered,
			Cwd: "/w/r", Title: "worker", Model: "sonnet", Effort: "low", TmuxSession: "tm-0123456789",
			LeadAddress: "mlab/n10", Member: &member, CreatedAt: 1, UpdatedAt: 2},
			`{"id":"op","team_id":"t","host_id":"h","state":"done","step":"registered","cwd":"/w/r","title":"worker","model":"sonnet","effort":"low","tmux_session":"tm-0123456789","lead_address":"mlab/n10","member":` + memberFull + `,"created_at":1,"updated_at":2}`},
		{"SpawnOp failed", SpawnOp{ID: "op", TeamID: "t", HostID: "h", State: SpawnFailed, Step: StepLaunched,
			Reason: SpawnReasonStartTimeout, Cwd: "/w/r", TmuxSession: "tm-0123456789", CreatedAt: 1, UpdatedAt: 2},
			`{"id":"op","team_id":"t","host_id":"h","state":"failed","step":"launched","reason":"member_start_timeout","cwd":"/w/r","title":"","model":"","effort":"","tmux_session":"tm-0123456789","created_at":1,"updated_at":2}`},
		{"SpawnOp minimal", SpawnOp{},
			`{"id":"","team_id":"","host_id":"","state":"","step":"","cwd":"","title":"","model":"","effort":"","tmux_session":"","created_at":0,"updated_at":0}`},

		{"KillRequest", KillRequest{OriginInbox: "/tmp/in.sock", Target: "_abc123"}, `{"origin_inbox":"/tmp/in.sock","target":"_abc123"}`},

		{"TeamView with a member", TeamView{Team: Team{}, Members: []Member{{}}}, `{"team":` + teamMin + `,"members":[` + memberMin + `]}`},
		{"TeamView with nil members", TeamView{}, `{"team":` + teamMin + `,"members":[]}`},
		{"TeamView with empty members", TeamView{Members: []Member{}}, `{"team":` + teamMin + `,"members":[]}`},
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

// A TeamView behind a pointer, inside another value, is still never null:
// `pdx team --json` prints it as is and the table code ranges over it.
func TestWireTeam_TeamViewMembersNeverNull(t *testing.T) {
	b, err := json.Marshal(struct {
		V *TeamView `json:"v"`
	}{V: &TeamView{Team: Team{ID: "t"}}})
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		V struct {
			Members json.RawMessage `json:"members"`
		} `json:"v"`
	}
	if err := json.Unmarshal(b, &back); err != nil || string(back.V.Members) != `[]` {
		t.Fatalf("members = %s err=%v (from %s)", back.V.Members, err, b)
	}
	var tv TeamView
	if err := json.Unmarshal([]byte(`{"team":{"id":"t"},"members":[{"session_id":"s","state":"gone"}]}`), &tv); err != nil ||
		tv.Team.ID != "t" || len(tv.Members) != 1 || tv.Members[0].State != MemberGone {
		t.Fatalf("round trip: %+v err=%v", tv, err)
	}
}

// used_percentage is a pointer so that "no reading yet" (null) and
// "measured 0" (0) stay distinguishable to `pdx team` (CTX column "-" vs
// "0%"), the way RelayOp and the peers wire keep them apart.
func TestWireTeam_UsedPercentageNilVsZero(t *testing.T) {
	var absent MemberContext
	if err := json.Unmarshal([]byte(`{"used_percentage":null,"window":1,"at":1}`), &absent); err != nil || absent.UsedPercentage != nil {
		t.Fatalf("null: %v err=%v", absent.UsedPercentage, err)
	}
	var zero MemberContext
	if err := json.Unmarshal([]byte(`{"used_percentage":0,"window":1,"at":1}`), &zero); err != nil || zero.UsedPercentage == nil || *zero.UsedPercentage != 0 {
		t.Fatalf("zero: %v err=%v", zero.UsedPercentage, err)
	}
}
