import { createJSONStorage } from 'zustand/middleware'
import { browserStorage } from './browser-backend'

export { STORAGE_KEYS } from './keys'
export { syncManager } from './sync'

/**
 * Zustand persist storage backend.
 * Phase 1a: BrowserBackend（localStorage + BroadcastChannel）
 * Phase 1b: 將加入 ElectronBackend 自動偵測
 */
export const purdexStorage = createJSONStorage(() => browserStorage)
