package scrub

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"strings"
	"testing"
)

var testOpts = Options{Home: "/Users/wake", Users: []string{"wake"}}

// run scrubs the given rows and returns the output lines.
func run(t *testing.T, rows ...string) []string {
	t.Helper()
	return runWith(t, testOpts, rows...)
}

// lenientOpts is testOpts that count and skip rows that are not JSON objects.
var lenientOpts = Options{Home: "/Users/wake", Users: []string{"wake"}, AllowBadRows: true}

func runWith(t *testing.T, o Options, rows ...string) []string {
	t.Helper()
	var out bytes.Buffer
	var in []string
	for _, r := range rows {
		var c bytes.Buffer
		if json.Compact(&c, []byte(r)) == nil {
			r = c.String() // the test rows are written over several lines
		}
		in = append(in, r)
	}
	if _, err := Scrub(strings.NewReader(strings.Join(in, "\n")+"\n"), &out, o); err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSuffix(out.String(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func decode(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, line)
	}
	return m
}

// one scrubs a single row and returns it decoded.
func one(t *testing.T, row string) map[string]any {
	t.Helper()
	out := run(t, row)
	if len(out) != 1 {
		t.Fatalf("got %d rows, want 1: %q", len(out), out)
	}
	return decode(t, out[0])
}

func TestScrub_DropsRowsTheNormalizerIgnores(t *testing.T) {
	out := runWith(t, lenientOpts,
		`{"type":"file-history-snapshot","messageId":"m"}`,
		`{"type":"last-prompt","lastPrompt":"x"}`,
		`{"type":"permission-mode","permissionMode":"auto"}`,
		`{"type":"queue-operation","operation":"enqueue","content":"x"}`,
		`{"type":"system","subtype":"stop_hook_summary","uuid":"s1"}`,
		`{"type":"attachment","uuid":"a1","attachment":{"type":"hook_success"}}`,
		`{"type":"attachment","uuid":"a2","attachment":{"type":"instructions","content":"CLAUDE.md wake@protype.tw"}}`,
		`{"type":"ai-title","aiTitle":"t"}`,
		`{"type":"system","subtype":"turn_duration","uuid":"s2"}`,
		`{"type":"attachment","uuid":"a3","attachment":{"type":"queued_command","prompt":"hi"}}`,
		`not json at all`,
		``,
	)
	var types []string
	for _, l := range out {
		m := decode(t, l)
		typ := m["type"].(string)
		if sub, ok := m["subtype"].(string); ok {
			typ += "/" + sub
		}
		if a, ok := m["attachment"].(map[string]any); ok {
			typ += "/" + a["type"].(string)
		}
		types = append(types, typ)
	}
	want := "ai-title system/turn_duration attachment/queued_command"
	if got := strings.Join(types, " "); got != want {
		t.Errorf("kept %q, want %q", got, want)
	}
}

func TestScrub_KeepsOnlyReadFields(t *testing.T) {
	m := one(t, `{"type":"assistant","uuid":"u1","parentUuid":"p","requestId":"req_1","timestamp":"2026-10-07T13:00:00.000Z",
		"isSidechain":false,"userType":"external","entrypoint":"cli","apiBlockIndex":0,"perTurnEffort":"high",
		"message":{"id":"msg_1","model":"claude-opus-5-5","role":"assistant","usage":{"input_tokens":5},
		  "content":[{"type":"thinking","thinking":"hmm","signature":"c2lnbmF0dXJl"},
		             {"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls","extra":{"deep":[1,2]}},"caller":{"type":"direct"}}]}}`)
	for _, gone := range []string{"parentUuid", "requestId", "userType", "apiBlockIndex"} {
		if _, ok := m[gone]; ok {
			t.Errorf("%s kept", gone)
		}
	}
	msg := m["message"].(map[string]any)
	if _, ok := msg["usage"]; ok {
		t.Error("message.usage kept")
	}
	if msg["model"] != "claude-opus-5-5" {
		t.Errorf("model = %v", msg["model"])
	}
	blocks := msg["content"].([]any)
	th := blocks[0].(map[string]any)
	if _, ok := th["signature"]; ok || th["thinking"] != "hmm" {
		t.Errorf("thinking block = %v", th)
	}
	tu := blocks[1].(map[string]any)
	if _, ok := tu["caller"]; ok {
		t.Error("tool_use.caller kept")
	}
	in, _ := json.Marshal(tu["input"])
	if string(in) != `{"command":"ls","extra":{"deep":[1,2]}}` {
		t.Errorf("tool_use.input (a Subtree) = %s, want it whole", in)
	}
	if m["perTurnEffort"] != "high" || m["timestamp"] == nil || m["entrypoint"] != "cli" {
		t.Errorf("read top-level fields lost: %v", m)
	}
}

func TestScrub_EmptyContainersAndScalarsKeepTheirPresence(t *testing.T) {
	// origin without a kind is "invalid", not absent: it must stay an object;
	// a scalar origin stays too
	m := one(t, `{"type":"user","uuid":"u1","origin":{"producer":"p"},"message":{"content":"x"}}`)
	if o, ok := m["origin"].(map[string]any); !ok || len(o) != 0 {
		t.Errorf("origin = %v, want {}", m["origin"])
	}
	m = one(t, `{"type":"user","uuid":"u1","interruptedMessageId":"abc","message":{"content":"x"}}`)
	if m["interruptedMessageId"] != "abc" {
		t.Errorf("interruptedMessageId = %v", m["interruptedMessageId"])
	}
	// a block with only unread members stays as an object, so the count holds
	m = one(t, `{"type":"user","uuid":"u1","message":{"content":[{"foo":1},{"type":"text","text":"a"}]}}`)
	if c := m["message"].(map[string]any)["content"].([]any); len(c) != 2 {
		t.Errorf("content = %v, want both blocks", c)
	}
}

func TestScrub_RewritesIdentityMetadata(t *testing.T) {
	m := one(t, `{"type":"user","uuid":"u1","cwd":"/private/tmp/purdex-c2test","sessionId":"7e7f214b-c4e3-48cd-ab15-62a3471bd4fd","session_id":"7e7f214b-c4e3-48cd-ab15-62a3471bd4fd","gitBranch":"feature/secret","version":"2.1.292","message":{"content":"x"}}`)
	if m["cwd"] != FixtureCwd || m["sessionId"] != FixtureSessionID || m["session_id"] != FixtureSessionID || m["gitBranch"] != FixtureBranch {
		t.Errorf("metadata = %v", m)
	}
	if m["version"] != "2.1.292" {
		t.Errorf("version = %v, want it kept (cc_version of the case)", m["version"])
	}
}

func TestScrub_SessionIDAndCwdAreReplacedInText(t *testing.T) {
	out := run(t,
		`{"type":"user","uuid":"u1","cwd":"/private/tmp/purdex-c2test","sessionId":"7e7f214b-c4e3-48cd-ab15-62a3471bd4fd","message":{"content":"cd /private/tmp/purdex-c2test && cat x/7e7f214b-c4e3-48cd-ab15-62a3471bd4fd/f"}}`,
		// a later row mentions it too, and its own cwd is a different one
		`{"type":"assistant","uuid":"u2","message":{"content":[{"type":"text","text":"in /private/tmp/purdex-c2test: session 7e7f214b-c4e3-48cd-ab15-62a3471bd4fd"}]}}`,
	)
	for _, l := range out {
		if strings.Contains(l, "purdex-c2test") || strings.Contains(l, "7e7f214b") {
			t.Errorf("not replaced: %s", l)
		}
	}
	if !strings.Contains(out[0], "cd "+FixtureCwd+" && cat x/"+FixtureSessionID+"/f") {
		t.Errorf("row 1 = %s", out[0])
	}
}

func TestScrub_Paths(t *testing.T) {
	cases := map[string]string{
		"/Users/wake/Workspace/wake/purdex/x.go":                      "/work/Workspace/user/purdex/x.go",
		"/Users/other/proj/a":                                         "/work/proj/a",
		"/home/ubuntu/.ssh/id":                                        "/work/.ssh/id",
		"/private/tmp/claude-501/-Users-wake-Workspace-p/sid/x.out":   "/work/tmp/sid/x.out",
		"/private/tmp/scratch/a.txt":                                  "/work/tmp/scratch/a.txt",
		"see /tmp/cc-socks/1.sock now":                                "see /work/tmp/cc-socks/1.sock now",
		"/var/folders/ab/cdef0123/T/x/y":                              "/work/tmp/x/y",
		"/private/var/folders/ab/cdef0123/T/x":                        "/work/tmp/x",
		"relative/tmp/dir and src/tmp":                                "relative/tmp/dir and src/tmp",
		"/work/tmp/already and /work/fixture/f":                       "/work/tmp/already and /work/fixture/f",
		"ls /tmp":                                                     "ls /work/tmp",
		"-Users-wake-Workspace-wake-purdex is an encoded project dir": "-work-Workspace-user-purdex is an encoded project dir",
	}
	for in, want := range cases {
		m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote(in)+`}}`)
		if got := m["message"].(map[string]any)["content"]; got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestScrub_McpToolNamesNeutralized(t *testing.T) {
	cases := map[string]string{
		"mcp__ploom__issue_get":                                                  "mcp__server__tool",
		"mcp__outline-protype__create_attachment":                                "mcp__server__tool",
		"mcp__claude_ai_Claude_Docs__batch":                                      "mcp__server__tool",
		"mcp__plugin_context7_context7__authenticate":                            "mcp__server__tool",
		"mcp__server__tool":                                                      "mcp__server__tool",
		"allow mcp__ploom__* and mcp__invoiceplane":                              "allow mcp__server__tool and mcp__server",
		"\x1b[38;5;246mmcp__google-sheets__list_spreadsheets\x1b[39m (3 tokens)": "\x1b[38;5;246mmcp__server__tool\x1b[39m (3 tokens)",
		"├ mcp__ploom__whoami: 1\n├ mcp__ploom__label_list: 2":                   "├ mcp__server__tool: 1\n├ mcp__server__tool: 2",
		"no mention of the protocol":                                             "no mention of the protocol",
		"| mcp__outline-protype__fetch | outline-protype | 326 |\n| mcp__ploom__whoami | ploom | 1k |": "| mcp__server__tool | server | 326 |\n| mcp__server__tool | server | 1k |",
	}
	for in, want := range cases {
		m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote(in)+`}}`)
		if got := m["message"].(map[string]any)["content"]; got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestScrub_ClaudeProjectsPathNeutralized(t *testing.T) {
	const u1 = "d85ca294-842a-41d3-b6b9-84526b2b7d0a"
	cases := map[string]string{
		"/Users/wake/.claude/projects/-private-tmp-claude-501--Users-wake-Workspace-wake-purdex-" + u1 + "-scratchpad/" + u1 + ".jsonl": "/work/.claude/projects/-work-fixture/" + FixtureSessionID + ".jsonl",
		"read /Users/wake/.claude/projects/-Users-wake-Workspace-x/" + u1 + "/subagents/agent-a1.jsonl now":                             "read /work/.claude/projects/-work-fixture/" + FixtureSessionID + "/subagents/agent-a1.jsonl now",
		"at ~/.claude/projects/-tmp-p/memory/MEMORY.md":                                                                                 "at ~/.claude/projects/-work-fixture/memory/MEMORY.md",
		"/work/.claude/projects/-work-fixture/" + FixtureSessionID + ".jsonl":                                                           "/work/.claude/projects/-work-fixture/" + FixtureSessionID + ".jsonl",
		"uuid " + u1 + " outside any path is not a project path":                                                                        "uuid " + u1 + " outside any path is not a project path",
	}
	for in, want := range cases {
		m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote(in)+`}}`)
		got := m["message"].(map[string]any)["content"]
		if got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestScrub_UsernameIsAWholeWord(t *testing.T) {
	m := one(t, `{"type":"user","uuid":"u","message":{"content":"wake and awake, wake-iphone, 這是wake的"}}`)
	got := m["message"].(map[string]any)["content"]
	if want := "user and awake, user-iphone, 這是user的"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestScrub_SecretsEmailsAndAddresses(t *testing.T) {
	in := "mail wake@protype.tw or a.b+c@sub.example.org; ip 100.64.0.2 and 100.64.12.200; " +
		"Authorization: Bearer abc.def-123_456 ok; key sk-ant-api03-ABCDEFGHIJKLMNOP1234; " +
		"gh ghp_abcdefghijklmnopqrstuvwxyz0123456789 and gho_AbCdEf123456; slack xoxb-1234-5678-abcd; aws AKIAIOSFODNN7EXAMPLE; " +
		"keep task-notification and mask-like and disk-usage and 192.168.1.5 and 10.0.0.7"
	m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote(in)+`}}`)
	got := m["message"].(map[string]any)["content"].(string)
	for _, bad := range []string{"@protype", "@sub.example", "100.64.", "Bearer ", "sk-ant", "ghp_", "gho_", "xoxb", "AKIA", "192.168.1.5", "10.0.0.7"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived in %q", bad, got)
		}
	}
	for _, keep := range []string{"task-notification", "mask-like", "disk-usage"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q was rewritten in %q", keep, got)
		}
	}
}

func TestScrub_PdxAddressRewritten(t *testing.T) {
	in := "from mlab/purdex-75-6c and air26/purdex-b0-q3, air19/_q34psn, air-2026/x_y and air-2019/z; " +
		"quoted \"mlab/purdex-b0-q3\" and (mlab/purdex-b0-q3). keep mlabs/foo, notmlab/foo and amlab/x"
	m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote(in)+`}}`)
	got := m["message"].(map[string]any)["content"].(string)
	for _, bad := range []string{"purdex-75", "purdex-b0", "_q34psn", "x_y", "air-2019/z", "air26/", "air19/", "air-2026/", "air-2019/"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived in %q", bad, got)
		}
	}
	for _, want := range []string{"from host/fixture-peer and", "host/fixture-peer,", "host/fixture-peer;", `"host/fixture-peer"`, "(host/fixture-peer)."} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
	for _, keep := range []string{"mlabs/foo", "notmlab/foo", "amlab/x"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q was rewritten in %q", keep, got)
		}
	}
}

