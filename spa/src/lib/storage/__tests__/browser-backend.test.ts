import { describe, it, expect, vi, beforeEach } from 'vitest'

// Mock sync module before importing browser-backend
vi.mock('../sync', () => ({
  syncManager: { notify: vi.fn(), register: vi.fn(), destroy: vi.fn() },
}))

import { browserStorage } from '../browser-backend'
import { syncManager } from '../sync'

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
})

describe('browserStorage', () => {
  it('getItem returns null for missing key', () => {
    expect(browserStorage.getItem('missing')).toBeNull()
  })

  it('setItem stores value in localStorage', () => {
    browserStorage.setItem('key', '"value"')
    expect(localStorage.getItem('key')).toBe('"value"')
  })

  it('setItem notifies sync manager', () => {
    browserStorage.setItem('purdex-tabs', '{}')
    expect(syncManager.notify).toHaveBeenCalledWith('purdex-tabs')
  })

  it('getItem retrieves stored value', () => {
    localStorage.setItem('key', '"hello"')
    expect(browserStorage.getItem('key')).toBe('"hello"')
  })

  it('removeItem deletes from localStorage', () => {
    localStorage.setItem('key', '"value"')
    browserStorage.removeItem('key')
    expect(localStorage.getItem('key')).toBeNull()
  })

  it('removeItem notifies sync manager', () => {
    browserStorage.removeItem('purdex-tabs')
    expect(syncManager.notify).toHaveBeenCalledWith('purdex-tabs')
  })

  it('setItem does not write or notify when value is unchanged', () => {
    localStorage.setItem('key', '"same"')
    vi.clearAllMocks()

    browserStorage.setItem('key', '"same"')

    expect(syncManager.notify).not.toHaveBeenCalled()
    // Verify localStorage value is still the same (no unnecessary write)
    expect(localStorage.getItem('key')).toBe('"same"')
  })
})
