import { describe, it, expect, afterEach } from 'vitest'
import { getPlatformCapabilities } from './platform'

// The App (Electron) is the only shell that loads this SPA, so there is no
// "browser vs Electron" flag left. What remains is real capability detection:
// the Mac App can hot-load a newer SPA from the dev server into an OLDER shell
// whose preload predates an IPC, so each flag follows the preload method itself.
describe('getPlatformCapabilities', () => {
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
  })

  function setElectronAPI(api: Record<string, unknown>): void {
    ;(window as unknown as Record<string, unknown>).electronAPI = api
  }

  it('carries only the two preload-detected flags', () => {
    expect(Object.keys(getPlatformCapabilities()).sort()).toEqual(['devUpdateEnabled', 'hasLocalFilesystem'])
  })

  it('both flags are false without electronAPI', () => {
    expect(getPlatformCapabilities()).toEqual({ devUpdateEnabled: false, hasLocalFilesystem: false })
  })

  it('both flags are false for a preload that exposes neither getAppInfo nor fs', () => {
    setElectronAPI({ tearOffTab: async () => {} })
    expect(getPlatformCapabilities()).toEqual({ devUpdateEnabled: false, hasLocalFilesystem: false })
  })

  it('devUpdateEnabled follows getAppInfo', () => {
    setElectronAPI({ getAppInfo: async () => ({}) })
    expect(getPlatformCapabilities()).toEqual({ devUpdateEnabled: true, hasLocalFilesystem: false })
  })

  it('hasLocalFilesystem follows fs', () => {
    setElectronAPI({ fs: {} })
    expect(getPlatformCapabilities()).toEqual({ devUpdateEnabled: false, hasLocalFilesystem: true })
  })
})