// The recording machine's name must not survive in an address (the host part
// is the literal word "host") nor as a bare word.
func TestScrub_PdxAddressHostNeutralized(t *testing.T) {
	cases := map[string]string{
		"mlab/purdex-b0-q3":                 "host/fixture-peer",
		"AIR26/x":                           "host/fixture-peer",
		"send to air-2026/_q34psn now":      "send to host/fixture-peer now",
		"run on mlab, then air26 and Air19": "run on host, then host and host",
		"https://mlab.host/x and air-2019":  "https://host.host/x and host",
		"host/fixture-peer":                 "host/fixture-peer",
		"mlabs and notair26 and air260":     "mlabs and notair26 and air260",
	}
	for in, want := range cases {
		m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote(in)+`}}`)
		if got := m["message"].(map[string]any)["content"]; got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestScrub_UdsSocketPathRewritten(t *testing.T) {
	in := `<peer from="uds:/private/tmp/claude-501/-Users-wake-proj/cc-socks/55982.sock" x=1> and uds:/tmp/cc-socks/7.sock; ` +
		`escaped from=\"uds:/Users/wake/.claude/socks/12345.sock\" end; keep uds:notasocket and /work/tmp/cc-socks/9.sock`
	m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote(in)+`}}`)
	got := m["message"].(map[string]any)["content"].(string)
	for _, bad := range []string{"55982", "12345", "claude-501", "wake", "7.sock"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived in %q", bad, got)
		}
	}
	if n := strings.Count(got, "uds:/work/tmp/cc-socks/1.sock"); n != 3 {
		t.Errorf("%d rewritten sockets in %q, want 3", n, got)
	}
	if !strings.Contains(got, `from=\"uds:/work/tmp/cc-socks/1.sock\" end`) {
		t.Errorf("the escaped quotes around the socket were disturbed: %q", got)
	}
	for _, keep := range []string{"uds:notasocket", " /work/tmp/cc-socks/9.sock"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q was rewritten in %q", keep, got)
		}
	}
}

