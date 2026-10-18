package conversation

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel/ccnorm"
	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/tmux"
)

// POST /api/sessions/{code}/recall (K-1b; plan I5/I6, spec §2 step 0): take the messages waiting in Claude Code's queue back
// out of the TUI. Up on an EMPTY input pops all queued items joined by one "\n" (each trailing-whitespace trimmed). The check,
// Up and the read are not atomic, so a person typing meanwhile is possible: only a read that EQUALS that join is ours; it is
// cleared with clear_if(that text) and answered 200 {text}. Any other non-empty read (a draft, history, a cut text) is
// 409 recall_mismatch with no clear and no text: the terminal is left as it is. After Up the reads and the clear run on their
// own deadline (a client disconnect does not stop them); nothing read within the budget is outcome unknown (recall_empty).
// client_msg_id is required (400 bad_client_msg_id otherwise). Every answer after Up is recorded under the key session code +
// tmux instance + client_msg_id (typed_recall_ledger.go) and replayed before the owner is resolved: at most one Up per id.
// Mod owner required (no keys fallback).

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
	case !clientMsgIDRe.MatchString(in.ClientMsgID):
		fail(w, http.StatusBadRequest, "bad_client_msg_id")
		return
	case !tmux.ValidInstance(in.ExpectedTmuxInstance):
		fail(w, http.StatusBadRequest, "bad_expected_tmux_instance")
		return
	}
	tune := m.tuning()
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
	key := recallKey(scode, in.ExpectedTmuxInstance, in.ClientMsgID)
	if m.recalls.replay(w, key, tune.now()) { // before the owner is resolved: a text already taken out must reach the client
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
	if m.recalls.replay(w, key, tune.now()) { // a concurrent request with this id may have finished meanwhile
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
	want := joinQueued(queued)
	if len(want) > promptq.MaxTextBytes || anyAtFeedCap(queued) {
		refuse(w, http.StatusConflict, "recall_too_long") // the read (or the feed's text) would be cut: it could never match
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
		m.recalls.put(key, status, a, tune.now())
		if status == http.StatusOK && a.Outcome == "" && a.Text != "" { // the text came back: Claude Code's whole queue is gone
			m.stages.recallAll(again.SessionID, tune.now())
		}
		writeTyped(w, status, a)
	}
	unknown := func(reason string) { done(http.StatusOK, typedAnswer{Outcome: "unknown", Reason: reason}) }
	// The reads are detached from the request: a client that went away must not leave a popped text in the input unjudged.
	rctx, cancel := context.WithTimeout(context.Background(), tune.recallAfterUp)
	defer cancel()
	got := readAfterUp(rctx, td, again.SessionID, tune.recallSettle, tune.recallReadBudget)
	switch {
	case got.err != nil || got.status != promptq.Accepted:
		unknown("read_failed")
	case got.text == "":
		unknown("recall_empty") // the turn may have taken the queue, or the TUI may still draw it
	case got.truncated || got.text != want:
		// Not provably what Up popped (a draft typed meanwhile, history, a cut text): touch nothing, return nothing.
		done(http.StatusConflict, typedAnswer{Outcome: "refused", Reason: "recall_mismatch", Error: "recall_mismatch"})
	default:
		recallClear(done, unknown, td, again.SessionID, want)
	}
}

// recallClear clears the exact text Up popped and answers; a refused clear is judged by one more read.
func recallClear(done func(int, typedAnswer), unknown func(string), td *typedDeps, sid, want string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if res, err := td.q.ClearIf(ctx, sid, want); err == nil && res.Status == promptq.Accepted {
		done(http.StatusOK, typedAnswer{Text: want})
		return
	}
	again := readOnce(ctx, td, sid)
	switch {
	case again.err != nil || again.status != promptq.Accepted:
		unknown("clear_unknown")
	case again.text == "":
		done(http.StatusOK, typedAnswer{Text: want}) // gone anyway
	case again.text == want && !again.truncated:
		done(http.StatusConflict, typedAnswer{Outcome: "refused", Reason: "input_not_cleared", Error: "input_not_cleared"})
	default:
		unknown("clear_unknown")
	}
}

type recallRead struct {
	status, text string
	truncated    bool
	err          error
}

func readOnce(ctx context.Context, td *typedDeps, sid string) recallRead {
	res, err := td.q.Read(ctx, sid)
	return recallRead{status: res.Status, text: res.Text, truncated: res.Truncated, err: err}
}

// readAfterUp polls the input every settle until it holds text or the budget has elapsed (the TUI draws what Up popped
// asynchronously). The last read happens at or after the budget, never before a wait that ends the loop.
func readAfterUp(ctx context.Context, td *typedDeps, sid string, settle, budget time.Duration) recallRead {
	start := time.Now()
	for {
		out := readOnce(ctx, td, sid)
		if out.err != nil || out.status != promptq.Accepted || out.text != "" || time.Since(start) >= budget {
			return out
		}
		select {
		case <-time.After(settle):
		case <-ctx.Done():
			out.err = ctx.Err()
			return out
		}
	}
}

// anyAtFeedCap: the conversation feed keeps at most ccnorm.MaxQueuedText runes of a queued text, so an item at the cap may
// have been cut and the join cannot be trusted.
func anyAtFeedCap(q []ccnorm.Queued) bool {
	for _, e := range q {
		if utf8.RuneCountInString(e.Text) >= ccnorm.MaxQueuedText {
			return true
		}
	}
	return false
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
