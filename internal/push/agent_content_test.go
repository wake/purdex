package push

import (
	"encoding/json"
	"strings"
	"testing"
)

// PU-3 Task 5 (content): the Mac's buildNotificationContent cases (spec §5.3), minus WorkerTerminated.

func agentIn(ev string, detail map[string]any) AgentInput {
	return AgentInput{HostLabel: "mlab", SessionCode: "c1", SessionID: "sid-1", SessionName: "dev", EventName: ev, Detail: detail}
}

func TestAgentContent_Bodies(t *testing.T) {
	cases := []struct {
		name   string
		in     AgentInput
		locale string
		want   string
		pushed bool
	}{
		{"notification message", agentIn("Notification", map[string]any{"message": "build done"}), "zh-TW", "build done", true},
		{"notification permission_prompt zh", agentIn("Notification", map[string]any{"notification_type": "permission_prompt"}), "zh-TW", "需要授權核准", true},
		{"notification permission_prompt en", agentIn("Notification", map[string]any{"notification_type": "permission_prompt"}), "en", "Permission approval required", true},
		{"notification elicitation_dialog zh", agentIn("Notification", map[string]any{"notification_type": "elicitation_dialog"}), "zh-TW", "需要輸入資訊（MCP）", true},
		{"notification elicitation_dialog en", agentIn("Notification", map[string]any{"notification_type": "elicitation_dialog"}), "en", "Input required (MCP)", true},
		{"notification fallback zh", agentIn("Notification", nil), "zh-TW", "新通知", true},
		{"notification fallback en", agentIn("Notification", nil), "en", "New notification", true},
		{"notification message wins over type", agentIn("Notification", map[string]any{"message": "m", "notification_type": "permission_prompt"}), "en", "m", true},
		{"permission request tool zh", agentIn("PermissionRequest", map[string]any{"tool_name": "Bash"}), "zh-TW", "需要授權：Bash", true},
		{"permission request tool en", agentIn("PermissionRequest", map[string]any{"tool_name": "Bash"}), "en", "Permission required: Bash", true},
		{"permission request no tool zh", agentIn("PermissionRequest", nil), "zh-TW", "需要授權：未知工具", true},
		{"permission request no tool en", agentIn("PermissionRequest", nil), "en", "Permission required: unknown tool", true},
		{"stop last message", agentIn("Stop", map[string]any{"last_assistant_message": "All **done**."}), "en", "All done.", true},
		{"stop fallback zh", agentIn("Stop", nil), "zh-TW", "任務完成", true},
		{"stop fallback en", agentIn("Stop", map[string]any{"last_assistant_message": ""}), "en", "Task completed", true},
		{"stop failure error_details first", agentIn("StopFailure", map[string]any{"error_details": "rate limited", "error": "rate_limit"}), "en", "rate limited", true},
		{"stop failure error", agentIn("StopFailure", map[string]any{"error": "rate_limit"}), "en", "rate_limit", true},
		{"stop failure fallback zh", agentIn("StopFailure", nil), "zh-TW", "任務異常中斷", true},
		{"stop failure fallback en", agentIn("StopFailure", nil), "en", "Task stopped unexpectedly", true},
		{"a probe / sweep reason is no event", agentIn("screen_probe", map[string]any{"message": "x"}), "en", "", false},
		{"an empty event name", agentIn("", nil), "en", "", false},
		{"worker terminated is out of v1", agentIn("WorkerTerminated", nil), "en", "", false},
		{"an unknown locale is zh-TW", agentIn("Stop", nil), "fr", "任務完成", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := AgentContent(c.in, c.locale)
			if ok != c.pushed {
				t.Fatalf("pushed = %v, want %v", ok, c.pushed)
			}
			if ok && got.Body != c.want {
				t.Fatalf("body = %q, want %q", got.Body, c.want)
			}
		})
	}
}

// Both PdxXxx and the legacy event names reach the same content (the caller normalises, but the content is robust).
func TestAgentContent_AcceptsThePdxNames(t *testing.T) {
	got, ok := AgentContent(agentIn("PdxStopFailure", map[string]any{"error": "boom"}), "en")
	if !ok || got.Body != "boom" || got.Event != "StopFailure" {
		t.Fatalf("content = %+v ok=%v", got, ok)
	}
}

