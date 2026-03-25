import { describe, it, expect, afterEach } from 'vitest'
import { getPlatformCapabilities } from './platform'

describe('getPlatformCapabilities', () => {
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
  })

  it('returns all false when no electronAPI', () => {
    const caps = getPlatformCapabilities()
    expect(caps.canTearOffTab).toBe(false)
    expect(caps.canMergeWindow).toBe(false)
    expect(caps.canBrowserPane).toBe(false)
    expect(caps.canSystemTray).toBe(false)
  })

  it('returns all true when electronAPI exists', () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = {
      tearOffTab: async () => {},
      mergeTab: async () => {},
      openBrowserView: async () => {},
      closeBrowserView: async () => {},
      navigateBrowserView: async () => {},
      onTabReceived: () => () => {},
    }
    const caps = getPlatformCapabilities()
    expect(caps.canTearOffTab).toBe(true)
    expect(caps.canMergeWindow).toBe(true)
    expect(caps.canBrowserPane).toBe(true)
    expect(caps.canSystemTray).toBe(true)
  })
})
