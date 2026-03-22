package cc

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/detect"
)

// startPoller launches a background goroutine that periodically detects CC
// status for all sessions and broadcasts changes via core.Events.
func (m *CCModule) startPoller(ctx context.Context) {
	interval := m.core.Cfg.Detect.PollInterval
	if interval <= 0 {
		interval = 2
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)

	go func() {
		defer ticker.Stop()

		lastStatus := make(map[string]detect.Status)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !m.core.Events.HasSubscribers() {
					continue
				}
				sessions, err := m.sessions.ListSessions()
				if err != nil {
					log.Printf("status poller: list sessions: %v", err)
					continue
				}

				currentCodes := make(map[string]struct{}, len(sessions))
				for _, sess := range sessions {
					currentCodes[sess.Code] = struct{}{}
					detectTarget := sess.Name + ":0"
					status := m.detector.Detect(detectTarget)
					if prev, ok := lastStatus[sess.Code]; !ok || prev != status {
						lastStatus[sess.Code] = status
						m.core.Events.Broadcast(sess.Code, "status", string(status))
					}
				}
				// Prune deleted sessions to avoid unbounded map growth.
				for code := range lastStatus {
					if _, ok := currentCodes[code]; !ok {
						delete(lastStatus, code)
					}
				}
			}
		}
	}()
}

// sendStatusSnapshot detects current status of all sessions and pushes
// them to a single subscriber. Called on new WS connections so the frontend
// doesn't need to wait for the next poller tick.
func (m *CCModule) sendStatusSnapshot(sub *core.EventSubscriber) {
	sessions, err := m.sessions.ListSessions()
	if err != nil {
		return
	}
	for _, sess := range sessions {
		detectTarget := sess.Name + ":0"
		status := m.detector.Detect(detectTarget)
		msg, err := json.Marshal(core.SessionEvent{
			Type:    "status",
			Session: sess.Code,
			Value:   string(status),
		})
		if err != nil {
			continue
		}
		sub.Send(msg)
	}
}
