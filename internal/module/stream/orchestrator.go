package stream

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/module/session"
)

// runHandoff executes the handoff-to-stream/jsonl sequence asynchronously:
//  1. Disconnect existing relay if present (send shutdown, wait 5s)
//  2. Prepare pane (exit copy-mode, Escape, C-c)
//  3. Detect CC status — must be running (not normal/not-in-cc)
//  4. If CC busy (not idle), interrupt to idle
//  5. Extract session ID + cwd via GetStatus
//  6. Exit CC gracefully
//  7. Launch tbox relay command via SendKeys
//  8. Wait for relay to connect via bridge (15s timeout)
//  9. Update meta (mode + cc_session_id + cwd) and broadcast "connected"
func (m *StreamModule) runHandoff(sess session.SessionInfo, code, mode, command, handoffID, token string, port int, bind string) {
	defer m.locks.Unlock(code)

	broadcast := func(value string) {
		m.core.Events.Broadcast(code, "handoff", value)
	}

	target := sess.Name + ":0"

	// Step 1: Disconnect existing relay if present.
	// Pre-update mode to "term" so handleCliBridge's defer (revertModeOnRelayDisconnect)
	// sees mode=="term" and skips the spurious "failed:relay disconnected" broadcast.
	// Step 9 will set the correct mode after relay connects successfully.
	if m.bridge.HasRelay(code) {
		if sess.Mode != "term" {
			termMode := "term"
			if err := m.sessions.UpdateMeta(code, session.MetaUpdate{Mode: &termMode}); err != nil {
				broadcast("failed:meta pre-update error: " + err.Error())
				return
			}
		}
		broadcast("stopping-relay")
		if !m.waitRelayDisconnect(code, 5*time.Second) {
			broadcast("failed:existing relay did not disconnect")
			return
		}
	}

	// Step 2: Prepare pane for Detect (Step 3).
	// This is separate from Exit's internal pane prep (which prepares for /exit).
	// Detect needs a clean pane surface to read CC status from the terminal buffer,
	// so we exit copy-mode, dismiss any prompts, and cancel any pending input.
	if err := m.core.Tmux.SendKeysRaw(target, "-X", "cancel"); err != nil {
		log.Printf("handoff: pane prep cancel (%s): %v", target, err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := m.core.Tmux.SendKeysRaw(target, "Escape"); err != nil {
		log.Printf("handoff: pane prep escape (%s): %v", target, err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := m.core.Tmux.SendKeysRaw(target, "C-c"); err != nil {
		log.Printf("handoff: pane prep C-c (%s): %v", target, err)
	}
	time.Sleep(500 * time.Millisecond)

	// Step 3: Detect CC status — must be running
	broadcast("detecting")
	status := m.ccDetect.Detect(target)
	if status == detect.StatusNormal || status == detect.StatusNotInCC {
		broadcast("failed:no CC running")
		return
	}

	// Step 4: If CC is busy (not idle), interrupt to idle
	if status != detect.StatusCCIdle {
		broadcast("stopping-cc")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.ccOps.Interrupt(ctx, target); err != nil {
			broadcast("failed:interrupt CC: " + err.Error())
			return
		}
	}

	// Step 5: Extract session ID + cwd via GetStatus
	broadcast("extracting-id")
	statusCtx, statusCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer statusCancel()
	statusInfo, err := m.ccOps.GetStatus(statusCtx, target)
	if err != nil {
		broadcast("failed:get status: " + err.Error())
		return
	}

	// Step 6: Exit CC gracefully
	broadcast("exiting-cc")
	exitCtx, exitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer exitCancel()
	if err := m.ccOps.Exit(exitCtx, target); err != nil {
		broadcast("failed:exit CC: " + err.Error())
		return
	}

	// Step 7: Launch tbox relay with --resume
	broadcast("launching")
	tokenFile := filepath.Join(os.TempDir(), fmt.Sprintf("tbox-token-%s", handoffID))
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		broadcast("failed:write token file: " + err.Error())
		return
	}
	// Relay reads the token file on startup (within seconds), so removing it
	// when runHandoff returns is safe. defer covers all exit paths including
	// daemon crash (process exit cleans deferred resources).
	defer os.Remove(tokenFile)

	relayCmd := fmt.Sprintf("tbox relay --session %s --daemon ws://%s:%d --token-file %s -- %s --resume %s",
		code, bind, port, tokenFile, command, statusInfo.SessionID)
	if err := m.core.Tmux.SendKeys(target, relayCmd); err != nil {
		broadcast("failed:send-keys error: " + err.Error())
		return
	}

	// Step 8: Wait for relay to connect (15s timeout)
	if !m.waitRelayConnect(code, 15*time.Second) {
		broadcast("failed:relay did not connect within 15s")
		return
	}

	// Step 9: Update meta and broadcast success
	ccID := statusInfo.SessionID
	metaUpdate := session.MetaUpdate{Mode: &mode, CCSessionID: &ccID}
	if statusInfo.Cwd != "" {
		metaUpdate.Cwd = &statusInfo.Cwd
	}
	if err := m.sessions.UpdateMeta(code, metaUpdate); err != nil {
		broadcast("failed:meta update: " + err.Error())
		return
	}
	broadcast("connected")
}

// runHandoffToTerm handles the handoff from stream back to interactive terminal mode:
//  1. Get CCSessionID from session info
//  2. Pre-update mode to "term" (prevents spurious revert on relay disconnect)
//  3. Shutdown relay (send shutdown, wait 5s)
//  4. Wait for shell (detect StatusNormal, 10s timeout)
//  5. Launch claude --resume via SendKeys
//  6. Verify CC started (15s timeout)
//  7. Clear cc_session_id via UpdateMeta
//  8. Broadcast "connected"
func (m *StreamModule) runHandoffToTerm(sess session.SessionInfo, code, handoffID string) {
	defer m.locks.Unlock(code)

	broadcast := func(value string) {
		m.core.Events.Broadcast(code, "handoff", value)
	}

	target := sess.Name + ":0"

	// Step 1: Get CCSessionID
	ccSessionID := sess.CCSessionID
	if ccSessionID == "" {
		broadcast("failed:no CC session ID stored")
		return
	}
	origMode := sess.Mode

	// Step 2: Pre-update mode to "term" before shutting down relay.
	// This prevents revertModeOnRelayDisconnect from firing a spurious
	// "failed:relay disconnected" event during an intentional handoff.
	termMode := "term"
	if err := m.sessions.UpdateMeta(code, session.MetaUpdate{Mode: &termMode}); err != nil {
		broadcast("failed:meta pre-update error: " + err.Error())
		return
	}
	rollbackMode := func() {
		if err := m.sessions.UpdateMeta(code, session.MetaUpdate{Mode: &origMode}); err != nil {
			log.Printf("stream: rollback mode error for %s: %v", code, err)
		}
	}

	// Step 3: Shutdown relay
	if m.bridge.HasRelay(code) {
		broadcast("stopping-relay")
		if !m.waitRelayDisconnect(code, 5*time.Second) {
			rollbackMode()
			broadcast("failed:relay did not disconnect")
			return
		}
	}

	// Step 4: Wait for shell
	broadcast("waiting-shell")
	shellDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(shellDeadline) {
		if m.ccDetect.Detect(target) == detect.StatusNormal {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if m.ccDetect.Detect(target) != detect.StatusNormal {
		rollbackMode()
		broadcast("failed:shell did not recover")
		return
	}

	// Step 5: Launch interactive CC with --resume
	broadcast("launching-cc")
	resumeCmd := fmt.Sprintf("claude --resume %s", ccSessionID)
	if err := m.core.Tmux.SendKeys(target, resumeCmd); err != nil {
		rollbackMode()
		broadcast("failed:send-keys error: " + err.Error())
		return
	}

	// Step 6: Verify CC started (15s timeout)
	ccDeadline := time.Now().Add(15 * time.Second)
	ccStarted := false
	for time.Now().Before(ccDeadline) {
		st := m.ccDetect.Detect(target)
		if st == detect.StatusCCIdle || st == detect.StatusCCRunning || st == detect.StatusCCWaiting {
			ccStarted = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !ccStarted {
		rollbackMode()
		broadcast("failed:CC did not start")
		return
	}

	// Step 7: Clear cc_session_id (mode already set to "term" above).
	// At this point, CC is already running in terminal mode — the handoff
	// functionally succeeded. Only the cleanup (clearing cc_session_id) remains,
	// so we log any error instead of broadcasting failure.
	emptyID := ""
	if err := m.sessions.UpdateMeta(code, session.MetaUpdate{CCSessionID: &emptyID}); err != nil {
		log.Printf("stream: clear cc_session_id error for %s: %v", code, err)
	}

	// Step 8: Broadcast success
	broadcast("connected")
}

// waitRelayDisconnect sends a shutdown message to the relay and polls until it
// disconnects or the timeout expires. Returns true if the relay disconnected.
func (m *StreamModule) waitRelayDisconnect(code string, timeout time.Duration) bool {
	m.bridge.SubscriberToRelay(code, []byte(`{"type":"shutdown"}`))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !m.bridge.HasRelay(code) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return !m.bridge.HasRelay(code)
}

// waitRelayConnect polls until a relay registers on the bridge or the timeout expires.
// Returns true if a relay connected within the timeout.
func (m *StreamModule) waitRelayConnect(code string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.bridge.HasRelay(code) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return m.bridge.HasRelay(code)
}
