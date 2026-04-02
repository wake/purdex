type SyncableStore = {
  persist: { rehydrate: () => void | Promise<void> }
}

const CHANNEL_NAME = 'purdex-sync'

export function createSyncManager() {
  const registry = new Map<string, SyncableStore>()
  let channel: BroadcastChannel | null = null

  function ensureChannel(): BroadcastChannel | null {
    if (channel) return channel
    if (typeof BroadcastChannel === 'undefined') return null
    channel = new BroadcastChannel(CHANNEL_NAME)
    channel.onmessage = (event: MessageEvent) => {
      const key = (event.data as { key?: string })?.key
      if (key) registry.get(key)?.persist.rehydrate()
    }
    return channel
  }

  return {
    register(key: string, store: SyncableStore) {
      registry.set(key, store)
      ensureChannel()
    },
    notify(key: string) {
      ensureChannel()?.postMessage({ key })
    },
    destroy() {
      channel?.close()
      channel = null
      registry.clear()
    },
  }
}

/** Default singleton for production use */
export const syncManager = createSyncManager()
