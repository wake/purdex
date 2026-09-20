// spa/src/lib/storage/sync.ts — every window of this client sees what another one
// persisted: the registered store reads storage again (`persist.rehydrate()`).
//
// WHY TWO SIGNALS. The writer announces a write on a BroadcastChannel
// (`notify`), and that used to be all. Measured on real hardware, two windows =
// two renderer processes: the message arrives BEFORE the write is visible to the
// receiving process's `localStorage`, so the rehydrate it triggers reads the
// PREVIOUS value — every registered store ran exactly one write behind (n = 5, 6,
// 7 written; 4, 5, 6 held), unnoticed as long as the next write brought it level.
// The world tag of Profile Sync (lib/profile/master-world.ts) needs three stores
// to agree exactly, and a switch writes each of them once: one write behind is
// unsettled for good. The native `storage` event is dispatched by the HTML spec
// AFTER the document's storage area has been updated, and only in OTHER
// documents, so its rehydrate reads the new value and a window never answers its
// own write. The BroadcastChannel stays: for windows of the same process, and for
// environments without `storage` events.
//
// THE COST, AND THE ONE THING DONE ABOUT IT. A write in another window now means
// up to two rehydrates here, and for the tab / workspace stores a rehydrate
// rebuilds every object from JSON — one more re-render, only ever on ANOTHER
// window's write. The `storage` event carries the new value, so the second one is
// skipped when the broadcast's rehydrate has provably read that very string
// (`seen`: what storage held for the key when this manager last rehydrated it;
// forgotten on a local `notify`, after which memory is no longer that string).
// No more than that — one map, no state machine; an unnecessary rehydrate is
// harmless, a skipped necessary one is the bug this file fixes.
type SyncableStore = {
  persist: { rehydrate: () => void | Promise<void> }
}

const CHANNEL_NAME = 'purdex-sync'

export function createSyncManager() {
  const registry = new Map<string, SyncableStore>()
  /** key → the raw value storage held when the key was last rehydrated from here (see THE COST). */
  const seen = new Map<string, string | null>()
  let channel: BroadcastChannel | null = null
  let listening = false

  function read(key: string): string | null | undefined {
    try {
      return localStorage.getItem(key)
    } catch {
      return undefined // unreadable: equal to no `newValue`, so nothing is ever skipped on its account
    }
  }

  function rehydrate(key: string, value: string | null | undefined): void {
    const store = registry.get(key)
    if (store === undefined) return
    if (value === undefined) seen.delete(key)
    else seen.set(key, value)
    void store.persist.rehydrate()
  }

  function onStorage(event: StorageEvent): void {
    if (event.key === null || event.storageArea !== localStorage) return
    if (seen.has(event.key) && seen.get(event.key) === event.newValue) return
    rehydrate(event.key, event.newValue)
  }

  function ensureChannel(): BroadcastChannel | null {
    if (channel) return channel
    if (typeof BroadcastChannel === 'undefined') return null
    channel = new BroadcastChannel(CHANNEL_NAME)
    channel.onmessage = (event: MessageEvent) => {
      const key = (event.data as { key?: string })?.key
      if (key && registry.has(key)) rehydrate(key, read(key))
    }
    return channel
  }

  function ensureStorageListener(): void {
    if (listening || typeof window === 'undefined') return
    window.addEventListener('storage', onStorage)
    listening = true
  }

  return {
    register(key: string, store: SyncableStore) {
      registry.set(key, store)
      ensureChannel()
      ensureStorageListener()
    },
    notify(key: string) {
      seen.delete(key)
      ensureChannel()?.postMessage({ key })
    },
    destroy() {
      channel?.close()
      channel = null
      if (listening && typeof window !== 'undefined') window.removeEventListener('storage', onStorage)
      listening = false
      registry.clear()
      seen.clear()
    },
  }
}

/** Default singleton for production use */
export const syncManager = createSyncManager()
