import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'

const MAX_URLS = 100

interface BrowserHistoryState {
  urls: string[]
  addUrl: (url: string) => void
}

export const useBrowserHistoryStore = create<BrowserHistoryState>()(
  persist(
    (set) => ({
      urls: [],
      addUrl: (url) =>
        set((state) => {
          const filtered = state.urls.filter((u) => u !== url)
          return { urls: [url, ...filtered].slice(0, MAX_URLS) }
        }),
    }),
    {
      name: STORAGE_KEYS.BROWSER_HISTORY,
      storage: purdexStorage,
    },
  ),
)
