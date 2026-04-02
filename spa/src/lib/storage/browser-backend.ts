import type { StateStorage } from 'zustand/middleware'
import { syncManager } from './sync'

export const browserStorage: StateStorage = {
  getItem(name: string) {
    return localStorage.getItem(name)
  },
  setItem(name: string, value: string) {
    const prev = localStorage.getItem(name)
    if (prev === value) return
    localStorage.setItem(name, value)
    syncManager.notify(name)
  },
  removeItem(name: string) {
    localStorage.removeItem(name)
    syncManager.notify(name)
  },
}