func TestAgentContent_FieldsAndCollapse(t *testing.T) {
	got, ok := AgentContent(agentIn("Stop", map[string]any{"last_assistant_message": "done"}), "en")
	if !ok {
		t.Fatal("no content")
	}
	if got.Kind != "agent" || got.Event != "Stop" || got.SessionCode != "c1" || got.SessionID != "sid-1" || got.SessionName != "dev" {
		t.Fatalf("content = %+v", got)
	}
	if got.CollapseID != "agent-c1" {
		t.Fatalf("collapse id = %q, want agent-c1: a newer event of the same session replaces the older on the phone", got.CollapseID)
	}
	if got.ApprovalID != "" {
		t.Fatalf("approval id = %q on an agent push", got.ApprovalID)
	}
}

func TestAgentContent_Title(t *testing.T) {
	zh, _ := AgentContent(agentIn("Stop", nil), "zh-TW")
	en, _ := AgentContent(agentIn("Stop", nil), "en")
	if zh.Title != "mlab：dev" || en.Title != "mlab: dev" {
		t.Fatalf("titles = %q / %q", zh.Title, en.Title)
	}
	noHost := agentIn("Stop", nil)
	noHost.HostLabel = ""
	if c, _ := AgentContent(noHost, "en"); c.Title != "dev" {
		t.Fatalf("title without a host label = %q", c.Title)
	}
	noName := agentIn("Stop", nil)
	noName.SessionName = ""
	if c, _ := AgentContent(noName, "en"); c.Title != "mlab: c1" || c.SessionName != "c1" {
		t.Fatalf("a session with no name is titled by its code: %q / %q", c.Title, c.SessionName)
	}
	long := agentIn("Stop", nil)
	long.SessionName = strings.Repeat("長", 300)
	if c, _ := AgentContent(long, "en"); len([]rune(c.Title)) > maxTitleRunes+1 {
		t.Fatalf("title has %d runes", len([]rune(c.Title)))
	}
}

// The lock-screen normalisation of §5.3 applies to agent bodies as to approvals: Markdown stripped, whitespace
// collapsed, cut at 240 runes with an ellipsis.
func TestAgentContent_BodyIsNormalised(t *testing.T) {
	got, _ := AgentContent(agentIn("Stop", map[string]any{"last_assistant_message": "# Title\n\n- one\n- two\n\n[link](http://x)"}), "en")
	if got.Body != "Title one two link" {
		t.Fatalf("body = %q", got.Body)
	}
	long, _ := AgentContent(agentIn("Stop", map[string]any{"last_assistant_message": strings.Repeat("a", 500)}), "en")
	if r := []rune(long.Body); len(r) != maxBodyRunes+1 || r[len(r)-1] != '…' {
		t.Fatalf("body has %d runes", len(r))
	}
	// Non-string detail values are not a body.
	odd, _ := AgentContent(agentIn("Stop", map[string]any{"last_assistant_message": 42}), "en")
	if odd.Body != "Task completed" {
		t.Fatalf("a non-string last_assistant_message became %q", odd.Body)
	}
}

// The payload carries the agent fields and stays under 4 KiB.
func TestAgentContent_PayloadCarriesTheAgentFields(t *testing.T) {
	c, _ := AgentContent(agentIn("StopFailure", map[string]any{"error": "boom"}), "en")
	b, err := c.Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"kind":"agent"`, `"event":"StopFailure"`, `"session_code":"c1"`, `"session_id":"sid-1"`, `"session_title":"dev"`, `"host_id":"host-1"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("payload lacks %s: %s", want, s)
		}
	}
	if strings.Contains(s, "approval_id") {
		t.Fatalf("an agent payload names an approval: %s", s)
	}
}

// WB-3 (plan item 3): a Stop push that has the workbook's line takes its body and puts the thing on the title.

func wbIn(ev string, host string, name string, line *WorkbookLine) AgentInput {
	return AgentInput{HostLabel: host, SessionCode: "c1", SessionID: "sid-1", SessionName: name, EventName: ev,
		Detail: map[string]any{"last_assistant_message": "the whole last message"}, Workbook: line}
}

