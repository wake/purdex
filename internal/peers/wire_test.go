package peers

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// --- golden JSON: WireFrom / WireTo ---------------------------------------

func TestWireFrom_JSON(t *testing.T) {
	f := WireFrom{
		HostID:         "h1",
		AgentSessionID: "s1",
		PID:            123,
		ProcStart:      "Sun Sep 13 15:22:36 2026",
		PeerName:       "pn",
		SessionName:    "sn",
		DeclaredMode:   ModePrompting,
	}
	want := `{"host_id":"h1","agent_session_id":"s1","pid":123,"proc_start":"Sun Sep 13 15:22:36 2026","peer_name":"pn","session_name":"sn","declared_mode":"prompting"}`
	got, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Marshal(WireFrom) =\n%s\nwant\n%s", got, want)
	}

	var back WireFrom
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != f {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", back, f)
	}
}

func TestWireTo_JSON(t *testing.T) {
	to := WireTo{
		AgentSessionID: "s2",
		PID:            456,
		ProcStart:      "Mon Sep 14 10:00:00 2026",
	}
	want := `{"agent_session_id":"s2","pid":456,"proc_start":"Mon Sep 14 10:00:00 2026"}`
	got, err := json.Marshal(to)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Marshal(WireTo) =\n%s\nwant\n%s", got, want)
	}

	var back WireTo
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != to {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", back, to)
	}
}

// --- golden JSON: DeliverRequest / DeliverResponse ------------------------

func TestDeliverRequest_JSON_WithHopChain(t *testing.T) {
	req := DeliverRequest{
		MsgID:    "123e4567-e89b-12d3-a456-426614174000",
		HopChain: "h1,h2",
		From: WireFrom{
			HostID:         "h1",
			AgentSessionID: "s1",
			PID:            111,
			ProcStart:      "Sun Sep 13 15:22:36 2026",
			PeerName:       "pn",
			SessionName:    "sn",
			DeclaredMode:   ModePrompting,
		},
		To: WireTo{
			AgentSessionID: "s2",
			PID:            222,
			ProcStart:      "Mon Sep 14 10:00:00 2026",
		},
		Text: "hello",
	}
	want := `{"msg_id":"123e4567-e89b-12d3-a456-426614174000","hop_chain":"h1,h2",` +
		`"from":{"host_id":"h1","agent_session_id":"s1","pid":111,"proc_start":"Sun Sep 13 15:22:36 2026","peer_name":"pn","session_name":"sn","declared_mode":"prompting"},` +
		`"to":{"agent_session_id":"s2","pid":222,"proc_start":"Mon Sep 14 10:00:00 2026"},"text":"hello"}`
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Marshal(DeliverRequest) =\n%s\nwant\n%s", got, want)
	}

	var back DeliverRequest
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != req {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", back, req)
	}
}

func TestDeliverRequest_JSON_HopChainOmittedWhenEmpty(t *testing.T) {
	req := DeliverRequest{
		MsgID: "123e4567-e89b-12d3-a456-426614174000",
		From:  WireFrom{HostID: "h1"},
		To:    WireTo{AgentSessionID: "s2"},
		Text:  "hi",
	}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(got), "hop_chain") {
		t.Fatalf("Marshal(DeliverRequest) with empty HopChain should omit the field, got %s", got)
	}
}

func TestDeliverResponse_JSON(t *testing.T) {
	resp := DeliverResponse{
		MsgID:         "123e4567-e89b-12d3-a456-426614174000",
		Result:        ResultDelivered,
		EffectiveMode: ModeBypass,
		OneWay:        true,
	}
	want := `{"msg_id":"123e4567-e89b-12d3-a456-426614174000","result":"delivered","effective_mode":"bypass","one_way":true}`
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Marshal(DeliverResponse) =\n%s\nwant\n%s", got, want)
	}

	var back DeliverResponse
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != resp {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", back, resp)
	}
}

// --- golden JSON: SendRequest / SendResponse ------------------------------

func TestSendRequest_JSON(t *testing.T) {
	req := SendRequest{
		To:          "host1/session1",
		Text:        "hello",
		Mode:        ModeBypass,
		OriginInbox: "/tmp/inbox.sock",
	}
	want := `{"to":"host1/session1","text":"hello","mode":"bypass","origin_inbox":"/tmp/inbox.sock"}`
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Marshal(SendRequest) =\n%s\nwant\n%s", got, want)
	}

	var back SendRequest
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != req {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", back, req)
	}
}

