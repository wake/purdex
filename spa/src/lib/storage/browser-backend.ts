import type { StateStorage } from 'zustand/middleware'
import { syncManager } from './sync'

export const browserStorage: StateStorage = {
  getItem(name: string) {
    return localStorage.getItem(name)
  },
  setItem(name: string, value: string) {
    localStorage.setItem(name, value)
    syncManager.notify(name)
  },
  removeItem(name: string) {
    localStorage.removeItem(name)
    syncManager.notify(name)
  },
}
