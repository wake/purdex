package nex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// listSlotWaitDefault bounds how long a list page waits for the read
	// slot before giving up on it (§3.8 "Bounds"): 503 nex_busy for a
	// client that opted in, an unstamped page for one that did not (see
	// handleListExecutions). Every measured page holds the slot for well
	// under it (§3.8's table), so giving up means something held the slot
	// pathologically long — and the client either retries the same page
	// with backoff (§8 R3-3) or gets it unversioned, rather than hanging on
	// it.
	listSlotWaitDefault = 2 * time.Second

	// listRetryParam is the query parameter a client sends, as
	// listRetryParam=listRetryValue, to say it retries 503 nex_busy (§8
	// R3-3). It is the module's, never the engine's: the wrapper removes it
	// before the engine sees the request.
	listRetryParam = "pdx"
	listRetryValue = "retry"

	// listBodyLimit caps one buffered list page. A 500-row page is a few
	// hundred KiB; 32 MiB is far past any real page, so the cap only stops
	// a runaway response from being held in memory whole.
	listBodyLimit = 32 << 20
)

// errListNotStampable is the list read's failure for an answer that is not
// a 200 JSON object: the read consumes no ver, and the answer goes to the
// client exactly as the engine wrote it.
var errListNotStampable = errors.New("nex list answer is not a stampable page")

// errListPanicked is the list read's failure when the engine's handler
// panicked, whatever it had written by then: the read consumes no ver, and
// the client gets 500 nex_list_panicked instead of the buffered answer.
var errListPanicked = errors.New("nex list engine handler panicked")

// listSlotWait is the configured wait (the listWait test seam), or the
// default.
func (m *Module) listSlotWait() time.Duration {
	if m.listWait > 0 {
		return m.listWait
	}
	return listSlotWaitDefault
}

// reads returns the module's read slot (readslot.go), building it on first
// use: New's Modules and the struct-literal Modules some tests build alike.
// In production the first use is RegisterRoutes, before any request. The
// slot's log lines go through m.logf as it is when they are written, so a
// test that swaps logf after New still captures them.
func (m *Module) reads() *readSlot {
	m.slotOnce.Do(func() {
		m.slot = newReadSlot(func(format string, args ...any) { m.logf(format, args...) })
	})
	return m.slot
}