func TestScrub_LongTokensAreRedactedButIdsAreNot(t *testing.T) {
	tok := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	b64 := "dGhpcyBpcyBhIHNlY3JldCB0b2tlbiB3aXRoIGxvdHMgb2YgY2hhcnM9"
	m := one(t, `{"type":"user","uuid":"7e7f214b-c4e3-48cd-ab15-62a3471bd4fd","message":{"content":"sha `+tok+` and `+b64+` and toolu_01ABCDEFGHIJKLMNOPQRSTUV and a-very-long-hyphenated-identifier-without-digits-0"}}`)
	got := m["message"].(map[string]any)["content"].(string)
	if strings.Contains(got, tok) || strings.Contains(got, b64) {
		t.Errorf("token survived: %q", got)
	}
	if !strings.Contains(got, "toolu_01ABCDEFGHIJKLMNOPQRSTUV") || !strings.Contains(got, "a-very-long-hyphenated-identifier-without-digits-0") {
		t.Errorf("id rewritten: %q", got)
	}
	if m["uuid"] != "7e7f214b-c4e3-48cd-ab15-62a3471bd4fd" {
		t.Errorf("uuid = %v", m["uuid"])
	}
}

func TestScrub_ImageDataBecomesATinyPNG(t *testing.T) {
	big := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAB, 0xCD, 0xEF}, 20000))
	m := one(t, `{"type":"user","uuid":"u","message":{"content":[
		{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+big+`"}},
		{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+big+`"}}]}]}}`)
	blocks := m["message"].(map[string]any)["content"].([]any)
	imgs := []map[string]any{
		blocks[0].(map[string]any)["source"].(map[string]any),
		blocks[1].(map[string]any)["content"].([]any)[0].(map[string]any)["source"].(map[string]any),
	}
	for i, src := range imgs {
		if src["media_type"] != "image/png" {
			t.Errorf("image %d media_type = %v", i, src["media_type"])
		}
		raw, err := base64.StdEncoding.DecodeString(src["data"].(string))
		if err != nil {
			t.Fatalf("image %d: %v", i, err)
		}
		if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
			t.Errorf("image %d is not a valid PNG: %v", i, err)
		}
		if len(raw) > 200 {
			t.Errorf("image %d is %d bytes", i, len(raw))
		}
		if _, ok := src["type"]; ok {
			t.Errorf("image %d: source.type is not read and must go", i)
		}
	}
}

