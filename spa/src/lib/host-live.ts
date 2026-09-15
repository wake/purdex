// spa/src/lib/host-live.ts — "can this host take a new session right now?"
//
// Lifted out of `SessionSection.tsx` so the launcher can ask the same question
// the New Tab block asks. Read from the live store snapshot, never from a
// render-time value: the runtime can go offline while a launcher sits open, and
// the check that matters is the one made at the moment of launching.
import { useHostStore } from '../stores/useHostStore'

/** The host still exists, its daemon is connected, and its tmux is usable. */
export function isHostLive(hostId: string): boolean {
  const s = useHostStore.getState()
  const rt = s.runtime[hostId]
  return !!s.hosts[hostId] && !!rt && rt.status === 'connected' && rt.tmuxState !== 'unavailable'
}