// Mutation gate: ignore the line, or apply it to a Notification → red.
func TestAgentContent_WorkbookLine(t *testing.T) {
	line := &WorkbookLine{Thing: "推播整合", Push: "PR 已 merge，等部署", ConvKey: "root-1", EntryID: 42}
	c, ok := AgentContent(wbIn("Stop", "mlab", "dev", line), "zh-TW")
	if !ok || c.Body != "PR 已 merge，等部署" || c.Title != "mlab：dev・推播整合" || c.WorkbookConv != "root-1" || c.WorkbookEntry != 42 {
		t.Fatalf("content = %+v", c)
	}
	// no host prefix when the device has none
	if c, _ := AgentContent(wbIn("PdxStop", "", "dev", line), "en"); c.Title != "dev・推播整合" || c.Body != "PR 已 merge，等部署" {
		t.Fatalf("no host: %+v", c)
	}
	// StopFailure takes it too (its entry summarised the failed turn)
	if c, _ := AgentContent(wbIn("StopFailure", "mlab", "dev", line), "en"); c.Body != "PR 已 merge，等部署" || c.WorkbookEntry != 42 {
		t.Fatalf("stop failure: %+v", c)
	}
	// every other event keeps today's text and carries no workbook block
	for _, ev := range []string{"Notification", "PermissionRequest"} {
		c, ok := AgentContent(wbIn(ev, "mlab", "dev", line), "en")
		if !ok || c.Title != "mlab: dev" || c.WorkbookEntry != 0 || c.WorkbookConv != "" || strings.Contains(c.Body, "PR 已") {
			t.Fatalf("%s: %+v", ev, c)
		}
	}
	// absent: today's push
	if c, _ := AgentContent(wbIn("Stop", "mlab", "dev", nil), "en"); c.Title != "mlab: dev" || c.Body != "the whole last message" || c.WorkbookEntry != 0 {
		t.Fatalf("absent: %+v", c)
	}
	// an empty push line falls back to today's body (and the thing is still the subject)
	if c, _ := AgentContent(wbIn("Stop", "mlab", "dev", &WorkbookLine{Thing: "事", Push: "  ", EntryID: 7, ConvKey: "r"}), "en"); c.Body != "the whole last message" || c.Title != "mlab: dev・事" {
		t.Fatalf("empty push: %+v", c)
	}
}

// The title limit cuts the thing first: the session name (and the host) always stay.
// Mutation gate: cut the whole title → the name is lost → red.
func TestAgentContent_WorkbookTitleCutsTheThingFirst(t *testing.T) {
	long := strings.Repeat("長", 200)
	c, _ := AgentContent(wbIn("Stop", "mlab", "dev", &WorkbookLine{Thing: long, Push: "x", ConvKey: "r", EntryID: 1}), "zh-TW")
	if !strings.HasPrefix(c.Title, "mlab：dev・") || len([]rune(c.Title)) > maxTitleRunes+1 { // +1: the ellipsis
		t.Fatalf("title = %q (%d runes)", c.Title, len([]rune(c.Title)))
	}
	if !strings.HasSuffix(c.Title, "…") {
		t.Fatalf("a cut thing ends with an ellipsis: %q", c.Title)
	}
}

func TestContent_PayloadCarriesTheWorkbookBlock(t *testing.T) {
	c := Content{Title: "t", Body: "b", Kind: "agent", SessionCode: "c1", WorkbookConv: "root-1", WorkbookEntry: 42}
	raw, err := c.Payload("h1")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Purdex map[string]any `json:"purdex"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	wb, ok := got.Purdex["workbook"].(map[string]any)
	if !ok || wb["conv_key"] != "root-1" || wb["entry_id"] != float64(42) {
		t.Fatalf("purdex = %v", got.Purdex)
	}
	// without an entry the block is left out
	raw, _ = Content{Title: "t", Body: "b", Kind: "agent"}.Payload("h1")
	if strings.Contains(string(raw), "workbook") {
		t.Fatalf("payload = %s", raw)
	}
}

// The thing is model text on a lock screen: newlines, control and bidi characters are removed like the body's (codex attack).
// Mutation gate: only TrimSpace the thing → red.
func TestAgentContent_WorkbookThingIsSanitised(t *testing.T) {
	line := &WorkbookLine{Thing: "推播\n整合\u202eevil\u200b\u0007", Push: "x", ConvKey: "r", EntryID: 1}
	c, _ := AgentContent(wbIn("Stop", "mlab", "dev", line), "en")
	if strings.ContainsAny(c.Title, "\n\u202e\u200b\u0007") {
		t.Fatalf("title = %q", c.Title)
	}
	if !strings.HasPrefix(c.Title, "mlab: dev・推播") {
		t.Fatalf("title = %q", c.Title)
	}
}
