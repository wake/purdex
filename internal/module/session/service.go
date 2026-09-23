package session

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/terminal"
)

// --- SessionProvider implementation ---

// TmuxInstance re-reads the tmux server identity. Every payload that carries a
// generation samples it here rather than reusing a tick-scoped value, so a
// restart between ticks cannot be labelled with the previous generation.
// Returns "" when the probe fails; "" is a legitimate, transmitted value
// meaning "unknown" — never a match for another "".
func (m *SessionModule) TmuxInstance() string {
	return m.tmuxInstance(context.Background())
}

// tmuxInstance is TmuxInstance bounded by ctx (the probe's own timeout still
// applies when ctx has a later deadline or none).
func (m *SessionModule) tmuxInstance(ctx context.Context) string {
	if m.tmuxInstanceFn == nil {
		return ""
	}
	return m.tmuxInstanceFn(ctx)
}

// ListSessions returns all live tmux sessions merged with cached meta, under a
// fresh listReadTimeout budget. Callers that hold a context or budget of
// their own use ListSessionsContext.
func (m *SessionModule) ListSessions() ([]SessionInfo, error) {
	return m.ListSessionsContext(context.Background())
}

// ListSessionsContext builds the session list under ONE deadline — ctx's,
// capped at listReadTimeout — covering the whole chain: tmux list-sessions,
// the tmux-instance probe, the meta-DB reads and every session's pane
// metadata (#1293 §3.2). When the deadline (or a cancellation) ends the read
// it returns an error wrapping ctx.Err() and no list — never a partial one.
func (m *SessionModule) ListSessionsContext(ctx context.Context) ([]SessionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, listReadTimeout)
	defer cancel()
	list, err := m.listSessions(ctx)
	if err != nil {
		return nil, err
	}
	// The probe reports failure as "" rather than an error, and CleanOrphans
	// skips the DB for an empty list, so a deadline hit there could otherwise
	// go unnoticed and hand out a list stamped with an unknown generation.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("session list: %w", err)
	}
	return list, nil
}

func (m *SessionModule) listSessions(ctx context.Context) ([]SessionInfo, error) {
	sessions, err := m.tmux.ListSessions(ctx)
	if err != nil {
		return nil, err
	}

	// Sampled once per list so every entry in one payload reports the same
	// generation, and sampled here rather than on the watcher tick so a
	// restart between two ticks is never labelled with the old generation.
	instance := m.tmuxInstance(ctx)

	// Build live ID set for orphan cleanup
	liveIDs := make([]string, len(sessions))
	for i, s := range sessions {
		liveIDs[i] = s.ID
	}
	if _, err := m.meta.CleanOrphansContext(ctx, liveIDs); err != nil {
		return nil, err
	}

	result := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		code, err := EncodeSessionID(s.ID)
		if err != nil {
			log.Printf("session: skipping tmux session with invalid id %q: %v", s.ID, err)
			continue
		}
		info := SessionInfo{
			Code:         code,
			TmuxID:       s.ID,
			Name:         s.Name,
			Exists:       true,
			Mode:         "terminal", // default
			Cwd:          s.Cwd,
			TmuxInstance: instance,
		}
		if err := m.applyActivePaneMetadata(ctx, &info); err != nil {
			return nil, err
		}

		// Merge meta from DB (Cwd always comes from tmux — SOT)
		meta, err := m.meta.GetMetaContext(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		if meta != nil {
			info.Mode = meta.Mode
		}

		result = append(result, info)
	}

	return result, nil
}

// GetSession returns a single session by its code, or nil if not found,
// under a fresh listReadTimeout budget.
func (m *SessionModule) GetSession(code string) (*SessionInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), listReadTimeout)
	defer cancel()
	return m.getSession(ctx, code)
}

// getSession is GetSession under ctx. A (nil, nil) answer is a reliable "not
// found" only when ctx was still live when the list came back and when the
// orphan meta row was dropped: once ctx has ended the answer is an error
// wrapping ctx.Err() (#1293) — a list read past its deadline is not evidence
// that a session is gone, and is not acted on.
func (m *SessionModule) getSession(ctx context.Context, code string) (*SessionInfo, error) {
	tmuxID, err := DecodeSessionID(code)
	if err != nil {
		return nil, nil // invalid code → not found
	}

	sessions, err := m.tmux.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("session get: %w", err)
	}

	for _, s := range sessions {
		if s.ID == tmuxID {
			info := &SessionInfo{
				Code:         code,
				TmuxID:       s.ID,
				Name:         s.Name,
				Exists:       true,
				Mode:         "terminal",
				Cwd:          s.Cwd,
				TmuxInstance: m.tmuxInstance(ctx),
			}
			if err := m.applyActivePaneMetadata(ctx, info); err != nil {
				return nil, err
			}

			// Merge meta from DB (Cwd always comes from tmux — SOT)
			meta, err := m.meta.GetMetaContext(ctx, s.ID)
			if err != nil {
				return nil, err
			}
			if meta != nil {
				info.Mode = meta.Mode
			}

			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("session get: %w", err)
			}
			return info, nil
		}
	}

	// Not found in tmux — clean up orphan meta
	if err := m.meta.DeleteMetaContext(ctx, tmuxID); err != nil {
		return nil, fmt.Errorf("session get: drop orphan meta: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("session get: %w", err)
	}
	return nil, nil
}

