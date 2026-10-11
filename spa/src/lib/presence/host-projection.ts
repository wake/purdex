// The part of the host list the presence reporter in the main process uses (PA-2 D8), and the rev patch that goes with it.
import type { HostConfig } from '../../stores/useHostStore'

/** Canonical string of what the reporter sees of a host list: id, endpoint and token. Contains tokens: never log it. */
export function presenceProjection(hosts: Record<string, HostConfig>): string {
  return JSON.stringify(Object.values(hosts).map((h) => [h.id, h.ip, h.port, h.token ?? null]).sort((a, b) => (a[0] as string).localeCompare(b[0] as string)))
}

/** `{ hostsRev }` for a patch that turns `before` into `after`; empty when the reporter's view did not change.
 *  max(prev + 1, now): a write time, so the highest rev is the latest write even across windows. */
export function revPatch(prevRev: number, before: Record<string, HostConfig>, after: Record<string, HostConfig>): { hostsRev?: number } {
  if (presenceProjection(before) === presenceProjection(after)) return {}
  return { hostsRev: Math.max((prevRev || 0) + 1, Date.now()) }
}
