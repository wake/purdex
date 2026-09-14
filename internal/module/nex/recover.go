// Package nex embeds the Nexen execution engine as a pdx daemon module.
package nex

import (
	"net/http"
	"runtime/debug"
)

// recoverer wraps next so that a panic occurring while it serves a request
// is contained to that request instead of taking down the daemon process.
//
// The original http.ResponseWriter is passed through to next unmodified —
// recoverer never wraps it — so any optional interface next relies on
// (http.Flusher, http.Hijacker, and so on) is preserved by construction.
//
// On panic, recoverer attempts w.WriteHeader(500) first, then logs the
// request method, path, the recovered value, and a stack trace via logf.
// If the handler already wrote a response (headers, or a flushed body),
// the WriteHeader call is a no-op that net/http merely logs internally
// ("superfluous WriteHeader") — it cannot un-send bytes already flushed to
// the client, so the client simply sees the response end early.
//
// The WriteHeader(500) attempt does not depend on logf: it runs first and
// unconditionally, and logf is then invoked behind its own recover guard.
// This way a misbehaving logf (one that itself panics) can neither skip
// the 500 response nor escape recoverer and crash the process.
//
// http.ErrAbortHandler is re-panicked rather than recovered: net/http
// treats it specially (it aborts the handler without logging a stack
// trace), and recoverer must not interfere with that contract.
func recoverer(logf func(string, ...any), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			w.WriteHeader(http.StatusInternalServerError)
			func() {
				defer func() { recover() }()
				logf("nex: panic recovered: method=%s path=%s value=%v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
			}()
		}()
		next.ServeHTTP(w, r)
	})
}