// applyActivePaneMetadata fills the pane fields from tmux. A metadata read
// that fails on its own leaves them empty (today's behaviour: the session is
// still listed), but one that failed because ctx ended aborts the caller's
// read with an error wrapping ctx.Err() (#1293 §3.2) — continuing would only
// read further sessions past the deadline.
func (m *SessionModule) applyActivePaneMetadata(ctx context.Context, info *SessionInfo) error {
	metadata, err := m.tmux.ActivePaneMetadata(ctx, info.Name)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(err, ctxErr) {
				return fmt.Errorf("pane metadata for %q: %w", info.Name, err)
			}
			return fmt.Errorf("pane metadata for %q: %w (%v)", info.Name, ctxErr, err)
		}
		return nil
	}
	info.PaneTitle = metadata.PaneTitle
	info.WindowName = metadata.WindowName
	info.CurrentCommand = metadata.PaneCurrentCommand
	return nil
}

// UpdateMeta performs a partial meta update for the session identified by code.
func (m *SessionModule) UpdateMeta(code string, update MetaUpdate) error {
	tmuxID, err := DecodeSessionID(code)
	if err != nil {
		return err
	}

	storeUpdate := store.MetaUpdate{
		Mode: update.Mode,
		Cwd:  update.Cwd,
	}

	return m.meta.UpdateMeta(tmuxID, storeUpdate)
}

// HandleTerminalWS attaches a WebSocket connection to the tmux session PTY relay.
func (m *SessionModule) HandleTerminalWS(w http.ResponseWriter, r *http.Request, code string) {
	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Snapshot sizing mode under read lock to avoid race with handlePutConfig
	// (config_handler.go writes Terminal.SizingMode under CfgMu.Lock).
	sizingMode := "auto"
	if m.core != nil && m.core.Cfg != nil {
		m.core.CfgMu.RLock()
		sizingMode = m.core.Cfg.Terminal.GetSizingMode()
		m.core.CfgMu.RUnlock()
	}

	// Build tmux attach-session command and args.
	target := info.Name
	args := buildTerminalRelayArgs(target, sizingMode)

	relay := terminal.NewRelay("tmux", args, "/")

	switch sizingMode {
	case "terminal-first":
		// no OnStart — relay uses -f ignore-size, sizing handled by terminal
	case "minimal-first":
		relay.OnStart = func() {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				if err := m.tmux.ResizeWindowAuto(target); err != nil {
					log.Printf("HandleTerminalWS: ResizeWindowAuto(%s): %v", target, err)
				}
				if err := m.tmux.SetWindowOption(target, "window-size", "smallest"); err != nil {
					log.Printf("HandleTerminalWS: SetWindowOption(%s): %v", target, err)
				}
			}()
		}
	default:
		if sizingMode != "auto" && sizingMode != "" {
			log.Printf("HandleTerminalWS: unknown sizing_mode %q, falling back to auto", sizingMode)
		}
		relay.OnStart = func() {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				if err := m.tmux.ResizeWindowAuto(target); err != nil {
					log.Printf("HandleTerminalWS: ResizeWindowAuto(%s): %v", target, err)
				}
				if err := m.tmux.SetWindowOption(target, "window-size", "latest"); err != nil {
					log.Printf("HandleTerminalWS: SetWindowOption(%s): %v", target, err)
				}
			}()
		}
	}

	relay.HandleWebSocket(w, r)
}

// buildTerminalRelayArgs returns the tmux attach-session args for the given sizing mode.
func buildTerminalRelayArgs(target, sizingMode string) []string {
	args := []string{"attach-session", "-t", target}
	if sizingMode == "terminal-first" {
		args = append(args, "-f", "ignore-size")
	}
	return args
}

// windowSizeForMode returns the window-size option value for the given sizing mode.
func windowSizeForMode(sizingMode string) string {
	switch sizingMode {
	case "minimal-first":
		return "smallest"
	default:
		return "latest"
	}
}
