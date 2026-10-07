// cmd/pdx/shutdown.go
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// errRestart is what serveAndWait returns when a restart request — not a
// signal, not a Serve failure — started the sequence and no signal arrived
// during it: the caller re-execs once its own deferred cleanup has run
// (daemon restart spec §3.1). Like http.ErrServerClosed, it is an outcome,
// not a failure.
var errRestart = errors.New("restart requested")

// restartRequested is the restart outcome serveAndWait returns; it is
// errRestart to errors.Is. The restart still re-execs after a cleanup
// error, but does not hide it: warnings (one per logged error) go to
// last-shutdown.json for the next image to report (spec D13).
type restartRequested struct{ warnings []string }

func (*restartRequested) Error() string        { return errRestart.Error() }
func (*restartRequested) Is(target error) bool { return target == errRestart }

// shutdownTarget is the module side of the shutdown sequence (*core.Core).
type shutdownTarget interface {
	StopModules(context.Context) error
	CloseModules() error
	// BeginShutdown closes the restart endpoint to new requests and reports
	// whether one had already been accepted (202 sent).
	BeginShutdown() bool
}

// shutdownServer is the HTTP side of the shutdown sequence (*http.Server).
type shutdownServer interface {
	Shutdown(context.Context) error
	Close() error
}

// server is what serveAndWait drives: it serves a listener and can be
// shut down gracefully or closed outright (*http.Server).
type server interface {
	shutdownServer
	Serve(net.Listener) error
}

// serveAndWait runs srv.Serve(ln) and guarantees the shutdown sequence
// (spec §4.5) runs exactly once — triggered by the first of: a value on
// sig, a value on restart, or Serve returning (error or http.ErrServerClosed) — and does not
// return until the sequence's last step (CloseModules) has returned.
//
// Sequence:
//
//	cancel(); ctx = WithTimeout(budget)
//	StopModules(ctx)   error logged, continue
//	srv.Shutdown(ctx)  error (incl. deadline) logged → srv.Close()
//	CloseModules()     error logged
//
// The SAME ctx is passed to StopModules and Shutdown (N1 rule 1), so a
// StopModules that overruns the budget hands Shutdown an already-expired
// ctx and Shutdown falls through to Close immediately.
//
// Returns Serve's error unless it is http.ErrServerClosed, and a
// *restartRequested (errors.Is errRestart) when restart triggered the sequence and no signal cancelled it. A nil
// restart channel never fires. A restart the endpoint accepted just before
// a Serve failure won the trigger select still counts (target.BeginShutdown
// reports it); a signal does not yield to it.
//
// A forced exit is always available while the sequence below is still
// running (e.g. a slow or blocking StopModules): a SECOND signal exits
// immediately via exit(130) rather than waiting out the rest of the
// shutdown budget — a second Ctrl-C should not need to wait for a stuck
// module. Which signal counts as the second depends on what triggered the
// sequence: when a signal did, the trigger consumed the first and the next
// one is the second; when Serve returned first (the select consumed no
// signal), the first value the watcher takes from sig — whether it was
// already buffered when Serve won the race or arrives during the sequence
// — is the FIRST one: it is logged with a "send again" hint and must not
// short-circuit CloseModules, and only the one after it forces the exit.
// Nothing is ever drained from sig outside those two consumers, so a
// keypress is never silently discarded (which would make the forced exit
// need a third Ctrl-C).
//
// A restart trigger behaves like Serve-first for signal counting, except
// that the first signal also cancels the restart: `pdx stop` during a
// restart must stop the daemon, not see it come back.
func serveAndWait(srv server, ln net.Listener, sig <-chan os.Signal,
	restart <-chan struct{}, cancel context.CancelFunc, target shutdownTarget, budget time.Duration,
	logf func(string, ...any), exit func(int)) error {

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	// First trigger wins; the sequence below runs exactly once because
	// this select is the only place it is entered.
	var err error
	serveReturned := false
	signalTriggered := false
	restartTriggered := false
	var restartCancelled atomic.Bool
	select {
	case s := <-sig:
		signalTriggered = true
		logf("received %v, shutting down...", s)
	case <-restart:
		restartTriggered = true
		logf("restart requested, shutting down...")
	case err = <-serveErr:
		serveReturned = true
		// A signal may already be sitting in sig (buffered, not yet
		// delivered to this select) even though Serve is what won the
		// race. It is left there: the Serve-first watcher below counts
		// it as the FIRST signal (hint, no exit), exactly as it would a
		// signal arriving later.
	}

	// The restart endpoint stops accepting now, whatever started the
	// sequence. A restart it accepted just before (202 already sent) is
	// honoured even if a Serve failure won the select above; a signal
	// still wins (D4).
	if target.BeginShutdown() && !signalTriggered && !restartTriggered {
		restartTriggered = true
		logf("restart accepted before shutdown began; restarting")
	}

	done := make(chan struct{})
	var closeDone sync.Once
	watcherDone := make(chan struct{})
	// Deferred as well as done below: a panic out of the sequence must neither leak the signal
	// watcher nor leave it running (it could still take a later signal and exit) once we unwind.
	defer func() {
		closeDone.Do(func() { close(done) })
		<-watcherDone
	}()
	go func() {
		defer close(watcherDone)
		if !signalTriggered {
			// Serve-first: the first value out of sig — buffered before
			// the race or sent during the sequence — is the first signal
			// the user sent; acknowledge it and keep going.
			select {
			case s := <-sig:
				if restartTriggered {
					restartCancelled.Store(true)
					logf("received %v during restart; exiting instead of "+
						"restarting (send again to exit immediately)", s)
				} else {
					logf("received %v during shutdown; send again to exit immediately", s)
				}
			case <-done:
				return
			}
		}
		select {
		case <-sig:
			logf("received second signal, exiting immediately")
			exit(130)
		case <-done:
		}
	}()

	cancel() // stop module background goroutines (pollers, watchers)
	ctx, ctxCancel := context.WithTimeout(context.Background(), budget)
	defer ctxCancel()

	// Each error logged below is also kept for a restart's last-shutdown record.
	var warnings []string
	if e := target.StopModules(ctx); e != nil {
		logf("stop modules: %v", e)
		warnings = append(warnings, fmt.Sprintf("stop modules: %v", e))
	}
	if e := srv.Shutdown(ctx); e != nil {
		logf("http shutdown: %v; closing connections", e)
		warnings = append(warnings, fmt.Sprintf("http shutdown: %v", e))
		if ce := srv.Close(); ce != nil {
			logf("http close: %v", ce)
			warnings = append(warnings, fmt.Sprintf("http close: %v", ce))
		}
	}
	// Shutdown/Close makes Serve return; collect it so the accept loop is
	// fully gone before modules release their resources.
	if !serveReturned {
		err = <-serveErr
	}
	if e := target.CloseModules(); e != nil {
		logf("close modules: %v", e)
		warnings = append(warnings, fmt.Sprintf("close modules: %v", e))
	}

	closeDone.Do(func() { close(done) })
	<-watcherDone // a signal the watcher took is now recorded in restartCancelled
	if restartTriggered && !restartCancelled.Load() {
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("server error during restart: %v", err)
			warnings = append(warnings, fmt.Sprintf("server error during restart: %v", err))
		}
		if len(warnings) > 0 {
			logf("restart: continuing despite %d cleanup error(s)", len(warnings))
		}
		return &restartRequested{warnings: warnings}
	}

	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
