// spa/src/lib/host-reresolve.ts — the host re-resolve pass (host ownership spec §3.3).
//
// A host reference this device cannot resolve is stored verbatim — the wire id, byte for byte (§3.2). When that host
// later arrives here (added, its daemonId learned, an alias learned, a conflict cleared), every such reference must
// point at the local host. This pass does that, over everything this device holds: the tab store on screen, every
// parked world, `purdex-host-settings` keys and the New Tab host-bearing columns (presets and knownIds).
//
// No push follows: for a `d1_…` reference the build maps the new local id back to the same `d1_…` (§3.3 no-push
// invariant). Only a reference resolved through a legacy alias is canonicalised by the next build — one push, as a
// pull of it already does.
//
// Lock: the in-process OPERATION lock, owner `host-reresolve` (plan §0.1) — what the tabs apply and the hosts
// cascade take to rewrite the tab tree. The body is one synchronous stretch: nothing can interleave with it in this
// window. Refused → retried later; a newer request supersedes a pending retry.
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../stores/useLocalProfilesStore'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { hostSettingsFromWire, layoutFromWire, presetColumnIdFromWire } from './profile/host-identity'
import { wireResolverOf } from './profile/sections'
import type { Tab } from '../types/tab'

export const HOST_RERESOLVE_LOCK_OWNER = 'host-reresolve'
export const HOST_RERESOLVE_RETRY_MS = 500

/** `done` — ran (whether or not anything moved); `conflict` — identity conflict, nothing done; `busy` — lock held. */
export type HostReresolveOutcome = 'done' | 'conflict' | 'busy'

type HostMap = (hostId: string) => string

function mapWorld(world: ParkedWorld, map: HostMap): ParkedWorld {
  let tabs: Record<string, Tab> | null = null
  for (const [id, tab] of Object.entries(world.tabs)) {
    const layout = layoutFromWire(tab.layout, map)
    if (layout === tab.layout) continue
    tabs ??= { ...world.tabs }
    tabs[id] = { ...tab, layout }
  }
  return tabs === null ? world : { ...world, tabs }
}

/**
 * Rewrite every host reference this device holds through `map`: on screen, in every parked world, the host-settings
 * keys (a sync-id entry wins a collision — `rekeyEntries`) and the New Tab columns (first occurrence wins). A store
 * nothing moves in is not written. Synchronous; the caller holds the operation lock.
 */
function rewriteHostRefs(map: HostMap): void {
  useTabStore.getState().rewritePaneHosts(map)
  useLocalProfilesStore.getState().updateParkedWorlds((world) => mapWorld(world, map))
  const settings = useHostSettingsStore.getState().hosts
  if (Object.keys(settings).some((id) => map(id) !== id)) {
    useHostSettingsStore.setState({ hosts: hostSettingsFromWire(settings, map) })
  }
  useNewTabLayoutStore.getState().renameIds((id) => presetColumnIdFromWire(id, map))
}

/** One pass, now. Never throws on a busy lock or a conflict — it says so. */
export function runHostReresolve(): HostReresolveOutcome {
  const { hosts, hostOrder } = useHostStore.getState()
  const resolve = wireResolverOf({ hosts, hostOrder })
  if (resolve === null) return 'conflict'
  // Only an id that is NOT a local host moves, and only onto a local host.
  const map: HostMap = (id) => {
    if (Object.hasOwn(hosts, id)) return id
    const local = resolve(id)
    return Object.hasOwn(hosts, local) ? local : id
  }
  const lock = useRebuildStore.getState()
  const grant = lock.acquireOperationLock(HOST_RERESOLVE_LOCK_OWNER)
  if (grant === null) return 'busy'
  try {
    rewriteHostRefs(map)
  } finally {
    useRebuildStore.getState().releaseOperationLock(grant)
  }
  return 'done'
}

let retry: ReturnType<typeof setTimeout> | null = null

/**
 * Ask for a pass: it runs now, and while the lock is held elsewhere it is retried every
 * `HOST_RERESOLVE_RETRY_MS` until it runs. A newer request supersedes a pending retry.
 * Under a conflict nothing is scheduled: the conflict clearing changes the identity, which requests again.
 */
export function requestHostReresolve(): void {
  if (retry !== null) {
    clearTimeout(retry)
    retry = null
  }
  if (runHostReresolve() !== 'busy') return
  retry = setTimeout(() => {
    retry = null
    requestHostReresolve()
  }, HOST_RERESOLVE_RETRY_MS)
}

export function __resetHostReresolveForTest(): void {
  if (retry !== null) clearTimeout(retry)
  retry = null
}
