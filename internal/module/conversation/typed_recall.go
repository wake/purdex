package conversation

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel/ccnorm"
	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/tmux"
)

// POST /api/sessions/{code}/recall (K-1b; plan I5/I6, spec §2 step 0): take the messages waiting in Claude Code's queue back
// out of the TUI. Up on an EMPTY input pops all queued items joined by one "\n" (each trailing-whitespace trimmed); the daemon
// reads the input through the mod, checks it is exactly that, clears it (clear_if, so only our text) and answers {text}.
// Mod owner required (no keys fallback). Whatever Up left in the input, the input is never left holding it.

type recallRequest struct {
	ClientMsgID          string `json:"client_msg_id"`
	ExpectedTmuxInstance string `json:"expected_tmux_instance"`
}

func (m *Module) handleSessionRecall(w http.ResponseWriter, r *http.Request) {
	td := m.typedDeps()
	if td == nil {
		fail(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	var in recallRequest
	if status, code := decodeBodyErr(w, r, &in); code != "" {
		fail(w, status, code)
		return
	}
	switch {
	case in.ClientMsgID != "" && !clientMsgIDRe.MatchString(in.ClientMsgID):
		fail(w, http.StatusBadRequest, "bad_client_msg_id")
		return
	case !tmux.ValidInstance(in.ExpectedTmuxInstance):
		fail(w, http.StatusBadRequest, "bad_expected_tmux_instance")
		return
	}
	tune := m.tuning()
	if m.recalls.replay(w, in.ClientMsgID, tune.now()) {
		return
	}
	scode := r.PathValue("code")
	info, err := td.sessions.GetSession(scode)
	switch {
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal")
		return
	case info == nil:
		fail(w, http.StatusNotFound, "session_not_found")
		return
	case info.TmuxInstance != in.ExpectedTmuxInstance:
		refuse(w, http.StatusConflict, "generation")
		return
	}
	owner, ok := m.resolveClaudeOwner(w, r, td, scode)
	if !ok {
		return
	}
	release, ok := m.lockPaneAndSession(r.Context(), owner.TmuxPaneID, owner.SessionID, tune.lockWait)
	if !ok {
		refuse(w, http.StatusTooManyRequests, "busy")
		return
	}
	defer release()
	if m.recalls.replay(w, in.ClientMsgID, tune.now()) { // a concurrent request with this id may have finished meanwhile
		return
	}
	again, ok := m.resolveClaudeOwner(w, r, td, scode)
	if !ok {
		return
	}
	if again.SessionID != owner.SessionID || again.TmuxPaneID != owner.TmuxPaneID {
		refuse(w, http.StatusConflict, "owner_changed")
		return
	}
	if !m.paneIsUnique(w, r, td, again) {
		return
	}
	if !td.q.HasOwner(again.SessionID) {
		refuse(w, http.StatusConflict, "no_owner")
		return
	}
	queued, hidden, ok := m.queuedOf(w, r.Context(), again.SessionID)
	if !ok {
		return
	}
	switch {
	case len(queued) == 0 && hidden == 0:
		refuse(w, http.StatusConflict, "nothing_queued")
		return
	case hidden > 0 || !allUser(queued):
		refuse(w, http.StatusConflict, "recall_unsupported") // not measured whether Up pops peer / harness items
		return
	}
	res, err := td.q.Read(r.Context(), again.SessionID)
	if !readOK(w, res, err) {
		return
	}
	if res.Text != "" {
		refuse(w, http.StatusConflict, "input_not_empty") // Up would move the cursor in a draft, not pop the queue
		return
	}
	if err := td.keys.SendKeysIfInstanceAndMode(info.TmuxID, in.ExpectedTmuxInstance, again.TmuxPaneID, tmux.KeyUp); err != nil {
		if reason, ok := guardReason(err); ok {
			refuse(w, http.StatusConflict, reason) // nothing was sent
			return
		}
		fail(w, http.StatusServiceUnavailable, "keys_failed")
		return
	}
	// From here Up has been pressed: whatever happens, a repeat of this id gets the same answer and never presses it again.
	done := func(status int, a typedAnswer) {
		m.recalls.put(in.ClientMsgID, status, a, tune.now())
		writeTyped(w, status, a)
	}
	got := m.readAfterUp(r.Context(), td, again.SessionID, tune)
	want := joinQueued(queued)
	switch {
	case got.err != nil || got.status != promptq.Accepted:
		// The mod could not tell: the input may hold what Up popped. Nothing can be cleared without the text.
		done(http.StatusConflict, typedAnswer{Outcome: "refused", Reason: "read_failed", Error: "read_failed"})
	case got.text == "":
		done(http.StatusConflict, typedAnswer{Outcome: "refused", Reason: "already_sent", Error: "already_sent"})
	case got.text == want:
		a := typedAnswer{Text: want}
		if !clearOurs(td, again.SessionID, want) {
			a.Reason = "input_not_cleared" // the text is returned but still sits in the TUI input
		}
		done(http.StatusOK, a)
	default:
		// The input was empty before Up, so this came from Up: history, or part of a queue that moved. Clear it.
		clearOurs(td, again.SessionID, got.text)
		reason := "recall_mismatch"
		if q2, h2, ok2 := m.queuedNow(r.Context(), again.SessionID); ok2 && len(q2) == 0 && h2 == 0 {
			reason = "already_sent" // the turn took the queue meanwhile and Up recalled history
		}
		done(http.StatusConflict, typedAnswer{Outcome: "refused", Reason: reason, Error: reason})
	}
}

type recallRead struct {
	status, text string
	err          error
}

// readAfterUp reads the input once, and once more after the settle time when it is still empty (the TUI draws what Up
// popped asynchronously).
func (m *Module) readAfterUp(ctx context.Context, td *typedDeps, sid string, tune typedTuning) recallRead {
	var out recallRead
	for i := 0; i < 2; i++ {
		res, err := td.q.Read(ctx, sid)
		out = recallRead{status: res.Status, text: res.Text, err: err}
		if err != nil || res.Status != promptq.Accepted || res.Text != "" {
			return out
		}
		select {
		case <-time.After(tune.recallSettle):
		case <-ctx.Done():
			return out
		}
	}
	return out
}

// readOK answers the refusal itself when the read did not produce the input's text.
func readOK(w http.ResponseWriter, res promptq.Result, err error) bool {
	switch {
	case errors.Is(err, promptq.ErrBusy):
		refuse(w, http.StatusTooManyRequests, "busy")
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal")
	case res.Status == promptq.NoMod:
		refuse(w, http.StatusConflict, "no_owner")
	case res.Status != promptq.Accepted:
		refuse(w, http.StatusConflict, "read_failed")
	default:
		return true
	}
	return false
}

// clearOurs clears the input only if it still holds text (clear_if); reports whether the mod accepted.
func clearOurs(td *typedDeps, sid, text string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	res, err := td.q.ClearIf(ctx, sid, text)
	return err == nil && res.Status == promptq.Accepted
}

func allUser(q []ccnorm.Queued) bool {
	for _, e := range q {
		if e.Source != ccnorm.QueuedUser {
			return false
		}
	}
	return true
}

// joinQueued is what Up pops: the items, each trailing whitespace trimmed, joined by one "\n" (spec §2 step 0).
func joinQueued(q []ccnorm.Queued) string {
	parts := make([]string, len(q))
	for i, e := range q {
		parts[i] = strings.TrimRightFunc(e.Text, unicode.IsSpace)
	}
	return strings.Join(parts, "\n")
}

// queuedNow: the session's CC queue as the conversation feed sees it after a refresh.
func (m *Module) queuedNow(ctx context.Context, sid string) ([]ccnorm.Queued, int, bool) {
	if m.cache == nil || m.resolver == nil {
		return nil, 0, false
	}
	entry, release, err := m.cache.Acquire(ctx, sid)
	if err != nil {
		return nil, 0, false
	}
	defer release()
	if err := entry.Exclusive(ctx, func() error { return m.refresh(ctx, entry, sid) }); err != nil && !errors.Is(err, convfeed.ErrNotFound) {
		return nil, 0, false
	}
	h := entry.Header()
	return h.Queued, h.HiddenQueued, true
}

func (m *Module) queuedOf(w http.ResponseWriter, ctx context.Context, sid string) ([]ccnorm.Queued, int, bool) {
	q, h, ok := m.queuedNow(ctx, sid)
	if !ok {
		fail(w, http.StatusServiceUnavailable, "queue_unavailable")
	}
	return q, h, ok
}

// recallLedger remembers the answer of a recall that pressed Up, by client_msg_id (optional): in memory, 24 h, bounded.
type recallLedger struct {
	mu sync.Mutex
	m  map[string]recallRow
}

type recallRow struct {
	status int
	a      typedAnswer
	at     time.Time
}

const maxRecallLedger = 1024

func (l *recallLedger) replay(w http.ResponseWriter, id string, now time.Time) bool {
	if id == "" {
		return false
	}
	l.mu.Lock()
	row, ok := l.m[id]
	if ok && now.Sub(row.at) > keysTombstoneTTL {
		delete(l.m, id)
		ok = false
	}
	l.mu.Unlock()
	if ok {
		writeTyped(w, row.status, row.a)
	}
	return ok
}

func (l *recallLedger) put(id string, status int, a typedAnswer, now time.Time) {
	if id == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]recallRow{}
	}
	for len(l.m) >= maxRecallLedger {
		var oldest string
		var at time.Time
		for k, v := range l.m {
			if oldest == "" || v.at.Before(at) {
				oldest, at = k, v.at
			}
		}
		delete(l.m, oldest)
	}
	l.m[id] = recallRow{status, a, now}
}