func TestScrub_TextIsKeptAsWritten(t *testing.T) {
	text := "請寫一篇 1500 字的短篇故事 <command-name>/model</command-name> a&b \"q\" \u2028"
	out := run(t, `{"type":"user","uuid":"u","message":{"content":`+quote(text)+`}}`)
	if strings.Contains(out[0], `\u003c`) || strings.Contains(out[0], `\u0026`) {
		t.Errorf("HTML-escaped: %s", out[0])
	}
	if got := decode(t, out[0])["message"].(map[string]any)["content"]; got != text {
		t.Errorf("text changed: %q", got)
	}
}

func TestScrub_NumbersSurviveExactly(t *testing.T) {
	out := run(t, `{"type":"user","uuid":"u","toolUseResult":{"structuredPatch":[{"oldStart":12,"oldLines":3,"newStart":12,"newLines":9007199254740993,"lines":[" a","-b","+c"]}]},"message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"x"}]}}`)
	if !strings.Contains(out[0], `"newLines":9007199254740993`) {
		t.Errorf("number changed: %s", out[0])
	}
}

func TestScrub_ToolResultFieldsKeptAndTheRestOfToolUseResultDropped(t *testing.T) {
	m := one(t, `{"type":"user","uuid":"u","toolDenialKind":"permission-rule","toolUseResult":{"stdout":"leak /Users/wake","backgroundTaskId":"bg1","agentId":"a1b2","isAsync":true,"description":"d","filePath":"/Users/wake/x.go","oldString":"zzz"},
		"message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"nope"}]}}`)
	if m["toolDenialKind"] != "permission-rule" {
		t.Errorf("toolDenialKind = %v", m["toolDenialKind"])
	}
	tur := m["toolUseResult"].(map[string]any)
	if _, ok := tur["stdout"]; ok {
		t.Error("toolUseResult.stdout kept")
	}
	if _, ok := tur["oldString"]; ok {
		t.Error("toolUseResult.oldString kept")
	}
	if tur["backgroundTaskId"] != "bg1" || tur["agentId"] != "a1b2" || tur["isAsync"] != true || tur["filePath"] != "/work/x.go" {
		t.Errorf("toolUseResult = %v", tur)
	}
}