// handleListExecutions wraps the engine's GET /v1/executions (spec
// 2026-10-08 §3.4). handler is the engine's own handler (engine.handler,
// unprefixed); every page runs through runList, which mounts it the way
// RegisterRoutes does (RoutePrefix stripped, recoverer inside), so the
// engine sees exactly the request it would have seen unwrapped — same path,
// the full query string (limit, cursor, include_archived, state,
// label.<k>, session_id) less the module's own pdx parameter, the client's
// headers and principal, the request's context.
//
// The page runs inside the read slot (who "list"), into a buffer capped at
// listBodyLimit. Only a 200 whose body is a JSON object, from a handler
// that did not panic, is a successful read: it consumes a ver, and the
// client gets the page with one more top-level key,
//
//	"pdx": {"epoch": E, "ver": V, "bseq": H}
//
// all three taken inside the slot (§8 R3-1: H, the broadcast high-water
// mark, tells the SPA which deltas were enqueued before this page was
// read). items and next_cursor are untouched. The version travels in the
// body because the SPA calls the daemon cross-origin and a custom response
// header would be unreadable to it (§1 F7).
//
// Everything else consumes nothing:
//   - a handler that panicked is 500 nex_list_panicked, whatever it had
//     written before (a complete 200 object included — see runList);
//   - any other answer (a 400 bad_state, a 500, a non-object 200) goes out
//     as the engine wrote it: status, headers, body, no pdx;
//   - a page over listBodyLimit is 502 nex_list_too_large;
//   - a slot not acquired within listSlotWait: see below;
//   - a request whose context ended while waiting gets no answer at all,
//     opted in or not — the client is gone;
//   - http.ErrAbortHandler gets none either: it propagates to net/http,
//     which aborts the response (see runList).
//
// A slot not acquired in time is 503 nex_busy, in Nexen's error shape so
// the SPA's NexApiError carries the code (§8 R3-3) — but only for a client
// that sent pdx=retry. The SPA in production today has no retry for a 503:
// its walk would go straight to error. Until #1866 PR2a ships the retry,
// and the opt-in with it, a client that did not opt in gets the page
// instead, run through the same contained mount (runList) but outside the
// slot: as the engine wrote it, no pdx, no ver consumed, and one log line.
// That is the unversioned page such a client has always read — and the one
// §4.3 has a delta-aware SPA treat as unversioned — so it loses nothing it
// relies on. A panic there is still 500 nex_list_panicked, and
// http.ErrAbortHandler still propagates. Either way the engine never sees
// the pdx parameter (retryOptIn).
//
// The slot is released in a defer inside readSlot.read, so a page that
// failed, was cancelled mid-read (Nexen's handler sees the same context) or
// panicked frees it at once.
func (m *Module) handleListExecutions(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, retries := retryOptIn(r)
		var res *bufferedResponse // non-nil once the page ran inside the slot
		var panicked bool
		var page map[string]json.RawMessage
		waitStart := time.Now()
		stamp, err := m.reads().read(r.Context(), "list", m.listSlotWait(), func(ctx context.Context) error {
			res, panicked = m.runList(handler, r.WithContext(ctx))
			if panicked {
				return errListPanicked
			}
			if res.overflow || res.code() != http.StatusOK {
				return errListNotStampable
			}
			obj, err := decodeObject(res.body.Bytes())
			if err != nil {
				return errListNotStampable
			}
			page = obj
			return nil
		})

		switch {
		case res == nil && errors.Is(err, errSlotBusy) && retries:
			writeNexError(w, http.StatusServiceUnavailable, "nex list busy", "nex_busy")
		case res == nil && errors.Is(err, errSlotBusy):
			m.logf("nex-delta: list page served unstamped after %dms slot wait", time.Since(waitStart).Milliseconds())
			res, panicked = m.runList(handler, r)
			writeUnstampedList(w, res, panicked)
		case res == nil:
			// The request's context ended while the page waited for the
			// slot: nobody is left to answer.
		case err != nil:
			writeUnstampedList(w, res, panicked)
		default:
			m.writeStampedPage(w, res, page, stamp)
		}
	})
}

// writeUnstampedList answers a page that is not stamped — a failed read, or
// one served outside the slot: a panic is 500 nex_list_panicked and an
// oversize page 502 nex_list_too_large, neither ever showing what was
// buffered; anything else goes out exactly as the engine wrote it.
func writeUnstampedList(w http.ResponseWriter, res *bufferedResponse, panicked bool) {
	switch {
	case panicked:
		writeNexError(w, http.StatusInternalServerError, "nex list failed", "nex_list_panicked")
	case res.overflow:
		writeNexError(w, http.StatusBadGateway,
			fmt.Sprintf("nex list page exceeds %d bytes", listBodyLimit), "nex_list_too_large")
	default:
		res.writeTo(w, res.body.Bytes())
	}
}

// retryOptIn reports whether r opted into 503 nex_busy (pdx=retry) and
// returns the request to hand the engine: r itself when the query has no
// pdx parameter, otherwise a shallow copy (as http.StripPrefix makes) whose
// query has every pdx pair removed and every other pair byte for byte as
// the client sent it, in order. Any pdx value is removed; only "retry"
// opts in.
//
// The query is parsed with url.ParseQuery, the strict parse Nexen's list
// handler uses (N/api/query.go:354-363) — not r.URL.Query(), which
// silently drops a pair with a broken escape. When that parse fails the
// query goes to the engine untouched, pdx included: removing pairs from it,
// or rebuilding it from the pairs that did parse, could turn it into a
// query that parses with the broken filter quietly gone — the wrong-rows
// answer Nexen's 400 exists to prevent. Nexen ignores a parameter it does
// not know, so the leftover pdx is harmless there. Such a query is also
// never an opt-in, whatever its pdx pair says: it can only be a 400.
func retryOptIn(r *http.Request) (*http.Request, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		// On error q still holds the pairs that did parse, a "pdx=retry"
		// among them — but this request can only ever be Nexen's 400, so it
		// is never an opt-in: a busy slot must not turn it into a retryable
		// 503 (codex PR1a critic). The query goes to the engine untouched.
		return r, false
	}
	retries := q.Get(listRetryParam) == listRetryValue
	if !q.Has(listRetryParam) {
		return r, retries
	}
	pairs := strings.Split(r.URL.RawQuery, "&")
	kept := pairs[:0]
	for _, pair := range pairs {
		key, _, _ := strings.Cut(pair, "=")
		// The same unescape ParseQuery applied: "pd%78" is pdx too.
		if k, err := url.QueryUnescape(key); err == nil && k == listRetryParam {
			continue
		}
		kept = append(kept, pair)
	}
	r2 := new(http.Request)
	*r2 = *r
	u := *r.URL
	u.RawQuery = strings.Join(kept, "&")
	r2.URL = &u
	return r2, retries
}

