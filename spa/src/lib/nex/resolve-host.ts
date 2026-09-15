// spa/src/lib/nex/resolve-host.ts — maps an optional `host` hint (pane
// content, route segment, deeplink) onto a known hostId; falls back to the
// first host so an execution always has a daemon to talk to. Kept free of
// fetch imports so pane-utils / route code can use it.
import { useHostStore } from '../../stores/useHostStore'

export function resolveExecutionHostId(host?: string): string {
  const { hostOrder } = useHostStore.getState()
  if (host && hostOrder.includes(host)) return host
  return hostOrder[0] ?? ''
}
