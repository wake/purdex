// cmd/pdx/shutdown.go
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"time"
)

// shutdownTarget is the module side of the shutdown sequence (*core.Core).
type shutdownTarget interface {
	StopModules(context.Context) error
	CloseModules() error
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
// sig, or Serve returning (error or http.ErrServerClosed) — and does not
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
// Returns Serve's error unless it is http.ErrServerClosed.
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
func serveAndWait(srv server, ln net.Listener, sig <-chan os.Signal,
	cancel context.CancelFunc, target shutdownTarget, budget time.Duration,
	logf func(string, ...any), exit func(int)) error {

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	// First trigger wins; the sequence below runs exactly once because
	// this select is the only place it is entered.
	var err error
	serveReturned := false
	signalTriggered := false
	select {
	case s := <-sig:
		signalTriggered = true
		logf("received %v, shutting down...", s)
	case err = <-serveErr:
		serveReturned = true
		// A signal may already be sitting in sig (buffered, not yet
		// delivered to this select) even though Serve is what won the
		// race. It is left there: the Serve-first watcher below counts
		// it as the FIRST signal (hint, no exit), exactly as it would a
		// signal arriving later.
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		if !signalTriggered {
			// Serve-first: the first value out of sig — buffered before
			// the race or sent during the sequence — is the first signal
			// the user sent; acknowledge it and keep going.
			select {
			case s := <-sig:
				logf("received %v during shutdown; send again to exit immediately", s)
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

	if e := target.StopModules(ctx); e != nil {
		logf("stop modules: %v", e)
	}
	if e := srv.Shutdown(ctx); e != nil {
		logf("http shutdown: %v; closing connections", e)
		if ce := srv.Close(); ce != nil {
			logf("http close: %v", ce)
		}
	}
	// Shutdown/Close makes Serve return; collect it so the accept loop is
	// fully gone before modules release their resources.
	if !serveReturned {
		err = <-serveErr
	}
	if e := target.CloseModules(); e != nil {
		logf("close modules: %v", e)
	}

	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
