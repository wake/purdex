package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// healthCheckTimeout is how long `pdx start` waits for the freshly spawned
	// daemon to answer /api/health before giving up.
	healthCheckTimeout = 60 * time.Second
	// healthCheckInterval is the gap between health probes.
	healthCheckInterval = 200 * time.Millisecond
	// healthCheckNotifyAfter is how long the wait must last before we print a
	// progress line, so a slow (but healthy) startup does not look hung.
	healthCheckNotifyAfter = 3 * time.Second
)

// waitForHealthy polls healthURL every interval until it returns 200 from the child, the child
// exits, or timeout elapses. "From the child" means the body's pid is wantPID (a 200 from another
// daemon on the same address, or from an older build that sends no pid, is not it: #959); wantPID 0
// accepts any 200. A value on childExited (including a nil error, or a
// closed channel) means the daemon died during startup and the wait ends
// immediately instead of burning the rest of the window. notify, when non-nil, is
// called at most once — only if the wait outlasts healthCheckNotifyAfter.
func waitForHealthy(healthURL string, wantPID int, childExited <-chan error, timeout, interval time.Duration, notify func(string)) error {
	return waitForHealthyWithNotify(healthURL, wantPID, childExited, timeout, interval, healthCheckNotifyAfter, notify)
}

// waitForHealthyWithNotify is waitForHealthy with an injectable notify threshold,
// so tests do not have to wait out the production one.
func waitForHealthyWithNotify(healthURL string, wantPID int, childExited <-chan error, timeout, interval, notifyAfter time.Duration, notify func(string)) error {
	maxProbeTimeout := 5 * interval
	if maxProbeTimeout > timeout {
		maxProbeTimeout = timeout
	}
	client := healthClient()

	start := time.Now()
	deadline := start.Add(timeout)
	// The whole wait is bounded by one context, so no probe can outlive the window.
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	notified := false
	otherPID := 0 // the pid of a daemon that answered but is not the child

	for {
		select {
		case err := <-childExited:
			return childExitError(err)
		default:
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return healthTimeoutError(timeout, wantPID, otherPID)
		}

		// Never let a single probe run past the deadline.
		probeTimeout := maxProbeTimeout
		if probeTimeout > remaining {
			probeTimeout = remaining
		}
		healthy, gotPID, err := probeHealth(ctx, client, healthURL, probeTimeout)
		if err == nil && healthy && wantPID > 0 && gotPID != wantPID {
			healthy, otherPID = false, gotPID
		}
		if err == nil && healthy {
			// The 200 is only trustworthy if the daemon is still alive and the
			// window has not closed while we were holding the response.
			select {
			case exitErr := <-childExited:
				return childExitError(exitErr)
			default:
			}
			if time.Until(deadline) <= 0 {
				return healthTimeoutError(timeout, wantPID, otherPID)
			}
			return nil
		}

		remaining = time.Until(deadline)
		if remaining <= 0 {
			return healthTimeoutError(timeout, wantPID, otherPID)
		}

		if notify != nil && !notified && time.Since(start) >= notifyAfter {
			notified = true
			notify(fmt.Sprintf("still waiting for daemon to become healthy (%s elapsed, giving it up to %s)",
				time.Since(start).Round(time.Second), timeout))
		}

		wait := interval
		if wait > remaining {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case err := <-childExited:
			timer.Stop()
			return childExitError(err)
		case <-timer.C:
		}
	}
}

// probeHealth issues one health request bounded by both probeTimeout and the
// caller's overall window (parent ctx).
// It returns whether the answer was a 200 and the pid its body carried (0 when absent or unreadable).
func probeHealth(ctx context.Context, client *http.Client, healthURL string, probeTimeout time.Duration) (bool, int, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return false, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, 0, nil
	}
	var body struct {
		PID int `json:"pid"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
	return true, body.PID, nil
}

// healthClient asks the configured address itself: no environment proxy (HTTP_PROXY can answer for a non-loopback
// bind such as a Tailscale IP, and an answer without the child's pid now fails the start and kills the group) and no
// redirect followed (a 3xx is simply not healthy).
func healthClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// healthTimeoutError describes a daemon that never answered inside the window.
// When another process answered with a different pid, it is named: that is the real cause (#959).
func healthTimeoutError(timeout time.Duration, wantPID, otherPID int) error {
	if wantPID > 0 && otherPID != 0 {
		return fmt.Errorf("daemon did not become healthy within %s: another process answered /api/health (pid %d, expected %d)", timeout, otherPID, wantPID)
	}
	return fmt.Errorf("daemon did not become healthy within %s", timeout)
}

// childExitError describes a daemon that exited before it became healthy.
func childExitError(err error) error {
	if err == nil {
		return fmt.Errorf("daemon exited during startup (exited before becoming healthy)")
	}
	return fmt.Errorf("daemon exited during startup (%v)", err)
}
