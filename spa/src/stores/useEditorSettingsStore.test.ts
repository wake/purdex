import { describe, it, expect, beforeEach, vi } from 'vitest'

const registerSpy = vi.hoisted(() => vi.fn())
const notifySpy = vi.hoisted(() => vi.fn())

vi.mock('../lib/storage/sync', () => ({
  syncManager: {
    register: registerSpy,
    notify: notifySpy,
    destroy: vi.fn(),
  },
  createSyncManager: vi.fn(),
}))

import {
  DEFAULT_EDITOR_SETTINGS,
  useEditorSettingsStore,
} from './useEditorSettingsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

function resetStore() {
  // merge-mode reset — do NOT pass `true` (replace mode wipes actions).
  useEditorSettingsStore.setState({ ...DEFAULT_EDITOR_SETTINGS })
}

beforeEach(() => {
  localStorage.clear()
  resetStore()
})

describe('useEditorSettingsStore', () => {
  it('S1-1: defaults match spec', () => {
    const s = useEditorSettingsStore.getState()
    expect(s.tabSize).toBe(2)
    expect(s.insertSpaces).toBe(true)
    expect(s.wordWrap).toBe('on')
    expect(s.lineNumbers).toBe('on')
    expect(s.minimap).toBe(true)
    expect(s.fontSize).toBe(13)
  })

  it('S1-2: setFontSize(5) clamps to 10', () => {
    useEditorSettingsStore.getState().setFontSize(5)
    expect(useEditorSettingsStore.getState().fontSize).toBe(10)
  })

  it('S1-3: setFontSize(30) clamps to 24', () => {
    useEditorSettingsStore.getState().setFontSize(30)
    expect(useEditorSettingsStore.getState().fontSize).toBe(24)
  })

  it('S1-4: setFontSize(NaN) falls back to default 13', () => {
    useEditorSettingsStore.getState().setFontSize(NaN)
    expect(useEditorSettingsStore.getState().fontSize).toBe(13)
  })

  it('S1-5: reset restores defaults after mutating all fields', () => {
    const {
      setTabSize,
      setInsertSpaces,
      setWordWrap,
      setLineNumbers,
      setMinimap,
      setFontSize,
      reset,
    } = useEditorSettingsStore.getState()
    setTabSize(8)
    setInsertSpaces(false)
    setWordWrap('off')
    setLineNumbers('off')
    setMinimap(false)
    setFontSize(18)
    reset()
    const s = useEditorSettingsStore.getState()
    expect(s.tabSize).toBe(2)
    expect(s.insertSpaces).toBe(true)
    expect(s.wordWrap).toBe('on')
    expect(s.lineNumbers).toBe('on')
    expect(s.minimap).toBe(true)
    expect(s.fontSize).toBe(13)
  })

  it('S1-6: setWordWrap("off") persists to localStorage under EDITOR_SETTINGS', () => {
    useEditorSettingsStore.getState().setWordWrap('off')
    const raw = localStorage.getItem(STORAGE_KEYS.EDITOR_SETTINGS)
    expect(raw).toBeTruthy()
    const parsed = JSON.parse(raw!)
    expect(parsed.state.wordWrap).toBe('off')
  })

  it('S1-7: rehydrate with invalid fields replaces them with defaults', async () => {
    localStorage.setItem(
      STORAGE_KEYS.EDITOR_SETTINGS,
      JSON.stringify({
        state: { tabSize: 99, wordWrap: 'maybe', fontSize: 'big' },
        version: 1,
      }),
    )
    await useEditorSettingsStore.persist.rehydrate()
    const s = useEditorSettingsStore.getState()
    expect(s.tabSize).toBe(2)
    expect(s.wordWrap).toBe('on')
    expect(s.fontSize).toBe(13)
  })

  it('S1-8: rehydrate with null persisted state — no throw, all defaults', async () => {
    localStorage.setItem(
      STORAGE_KEYS.EDITOR_SETTINGS,
      JSON.stringify({ state: null, version: 1 }),
    )
    await expect(useEditorSettingsStore.persist.rehydrate()).resolves.not.toThrow()
    const s = useEditorSettingsStore.getState()
    expect(s.tabSize).toBe(2)
    expect(s.insertSpaces).toBe(true)
    expect(s.wordWrap).toBe('on')
    expect(s.lineNumbers).toBe('on')
    expect(s.minimap).toBe(true)
    expect(s.fontSize).toBe(13)
  })
})
