// spa/src/lib/host-config-loader.ts — load host config when a host connects
// (spec §4.1). One module-level
// subscription for the app's lifetime, started from main.tsx.
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useHostConfigStore } from '../stores/useHostConfigStore'

function endpoint(h: HostConfig | undefined): string {
  return h ? `${h.ip}:${h.port}:${h.token ?? ''}` : ''
}

export function startHostConfigLoader(): () => void {
  const load = (hostId: string) => { void useHostConfigStore.getState().load(hostId) }

  const initial = useHostStore.getState()
  for (const [hostId, rt] of Object.entries(initial.runtime)) {
    if (rt?.status === 'connected' && initial.hosts[hostId]) load(hostId)
  }

  return useHostStore.subscribe((next, prev) => {
    const config = useHostConfigStore.getState()
    for (const hostId of Object.keys(prev.hosts)) {
      if (!next.hosts[hostId]) config.forget(hostId)
    }
    for (const hostId of Object.keys(next.hosts)) {
      const connected = next.runtime[hostId]?.status === 'connected'
      const wasConnected = prev.runtime[hostId]?.status === 'connected'
      const moved = !!prev.hosts[hostId] && endpoint(prev.hosts[hostId]) !== endpoint(next.hosts[hostId])
      if (moved) {
        // The cached copy (and its revisions) belong to the old daemon.
        config.forget(hostId)
        if (connected) load(hostId)
        continue
      }
      if (connected && !wasConnected) load(hostId)
    }
  })
}
