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
// A second value on sig while the sequence below is still running (e.g. a
// slow or blocking StopModules) exits immediately via exit(130) rather than
// waiting out the rest of the shutdown budget — a second Ctrl-C should not
// need to wait for a stuck module. That watcher is armed only when a signal
// is what triggered the sequence: if Serve returned first (no signal yet),
// a signal arriving during the sequence is the FIRST signal, not a second
// impatient one, and must not short-circuit CloseModules.
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
		// race. That is still the FIRST signal, not a second one — drain
		// it so it isn't misread as a second Ctrl-C and triggers an
		// immediate exit(130) that skips CloseModules. Since this path
		// did not trigger via signal, the watcher below stays unarmed
		// regardless.
		select {
		case <-sig:
		default:
		}
	}

	done := make(chan struct{})
	defer close(done)
	if signalTriggered {
		go func() {
			select {
			case <-sig:
				logf("received second signal, exiting immediately")
				exit(130)
			case <-done:
			}
		}()
	}

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
