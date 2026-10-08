package scrub

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const rowUUID = "7e7f214b-c4e3-48cd-ab15-62a3471bd4fd"

// An id-named key is exempt from nothing but pseudonymization: a canonical
// uuid or a toolu_/msg_/req_ id stays, any other string under such a key is
// ordinary text and goes through every redaction rule.
func TestScrub_IdNamedFieldStillRedacted(t *testing.T) {
	m := one(t, `{"type":"assistant","uuid":"`+rowUUID+`","message":{"model":"m","content":[
		{"type":"tool_use","id":"toolu_01ABCDEFGHIJKLMNOPQRSTUV","name":"X","input":{
			"id":"sk-AAAAAAAAAAAAAAAAAAAA","uuid":"bob@corp.example.org","tool_use_id":"/Users/wake/secret/x","agentId":"Bearer abcdefgh12345678",
			"nested":{"uuid":"`+rowUUID+`","id":"toolu_01ZZZZZZZZZZZZZZZZZZZZZZ","list":[{"id":"ghp_abcdefghijklmnopqrstuvwxyz0123456789"}]}}}]}}`)
	if m["uuid"] != rowUUID {
		t.Errorf("row uuid = %v", m["uuid"])
	}
	msg := m["message"].(map[string]any)
	tu := msg["content"].([]any)[0].(map[string]any)
	if tu["id"] != "toolu_01ABCDEFGHIJKLMNOPQRSTUV" {
		t.Errorf("tool_use.id = %v", tu["id"])
	}
	in := tu["input"].(map[string]any)
	for k, want := range map[string]string{"id": "[redacted-key]", "uuid": "user@example.com", "tool_use_id": "/work/secret/x", "agentId": "[redacted-auth]"} {
		if in[k] != want {
			t.Errorf("input.%s = %v, want %q", k, in[k], want)
		}
	}
	nested := in["nested"].(map[string]any)
	if nested["uuid"] != rowUUID || nested["id"] != "toolu_01ZZZZZZZZZZZZZZZZZZZZZZ" {
		t.Errorf("real ids under id-named keys were changed: %v", nested)
	}
	if got := nested["list"].([]any)[0].(map[string]any)["id"]; got != "[redacted-token]" {
		t.Errorf("list id = %v", got)
	}
}

func TestScrub_UrlSafeAndSlashSplitTokensRedacted(t *testing.T) {
	secrets := []string{
		"AbCd1234efGh5678IjKl9012/mnOp3456qrSt7890UvWx1234",                 // std base64 split by "/" into pieces under 32 characters
		"AbCd1234efGh5678IjKl90-12mnOp3456qrSt7890UvWx1234_Zz",              // url-safe base64
		"dGhpcyBpcyBhIHNlY3JldCB0b2tlbiB3aXRoIGxvdHMgb2YgY2hhcnM+/Zm9vYmFy", // "+" and "/" together
		"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",  // hex
		"AbCd1234efGh5678IjKl9012mnOp3456qrSt7890UvWx1234==",                // padded
		"AAAAAAAAAAAA7e7f214b-c4e3-48cd-ab15-62a3471bd4fdBBBBBBBBBBBB",      // a uuid inside a longer secret is part of it
	}
	for _, s := range secrets {
		m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote("token "+s+" end")+`}}`)
		got := m["message"].(map[string]any)["content"].(string)
		if got != "token [redacted-token] end" {
			t.Errorf("%q -> %q, want it redacted", s, got)
		}
	}
	keep := []string{
		"/work/Workspace/user/purdex/internal/convmodel/ccnorm/scrub/scrub",
		"testdata/conversation/v1/cc-transcript/api-error/input",
		"a-very-long-hyphenated-identifier-without-digits-0",
		"gpt-5-6-sol-and-claude-opus-5-5-and-haiku-5-5-names",
		rowUUID,
		"logs/" + rowUUID + "/out",
		"toolu_01ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		"msg_01ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		"req_011CTabcdefghijklmnopqrstuvwxyz0123456",
		"/work/.claude/projects/-work-fixture/" + FixtureSessionID + "/subagents/agent-a7a639d97d57c6f43",
		"tests/TestFacts/child-a7a639d97d57c6f43",
		"2026-10-07T13:00:00.000Z",
	}
	for _, s := range keep {
		m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote("see "+s+" ok")+`}}`)
		if got := m["message"].(map[string]any)["content"]; got != "see "+s+" ok" {
			t.Errorf("%q -> %q, want it kept", s, got)
		}
	}
}

func TestScrub_CaseInsensitivePrefixes(t *testing.T) {
	in := "authorization: bearer abcdef123456 and BEARER xyz987654; KEY SK-ANT-API03-ABCDEFGHIJKLMNOP; GHP_abcdefghijklmnop; XOXB-1234-5678-abcd; akiaiosfodnn7example"
	m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote(in)+`}}`)
	got := m["message"].(map[string]any)["content"].(string)
	for _, bad := range []string{"abcdef123456", "xyz987654", "ANT-API03", "GHP_abc", "XOXB", "iosfodnn7"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived in %q", bad, got)
		}
	}
}

// ---- bounds and fail-closed ------------------------------------------------

func scrubErr(t *testing.T, o Options, input string) (Report, string, error) {
	t.Helper()
	var out bytes.Buffer
	rep, err := Scrub(strings.NewReader(input), &out, o)
	return rep, out.String(), err
}