func TestSendRequest_JSON_ModeOmittedWhenEmpty(t *testing.T) {
	req := SendRequest{To: "host1/session1", Text: "hi", OriginInbox: "/tmp/inbox.sock"}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(got), `"mode"`) {
		t.Fatalf("Marshal(SendRequest) with empty Mode should omit the field, got %s", got)
	}
}

func TestSendResponse_JSON(t *testing.T) {
	resp := SendResponse{
		MsgID:     "123e4567-e89b-12d3-a456-426614174000",
		ToHostID:  "h2",
		ToAddress: "alias/session1",
		To: WireTo{
			AgentSessionID: "s2",
			PID:            222,
			ProcStart:      "Mon Sep 14 10:00:00 2026",
		},
		Result:        ResultDeliveryUncertain,
		EffectiveMode: ModePrompting,
		OneWay:        false,
	}
	want := `{"msg_id":"123e4567-e89b-12d3-a456-426614174000","to_host_id":"h2","to_address":"alias/session1",` +
		`"to":{"agent_session_id":"s2","pid":222,"proc_start":"Mon Sep 14 10:00:00 2026"},` +
		`"result":"delivery_uncertain","effective_mode":"prompting","one_way":false}`
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Marshal(SendResponse) =\n%s\nwant\n%s", got, want)
	}

	var back SendResponse
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != resp {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", back, resp)
	}
}

// --- golden JSON: APIError / RemoteError ----------------------------------

func TestAPIError_JSON_Minimal(t *testing.T) {
	e := APIError{Error: ErrBadRequest}
	want := `{"error":"bad_request"}`
	got, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Marshal(APIError) =\n%s\nwant\n%s", got, want)
	}
}

func TestAPIError_JSON_Full(t *testing.T) {
	e := APIError{
		Error:      ErrAmbiguous,
		Detail:     "multiple candidates",
		Candidates: []string{"h1/s1", "h1/s2"},
		Remote: &RemoteError{
			Status: 502,
			Error:  ErrRemoteError,
			Detail: "upstream failed",
		},
	}
	want := `{"error":"ambiguous","detail":"multiple candidates","candidates":["h1/s1","h1/s2"],` +
		`"remote":{"status":502,"error":"remote_error","detail":"upstream failed"}}`
	got, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Marshal(APIError) =\n%s\nwant\n%s", got, want)
	}

	var back APIError
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Error != e.Error || back.Detail != e.Detail || len(back.Candidates) != 2 || back.Remote == nil || *back.Remote != *e.Remote {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", back, e)
	}
}

func TestRemoteError_JSON_DetailOmittedWhenEmpty(t *testing.T) {
	re := RemoteError{Status: 404, Error: ErrPeerNotFound}
	want := `{"status":404,"error":"peer_not_found"}`
	got, err := json.Marshal(re)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Marshal(RemoteError) =\n%s\nwant\n%s", got, want)
	}
}

// --- golden JSON + map key: OriginKey -------------------------------------

func TestOriginKey_JSON(t *testing.T) {
	k := OriginKey{
		HostID:         "h1",
		AgentSessionID: "s1",
		PID:            123,
		ProcStart:      "Sun Sep 13 15:22:36 2026",
	}
	want := `{"host_id":"h1","agent_session_id":"s1","pid":123,"proc_start":"Sun Sep 13 15:22:36 2026"}`
	got, err := json.Marshal(k)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Marshal(OriginKey) =\n%s\nwant\n%s", got, want)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(got, &raw); err != nil {
		t.Fatalf("Unmarshal into map: %v", err)
	}
	for _, key := range []string{"host_id", "agent_session_id", "pid", "proc_start"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("OriginKey JSON missing key %q", key)
		}
	}

	var back OriginKey
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != k {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", back, k)
	}
}

func TestOriginKey_UsableAsMapKey(t *testing.T) {
	k1 := OriginKey{HostID: "h1", AgentSessionID: "s1", PID: 1, ProcStart: "p1"}
	k2 := OriginKey{HostID: "h1", AgentSessionID: "s1", PID: 1, ProcStart: "p1"}
	m := map[OriginKey]bool{k1: true}
	if !m[k2] {
		t.Fatalf("OriginKey with identical fields should be equal as map key")
	}
}

// --- Key() ------------------------------------------------------------

func TestWireFrom_Key(t *testing.T) {
	f := WireFrom{
		HostID:         "h1",
		AgentSessionID: "s1",
		PID:            123,
		ProcStart:      "Sun Sep 13 15:22:36 2026",
	}
	want := OriginKey{HostID: "h1", AgentSessionID: "s1", PID: 123, ProcStart: "Sun Sep 13 15:22:36 2026"}
	if got := f.Key(); got != want {
		t.Fatalf("WireFrom.Key() = %+v, want %+v", got, want)
	}
}

