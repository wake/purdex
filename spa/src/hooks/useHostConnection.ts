// spa/src/hooks/useHostConnection.ts — Convenience hook for host connection state + manual retry
import { useHostStore } from '../stores/useHostStore'

const noop = () => {}

export function useHostConnection(hostId: string) {
  const runtime = useHostStore((s) => s.runtime[hostId])
  return {
    status: runtime?.status ?? 'disconnected',
    daemonState: runtime?.daemonState,
    tmuxState: runtime?.tmuxState,
    latency: runtime?.latency,
    manualRetry: runtime?.manualRetry ?? noop,
  }
}
