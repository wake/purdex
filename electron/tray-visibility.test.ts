import { afterEach, describe, expect, it, vi } from 'vitest'
import type { AppPrefs } from './app-prefs'
import { applyTrayVisibility, type TrayVisibilityDeps } from './tray-visibility'

// A fake main process: a boolean for the runtime tray, an object for the
// prefs file. `createOk` controls whether createTray would find its icon.
function fakeDeps(opts: { visible: boolean; prefs: AppPrefs; createOk?: boolean }) {
  const state = { visible: opts.visible, prefs: { ...opts.prefs }, saves: [] as AppPrefs[] }
  const deps: TrayVisibilityDeps = {
    isVisible: vi.fn(() => state.visible),
    create: vi.fn(() => {
      if (opts.createOk === false) return false
      state.visible = true
      return true
    }),
    destroy: vi.fn(() => { state.visible = false }),
    loadPrefs: vi.fn(() => ({ ...state.prefs })),
    savePrefs: vi.fn((p: AppPrefs) => { state.prefs = { ...p }; state.saves.push({ ...p }) }),
  }
  return { state, deps }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('applyTrayVisibility', () => {
  it('(a) turns the tray on: saves first, then creates, returns true', () => {
    const { state, deps } = fakeDeps({ visible: false, prefs: { showTray: false } })
    expect(applyTrayVisibility(true, deps)).toBe(true)
    expect(deps.savePrefs).toHaveBeenCalledWith({ showTray: true })
    expect(deps.create).toHaveBeenCalledTimes(1)
    expect(deps.destroy).not.toHaveBeenCalled()
    expect(state.visible).toBe(true)
    expect(state.prefs).toEqual({ showTray: true })
    // save happened before create
    const saveOrder = (deps.savePrefs as ReturnType<typeof vi.fn>).mock.invocationCallOrder[0]
    const createOrder = (deps.create as ReturnType<typeof vi.fn>).mock.invocationCallOrder[0]
    expect(saveOrder).toBeLessThan(createOrder)
  })

  it('(b) turns the tray off: saves first, then destroys, returns false', () => {
    const { state, deps } = fakeDeps({ visible: true, prefs: { showTray: true } })
    expect(applyTrayVisibility(false, deps)).toBe(false)
    expect(deps.savePrefs).toHaveBeenCalledWith({ showTray: false })
    expect(deps.destroy).toHaveBeenCalledTimes(1)
    expect(deps.create).not.toHaveBeenCalled()
    expect(state.visible).toBe(false)
    expect(state.prefs).toEqual({ showTray: false })
  })

  it('(c) rethrows when savePrefs throws and leaves the runtime tray untouched', () => {
    const { state, deps } = fakeDeps({ visible: true, prefs: { showTray: true } })
    const boom = new Error('EACCES')
    ;(deps.savePrefs as ReturnType<typeof vi.fn>).mockImplementation(() => { throw boom })
    expect(() => applyTrayVisibility(false, deps)).toThrow(boom)
    expect(deps.destroy).not.toHaveBeenCalled()
    expect(deps.create).not.toHaveBeenCalled()
    expect(state.visible).toBe(true)
    expect(state.prefs).toEqual({ showTray: true })
  })

  it('(d) returns false and writes showTray:false back when create fails', () => {
    const { state, deps } = fakeDeps({ visible: false, prefs: { showTray: false }, createOk: false })
    expect(applyTrayVisibility(true, deps)).toBe(false)
    expect(deps.create).toHaveBeenCalledTimes(1)
    expect(state.visible).toBe(false)
    // first save = intent (true), second save = rollback (false)
    expect(state.saves).toEqual([{ showTray: true }, { showTray: false }])
    expect(state.prefs).toEqual({ showTray: false })
  })

  it('(d2) swallows a throw from the rollback save and still returns false', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { deps } = fakeDeps({ visible: false, prefs: { showTray: false }, createOk: false })
    let n = 0
    ;(deps.savePrefs as ReturnType<typeof vi.fn>).mockImplementation(() => {
      n += 1
      if (n === 2) throw new Error('disk full')
    })
    expect(applyTrayVisibility(true, deps)).toBe(false)
    expect(deps.savePrefs).toHaveBeenCalledTimes(2)
    expect(warn).toHaveBeenCalled()
  })

  it('(e) is idempotent: turning on an already-visible tray re-saves prefs and returns true', () => {
    // prefs drifted (file says false, runtime is on) — the call must reconcile
    const { state, deps } = fakeDeps({ visible: true, prefs: { showTray: false } })
    expect(applyTrayVisibility(true, deps)).toBe(true)
    expect(deps.savePrefs).toHaveBeenCalledWith({ showTray: true })
    expect(state.prefs).toEqual({ showTray: true })
    expect(state.visible).toBe(true)
    expect(deps.destroy).not.toHaveBeenCalled()
  })

  it('(e2) is idempotent: turning off an already-hidden tray re-saves prefs and returns false', () => {
    const { state, deps } = fakeDeps({ visible: false, prefs: { showTray: true } })
    expect(applyTrayVisibility(false, deps)).toBe(false)
    expect(deps.savePrefs).toHaveBeenCalledWith({ showTray: false })
    expect(state.prefs).toEqual({ showTray: false })
    expect(deps.create).not.toHaveBeenCalled()
  })

  it('preserves other prefs fields when saving', () => {
    const { deps } = fakeDeps({ visible: false, prefs: { showTray: false, extra: 42 } as unknown as AppPrefs })
    applyTrayVisibility(true, deps)
    expect(deps.savePrefs).toHaveBeenCalledWith({ showTray: true, extra: 42 })
  })
})
