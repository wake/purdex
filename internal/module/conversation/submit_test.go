package conversation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/promptq"
)

// U3-0b: POST …/submit and …/interrupt (plan D7).

type fakeSender struct {
	res   promptq.Result
	err   error
	owner bool
	// liveOwner is flipped while a WebSocket follows the conversation (another goroutine reads it)
	liveOwner atomic.Bool
	gotSID    string
	gotID     string
	gotText   string
	submits   int
	interrup  int
	echo      map[string]string
	matched   []promptq.EchoItem
}

func (f *fakeSender) Submit(_ context.Context, sid, id, text string) (promptq.Result, error) {
	f.submits++
	f.gotSID, f.gotID, f.gotText = sid, id, text
	return f.res, f.err
}

func (f *fakeSender) Interrupt(_ context.Context, sid string) (promptq.Result, error) {
	f.interrup++
	f.gotSID = sid
	return f.res, f.err
}

func (f *fakeSender) HasOwner(string) bool { return f.owner || f.liveOwner.Load() }

// Match pairs by text through the table echo: text -> client_msg_id.
func (f *fakeSender) Match(_ string, items []promptq.EchoItem) []string {
	f.matched = append(f.matched, items...)
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = f.echo[it.Text]
	}
	return out
}

func (e *env) sender(f *fakeSender) {
	e.mod.core.Registry.Register(promptq.Key, f)
}

func (e *env) post(path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return w
}

