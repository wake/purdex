package resourcesmod

import (
	"fmt"
	"strings"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

// sessionInteractive says whether a notice can reach the session: a live mod
// stream of it announced the prompt capability within modevents.CapsFresh
// (only an interactive session's mod does; `claude -p`, launchd and cron have
// none). An auto request from such a session is queued instead of waiting
// (lease v2 amendment A §2, decided with the M-P2(a) probe: a command prefix
// that would tell the daemon changes the permission prompts, so the daemon
// reads it from the session itself). The registry is looked up at use time:
// the mod event channel may start after this module.
func (m *Module) sessionInteractive(sid string) bool {
	if m.interactive != nil {
		return m.interactive(sid)
	}
	if m.core == nil || m.core.Registry == nil {
		return false
	}
	svc, ok := m.core.Registry.Get("modevents")
	if !ok {
		return false
	}
	reg, ok := svc.(*modevents.Registry)
	return ok && reg.SessionCapable(sid, modevents.CapPromptV1, modevents.CapsFresh)
}

// describeModStreams says, for the log, why a session was found to have no mod to take a notice: each stream whose current
// session is sid with the age of its last batch and of its last capability announcement (the notice goes to a stream that
// announced lease.notice.v1 within modevents.CapsFresh), or that no stream has the session. A 61 s user_alert on a session
// with a live stream (10-15, pos_464e13dd) could not be explained afterwards without it.
func (m *Module) describeModStreams(sid string) string {
	var streams []modevents.StreamInfo
	switch {
	case m.modStreams != nil:
		streams = m.modStreams(sid)
	case m.core != nil && m.core.Registry != nil:
		if svc, ok := m.core.Registry.Get("modevents"); ok {
			if reg, ok := svc.(*modevents.Registry); ok {
				streams = reg.Streams()
			}
		}
	}
	now := m.now()
	var parts []string
	other := 0
	for _, s := range streams {
		if s.SID != sid {
			if !s.Ended {
				other++
			}
			continue
		}
		caps := "caps never"
		if !s.CapsAt.IsZero() {
			caps = fmt.Sprintf("caps %s ago [%s]", now.Sub(s.CapsAt).Round(time.Second), strings.Join(s.Caps, ","))
		}
		parts = append(parts, fmt.Sprintf("stream %s mod %s ended=%v last batch %s ago, %s",
			s.Stream, s.ModVersion, s.Ended, now.Sub(s.AppliedAt).Round(time.Second), caps))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("no stream has this session (%d other live streams)", other)
	}
	return strings.Join(parts, "; ")
}