func TestScrub_IsDeterministicAndIdempotent(t *testing.T) {
	rows := []string{
		`{"zz":1,"type":"user","uuid":"u1","cwd":"/Users/wake/p","sessionId":"7e7f214b-c4e3-48cd-ab15-62a3471bd4fd","message":{"content":"a /Users/wake/p/x wake@protype.tw /tmp/x"}}`,
		`{"type":"assistant","uuid":"u2","message":{"model":"m","content":[{"type":"text","text":"b"}]}}`,
	}
	a := strings.Join(run(t, rows...), "\n")
	b := strings.Join(run(t, rows...), "\n")
	if a != b {
		t.Error("two runs differ")
	}
	again := strings.Join(run(t, strings.Split(a, "\n")...), "\n")
	if again != a {
		t.Errorf("not idempotent:\n%s\n%s", a, again)
	}
	if strings.Index(a, `"cwd"`) > strings.Index(a, `"type"`) {
		t.Errorf("keys are not in sorted order: %s", a)
	}
}

func TestScrub_ReportCountsWhatItDropped(t *testing.T) {
	var out bytes.Buffer
	rep, err := Scrub(strings.NewReader(`{"type":"last-prompt"}`+"\n"+`{"type":"last-prompt"}`+"\n"+`junk`+"\n"+`{"type":"ai-title","aiTitle":"t"}`+"\n"), &out, lenientOpts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 4 || rep.Kept != 1 || rep.Dropped["type:last-prompt"] != 2 || rep.Dropped["not_json"] != 1 {
		t.Errorf("report = %+v", rep)
	}
}

const omittedStdout = "<local-command-stdout>[output omitted by scrubber]</local-command-stdout>"

// A /context listing names the recording host's plugins and skills; any
// local-command output past 1 KiB is replaced whole.
func TestScrub_LongLocalCommandOutputOmitted(t *testing.T) {
	body := "\x1b[1mContext Usage\x1b[22m\n" + strings.Repeat("Plugin (figma): figma-skill 12 tokens\n", 60)
	long := "<local-command-stdout> " + body + "</local-command-stdout>"
	if len(long) <= 1024 {
		t.Fatalf("test text is only %d bytes", len(long))
	}
	sys := one(t, `{"type":"system","subtype":"local_command","uuid":"s1","content":`+quote(long)+`}`)
	if got := sys["content"]; got != omittedStdout {
		t.Errorf("system content = %.120q", got)
	}
	usr := one(t, `{"type":"user","uuid":"u1","message":{"content":`+quote(long)+`}}`)
	if got := usr["message"].(map[string]any)["content"]; got != omittedStdout {
		t.Errorf("user content = %.120q", got)
	}
	if usr["uuid"] != "u1" || sys["uuid"] != "s1" {
		t.Error("row ids changed")
	}
	// idempotent: the replacement is itself short and stays
	again := one(t, `{"type":"system","subtype":"local_command","uuid":"s1","content":`+quote(omittedStdout)+`}`)
	if again["content"] != omittedStdout {
		t.Errorf("replacement not stable: %v", again["content"])
	}
}

func TestScrub_ShortLocalCommandOutputKept(t *testing.T) {
	for _, s := range []string{
		"<local-command-stdout>Set model to Sonnet 5.5</local-command-stdout>",
		"<local-command-stdout>\x1b[2mSession usage: 12 tokens\x1b[22m</local-command-stdout>",
		"<local-command-stdout>" + strings.Repeat("x", 1024-len("<local-command-stdout></local-command-stdout>")) + "</local-command-stdout>",
	} {
		if len(s) > 1024 {
			t.Fatalf("test text is %d bytes", len(s))
		}
		m := one(t, `{"type":"system","subtype":"local_command","uuid":"s","content":`+quote(s)+`}`)
		if m["content"] != s {
			t.Errorf("short stdout changed: %.80q", m["content"])
		}
	}
	// long text that is not local-command output is not touched
	other := strings.Repeat("plain prose ", 200)
	m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote(other)+`}}`)
	if m["message"].(map[string]any)["content"] != other {
		t.Error("ordinary long text changed")
	}
}

