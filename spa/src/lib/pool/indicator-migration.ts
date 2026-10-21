// spa/src/lib/pool/indicator-migration.ts — the one-time copy of the old global 「主機格指示」 (`useUISettingsStore.fleetCellIndicator`) into each pool host's own indicator (monitoring tabs MON-9a-3; spec W7, M51).
// The control is gone from 設定 → 終端機 in the same release, so what the reader chose has to be in every host's own settings before it disappears. It runs between the host-look migration and `startProfileSync` (`main.tsx`), and does not wait for the pool:
// the first `settings` build is then not earlier than the copy, and the copied values go out with it. Delete with the global field (MON-12).
//
// The hosts are: those the pool stores know (order, looks) and those the pool answered with. When there is none (a fast boot that did not wait for the pool, or a new device) nothing is written and no marker is set; the first
// time any of them is there, the copy is done, then the marker is written, and the watch ends. This device's marker is `'1'`, never projected and never synced; a marker that does not read counts as not done, and a rerun only fills in what is missing.
import { usePoolHostsStore } from '../../stores/usePoolHostsStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { STORAGE_KEYS } from '../storage'
import { defaultIndicator } from './host-looks'
import { usePoolStore } from './store'

function migrated(): boolean {
  try { return localStorage.getItem(STORAGE_KEYS.POOL_INDICATOR_MIGRATED) === '1' } catch { return false }
}
function markMigrated(): void {
  try { localStorage.setItem(STORAGE_KEYS.POOL_INDICATOR_MIGRATED, '1') } catch { /* a failed write only means the next boot looks again; filling in what is missing twice is harmless */ }
}

// Only a wwd host id (16 lowercase hex) can be stored in the pool hosts store; anything else would never be written, and would keep the marker away for good.
const HOST_ID = /^[0-9a-f]{16}$/
function knownHostIds(): string[] {
  const own = usePoolHostsStore.getState()
  return [...new Set([...own.order, ...Object.keys(own.looks), ...usePoolStore.getState().hosts.map((h) => h.host_id)])].filter((id) => HOST_ID.test(id))
}

/** Writes the converted global value into every known host that has no indicator of its own, then marks this device done. A global value that converts to the default writes nothing. */
function copyNow(ids: readonly string[]): void {
  const global = useUISettingsStore.getState().fleetCellIndicator // read now, not when the copy was asked for
  if (global !== 'ring') {
    const indicator = defaultIndicator(global)
    const { patchLook } = usePoolHostsStore.getState()
    for (const id of ids) patchLook(id, (cur) => (cur?.indicator !== undefined ? cur : { ...cur, indicator }))
    // The store holds at most MAX_POOL_HOSTS looks and refuses a new one beyond that. A host it had no room for has not been written: no marker, so the next start tries again (until then the global value still reads for it).
    const looks = usePoolHostsStore.getState().looks
    if (ids.some((id) => looks[id]?.indicator === undefined)) return
  }
  markMigrated()
}

/** Synchronous and never throws. */
export function migrateGlobalIndicator(): void {
  try {
    if (migrated()) return
    if (useUISettingsStore.getState().fleetCellIndicator === 'ring') { markMigrated(); return }
    const ids = knownHostIds()
    if (ids.length > 0) { copyNow(ids); return }
    let off: Array<() => void> = []
    const onChange = () => {
      try {
        const now = knownHostIds()
        if (now.length === 0) return
        for (const u of off) u()
        off = []
        copyNow(now)
      } catch { /* the watch ends either way; the next boot starts again */ }
    }
    off = [usePoolStore.subscribe(onChange), usePoolHostsStore.subscribe(onChange)]
  } catch {
    // never block the boot
  }
}
