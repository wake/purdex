package push

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalise(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":       {"hello world", "hello world"},
		"bold":        {"this is **bold** and __also__", "this is bold and also"},
		"code":        {"run `go test` now", "run go test now"},
		"fenced":      {"```\ncode\n```", "code"},
		"heading":     {"# Title\ntext", "Title text"},
		"deep head":   {"### Deep", "Deep"},
		"quote":       {"> quoted\nnext", "quoted next"},
		"bullets":     {"- a\n* b\n+ c\n1. d", "a b c d"},
		"link":        {"see [the docs](https://x.example/y?z=1) now", "see the docs now"},
		"image":       {"![alt](http://x/y.png) after", "alt after"},
		"whitespace":  {"  a \t b \n\n c  ", "a b c"},
		"empty":       {" \n ", ""},
		"cjk":         {"**完成**：已修好 `bug`", "完成：已修好 bug"},
		"stray stars": {"2 * 3 = 6", "2 * 3 = 6"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Normalise(tc.in, 240); got != tc.want {
				t.Fatalf("Normalise(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalise_CutsAtARuneBoundaryWithAnEllipsis(t *testing.T) {
	in := strings.Repeat("字", 300)
	got := Normalise(in, 240)
	if utf8.RuneCountInString(got) != 241 || !strings.HasSuffix(got, "…") {
		t.Fatalf("len %d runes, tail %q", utf8.RuneCountInString(got), got[len(got)-3:])
	}
	if !utf8.ValidString(got) {
		t.Fatal("cut in the middle of a rune")
	}
	if got := Normalise(strings.Repeat("a", 240), 240); strings.HasSuffix(got, "…") {
		t.Fatal("a text exactly at the limit must not be cut")
	}
}

func approvalOpened(kind string, payload map[string]any, originTitle, originName string) Approval {
	raw, _ := json.Marshal(payload)
	return Approval{ID: "ap1", Kind: kind, Payload: raw, Origin: ApprovalOrigin{Title: originTitle, Name: originName, Ref: "_abc123"}}
}

func TestApprovalContent_ThreeKindsInBothLocales(t *testing.T) {
	lead := approvalOpened("lead", map[string]any{"reason": "need **a** team"}, "My Lead", "lead-1")
	c, ok := ApprovalContent(lead, "mlab", "zh-TW")
	if !ok || c.Title != "mlab：My Lead 申請成為 lead" || c.Body != "need a team" || c.Kind != "lead" || c.CollapseID != "ap1" {
		t.Fatalf("lead zh-TW = %+v ok %v", c, ok)
	}
	c, _ = ApprovalContent(lead, "mlab", "en")
	if c.Title != "mlab: My Lead requests to become lead" {
		t.Fatalf("lead en title = %q", c.Title)
	}

	relay := approvalOpened("self_relay", map[string]any{"used_percentage": 71.6}, "", "worker-7")
	c, ok = ApprovalContent(relay, "mlab", "zh-TW")
	if !ok || c.Title != "mlab：worker-7 申請接力（已用 72%）" || !strings.Contains(c.Body, "接力檔") || c.Kind != "self_relay" {
		t.Fatalf("self_relay zh-TW = %+v", c)
	}
	c, _ = ApprovalContent(relay, "mlab", "en")
	if c.Title != "mlab: worker-7 requests a relay (72% used)" || !strings.Contains(c.Body, "relay file") {
		t.Fatalf("self_relay en = %+v", c)
	}

	ask := approvalOpened("hook_ask", map[string]any{"questions": []any{map[string]any{"question": "Which `branch`?"}, map[string]any{"question": "second"}}}, "", "")
	c, ok = ApprovalContent(ask, "mlab", "zh-TW")
	if !ok || c.Title != "mlab：_abc123 在等你回答" || c.Body != "Which branch?" || c.Kind != "hook_ask" {
		t.Fatalf("hook_ask zh-TW = %+v (origin falls back title > name > ref)", c)
	}
	c, _ = ApprovalContent(ask, "mlab", "en")
	if c.Title != "mlab: _abc123 is waiting for your answer" {
		t.Fatalf("hook_ask en = %q", c.Title)
	}
}

// RQ-2: a lead's member relay that waits for a person (the lead's member pool is out).
func TestApprovalContent_MemberRelayInBothLocales(t *testing.T) {
	a := approvalOpened("member_relay", map[string]any{
		"op_id": "o", "team_id": "t", "lead_title": "iface-lead", "member_title": "iface-solo", "member_ref": "_bbbbbb", "used_percentage": 72.6,
	}, "origin-title", "origin-name")
	c, ok := ApprovalContent(a, "mlab", "zh-TW")
	if !ok || c.Title != "mlab：iface-lead 要幫 member iface-solo 接力（context 73%）" || c.Body != "member 額度用完，要核准嗎？" || c.Kind != "member_relay" || c.CollapseID != "ap1" || c.ApprovalID != "ap1" {
		t.Fatalf("member_relay zh-TW = %+v ok %v", c, ok)
	}
	c, _ = ApprovalContent(a, "mlab", "en")
	if c.Title != "mlab: iface-lead wants to relay member iface-solo (context 73%)" || c.Body != "The member quota is used up. Approve?" {
		t.Fatalf("member_relay en = %+v", c)
	}
}

// The lead falls back to the origin's own label, the member to its ref; titles written by a session are normalised and cut.
func TestApprovalContent_MemberRelayNamesFallBackAndAreCleaned(t *testing.T) {
	a := approvalOpened("member_relay", map[string]any{"lead_title": "  ", "member_title": "", "member_ref": "_bbbbbb", "used_percentage": 0}, "", "worker-7")
	c, ok := ApprovalContent(a, "mlab", "en")
	if !ok || c.Title != "mlab: worker-7 wants to relay member _bbbbbb (context 0%)" {
		t.Fatalf("fallbacks = %+v", c)
	}
	messy := approvalOpened("member_relay", map[string]any{
		"lead_title": "**bold**\n# head " + strings.Repeat("L", 200), "member_title": "`code`\r\n" + strings.Repeat("M", 200), "used_percentage": 5,
	}, "", "x")
	c, _ = ApprovalContent(messy, "mlab", "en")
	if strings.ContainsAny(c.Title, "\n\r*`#") {
		t.Fatalf("markup or a newline reached the title: %q", c.Title)
	}
	if n := len([]rune(c.Title)); n > maxTitleRunes+1 {
		t.Fatalf("title has %d runes", n)
	}
	// A payload that is not the wire shape still gives a card, not a panic.
	bad := Approval{ID: "ap2", Kind: "member_relay", Payload: json.RawMessage(`"not an object"`), Origin: ApprovalOrigin{Name: "n"}}
	if c, ok := ApprovalContent(bad, "mlab", "en"); !ok || !strings.Contains(c.Title, "wants to relay member") {
		t.Fatalf("bad payload = %+v ok %v", c, ok)
	}
}

// Titles are written by sessions: direction and zero-width marks and control characters never reach a lock screen, and a
// title made only of them is empty (the fallback), not a way to blank the identity. Mutation gate: Normalise keeps them → red.
func TestNormalise_DropsControlAndFormatCharacters(t *testing.T) {
	cases := map[string]string{
		"a\u202eb\u2066c\u2069d":   "abcd", // RLO, LRI, PDI
		"a\u200bb\u200dc\ufeffd":   "abcd", // zero width space, joiner, BOM
		"a\x00b\x07c":              "abc",
		"line one\u2028line two":   "line one line two", // a separator is a space, not a deletion
		"para\u2029two\u00a0three": "para two three",
		"tab\tand\nnewline":        "tab and newline",
		"\u202e":                   "",
		"\u202e \u200b":            "",
		"接力\u202e中":                "接力中",
	}
	for in, want := range cases {
		if got := Normalise(in, 100); got != want {
			t.Errorf("Normalise(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestApprovalContent_MemberRelayAliasesMadeOfControlsFallBackToTrustedNames(t *testing.T) {
	a := approvalOpened("member_relay", map[string]any{"lead_title": "\u202e\u200b", "member_title": "\u2066", "member_ref": "_bbbbbb", "used_percentage": 1}, "", "worker-7")
	c, ok := ApprovalContent(a, "mlab", "en")
	if !ok || c.Title != "mlab: worker-7 wants to relay member _bbbbbb (context 1%)" {
		t.Fatalf("title = %q ok %v", c.Title, ok)
	}
	// The origin's own label is session-written too: cleaned and cut like the payload's titles; a label that is nothing
	// but controls falls through to the ref the daemon made.
	evil := approvalOpened("member_relay", map[string]any{"lead_title": "", "member_title": "m", "used_percentage": 1}, "\u202e"+strings.Repeat("X", 200), "")
	c, _ = ApprovalContent(evil, "mlab", "en")
	if strings.ContainsRune(c.Title, '\u202e') || len([]rune(c.Title)) > maxTitleRunes+1 {
		t.Fatalf("title = %q", c.Title)
	}
	onlyControls := approvalOpened("member_relay", map[string]any{"member_title": "m", "used_percentage": 1}, "\u202e", "\u200b")
	c, _ = ApprovalContent(onlyControls, "mlab", "en")
	if c.Title != "mlab: _abc123 wants to relay member m (context 1%)" {
		t.Fatalf("title = %q", c.Title)
	}
}

func TestApprovalContent_OtherKindsAndTerminalOnlyAreNotPushed(t *testing.T) {
	for _, kind := range []string{"hook_permission", "adopt", "unknown"} {
		if _, ok := ApprovalContent(approvalOpened(kind, nil, "t", "n"), "mlab", "en"); ok {
			t.Fatalf("%s must not be pushed", kind)
		}
	}
	terminal := approvalOpened("hook_ask", map[string]any{"terminal_only": true, "questions": []any{map[string]any{"question": "q"}}}, "t", "n")
	if _, ok := ApprovalContent(terminal, "mlab", "en"); ok {
		t.Fatal("a terminal-only hook_ask must not be pushed (the phone cannot answer it)")
	}
}

func TestApprovalContent_TheTitleIsCutAndThePayloadStaysUnder4KiB(t *testing.T) {
	long := strings.Repeat("字", 400)
	lead := approvalOpened("lead", map[string]any{"reason": strings.Repeat("理由", 3000)}, long, "")
	c, ok := ApprovalContent(lead, "mlab", "zh-TW")
	if !ok || utf8.RuneCountInString(c.Title) > 120+1 {
		t.Fatalf("title %d runes", utf8.RuneCountInString(c.Title))
	}
	raw, err := c.Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 4096 {
		t.Fatalf("payload %d bytes", len(raw))
	}
}

func TestPayload_Shape(t *testing.T) {
	c := Content{Title: "T", Body: "B", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1"}
	raw, err := c.Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Aps struct {
			Alert             struct{ Title, Body string }
			Sound             string
			ThreadID          string `json:"thread-id"`
			InterruptionLevel string `json:"interruption-level"`
		}
		Purdex map[string]string
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Aps.Alert.Title != "T" || p.Aps.Alert.Body != "B" || p.Aps.Sound != "default" || p.Aps.ThreadID != "host-1" || p.Aps.InterruptionLevel != "time-sensitive" {
		t.Fatalf("aps = %+v", p.Aps)
	}
	if p.Purdex["host_id"] != "host-1" || p.Purdex["kind"] != "lead" || p.Purdex["approval_id"] != "ap1" {
		t.Fatalf("purdex = %v", p.Purdex)
	}
	if _, has := p.Purdex["session_code"]; has {
		t.Fatalf("empty fields are left out: %v", p.Purdex)
	}
}

// A payload whose fixed fields alone are over the limit is refused locally, not sent to be refused by APNs.
func TestPayload_FixedFieldsOverTheLimitAreAnError(t *testing.T) {
	c := Content{Title: "T", Body: "b", Kind: "lead", ApprovalID: strings.Repeat("x", 5000), CollapseID: "c"}
	if raw, err := c.Payload("host-1"); err == nil {
		t.Fatalf("a %d byte payload came back without an error", len(raw))
	}
	big := Content{Title: "T", Body: strings.Repeat("字", 400), Kind: "lead", ApprovalID: "ap1"}
	raw, err := big.Payload(strings.Repeat("h", 100))
	if err != nil || len(raw) > 4096 {
		t.Fatalf("a long body must be cut to fit: %d bytes, %v", len(raw), err)
	}
}