// A /context dump reaches the model as an isMeta user row: markdown, not a
// local-command element, but it lists the host's plugins and skills all the same.
func TestScrub_ContextUsageDumpOmitted(t *testing.T) {
	long := "## Context Usage\n" + strings.Repeat("- Plugin (figma): figma-skill 12 tokens\n", 60)
	if len(long) <= 1024 {
		t.Fatalf("test text is only %d bytes", len(long))
	}
	m := one(t, `{"type":"user","uuid":"u1","isMeta":true,"message":{"content":`+quote(long)+`}}`)
	if got := m["message"].(map[string]any)["content"]; got != OmittedContextUsage {
		t.Errorf("content = %.120q", got)
	}
	if m["uuid"] != "u1" {
		t.Error("row id changed")
	}
	if OmittedContextUsage != "## Context Usage\n[output omitted by scrubber]" {
		t.Errorf("OmittedContextUsage = %q", OmittedContextUsage)
	}
	// idempotent: the replacement is itself short and stays
	again := one(t, `{"type":"user","uuid":"u1","isMeta":true,"message":{"content":`+quote(OmittedContextUsage)+`}}`)
	if again["message"].(map[string]any)["content"] != OmittedContextUsage {
		t.Errorf("replacement not stable: %v", again["message"])
	}
	// a short string with the same heading stays; so does ordinary long text
	short := "## Context Usage\nsmall"
	other := "## Notes\n" + strings.Repeat("plain prose ", 200)
	for _, s := range []string{short, other} {
		m := one(t, `{"type":"user","uuid":"u","message":{"content":`+quote(s)+`}}`)
		if m["message"].(map[string]any)["content"] != s {
			t.Errorf("text changed: %.60q", s)
		}
	}
}

func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSpace(b.String())
}