func TestWireTo_Key(t *testing.T) {
	to := WireTo{
		AgentSessionID: "s2",
		PID:            456,
		ProcStart:      "Mon Sep 14 10:00:00 2026",
	}
	want := OriginKey{HostID: "h2", AgentSessionID: "s2", PID: 456, ProcStart: "Mon Sep 14 10:00:00 2026"}
	if got := to.Key("h2"); got != want {
		t.Fatalf("WireTo.Key(%q) = %+v, want %+v", "h2", got, want)
	}
}

func TestKeys_Equal_WhenFromAndToDescribeSameOrigin(t *testing.T) {
	from := WireFrom{HostID: "h1", AgentSessionID: "s1", PID: 123, ProcStart: "Sun Sep 13 15:22:36 2026"}
	to := WireTo{AgentSessionID: "s1", PID: 123, ProcStart: "Sun Sep 13 15:22:36 2026"}
	if from.Key() != to.Key("h1") {
		t.Fatalf("from.Key() = %+v, to.Key(h1) = %+v, want equal", from.Key(), to.Key("h1"))
	}
}

// --- ValidateText ----------------------------------------------------------

func TestValidateText_EmptyRejected(t *testing.T) {
	if err := ValidateText(""); err == nil {
		t.Fatal("ValidateText(\"\"): expected error, got nil")
	}
}

func TestValidateText_AtLimitOK(t *testing.T) {
	s := strings.Repeat("a", MaxTextBytes)
	if err := ValidateText(s); err != nil {
		t.Fatalf("ValidateText(65536 bytes): unexpected error: %v", err)
	}
}

func TestValidateText_OverLimitRejected(t *testing.T) {
	s := strings.Repeat("a", MaxTextBytes+1)
	if err := ValidateText(s); err == nil {
		t.Fatal("ValidateText(65537 bytes): expected error, got nil")
	}
}

func TestValidateText_InvalidUTF8Rejected(t *testing.T) {
	s := string([]byte{0xff, 0xfe, 0xfd})
	if err := ValidateText(s); err == nil {
		t.Fatal("ValidateText(invalid utf8): expected error, got nil")
	}
}

func TestValidateText_ValidOK(t *testing.T) {
	if err := ValidateText("hello world"); err != nil {
		t.Fatalf("ValidateText(\"hello world\"): unexpected error: %v", err)
	}
}

// --- ValidateMode -----------------------------------------------------

func TestValidateMode_Table(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", ModePrompting, false},
		{ModePrompting, ModePrompting, false},
		{ModeBypass, ModeBypass, false},
		{"bogus", "", true},
		{"Prompting", "", true}, // case-sensitive
	}
	for _, c := range cases {
		got, err := ValidateMode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ValidateMode(%q): expected error, got nil (got %q)", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ValidateMode(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ValidateMode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- IsUUID -------------------------------------------------------------

func TestIsUUID_Table(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"123e4567-e89b-12d3-a456-426614174000", true},
		{"00000000-0000-0000-0000-000000000000", true},
		{"123E4567-E89B-12D3-A456-426614174000", false},  // uppercase
		{"123e4567e89b12d3a456426614174000", false},      // no dashes
		{"123e4567-e89b-12d3-a456-42661417400", false},   // too short
		{"123e4567-e89b-12d3-a456-4266141740000", false}, // too long
		{"", false},
		{"not-a-uuid", false},
		{"123e4567-e89b-12d3-a456-42661417400g", false}, // bad hex char
	}
	for _, c := range cases {
		if got := IsUUID(c.in); got != c.want {
			t.Errorf("IsUUID(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// --- DeliverRequest.Validate ------------------------------------------

func validDeliverRequest() DeliverRequest {
	return DeliverRequest{
		MsgID: "123e4567-e89b-12d3-a456-426614174000",
		From: WireFrom{
			HostID:         "h1",
			AgentSessionID: "s1",
			PID:            111,
			ProcStart:      "Sun Sep 13 15:22:36 2026",
			DeclaredMode:   ModePrompting,
		},
		To: WireTo{
			AgentSessionID: "s2",
			PID:            222,
			ProcStart:      "Mon Sep 14 10:00:00 2026",
		},
		Text: "hello",
	}
}

func TestDeliverRequest_Validate_ValidOK(t *testing.T) {
	if err := validDeliverRequest().Validate(); err != nil {
		t.Fatalf("Validate(): unexpected error: %v", err)
	}
}

func TestDeliverRequest_Validate_NonUUIDMsgID(t *testing.T) {
	r := validDeliverRequest()
	r.MsgID = "not-a-uuid"
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for non-UUID msg_id, got nil")
	}
}

func TestDeliverRequest_Validate_MissingFromHostID(t *testing.T) {
	r := validDeliverRequest()
	r.From.HostID = ""
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for missing from.host_id, got nil")
	}
}

func TestDeliverRequest_Validate_MissingFromAgentSessionID(t *testing.T) {
	r := validDeliverRequest()
	r.From.AgentSessionID = ""
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for missing from.agent_session_id, got nil")
	}
}

