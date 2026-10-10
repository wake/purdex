package conversation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/promptq"
)

// U3-0b: POST …/submit and …/interrupt (plan D7).

type fakeSender struct {
	res      promptq.Result
	err      error
	owner    bool
	gotSID   string
	gotID    string
	gotText  string
	submits  int
	interrup int
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

func (f *fakeSender) HasOwner(string) bool { return f.owner }

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
