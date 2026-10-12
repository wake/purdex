// spa/src/lib/team/paused-refresh.ts — keeps usePausedStore current (unattended quota pause spec §3.4; UP-5b). One module-level
// subscription set for the app's lifetime, started from main.tsx. A host's list is read:
//   - when the host announces `team.pause.v1` (the probe answered, so it is connected);
//   - on each `team.roster` event of that host (the daemon re-sends the roster when a team member pauses or resumes);
//   - every 30 s WHILE that host's unattended switch is on (a solo session pausing changes no roster, so no event comes);
//   - once when the switch goes on -> off (the daemon clears every pause then);
//   - after a release (`refreshPaused`).
// A host that does not list the capability is never asked. At most one request per host is out: a read asked meanwhile joins it
// as ONE more read after it. An answer is applied only if the host is still there under the same endpoint and token and still
// lists the capability; a removed host's entry is dropped.
import { useHostStore } from '../../stores/useHostStore'
import { usePausedStore } from '../../stores/usePausedStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { fetchPaused } from './paused-api'
import { hostIdentityNow } from './quota-host'

export const PAUSED_POLL_MS = 30_000

interface Flight { done: Promise<void>; again: boolean }
const flights = new Map<string, Flight>()

const supported = (hostId: string): boolean => useUnattendedStore.getState().byHost[hostId]?.pauseSupport === 'yes'

/** Read the host's paused list now (a no-op for a host without `team.pause.v1`). Resolves when the read, and the one queued behind it, are done. */
export function refreshPaused(hostId: string): Promise<void> {
  if (!supported(hostId)) return Promise.resolve()
  const cur = flights.get(hostId)
  if (cur) {
    cur.again = true
    return cur.done
  }
  const flight: Flight = { again: false, done: Promise.resolve() }
  flight.done = (async () => {
    try {
      do {
        flight.again = false
        const identity = hostIdentityNow(hostId)
        if (identity === null || !supported(hostId)) break
        try {
          const sessions = await fetchPaused(hostId)
          // Stale: the host was removed or re-pointed, or lost the capability, while this was out.
          if (hostIdentityNow(hostId) === identity && supported(hostId)) usePausedStore.getState().set(hostId, sessions, Date.now())
        } catch {
          // Keep what is shown; the next trigger reads again.
        }
      } while (flight.again)
    } finally {
      flights.delete(hostId)
    }
  })()
  flights.set(hostId, flight)
  return flight.done
}

export function startPausedRefresh(): () => void {
  const timers = new Map<string, ReturnType<typeof setInterval>>()
  const stopTimer = (hostId: string) => {
    const t = timers.get(hostId)
    if (t !== undefined) { clearInterval(t); timers.delete(hostId) }
  }

  const sync = (hostId: string, prevOn: boolean, prevSupported: boolean) => {
    const entry = useUnattendedStore.getState().byHost[hostId]
    const sup = entry?.pauseSupport === 'yes'
    const on = entry?.state?.on === true
    if (!sup) {
      stopTimer(hostId)
      usePausedStore.getState().forgetHost(hostId)
      return
    }
    if (on && !timers.has(hostId)) timers.set(hostId, setInterval(() => { void refreshPaused(hostId) }, PAUSED_POLL_MS))
    if (!on) stopTimer(hostId)
    if (!prevSupported || (prevOn && !on)) void refreshPaused(hostId)
  }

  const unsubUnattended = useUnattendedStore.subscribe((next, prev) => {
    const ids = new Set([...Object.keys(next.byHost), ...Object.keys(prev.byHost)])
    for (const hostId of ids) {
      const a = prev.byHost[hostId]
      const b = next.byHost[hostId]
      if (a?.pauseSupport === b?.pauseSupport && a?.state?.on === b?.state?.on) continue
      sync(hostId, a?.state?.on === true, a?.pauseSupport === 'yes')
    }
  })

  const unsubRoster = useTeamRosterStore.subscribe((next, prev) => {
    for (const hostId of Object.keys(next.byHost)) {
      if (next.byHost[hostId] !== prev.byHost[hostId]) void refreshPaused(hostId)
    }
  })

  const unsubHosts = useHostStore.subscribe((next, prev) => {
    for (const hostId of Object.keys(prev.hosts)) {
      if (!next.hosts[hostId]) { stopTimer(hostId); usePausedStore.getState().forgetHost(hostId) }
    }
  })

  // Hosts already known when this starts.
  for (const hostId of Object.keys(useUnattendedStore.getState().byHost)) sync(hostId, false, false)

  return () => {
    unsubUnattended()
    unsubRoster()
    unsubHosts()
    for (const hostId of [...timers.keys()]) stopTimer(hostId)
  }
}

export function __resetPausedRefreshForTests(): void {
  flights.clear()
}
