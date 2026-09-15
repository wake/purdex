// spa/src/lib/nex/resolve-host.ts — maps an optional `host` hint (pane
// content, route segment, deeplink) onto a hostId. A present hint is
// returned verbatim, even when it names a host that no longer exists — the
// pane must surface `problem: 'host_removed'` for it, never silently fall
// back to a different daemon (spec §4.3.2 step 5). The first-host fallback
// applies only when the hint is absent or empty, for openers with no hint at
// all (legacy route, deeplink without `host`). Kept free of fetch imports so
// pane-utils / route code can use it.
import { useHostStore } from '../../stores/useHostStore'

export function resolveExecutionHostId(host?: string): string {
  if (host) return host
  const { hostOrder } = useHostStore.getState()
  return hostOrder[0] ?? ''
}