func TestScrub_BadRowsFailByDefaultWithoutEchoingContent(t *testing.T) {
	good := `{"type":"ai-title","aiTitle":"t"}`
	bad := map[string]string{
		"not json":         "SECRETMARK not json",
		"array":            `["SECRETMARK"]`,
		"null":             `null`,
		"scalar":           `"SECRETMARK"`,
		"trailing data":    `{"type":"ai-title","aiTitle":"t"} {"x":"SECRETMARK"}`,
		"truncated object": `{"type":"ai-title","aiTitle":"SECRETMARK`,
		"invalid utf-8":    "{\"type\":\"ai-title\",\"aiTitle\":\"SECRETMARK\xff\"}",
	}
	for name, row := range bad {
		_, out, err := scrubErr(t, testOpts, good+"\n\n"+row+"\n"+good+"\n")
		var re *RowError
		if !errors.As(err, &re) {
			t.Errorf("%s: err = %v, want a *RowError", name, err)
			continue
		}
		if re.Line != 3 {
			t.Errorf("%s: line = %d, want 3 (blank lines count)", name, re.Line)
		}
		if strings.Contains(err.Error(), "SECRETMARK") {
			t.Errorf("%s: the error echoes row content: %v", name, err)
		}
		if !strings.Contains(err.Error(), "line 3") {
			t.Errorf("%s: error %q does not name the line", name, err)
		}
		if out != "" {
			t.Errorf("%s: output written before the failure: %q", name, out)
		}
	}
}

func TestScrub_AllowBadRowsCountsAndSkips(t *testing.T) {
	o := testOpts
	o.AllowBadRows = true
	rep, out, err := scrubErr(t, o, `{"type":"ai-title","aiTitle":"t"}`+"\n"+`["x"]`+"\n"+"bad\xff\n"+`{"a":1} {"b":2}`+"\n"+`{"type":"ai-title","aiTitle":"u"}`+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Kept != 2 || rep.Rows != 5 || rep.Dropped["not_json"] != 2 || rep.Dropped["not_utf8"] != 1 {
		t.Errorf("report = %+v", rep)
	}
	if strings.Count(out, "\n") != 2 {
		t.Errorf("output = %q", out)
	}
}

func TestScrub_LineLimit(t *testing.T) {
	o := testOpts
	o.MaxLineBytes = 100
	ok := `{"type":"ai-title","aiTitle":"` + strings.Repeat("a", 60) + `"}` // 93 bytes
	if _, _, err := scrubErr(t, o, ok+"\n"); err != nil {
		t.Fatalf("a line under the limit failed: %v", err)
	}
	long := `{"type":"ai-title","aiTitle":"` + strings.Repeat("SECRETMARK", 20) + `"}`
	for _, input := range []string{ok + "\n" + long + "\n", ok + "\n" + long} { // with and without a final newline
		_, out, err := scrubErr(t, o, input)
		var re *RowError
		if !errors.As(err, &re) || re.Line != 2 {
			t.Fatalf("err = %v, want a *RowError at line 2", err)
		}
		if strings.Contains(err.Error(), "SECRETMARK") || out != "" {
			t.Errorf("err = %v, out = %q", err, out)
		}
	}
	// the lenient flag does not lift a size limit
	o.AllowBadRows = true
	if _, _, err := scrubErr(t, o, long+"\n"); err == nil {
		t.Error("-allow-bad-rows let an over-long line through")
	}
}

func TestScrub_TotalLimit(t *testing.T) {
	o := testOpts
	o.MaxTotalBytes = 200
	row := `{"type":"ai-title","aiTitle":"` + strings.Repeat("a", 60) + `"}` + "\n" // 94 bytes
	if _, _, err := scrubErr(t, o, row+row); err != nil {
		t.Fatalf("under the limit: %v", err)
	}
	_, out, err := scrubErr(t, o, row+row+row)
	if err == nil || !strings.Contains(err.Error(), "200") {
		t.Fatalf("err = %v, want the total limit", err)
	}
	if out != "" {
		t.Errorf("output written: %q", out)
	}
}

func TestScrub_DepthLimit(t *testing.T) {
	nest := func(n int) string {
		return `{"type":"assistant","uuid":"u","message":{"content":[{"type":"tool_use","id":"t","name":"X","input":` +
			strings.Repeat(`{"a":`, n) + `1` + strings.Repeat(`}`, n) + `}]}}`
	}
	o := testOpts
	o.MaxDepth = 12
	// row 1, message 2, content 3, block 4, then n levels of input: depth 4+n
	if _, _, err := scrubErr(t, o, nest(8)+"\n"); err != nil {
		t.Fatalf("depth 12 failed: %v", err)
	}
	_, out, err := scrubErr(t, o, `{"type":"ai-title"}`+"\n"+nest(9)+"\n")
	var re *RowError
	if !errors.As(err, &re) || re.Line != 2 || !strings.Contains(re.Reason, "12") {
		t.Fatalf("err = %v, want a depth *RowError at line 2", err)
	}
	if out != "" {
		t.Errorf("output written: %q", out)
	}
	// the default is 64
	if _, _, err := scrubErr(t, testOpts, nest(61)+"\n"); err == nil {
		t.Error("default depth limit missing (depth 65)")
	}
	if _, _, err := scrubErr(t, testOpts, nest(60)+"\n"); err != nil {
		t.Errorf("depth 64 under the default failed: %v", err)
	}
}