func errOf(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var b struct{ Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != status || b.Error != code {
		t.Fatalf("got %d %q, want %d %q (%s)", w.Code, b.Error, status, code, w.Body.String())
	}
}

func TestSanitizePrompt(t *testing.T) {
	long := strings.Repeat("字", 1334) // 4002 bytes
	ok := strings.Repeat("字", 1333)   // 3999 bytes
	for _, c := range []struct {
		in, want, code string
	}{
		{"hello", "hello", ""},
		{"  hello  \n", "  hello", ""}, // trailing spaces go, leading ones stay
		{"\n\nhello\n\n", "hello", ""},
		{"a\tb", "a    b", ""},
		{"a\x00b\x1b[31mc\x07", "ab[31mc", ""}, // control characters but \n dropped
		{"line1\r\nline2", "line1\nline2", ""},
		{"a  \nb   ", "a\nb", ""},
		{"a\n\n\nb", "a\n\n\nb", ""}, // blank lines in the middle stay
		{ok, ok, ""},
		{long, "", "too_long"},
		{"", "", "empty_text"},
		{" \n\t \n", "", "empty_text"},
		{"/clear", "", "needs_terminal"},
		{"!ls", "", "needs_terminal"},
		{"\n  /model", "  /model", ""}, // only the very first character counts, after the trim of blank lines
		{"say /hi", "say /hi", ""},
		{"\xff\xfe", "", "bad_text"},
	} {
		got, code := sanitizePrompt(c.in)
		if got != c.want || code != c.code {
			t.Errorf("%q → %q %q, want %q %q", c.in, got, code, c.want, c.code)
		}
	}
}

// Format characters are built from code points (a literal one in the source is invisible, and a BOM is not even legal).
func r(cp ...rune) string { return string(cp) }

func TestSanitizePrompt_InvisibleFormatCharacters(t *testing.T) {
	zwj := r(0x200d)
	for _, c := range []struct {
		name, in, want, code string
	}{
		{"bidi overrides, embeddings and isolates", "a" + r(0x202e) + "b" + r(0x202a) + "c" + r(0x2066) + "d" + r(0x2069) + "e", "abcde", ""},
		{"zero-width space, word joiner, BOM, direction marks", "a" + r(0x200b) + "b" + r(0x2060) + "c" + r(0xfeff) + "d" + r(0x200e) + "e" + r(0x200f), "abcde", ""},
		{"a hidden prefix cannot hide the slash", r(0x202e) + "/clear", "", "needs_terminal"},
		{"nor the bang", r(0x200b) + "!ls", "", "needs_terminal"},
		{"separators become line breaks", "one" + r(0x2028) + "two" + r(0x2029) + "three", "one\ntwo\nthree", ""},
		{"the joiner of an emoji sequence stays", "👨" + zwj + "👩" + zwj + "👧", "👨" + zwj + "👩" + zwj + "👧", ""},
	} {
		got, code := sanitizePrompt(c.in)
		if got != c.want || code != c.code {
			t.Errorf("%s: %q → %q %q, want %q %q", c.name, c.in, got, code, c.want, c.code)
		}
	}
}

func submitPath(sid string) string { return "/api/conversations/claude/" + sid + "/submit" }

func TestSubmit_ReportsTheModsAnswer(t *testing.T) {
	e := newEnv(t)
	f := &fakeSender{res: promptq.Result{Status: promptq.Accepted}}
	e.sender(f)
	w := e.post(submitPath(sid), `{"text":"  hi there \n","client_msg_id":"cm-1"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"accepted"`) || !strings.Contains(w.Body.String(), `"client_msg_id":"cm-1"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if f.gotSID != sid || f.gotID != "cm-1" || f.gotText != "  hi there" {
		t.Fatalf("queue got %q %q %q", f.gotSID, f.gotID, f.gotText)
	}
	for status, want := range map[string]string{promptq.Busy: `"status":"busy"`, promptq.Timeout: `"status":"timeout"`, promptq.Unknown: `"status":"unknown"`} {
		f.res = promptq.Result{Status: status}
		if w := e.post(submitPath(sid), `{"text":"x","client_msg_id":"cm-2"}`); w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("%s: %d %s", status, w.Code, w.Body.String())
		}
	}
	f.res = promptq.Result{Status: promptq.Dropped, Reason: "session_changed"}
	if w := e.post(submitPath(sid), `{"text":"x","client_msg_id":"cm-3"}`); !strings.Contains(w.Body.String(), `"reason":"session_changed"`) {
		t.Fatalf("dropped: %s", w.Body.String())
	}
}

// No mod stream: 409 no_mod, nothing typed anywhere.
func TestSubmit_NoMod(t *testing.T) {
	e := newEnv(t)
	e.sender(&fakeSender{res: promptq.Result{Status: promptq.NoMod}})
	errOf(t, e.post(submitPath(sid), `{"text":"x","client_msg_id":"cm-1"}`), 409, "no_mod")
}

func TestSubmit_Validation(t *testing.T) {
	e := newEnv(t)
	f := &fakeSender{}
	e.sender(f)
	cases := map[string]struct {
		path, body string
		status     int
		code       string
	}{
		"not json":          {submitPath(sid), `nope`, 400, "bad_json"},
		"two values":        {submitPath(sid), `{"text":"x","client_msg_id":"c"}{"text":"y"}`, 400, "bad_json"},
		"trailing garbage":  {submitPath(sid), `{"text":"x","client_msg_id":"c"} nope`, 400, "bad_json"},
		"no client id":      {submitPath(sid), `{"text":"x"}`, 400, "bad_client_msg_id"},
		"bad client id":     {submitPath(sid), `{"text":"x","client_msg_id":"has space"}`, 400, "bad_client_msg_id"},
		"long client id":    {submitPath(sid), `{"text":"x","client_msg_id":"` + strings.Repeat("a", 129) + `"}`, 400, "bad_client_msg_id"},
		"empty text":        {submitPath(sid), `{"text":" \n ","client_msg_id":"c"}`, 400, "empty_text"},
		"too long":          {submitPath(sid), `{"text":"` + strings.Repeat("a", 4001) + `","client_msg_id":"c"}`, 400, "too_long"},
		"slash":             {submitPath(sid), `{"text":"/clear","client_msg_id":"c"}`, 400, "needs_terminal"},
		"shell":             {submitPath(sid), `{"text":"!ls","client_msg_id":"c"}`, 400, "needs_terminal"},
		"bad session":       {submitPath("nope"), `{"text":"x","client_msg_id":"c"}`, 400, "bad_session_id"},
		"other provider":    {"/api/conversations/codex/" + sid + "/submit", `{"text":"x","client_msg_id":"c"}`, 404, "provider_unsupported"},
		"body over the cap": {submitPath(sid), `{"text":"` + strings.Repeat("a", 40<<10) + `","client_msg_id":"c"}`, 413, "too_large"},
	}
	for name, c := range cases {
		w := e.post(c.path, c.body)
		var b struct{ Error string }
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		if w.Code != c.status || b.Error != c.code {
			t.Errorf("%s: got %d %q, want %d %q", name, w.Code, b.Error, c.status, c.code)
		}
	}
	if f.submits != 0 {
		t.Fatalf("an invalid request reached the queue (%d)", f.submits)
	}
}

func TestSubmit_QueueFullAndMissing(t *testing.T) {
	e := newEnv(t)
	errOf(t, e.post(submitPath(sid), `{"text":"x","client_msg_id":"c"}`), 503, "unavailable") // no queue registered
	e.sender(&fakeSender{err: promptq.ErrBusy})
	errOf(t, e.post(submitPath(sid), `{"text":"x","client_msg_id":"c"}`), 429, "too_many_pending")
}

func TestInterrupt(t *testing.T) {
	e := newEnv(t)
	f := &fakeSender{res: promptq.Result{Status: promptq.Accepted}}
	e.sender(f)
	w := e.post("/api/conversations/claude/"+sid+"/interrupt", `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"accepted"`) || f.interrup != 1 || f.gotSID != sid {
		t.Fatalf("%d %s %+v", w.Code, w.Body.String(), f)
	}
	f.res = promptq.Result{Status: promptq.Dropped, Reason: "not_running"}
	if w := e.post("/api/conversations/claude/"+sid+"/interrupt", `{}`); !strings.Contains(w.Body.String(), `"reason":"not_running"`) {
		t.Fatalf("dropped: %s", w.Body.String())
	}
	f.res = promptq.Result{Status: promptq.NoMod}
	errOf(t, e.post("/api/conversations/claude/"+sid+"/interrupt", `{}`), 409, "no_mod")
	errOf(t, e.post("/api/conversations/claude/nope/interrupt", `{}`), 400, "bad_session_id")
}

// The conversation's capabilities say `prompt` for send and interrupt while a prompt.v1 stream is live, else stay
// not_wired. Mutation gate: always prompt → red.
func TestCapabilities_SendFollowsTheOwner(t *testing.T) {
	e := newEnv(t)
	f := &fakeSender{}
	e.sender(f)
	c := e.mod.capabilitiesFor(sid)
	if c.Send != "" || c.Interrupt != "" || c.Reasons["send"] != "not_wired" || c.Reasons["interrupt"] != "not_wired" {
		t.Fatalf("without an owner: %+v", c)
	}
	f.owner = true
	c = e.mod.capabilitiesFor(sid)
	if c.Send != "prompt" || c.Interrupt != "prompt" || c.Reasons["send"] != "" || c.Reasons["interrupt"] != "" {
		t.Fatalf("with an owner: %+v", c)
	}
	if c.Steer != "" || c.Reasons["steer"] != "not_wired" {
		t.Fatalf("steer must stay not wired: %+v", c)
	}
}

// ---- U3-2: the client_msg_id on the user item ----

type echoSnap struct {
	Conversation struct {
		Turns []struct {
			Items []struct {
				Type        string `json:"type"`
				Source      string `json:"source"`
				Text        string `json:"text"`
				ClientMsgID string `json:"client_msg_id"`
			} `json:"items"`
		} `json:"turns"`
	} `json:"conversation"`
}

func echoOfSnapshot(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	var s echoSnap
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tr := range s.Conversation.Turns {
		for _, it := range tr.Items {
			if it.Type == "user" {
				out = append(out, it.Text+"="+it.ClientMsgID)
			}
		}
	}
	return out
}

// A user item the daemon sent through the mod names its client_msg_id; others, and the cached item itself, do not.
// Mutation gate: drop the echo from the encoder → red.
func TestEcho_UserItemCarriesTheClientMsgID(t *testing.T) {
	e := newEnv(t)
	e.transcript(userRow("u1", 1, "typed in the terminal") + "\n" + assistantRow("a1", 2, obj{"type": "text", "text": "ok"}) + "\n" +
		userRow("u2", 3, "sent from the app") + "\n" + assistantRow("a2", 4, obj{"type": "text", "text": "done"}) + "\n")
	f := &fakeSender{echo: map[string]string{"sent from the app": "cm-77"}}
	e.sender(f)
	got := echoOfSnapshot(t, e.get("/api/conversations/claude/"+sid))
	if len(got) != 2 || got[0] != "typed in the terminal=" || got[1] != "sent from the app=cm-77" {
		t.Fatalf("items = %v", got)
	}
	if len(f.matched) != 2 || f.matched[1].At.UnixMilli() != 1791378003000 {
		t.Fatalf("the pairing was asked with %+v", f.matched)
	}
	// the cache is untouched: with no pairing a second answer carries no id
	f.echo = nil
	got = echoOfSnapshot(t, e.get("/api/conversations/claude/"+sid))
	if got[1] != "sent from the app=" {
		t.Fatalf("the id stuck to the cached item: %v", got)
	}
}

// ---- U3-2: capabilities that move while a conversation is open ----

// A mod coming or going is a conversation.capabilities frame (not only on the next fetch); nothing is sent while nothing
// changes. Mutation gate: never push → red.
func TestWS_CapabilitiesFrameFollowsTheOwner(t *testing.T) {
	e, _ := wsEnv(t)
	e.transcript(idleTurns(1))
	f := &fakeSender{}
	e.sender(f)
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	// Nothing changed yet; a stray frame would be what expect() reads next and fail on its type.
	time.Sleep(150 * time.Millisecond)
	f.liveOwner.Store(true) // the mod announces prompt.v1
	fr := c.expect("conversation.capabilities")
	var v struct {
		Capabilities struct {
			Send           string `json:"send"`
			Interrupt      string `json:"interrupt"`
			AnswerQuestion string `json:"answer_question"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(fr.Value, &v); err != nil || v.Capabilities.Send != "prompt" || v.Capabilities.Interrupt != "prompt" || v.Capabilities.AnswerQuestion != "approval" {
		t.Fatalf("frame %v: %s", err, fr.Value)
	}
	f.liveOwner.Store(false) // the mod goes away
	fr = c.expect("conversation.capabilities")
	v.Capabilities.Send, v.Capabilities.Interrupt = "", "" // omitted when empty: Unmarshal would keep the old value
	if err := json.Unmarshal(fr.Value, &v); err != nil || v.Capabilities.Send != "" || v.Capabilities.AnswerQuestion != "approval" {
		t.Fatalf("frame after the mod left %v: %s", err, fr.Value)
	}
}

// A question is answered through the approvals channel whatever the mod does: answer_question says so.
func TestCapabilities_AnswerQuestionIsApproval(t *testing.T) {
	e := newEnv(t)
	e.sender(&fakeSender{})
	c := e.mod.capabilitiesFor(sid)
	if c.AnswerQuestion != "approval" || c.Reasons["answer_question"] != "" {
		t.Fatalf("capabilities = %+v", c)
	}
	if c.AnswerPermission != "" || c.Reasons["answer_permission"] != "not_wired" {
		t.Fatalf("answer_permission must stay as it was: %+v", c)
	}
}

// The increments (HTTP ?after= and the WebSocket's changes frames) name it too. Mutation gate: skip the echo in encodeIncrement → red.
func TestEcho_IncrementsCarryTheClientMsgID(t *testing.T) {
	e := newEnv(t)
	e.sender(&fakeSender{echo: map[string]string{"from the app": "cm-9"}})
	inc := convfeed.Increment{Changes: []convfeed.TurnChange{{
		Turn:    convmodel.Turn{ID: "t1"},
		Items:   []convmodel.Item{{Type: convmodel.ItemUser, User: &convmodel.UserMessage{ID: "u1", At: 1791378001000, Text: "from the app", Source: convmodel.SourceUser}}},
		Indexes: []int{0},
	}}}
	body, ok := e.mod.encodeIncrement(inc, sid, "h", 0)
	if !ok || !strings.Contains(string(body), `"client_msg_id":"cm-9"`) {
		t.Fatalf("increment = %s", body)
	}
}

// Without a queue the snapshot is as before.
func TestEcho_NoQueueNoIDs(t *testing.T) {
	e := newEnv(t)
	e.transcript(userRow("u1", 1, "hello") + "\n")
	if got := echoOfSnapshot(t, e.get("/api/conversations/claude/"+sid)); len(got) != 1 || got[0] != "hello=" {
		t.Fatalf("items = %v", got)
	}
}
