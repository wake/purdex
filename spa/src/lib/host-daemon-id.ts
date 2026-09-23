// spa/src/lib/host-daemon-id.ts — verify each connected host's daemon identity
// (spec 2026-09-23 D4.2). One module-level subscription for the app's lifetime,
// started from main.tsx next to the host-config loader.
//
// Triggers, per host and only while connected: the transition to `connected`,
// a change of its endpoint or token, or a change of its stored `daemonId`
// (e.g. arriving by sync). Each trigger is one `/api/info`; the endpoint is
// captured before the request and handed to `observeDaemonId`, which drops an
// answer for a host that has since moved or gone. A failed request is not
// retried until the next trigger — nothing here polls.
import { fetchHostInfo } from './host-api'
import { hostEndpoint, useHostStore, type HostConfig } from '../stores/useHostStore'

function identity(h: HostConfig): string {
  return `${hostEndpoint(h)}:${h.token ?? ''}`
}

export function startHostDaemonIdVerification(): () => void {
  // Last answer per host (endpoint it came from + host_id). A stored-daemonId change
  // to exactly that value is our own learning write landing — already verified.
  const lastObserved = new Map<string, { endpoint: string; observed: string }>()

  const verify = (hostId: string) => {
    const host = useHostStore.getState().hosts[hostId]
    if (!host) return
    const endpoint = hostEndpoint(host)
    fetchHostInfo(hostId).then(
      (info) => {
        const observed = typeof info?.host_id === 'string' ? info.host_id : ''
        lastObserved.set(hostId, { endpoint, observed })
        useHostStore.getState().observeDaemonId(hostId, observed, endpoint)
      },
      () => { /* not retried until the next trigger (spec D4.2) */ },
    )
  }

  const initial = useHostStore.getState()
  for (const [hostId, rt] of Object.entries(initial.runtime)) {
    if (rt?.status === 'connected' && initial.hosts[hostId]) verify(hostId)
  }

  return useHostStore.subscribe((next, prev) => {
    for (const hostId of lastObserved.keys()) {
      if (!next.hosts[hostId]) lastObserved.delete(hostId)
    }
    for (const [hostId, host] of Object.entries(next.hosts)) {
      if (next.runtime[hostId]?.status !== 'connected') continue
      const before = prev.hosts[hostId]
      const wasConnected = prev.runtime[hostId]?.status === 'connected'
      if (!wasConnected || !before || identity(before) !== identity(host)) {
        verify(hostId)
        continue
      }
      if (before.daemonId !== host.daemonId) {
        const last = lastObserved.get(hostId)
        const ownWrite = !!last && last.endpoint === hostEndpoint(host) && last.observed === host.daemonId
        if (!ownWrite) verify(hostId)
      }
    }
  })
}