func TestDeliverRequest_Validate_FromPIDZero(t *testing.T) {
	r := validDeliverRequest()
	r.From.PID = 0
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for from.pid == 0, got nil")
	}
}

func TestDeliverRequest_Validate_FromPIDNegative(t *testing.T) {
	r := validDeliverRequest()
	r.From.PID = -1
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for from.pid < 0, got nil")
	}
}

func TestDeliverRequest_Validate_BadFromProcStart(t *testing.T) {
	r := validDeliverRequest()
	r.From.ProcStart = "not-a-timestamp"
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for bad from.proc_start, got nil")
	}
}

func TestDeliverRequest_Validate_MissingToAgentSessionID(t *testing.T) {
	r := validDeliverRequest()
	r.To.AgentSessionID = ""
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for missing to.agent_session_id, got nil")
	}
}

func TestDeliverRequest_Validate_ToPIDZero(t *testing.T) {
	r := validDeliverRequest()
	r.To.PID = 0
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for to.pid == 0, got nil")
	}
}

func TestDeliverRequest_Validate_BadToProcStart(t *testing.T) {
	r := validDeliverRequest()
	r.To.ProcStart = "not-a-timestamp"
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for bad to.proc_start, got nil")
	}
}

func TestDeliverRequest_Validate_BadDeclaredMode(t *testing.T) {
	r := validDeliverRequest()
	r.From.DeclaredMode = "bogus"
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for bad from.declared_mode, got nil")
	}
}

func TestDeliverRequest_Validate_EmptyText(t *testing.T) {
	r := validDeliverRequest()
	r.Text = ""
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for empty text, got nil")
	}
}

func TestDeliverRequest_Validate_TextTooLarge(t *testing.T) {
	r := validDeliverRequest()
	r.Text = strings.Repeat("a", MaxTextBytes+1)
	if err := r.Validate(); err == nil {
		t.Fatal("Validate(): expected error for text over MaxTextBytes, got nil")
	}
}

// --- Validate sentinels / ValidationCode ------------------------------------

// TestDeliverRequest_Validate_Sentinels pins that Validate wraps a
// distinguishable sentinel for every rule that maps to its own error code
// (text_too_large, bad_mode) and to the generic bad_request otherwise, so a
// handler can pick the wire code with errors.Is instead of string matching.
func TestDeliverRequest_Validate_Sentinels(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(r *DeliverRequest)
		sentinel error
		code     string
	}{
		{"oversized text", func(r *DeliverRequest) { r.Text = strings.Repeat("a", MaxTextBytes+1) }, ErrTextOversized, ErrTextTooLarge},
		{"empty text", func(r *DeliverRequest) { r.Text = "" }, ErrTextInvalid, ErrBadRequest},
		{"invalid utf8", func(r *DeliverRequest) { r.Text = "a\xffb" }, ErrTextInvalid, ErrBadRequest},
		{"bad mode", func(r *DeliverRequest) { r.From.DeclaredMode = "bogus" }, ErrModeInvalid, ErrBadMode},
		{"bad msg_id", func(r *DeliverRequest) { r.MsgID = "nope" }, nil, ErrBadRequest},
		{"bad from pid", func(r *DeliverRequest) { r.From.PID = 0 }, nil, ErrBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := validDeliverRequest()
			c.mutate(&r)
			err := r.Validate()
			if err == nil {
				t.Fatal("Validate(): expected error, got nil")
			}
			if c.sentinel != nil && !errors.Is(err, c.sentinel) {
				t.Errorf("errors.Is(%v, %v) = false", err, c.sentinel)
			}
			for _, other := range []error{ErrTextOversized, ErrTextInvalid, ErrModeInvalid} {
				if other != c.sentinel && errors.Is(err, other) {
					t.Errorf("errors.Is(%v, %v) = true, want false", err, other)
				}
			}
			if got := ValidationCode(err); got != c.code {
				t.Errorf("ValidationCode(%v) = %q, want %q", err, got, c.code)
			}
		})
	}
	if got := ValidationCode(nil); got != "" {
		t.Errorf("ValidationCode(nil) = %q, want \"\"", got)
	}
}

