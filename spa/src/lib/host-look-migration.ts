// spa/src/lib/host-look-migration.ts — the first-run copy of every host's `HostConfig` look into the workbench's
// look store (host ownership spec §4.3; plan H2c-2, §0.16).
//
// For each local host (in `hostOrder`, then any host outside it): key = its CURRENT wire id (`wireIdOfHost` — `d1_…`
// when it has a valid daemonId, else its local id); an entry already under the key is never overwritten; else the
// host's present look fields are copied (never `null`). One `setState`. Then the device-local marker
// `purdex-host-looks-migrated = '1'` is written — never projected, never registered with `syncManager` — so a later
// boot never resurrects a look the user reset. Marker reads and writes are in try/catch: a failed read means "not
// migrated" (the skip-if-present rule makes the rerun harmless), a failed write only means that rerun next boot.
//
// Before H3 the `hosts` section keeps every device's `HostConfig` looks equal, so two devices migrating one `d1_…`
// host write the same entry and the same `settings` hash.
//
// `bootHostLooks()` is the boot gate (`main.tsx`): it resolves once BOTH `useHostStore` and `useHostLookStore` have
// hydrated and the migration has returned; Profile Sync starts only after it (no `settings` build before the
// migration). With today's synchronous localStorage it resolves in a microtask.
//
// This file reads `HostConfig` look fields as a SOURCE (spec §4.4) — allowlisted in `host-look.guard.test.ts`.
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useHostLookStore, type HostLookEntry } from '../stores/useHostLookStore'
import { STORAGE_KEYS } from './storage'
import { wireIdOfHost } from './profile/host-identity'

export type HostLookMigration = 'already' | 'migrated'

/** The host's present look fields — what the migration copies. */
function lookOfConfig(host: HostConfig): HostLookEntry {
  const { name, colors, color, icon, iconWeight } = host
  const entry: HostLookEntry = {}
  if (name !== undefined) entry.name = name
  if (colors !== undefined) entry.colors = colors
  if (color !== undefined) entry.color = color
  if (icon !== undefined) entry.icon = icon
  if (iconWeight !== undefined) entry.iconWeight = iconWeight
  return entry
}

function markerSet(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEYS.HOST_LOOKS_MIGRATED) === '1'
  } catch {
    return false
  }
}

function setMarker(): void {
  try {
    localStorage.setItem(STORAGE_KEYS.HOST_LOOKS_MIGRATED, '1')
  } catch {
    // a harmless rerun next boot: every key written now is skipped then
  }
}

/** Runs the migration unless the marker says it ran. `snapshot` is the HYDRATED host store. */
export function migrateHostLooksOnce(snapshot: {
  hosts: Record<string, HostConfig>
  hostOrder: readonly string[]
}): HostLookMigration {
  if (markerSet()) return 'already'
  const { hosts, hostOrder } = snapshot
  const ordered = [...hostOrder.filter((id) => Object.hasOwn(hosts, id)), ...Object.keys(hosts).filter((id) => !hostOrder.includes(id))]
  const current = useHostLookStore.getState().looks
  let looks: Record<string, HostLookEntry> | null = null
  for (const id of [...new Set(ordered)]) {
    const host = hosts[id]
    if (host === null || typeof host !== 'object') continue
    const key = wireIdOfHost(host)
    if (Object.hasOwn(looks ?? current, key)) continue
    looks ??= { ...current }
    looks[key] = lookOfConfig(host)
  }
  if (looks !== null) useHostLookStore.setState({ looks })
  setMarker()
  return 'migrated'
}

interface Hydratable {
  persist: { hasHydrated: () => boolean; onFinishHydration: (fn: () => void) => () => void }
}

function hydrated(store: Hydratable): Promise<void> {
  if (store.persist.hasHydrated()) return Promise.resolve()
  return new Promise((resolve) => {
    const unsub = store.persist.onFinishHydration(() => {
      unsub()
      resolve()
    })
  })
}

/**
 * The §0.16 gate: waits for both stores to hydrate, migrates the hydrated hosts, resolves. Never rejects — a
 * migration that throws is reported (`'failed'`, marker not written, so it reruns next boot) and boot goes on.
 */
export async function bootHostLooks(): Promise<HostLookMigration | 'failed'> {
  await Promise.all([hydrated(useHostStore as unknown as Hydratable), hydrated(useHostLookStore as unknown as Hydratable)])
  try {
    const { hosts, hostOrder } = useHostStore.getState()
    return migrateHostLooksOnce({ hosts, hostOrder })
  } catch (err) {
    console.error(`[host-look-migration] the first-run look migration failed: ${err instanceof Error ? err.message : String(err)}`)
    return 'failed'
  }
}
