package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/promptq"
)

// Sending through the session's own mod (interface U3 plan D7): POST …/submit and …/interrupt. Nothing is ever typed
// into the pane; with no mod stream that can take the prompt the answer is 409 no_mod.

// PromptSender is the prompt queue as these routes see it (promptq.Queue).
type PromptSender interface {
	Submit(ctx context.Context, sessionID, clientMsgID, text string) (promptq.Result, error)
	Interrupt(ctx context.Context, sessionID string) (promptq.Result, error)
	HasOwner(sessionID string) bool
	Match(sessionID string, items []promptq.EchoItem) []string
}

// echoIDs pairs the session's user messages with the client_msg_ids of the requests that sent them (U3-2): user item id ->
// client_msg_id. The pairing runs over the whole conversation (not the window or increment being answered), so a request
// already paired with an older message is never handed to a later one with the same text. Only the person's own messages
// (source user) are looked at.
func (m *Module) echoIDs(sessionID string, entry *convfeed.Entry) map[string]string {
	ps := m.promptSender()
	if ps == nil || entry == nil {
		return nil
	}
	msgs := entry.UserMessages()
	if len(msgs) == 0 {
		return nil
	}
	items := make([]promptq.EchoItem, len(msgs))
	for i, u := range msgs {
		items[i] = promptq.EchoItem{Text: u.Text, At: time.UnixMilli(u.At)}
	}
	out := map[string]string{}
	for i, cm := range ps.Match(sessionID, items) {
		if cm != "" {
			out[msgs[i].ID] = cm
		}
	}
	return out
}

const (
	maxPromptBytes   = 4000 // the iOS SendPlan limit
	maxSubmitBody    = 32 << 10
	maxClientMsgIDLn = 128
)

var clientMsgIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// sanitizePrompt applies the iOS SendPlan rules: control characters other than \n are dropped (a tab first becomes four
// spaces), trailing spaces of every line and blank lines at the head and the tail go, and what is left must be 1 … 4000
// UTF-8 bytes. A leading "/" or "!" is a slash command or a shell line: those need the terminal (needs_terminal). code is
// the error code of a refusal.
func sanitizePrompt(s string) (text, code string) {
	if !utf8.ValidString(s) {
		return "", "bad_text"
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == ' ' || r == ' ': // line / paragraph separators read as line breaks
			b.WriteRune('\n')
		case unicode.IsControl(r) || hidden(r):
		default:
			b.WriteRune(r)
		}
	}
	lines := strings.Split(b.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	text = strings.Join(lines, "\n")
	switch {
	case text == "":
		return "", "empty_text"
	case len(text) > maxPromptBytes:
		return "", "too_long"
	case text[0] == '/' || text[0] == '!':
		return "", "needs_terminal"
	}
	return text, ""
}

// hidden: format characters that change how text is shown without being visible - the bidi overrides, embeddings and
// isolates, and the zero-width space, word joiner and byte-order mark. They would let the person's screen and the model's
// input disagree (and hide a leading "/" or "!"). The zero-width joiner and non-joiner stay: emoji sequences and several
// scripts need them.
func hidden(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069: // bidi embeddings, overrides, isolates
		return true
	case r == 0x200E || r == 0x200F || r == 0x061C: // implicit direction marks
		return true
	case r == 0x200B || r == 0x2060 || r == 0xFEFF: // zero-width space, word joiner, BOM
		return true
	}
	return false
}

func (m *Module) promptSender() PromptSender {
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	svc, ok := m.core.Registry.Get(promptq.Key)
	if !ok {
		return nil
	}
	ps, _ := svc.(PromptSender)
	return ps
}

// capabilitiesFor is the conversation's capability table with what the mod channel adds: send and interrupt are `prompt`
// while a mod stream that announced prompt.v1 is live for the session (else they stay not_wired, fail-closed).
func (m *Module) capabilitiesFor(sessionID string) *convmodel.Capabilities {
	c := convmodel.TranscriptCapabilities()
	// a question the agent asks is answered through the approvals channel (the conversation stream's approvals and the decide
	// API with hook answers), whatever the mod does
	c.AnswerQuestion = "approval"
	delete(c.Reasons, "answer_question")
	if ps := m.promptSender(); ps != nil && ps.HasOwner(sessionID) {
		c.Send, c.Interrupt = "prompt", "prompt"
		delete(c.Reasons, "send")
		delete(c.Reasons, "interrupt")
	}
	return &c
}

type submitRequest struct {
	Text        string `json:"text"`
	ClientMsgID string `json:"client_msg_id"`
}

type sendAnswer struct {
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	ClientMsgID string `json:"client_msg_id,omitempty"`
}

// handleSubmit answers POST /api/conversations/{provider}/{session_id}/submit {text, client_msg_id}: 200 {status:
// accepted | dropped | busy | timeout | unknown, reason?, client_msg_id}, 400 (bad_session_id, bad_json, bad_client_msg_id,
// empty_text, too_long, bad_text, needs_terminal), 404 provider_unsupported, 409 no_mod, 429 too_many_pending, 503.
func (m *Module) handleSubmit(w http.ResponseWriter, r *http.Request) {
	sid, ok := m.sendTarget(w, r)
	if !ok {
		return
	}
	var in submitRequest
	if !decodeBody(w, r, &in) {
		return
	}
	if !clientMsgIDRe.MatchString(in.ClientMsgID) {
		writeError(w, http.StatusBadRequest, "bad_client_msg_id")
		return
	}
	text, code := sanitizePrompt(in.Text)
	if code != "" {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	ps := m.promptSender()
	if ps == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	res, err := ps.Submit(r.Context(), sid, in.ClientMsgID, text)
	m.writeSendAnswer(w, res, err, in.ClientMsgID)
}

// handleInterrupt answers POST …/interrupt {}: the same answers (accepted | dropped), no client_msg_id.
func (m *Module) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	sid, ok := m.sendTarget(w, r)
	if !ok {
		return
	}
	var in struct{}
	if !decodeBody(w, r, &in) {
		return
	}
	ps := m.promptSender()
	if ps == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	res, err := ps.Interrupt(r.Context(), sid)
	m.writeSendAnswer(w, res, err, "")
}

func (m *Module) sendTarget(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.PathValue("provider") != "claude" {
		writeError(w, http.StatusNotFound, "provider_unsupported")
		return "", false
	}
	sid := r.PathValue("session_id")
	if !sessionIDRe.MatchString(sid) {
		writeError(w, http.StatusBadRequest, "bad_session_id")
		return "", false
	}
	return sid, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSubmitBody))
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large")
		} else {
			writeError(w, http.StatusBadRequest, "bad_json")
		}
		return false
	}
	var extra json.RawMessage // exactly one JSON value: a second one, or trailing garbage, is a malformed request
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_json")
		return false
	}
	return true
}

func (m *Module) writeSendAnswer(w http.ResponseWriter, res promptq.Result, err error, clientMsgID string) {
	switch {
	case errors.Is(err, promptq.ErrBusy):
		writeError(w, http.StatusTooManyRequests, "too_many_pending")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal")
	case res.Status == promptq.NoMod:
		writeError(w, http.StatusConflict, "no_mod")
	default:
		b, _ := json.Marshal(sendAnswer{Status: res.Status, Reason: res.Reason, ClientMsgID: clientMsgID})
		writeJSON(w, http.StatusOK, b)
	}
}
