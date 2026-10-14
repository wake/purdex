package core

import (
	"net/http"
	"net/http/pprof"
	"strconv"
)

// The longest a CPU profile or a trace may run (seconds). The stdlib takes any number; a request that asks for hours would hold the
// profiler (only one CPU profile can run at a time: the next one gets a 500) and keep a request open across a restart. The lead's
// 30 s profile fits; anything longer is refused with 400 before the profiler starts.
const (
	pprofMaxProfileSeconds = 60
	pprofMaxTraceSeconds   = 10
)

// registerPprof mounts the Go profiler under /debug/pprof/ on the daemon's own mux (task ed3920-10: find where a daemon that sits at 30 %
// CPU spends it). There is no listener of its own and no auth of its own: the request reaches the mux only through the daemon's general
// chain, whose TokenAuth knows the admin token alone (a peer host's token and a missing one are 401), and the device scope refuses a
// paired phone every pattern it does not list (this one is not listed). Only GET: a profile is read, never written. The handlers are the
// stdlib's; net/http/pprof also registers itself on http.DefaultServeMux at import, which this daemon does not serve.
//
// A 30 s CPU profile needs a response that lasts: the server sets no WriteTimeout (newHTTPServer), so /debug/pprof/profile?seconds=30 is
// not cut.
func registerPprof(mux *http.ServeMux) {
	mux.HandleFunc("GET /debug/pprof/", pprof.Index) // the index, and the named profiles: heap, goroutine, allocs, block, mutex, threadcreate
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", limitSeconds(pprofMaxProfileSeconds, pprof.Profile))
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", limitSeconds(pprofMaxTraceSeconds, pprof.Trace))
}

// limitSeconds refuses (400) a request whose seconds is not a number in 0..max, before the profiler starts; no seconds is the stdlib's
// own default.
func limitSeconds(max int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v := r.URL.Query().Get("seconds"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > max {
				http.Error(w, "seconds must be a whole number from 0 to "+strconv.Itoa(max), http.StatusBadRequest)
				return
			}
		}
		next(w, r)
	}
}
