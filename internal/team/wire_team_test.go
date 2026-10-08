package team

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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
		Model: "sonnet", Effort: "low", Context: ctx, Origin: MemberOriginSpawned, SpawnOp: "op", CreatedAt: 5}
	const memberFull = `{"session_id":"s","ref":"_abc123","address":"mlab/_abc123","team_id":"t","host_id":"h","title":"worker","cwd":"/w/r","tmux_session":"tm-0123456789","state":"active","origin":"spawned","model":"sonnet","effort":"low","context":{"used_percentage":41.5,"window":200000,"model_id":"claude-sonnet-5","effort":"low","at":7},"spawn_op":"op","created_at":5}`
	const memberMin = `{"session_id":"","ref":"","address":"","team_id":"","host_id":"","cwd":"","tmux_session":"","state":"","origin":"","spawn_op":"","created_at":0}`
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

// U20 (a): --model is single-quoted in the literal send, and it must also
// be a plain name, so nothing in it can reach the shell or read as a flag.
func TestValidModel_Table(t *testing.T) {
	name64 := "a" + strings.Repeat("b", 63)
	for _, s := range []string{
		"opus", "sonnet", "fable", "haiku", "claude-opus-5-5", "claude-sonnet-4-5-20250929",
		"opus[1m]", "claude-opus-5-5[1m]", "a", "A9", "x.y_z-1", "9x",
		name64, name64 + "[1m]",
	} {
		if !ValidModel(s) {
			t.Errorf("ValidModel(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"", "a b", " opus", "opus ", "opus\t", "opus\n", "\nopus",
		"'x'", `"x"`, "x'y", "x;y", "$(x)", "x$(y)", "`x`", "x|y", "x&y", "x>y", "x*", "x?", `x\y`, "x/y", "x:y",
		"-x", "--model", ".x", "_x", "opüs",
		name64 + "c", // 65 characters
		"opus[2m]", "opus[1M]", "opus[1m][1m]", "[1m]", "opus[1m]x", "opus[]", "opus[1m",
	} {
		if ValidModel(s) {
			t.Errorf("ValidModel(%q) = true, want false", s)
		}
	}
}

// M25: Claude Code's five effort levels, exact case.
func TestValidEffort_Table(t *testing.T) {
	want := []string{"low", "medium", "high", "xhigh", "max"}
	if !slices.Equal(Efforts, want) {
		t.Fatalf("Efforts = %q, want %q", Efforts, want)
	}
	for _, s := range want {
		if !ValidEffort(s) {
			t.Errorf("ValidEffort(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "High", "LOW", "Max", "ultra", "minimal", "none", "x-high", "xHigh", " low", "low ", "max\n", "'low'"} {
		if ValidEffort(s) {
			t.Errorf("ValidEffort(%q) = true, want false", s)
		}
	}
}

// §7.2 step 3 (D4): the tmux name is "tm-" plus the first 10 hex digits of
// the op id, and derives from the id alone, so a retry after a restart
// names the same session.
func TestSpawnTmuxName_FromUUID(t *testing.T) {
	for in, want := range map[string]string{
		"0f8e2c4a-91b3-4d5e-a6f7-1234567890ab": "tm-0f8e2c4a91",
		// Either case is a UUID; the name is always lowercase hex.
		"0F8E2C4A-91B3-4D5E-A6F7-1234567890AB": "tm-0f8e2c4a91",
		"0f8E2c4A-91b3-4D5e-A6f7-1234567890aB": "tm-0f8e2c4a91",
		// The dash after the 8th digit is skipped; only the first 10 digits count.
		"01234567-89ab-4cde-8f01-23456789abcd": "tm-0123456789",
		"01234567-89ff-4fff-bfff-ffffffffffff": "tm-0123456789",
	} {
		got, err := SpawnTmuxName(in)
		if err != nil || got != want {
			t.Errorf("SpawnTmuxName(%q) = %q, %v; want %q, nil", in, got, err, want)
		}
	}
}

// Anything but a canonical UUID v4 is an error, never a name: the name is a
// tmux target, so a ':', '.' or space in it would address something else.
func TestSpawnTmuxName_RefusesAnythingButACanonicalUUIDv4(t *testing.T) {
	for _, in := range []string{
		"", "abc", "0123456789abcdef", "日本語0123456789",
		"0f8e2c4a-91b3-4d5e-a6f7-1234567890a",   // 35 characters
		"0f8e2c4a-91b3-4d5e-a6f7-1234567890abc", // 37 characters
		"0f8e2c4a91b34d5ea6f71234567890ab",      // no dashes (uuid.Parse takes it)
		"{0f8e2c4a-91b3-4d5e-a6f7-1234567890ab}",
		"urn:uuid:0f8e2c4a-91b3-4d5e-a6f7-1234567890ab",
		"0f8e2c4a9-1b3-4d5e-a6f7-1234567890ab", // a dash out of place
		"0f8e2c4g-91b3-4d5e-a6f7-1234567890ab", // not hex
		"0f8e2c4a:91b3-4d5e-a6f7-1234567890ab", // tmux target characters
		"0f8e2c4a.91b3-4d5e-a6f7-1234567890ab",
		"0f8e2c4a 91b3-4d5e-a6f7-1234567890ab",
		" 0f8e2c4a-91b3-4d5e-a6f7-1234567890a",
		"0f8e2c4a-91b3-4d5e-a6f7-1234567890a\n",
		"日本ab-91b3-4d5e-a6f7-1234567890ab",     // 36 bytes, dashes in place, not hex
		"0f8e2c4a-91b3-1d5e-a6f7-1234567890ab", // version 1
		"0f8e2c4a-91b3-4d5e-c6f7-1234567890ab", // Microsoft variant, not RFC 4122
		"00000000-0000-0000-0000-000000000000",
	} {
		if got, err := SpawnTmuxName(in); err == nil || got != "" {
			t.Errorf("SpawnTmuxName(%q) = %q, %v; want \"\", an error", in, got, err)
		}
	}
}