// TestDeliverRequest_Validate_LabelBounds pins the caps on the
// sender-controlled labels that end up in a frame or a helper name:
// session_name and peer_name ≤ MaxLabelBytes, hop_chain ≤
// MaxHopChainBytes, all valid UTF-8 and free of control characters —
// each a bad_request via ErrFieldInvalid.
func TestDeliverRequest_Validate_LabelBounds(t *testing.T) {
	bad := []struct {
		name   string
		mutate func(r *DeliverRequest)
	}{
		{"session_name too long", func(r *DeliverRequest) { r.From.SessionName = strings.Repeat("s", MaxLabelBytes+1) }},
		{"session_name control char", func(r *DeliverRequest) { r.From.SessionName = "foo\nbar" }},
		{"session_name invalid utf8", func(r *DeliverRequest) { r.From.SessionName = "a\xffb" }},
		{"peer_name too long", func(r *DeliverRequest) { r.From.PeerName = strings.Repeat("p", MaxLabelBytes+1) }},
		{"peer_name control char", func(r *DeliverRequest) { r.From.PeerName = "x\x1by" }},
		{"hop_chain too long", func(r *DeliverRequest) { r.HopChain = strings.Repeat("h", MaxHopChainBytes+1) }},
		{"hop_chain invalid utf8", func(r *DeliverRequest) { r.HopChain = "h\xff" }},
		{"hop_chain control char", func(r *DeliverRequest) { r.HopChain = "h1\nh2" }},
		{"hop_chain NUL", func(r *DeliverRequest) { r.HopChain = "h1\x00h2" }},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			r := validDeliverRequest()
			c.mutate(&r)
			err := r.Validate()
			if !errors.Is(err, ErrFieldInvalid) {
				t.Fatalf("Validate() = %v, want ErrFieldInvalid", err)
			}
			if got := ValidationCode(err); got != ErrBadRequest {
				t.Errorf("ValidationCode = %q, want bad_request", got)
			}
		})
	}
	good := []struct {
		name   string
		mutate func(r *DeliverRequest)
	}{
		{"session_name at limit", func(r *DeliverRequest) { r.From.SessionName = strings.Repeat("s", MaxLabelBytes) }},
		{"session_name unicode", func(r *DeliverRequest) { r.From.SessionName = "工作區 ✓" }},
		{"empty labels", func(r *DeliverRequest) { r.From.SessionName, r.From.PeerName, r.HopChain = "", "", "" }},
		{"hop_chain at limit", func(r *DeliverRequest) { r.HopChain = strings.Repeat("h", MaxHopChainBytes) }},
	}
	for _, c := range good {
		t.Run(c.name, func(t *testing.T) {
			r := validDeliverRequest()
			c.mutate(&r)
			if err := r.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestValidate_QuotedRemoteTextBounded pins that the two Validate errors
// which quote a sender-supplied value (msg_id, declared_mode) never carry
// it whole: a receiver echoes these details to the peer and into its
// audit row, so the quoted value is cut to MaxQuotedBytes with a marker.
func TestValidate_QuotedRemoteTextBounded(t *testing.T) {
	huge := strings.Repeat("z", 10*1024)
	cases := []struct {
		name string
		err  error
	}{
		{"msg_id", func() error { r := validDeliverRequest(); r.MsgID = huge; return r.Validate() }()},
		{"declared_mode", func() error { r := validDeliverRequest(); r.From.DeclaredMode = huge; return r.Validate() }()},
		{"ValidateMode", func() error { _, err := ValidateMode(huge); return err }()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.err == nil {
				t.Fatal("expected an error")
			}
			msg := c.err.Error()
			if len(msg) > MaxQuotedBytes+64 {
				t.Errorf("error is %d bytes (%q…), want the quoted value cut to %d", len(msg), msg[:80], MaxQuotedBytes)
			}
			if !strings.Contains(msg, "…") {
				t.Errorf("error %q lacks the truncation marker", msg)
			}
		})
	}
	// A short value is quoted whole, no marker.
	if _, err := ValidateMode("bogus"); err == nil || !strings.Contains(err.Error(), `"bogus"`) || strings.Contains(err.Error(), "…") {
		t.Errorf("ValidateMode(bogus) = %v, want the value quoted whole", err)
	}
	// The cut lands on a rune boundary: no split rune, which %q would
	// otherwise render as a \x escape.
	r := validDeliverRequest()
	r.MsgID = strings.Repeat("工", MaxQuotedBytes)
	if err := r.Validate(); err == nil || strings.Contains(err.Error(), `\x`) || !utf8.ValidString(err.Error()) {
		t.Errorf("Validate() = %v, want the cut on a rune boundary", err)
	}
}