// runList serves r through the engine's handler into a buffer capped at
// listBodyLimit, mounted exactly as RegisterRoutes mounts it for every other
// engine path — http.StripPrefix(RoutePrefix, recoverer(...)) — and reports
// whether recoverer contained a panic.
//
// The mount is built per request, not shared, for that report. recoverer
// answers a panic with WriteHeader(500), which is a no-op once the handler
// has written its status (bufferedResponse keeps the first, as net/http
// does). An engine that wrote 200 and a complete JSON object and only then
// panicked would therefore look like a good page and be stamped: a ver
// consumed for a read that never finished, and a client told the page is
// current. recoverer calls its logf for every panic it contains, before or
// after any write, so a logf that sets the flag and then forwards to m.logf
// sees them all (rowReader does the same). m.logf is read when the line is
// written, as for the slot's own lines (reads).
//
// http.ErrAbortHandler is not contained: recoverer re-panics it for
// net/http, and runList lets it propagate out of the wrapper to the server,
// which aborts the response as its contract says. Inside the slot,
// readSlot.read's defer releases the slot on the way, and fn never
// returned, so no ver is consumed.
func (m *Module) runList(handler http.Handler, r *http.Request) (res *bufferedResponse, panicked bool) {
	mount := http.StripPrefix(RoutePrefix, recoverer(func(format string, args ...any) {
		panicked = true
		m.logf(format, args...)
	}, handler))
	res = newBufferedResponse(listBodyLimit)
	mount.ServeHTTP(res, r)
	return res, panicked
}

// writeStampedPage adds "pdx" to a successful page and writes it. The
// re-encode happens after the slot was released: only the read needs it.
func (m *Module) writeStampedPage(w http.ResponseWriter, res *bufferedResponse, page map[string]json.RawMessage, stamp slotStamp) {
	pdx, _ := json.Marshal(stamp) // three plain fields: cannot fail
	page["pdx"] = pdx
	body, err := encodeObject(page)
	if err != nil {
		// Cannot happen with values that just decoded. Should it ever, the
		// page still goes out, unstamped: the SPA reads a page without pdx
		// as unversioned (§4.3), which is safe, only slower to converge.
		m.logf("nex-delta: stamping a list page failed, sending it unstamped: %v", err)
		res.writeTo(w, res.body.Bytes())
		return
	}
	res.header.Del("Content-Length") // the body grew
	res.writeTo(w, body)
}

// writeTo sends a buffered response to w: the engine's headers (added to
// whatever w already carries, CORS included), its status, then body.
func (b *bufferedResponse) writeTo(w http.ResponseWriter, body []byte) {
	dst := w.Header()
	for k, v := range b.header {
		dst[k] = append([]string(nil), v...)
	}
	w.WriteHeader(b.code())
	_, _ = w.Write(body)
}

// writeNexError answers in the structured error shape Nexen uses
// ({"error", "code"}), so one client-side parser covers the engine's own
// errors and the module's.
func writeNexError(w http.ResponseWriter, status int, msg, code string) {
	body, _ := json.Marshal(struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}{msg, code})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
