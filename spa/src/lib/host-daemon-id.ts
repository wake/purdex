// spa/src/lib/host-daemon-id.ts — verify each connected host's daemon identity
// (spec 2026-09-23 D4.2). One module-level subscription for the app's lifetime,
// started from main.tsx next to the host-config loader.
//
// Triggers, per host and only while connected: the transition to `connected`,
// a change of its endpoint or token, or a change of its stored `daemonId`
// (e.g. arriving by sync). Each trigger is one `/api/info`; a failed request is
// not retried until the next trigger — nothing here polls.
//
// Freshness (PR review #2): every request takes the host's next generation, and
// a newer trigger or the host's deletion invalidates every older one, so only
// the newest answer can apply — and only for the host incarnation it was asked
// for (deleted and re-added under the same id → dropped). The endpoint + token
// captured before the request go to `observeDaemonId`, which re-checks them.
import { fetchHostInfo } from './host-api'
import { hostEndpoint, requestAtOf, useHostStore, type HostConfig } from '../stores/useHostStore'

function identity(h: HostConfig): string {
  return `${hostEndpoint(h)}:${h.token ?? ''}`
}

export function startHostDaemonIdVerification(): () => void {
  let counter = 0
  // Generation of the newest request per host; absent = none may apply.
  const current = new Map<string, number>()
  // Host whose answer is being applied right now: the stored-daemonId change that
  // write causes is our own learning, already verified — not a new trigger.
  let applying: string | null = null

  const verify = (hostId: string) => {
    const host = useHostStore.getState().hosts[hostId]
    if (!host) return
    const at = requestAtOf(host)
    const generation = ++counter
    current.set(hostId, generation)
    fetchHostInfo(hostId).then(
      (info) => {
        if (current.get(hostId) !== generation) return
        const observed = typeof info?.host_id === 'string' ? info.host_id : ''
        applying = hostId
        try {
          useHostStore.getState().observeDaemonId(hostId, observed, at)
        } finally {
          applying = null
        }
      },
      () => { /* not retried until the next trigger (spec D4.2) */ },
    )
  }

  const initial = useHostStore.getState()
  for (const [hostId, rt] of Object.entries(initial.runtime)) {
    if (rt?.status === 'connected' && initial.hosts[hostId]) verify(hostId)
  }

  return useHostStore.subscribe((next, prev) => {
    for (const hostId of [...current.keys()]) {
      if (!next.hosts[hostId]) current.delete(hostId)
    }
    for (const [hostId, host] of Object.entries(next.hosts)) {
      if (next.runtime[hostId]?.status !== 'connected') continue
      const before = prev.hosts[hostId]
      const wasConnected = prev.runtime[hostId]?.status === 'connected'
      if (!wasConnected || !before || identity(before) !== identity(host)) {
        verify(hostId)
        continue
      }
      if (before.daemonId !== host.daemonId && applying !== hostId) verify(hostId)
    }
  })
}
